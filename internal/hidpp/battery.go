package hidpp

import (
	"encoding/binary"
	"fmt"
)

// Battery feature IDs, tried in this priority order (most direct/precise
// first) by ResolveBatteryFeature, since not every device implements the
// same one — real hardware testing found both a G Pro Wireless and PRO X
// Wireless unit that only support the older voltage-based features.
const (
	FeatureUnifiedBattery uint16 = 0x1004
	FeatureBatteryStatus  uint16 = 0x1000 // legacy: percent + status, but 0% means "unknown"
	FeatureBatteryVoltage uint16 = 0x1001 // voltage-based estimate
	FeatureADCMeasurement uint16 = 0x1f20 // voltage-based estimate, last resort
)

// Battery status byte values, shared by BATTERY_STATUS and UNIFIED_BATTERY.
const (
	BatteryStatusDischarging  byte = 0x00
	BatteryStatusRecharging   byte = 0x01
	BatteryStatusAlmostFull   byte = 0x02
	BatteryStatusFull         byte = 0x03
	BatteryStatusSlowRecharge byte = 0x04
)

const (
	// BatteryFuncGetStatusUnified is UNIFIED_BATTERY's (0x1004) status
	// call: response is [percent, levelFlags, status, reserved].
	BatteryFuncGetStatusUnified byte = 0x10
	// BatteryFuncGetStatusLegacy is BATTERY_STATUS's (0x1000) status
	// call: response is [percent, nextThreshold, status]. A percent of 0
	// specifically means "unknown", not "empty".
	BatteryFuncGetStatusLegacy byte = 0x00
	// BatteryFuncGetVoltage is BATTERY_VOLTAGE's (0x1001) call: response
	// is [voltageHigh, voltageLow, flags] (millivolts, big-endian).
	BatteryFuncGetVoltage byte = 0x00
	// BatteryFuncGetADC is ADC_MEASUREMENT's (0x1F20) call: response is
	// [voltageHigh, voltageLow, flags] (millivolts, big-endian); flags bit
	// 0 marks the reading valid, bit 1 marks it charging.
	BatteryFuncGetADC byte = 0x00
)

// BatteryKind identifies which battery feature a device resolved to,
// since each has a different function number and response layout.
type BatteryKind int

const (
	BatteryKindNone BatteryKind = iota
	BatteryKindUnified
	BatteryKindLegacyStatus
	BatteryKindVoltage
	BatteryKindADC
)

// ResolveBatteryFeature tries each known battery feature in turn and
// returns whichever the device supports. err is non-nil only on an actual
// transport failure; a device with no battery feature at all is reported
// via kind == BatteryKindNone, err == nil, which callers typically treat
// as "this optional capability just isn't available" rather than fatal.
func ResolveBatteryFeature(conn *Conn, deviceIndex byte) (featureIndex byte, kind BatteryKind, err error) {
	candidates := []struct {
		id   uint16
		kind BatteryKind
	}{
		{FeatureUnifiedBattery, BatteryKindUnified},
		{FeatureBatteryStatus, BatteryKindLegacyStatus},
		{FeatureBatteryVoltage, BatteryKindVoltage},
		{FeatureADCMeasurement, BatteryKindADC},
	}

	for _, c := range candidates {
		idx, ok, err := GetFeatureIndex(conn, deviceIndex, c.id)
		if err != nil {
			return 0, BatteryKindNone, fmt.Errorf("hidpp: look up battery feature 0x%04x: %w", c.id, err)
		}
		if ok {
			return idx, c.kind, nil
		}
	}
	return 0, BatteryKindNone, nil
}

// ReadBattery reads the current percent/charging state using a feature
// index and kind previously resolved by ResolveBatteryFeature.
func ReadBattery(conn *Conn, deviceIndex, featureIndex byte, kind BatteryKind) (percent int, charging bool, err error) {
	switch kind {
	case BatteryKindUnified:
		return batteryFromStatusCall(conn, deviceIndex, featureIndex, BatteryFuncGetStatusUnified, false)
	case BatteryKindLegacyStatus:
		return batteryFromStatusCall(conn, deviceIndex, featureIndex, BatteryFuncGetStatusLegacy, true)
	case BatteryKindVoltage:
		return batteryFromVoltageCall(conn, deviceIndex, featureIndex, BatteryFuncGetVoltage, false)
	case BatteryKindADC:
		return batteryFromVoltageCall(conn, deviceIndex, featureIndex, BatteryFuncGetADC, true)
	default:
		return 0, false, fmt.Errorf("hidpp: device has no supported battery feature")
	}
}

// batteryFromStatusCall handles UNIFIED_BATTERY and BATTERY_STATUS, whose
// responses both start with [percent, _, status]. zeroMeansUnknown is only
// true for the legacy feature.
func batteryFromStatusCall(conn *Conn, deviceIndex, featureIndex, function byte, zeroMeansUnknown bool) (percent int, charging bool, err error) {
	resp, err := conn.Call(deviceIndex, featureIndex, function, nil)
	if err != nil {
		return 0, false, fmt.Errorf("hidpp: get battery status: %w", err)
	}
	if len(resp) < 3 {
		return 0, false, fmt.Errorf("hidpp: short battery status response")
	}

	percent = int(resp[0])
	if percent == 0 && zeroMeansUnknown {
		return 0, false, fmt.Errorf("hidpp: battery percentage unavailable")
	}

	status := resp[2]
	return percent, isChargingStatus(status), nil
}

// batteryFromVoltageCall handles BATTERY_VOLTAGE and ADC_MEASUREMENT,
// whose responses are both [voltage(2B BE, mV), flags(1B)]; ADC requires
// checking a "valid" bit first, and the two features use different flag
// bits for "charging".
func batteryFromVoltageCall(conn *Conn, deviceIndex, featureIndex, function byte, isADC bool) (percent int, charging bool, err error) {
	resp, err := conn.Call(deviceIndex, featureIndex, function, nil)
	if err != nil {
		return 0, false, fmt.Errorf("hidpp: get battery voltage: %w", err)
	}
	if len(resp) < 3 {
		return 0, false, fmt.Errorf("hidpp: short battery voltage response")
	}

	millivolts := int(binary.BigEndian.Uint16(resp[0:2]))
	flags := resp[2]

	if isADC {
		if flags&0x01 == 0 {
			return 0, false, fmt.Errorf("hidpp: no valid battery reading available")
		}
		charging = flags&0x02 != 0
	} else {
		charging = flags&0x80 != 0
	}

	return EstimateBatteryPercent(millivolts), charging, nil
}

func isChargingStatus(status byte) bool {
	return status == BatteryStatusRecharging ||
		status == BatteryStatusAlmostFull ||
		status == BatteryStatusFull ||
		status == BatteryStatusSlowRecharge
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

// EstimateBatteryPercent estimates a charge percentage from a millivolt
// reading via linear interpolation over batteryVoltageCurve.
func EstimateBatteryPercent(millivolts int) int {
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
