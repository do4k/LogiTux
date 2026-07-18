package gpro

import (
	"encoding/binary"
	"fmt"

	"logitux/internal/hidpp"
)

// Battery feature IDs, tried in this priority order (most precise first).
const (
	featureBatteryVoltage uint16 = 0x1001 // voltage-based estimate; fallback
	featureADCMeasurement uint16 = 0x1f20 // voltage-based estimate; last resort
)

// batteryKind identifies which battery feature a Mouse resolved to, since
// each has a different function number and response layout.
type batteryKind int

const (
	batteryKindNone batteryKind = iota
	batteryKindUnified
	batteryKindLegacyStatus
	batteryKindVoltage
	batteryKindADC
)

const (
	// batteryFuncGetStatusUnified is UNIFIED_BATTERY's (0x1004) status
	// call: response is [percent, levelFlags, status, reserved].
	batteryFuncGetStatusUnified byte = 0x10
	// batteryFuncGetStatusLegacy is BATTERY_STATUS's (0x1000) status
	// call: response is [percent, nextThreshold, status]. A percent of 0
	// specifically means "unknown", not "empty".
	batteryFuncGetStatusLegacy byte = 0x00
	// batteryFuncGetVoltage is BATTERY_VOLTAGE's (0x1001) call: response
	// is [voltageHigh, voltageLow, flags] (millivolts, big-endian).
	batteryFuncGetVoltage byte = 0x00
	// batteryFuncGetADC is ADC_MEASUREMENT's (0x1F20) call: response is
	// [voltageHigh, voltageLow, flags] (millivolts, big-endian); flags bit
	// 0 marks the reading valid, bit 1 marks it charging.
	batteryFuncGetADC byte = 0x00
)

// Status byte values, shared by BATTERY_STATUS and UNIFIED_BATTERY.
const (
	batteryStatusDischarging  byte = 0x00
	batteryStatusRecharging   byte = 0x01
	batteryStatusAlmostFull   byte = 0x02
	batteryStatusFull         byte = 0x03
	batteryStatusSlowRecharge byte = 0x04
)

// resolveBatteryFeature tries each known battery feature in order of how
// directly it reports a percentage, since not every mouse implements the
// same one. Real hardware testing found a G Pro Wireless unit that
// supports neither of the two "modern" features (0x1004, 0x1000), only
// the older voltage-based ones.
func resolveBatteryFeature(conn *hidpp.Conn, deviceIndex byte) (featureIndex byte, kind batteryKind, err error) {
	candidates := []struct {
		id   uint16
		kind batteryKind
	}{
		{featureUnifiedBattery, batteryKindUnified},
		{featureBatteryStatus, batteryKindLegacyStatus},
		{featureBatteryVoltage, batteryKindVoltage},
		{featureADCMeasurement, batteryKindADC},
	}

	for _, c := range candidates {
		idx, ok, err := hidpp.GetFeatureIndex(conn, deviceIndex, c.id)
		if err != nil {
			return 0, batteryKindNone, fmt.Errorf("gpro: look up battery feature 0x%04x: %w", c.id, err)
		}
		if ok {
			return idx, c.kind, nil
		}
	}

	return 0, batteryKindNone, fmt.Errorf("gpro: device supports no known battery feature (tried 0x%04x, 0x%04x, 0x%04x, 0x%04x)",
		featureUnifiedBattery, featureBatteryStatus, featureBatteryVoltage, featureADCMeasurement)
}

// Battery implements device.BatteryStatus.
func (m *Mouse) Battery() (percent int, charging bool, err error) {
	switch m.batteryKind {
	case batteryKindUnified:
		return m.batteryFromStatusCall(batteryFuncGetStatusUnified, false)
	case batteryKindLegacyStatus:
		return m.batteryFromStatusCall(batteryFuncGetStatusLegacy, true)
	case batteryKindVoltage:
		return m.batteryFromVoltageCall(batteryFuncGetVoltage, false)
	case batteryKindADC:
		return m.batteryFromVoltageCall(batteryFuncGetADC, true)
	default:
		return 0, false, fmt.Errorf("gpro: device has no supported battery feature")
	}
}

