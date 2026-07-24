package gpro

import (
	"encoding/binary"
	"fmt"
)

const (
	dpiSensorIndex byte = 0x00

	dpiFuncGetDpi byte = 0x20
	dpiFuncSetDpi byte = 0x30
)

// The G Pro Wireless has a single HERO 25K sensor supporting 100-25600 DPI
// in steps of 50. Hardcoded rather than parsed from getSensorDpiList
// (function 0x10), whose range-encoded response format is more involved
// than this plugin needs for a single known sensor.
const (
	dpiMin  = 100
	dpiMax  = 25600
	dpiStep = 50
)

// DPIRange implements device.DPIControl. Mice on the extended feature
// report their true range at open time; the classic feature's range is
// the G Pro Wireless's known HERO span.
func (m *Mouse) DPIRange() (min, max, step int) {
	if m.extDPIFeatureIndex != 0 {
		return m.extDPI.min, m.extDPI.max, m.extDPI.step
	}
	return dpiMin, dpiMax, dpiStep
}

// DPI implements device.DPIControl, reading the sensor's current DPI live
// from the device (falling back to its default DPI if getSensorDpi
// reports no override is set).
func (m *Mouse) DPI() (int, error) {
	if m.extDPIFeatureIndex != 0 {
		return m.extendedDPI()
	}
	resp, err := m.conn.Call(m.deviceIndex, m.dpiFeatureIndex, dpiFuncGetDpi, []byte{dpiSensorIndex})
	if err != nil {
		return 0, fmt.Errorf("gpro: get DPI: %w", err)
	}
	if len(resp) < 5 {
		return 0, fmt.Errorf("gpro: short DPI response")
	}
	current := int(binary.BigEndian.Uint16(resp[1:3]))
	if current == 0 {
		current = int(binary.BigEndian.Uint16(resp[3:5]))
	}
	return current, nil
}

// SetDPI implements device.DPIControl. dpi is clamped to the sensor's
// range and rounded to the nearest supported step.
func (m *Mouse) SetDPI(dpi int) error {
	if m.extDPIFeatureIndex != 0 {
		return m.setExtendedDPI(dpi)
	}
	dpi = clampToStep(dpi, dpiMin, dpiMax, dpiStep)
	params := []byte{dpiSensorIndex, byte(dpi >> 8), byte(dpi)}
	if _, err := m.conn.Call(m.deviceIndex, m.dpiFeatureIndex, dpiFuncSetDpi, params); err != nil {
		return fmt.Errorf("gpro: set DPI: %w", err)
	}
	return nil
}

func clampToStep(v, min, max, step int) int {
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	offset := v - min
	rounded := (offset + step/2) / step * step
	return min + rounded
}