// batteryFromStatusCall handles UNIFIED_BATTERY and BATTERY_STATUS, whose
// responses both start with [percent, _, status]. zeroMeansUnknown is only
// true for the legacy feature.
func (m *Mouse) batteryFromStatusCall(function byte, zeroMeansUnknown bool) (percent int, charging bool, err error) {
	resp, err := m.conn.Call(m.deviceIndex, m.batteryFeatureIndex, function, nil)
	if err != nil {
		return 0, false, fmt.Errorf("gpro: get battery status: %w", err)
	}
	if len(resp) < 3 {
		return 0, false, fmt.Errorf("gpro: short battery status response")
	}

	percent = int(resp[0])
	if percent == 0 && zeroMeansUnknown {
		return 0, false, fmt.Errorf("gpro: battery percentage unavailable")
	}

	status := resp[2]
	return percent, isChargingStatus(status), nil
}

// batteryFromVoltageCall handles BATTERY_VOLTAGE and ADC_MEASUREMENT,
// whose responses both are [voltage(2B BE, mV), flags(1B)]; adc requires
// checking a "valid" bit first, and the two features use different flag
// bits for "charging".
func (m *Mouse) batteryFromVoltageCall(function byte, isADC bool) (percent int, charging bool, err error) {
	resp, err := m.conn.Call(m.deviceIndex, m.batteryFeatureIndex, function, nil)
	if err != nil {
		return 0, false, fmt.Errorf("gpro: get battery voltage: %w", err)
	}
	if len(resp) < 3 {
		return 0, false, fmt.Errorf("gpro: short battery voltage response")
	}

	millivolts := int(binary.BigEndian.Uint16(resp[0:2]))
	flags := resp[2]

	if isADC {
		if flags&0x01 == 0 {
			return 0, false, fmt.Errorf("gpro: no valid battery reading available")
		}
		charging = flags&0x02 != 0
	} else {
		charging = flags&0x80 != 0
	}

	return estimateBatteryPercent(millivolts), charging, nil
}

func isChargingStatus(status byte) bool {
	return status == batteryStatusRecharging ||
		status == batteryStatusAlmostFull ||
		status == batteryStatusFull ||
		status == batteryStatusSlowRecharge
}

// batteryVoltageCurve maps single-cell Li-ion/Li-Po voltage (millivolts)
// to an estimated charge percentage, for devices that only report raw
// voltage rather than a computed percentage. Approximate discharge-curve
// values, consistent with the mapping widely used for this purpose across
// Linux Logitech tooling.
var batteryVoltageCurve = []struct {
	millivolts int
	percent    int
}{
	{4186, 100},
	{4067, 90},
	{3989, 80},
	{3922, 70},
	{3859, 60},
	{3811, 50},
	{3778, 40},
	{3751, 30},
	{3717, 20},
	{3671, 10},
	{3646, 5},
	{3579, 2},
	{3500, 0},
}

func estimateBatteryPercent(millivolts int) int {
	if millivolts >= batteryVoltageCurve[0].millivolts {
		return batteryVoltageCurve[0].percent
	}
	last := batteryVoltageCurve[len(batteryVoltageCurve)-1]
	if millivolts <= last.millivolts {
		return last.percent
	}

	for i := 0; i < len(batteryVoltageCurve)-1; i++ {
		hi, lo := batteryVoltageCurve[i], batteryVoltageCurve[i+1]
		if millivolts >= lo.millivolts && millivolts <= hi.millivolts {
			span := hi.millivolts - lo.millivolts
			frac := float64(millivolts-lo.millivolts) / float64(span)
			return lo.percent + int(frac*float64(hi.percent-lo.percent)+0.5)
		}
	}
	return 0
}
