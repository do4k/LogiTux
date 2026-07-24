package gpro

import (
	"encoding/binary"
	"fmt"

	"logitux/internal/hidpp"
)

// EXTENDED_ADJUSTABLE_DPI (0x2202) function numbers. This feature is the
// successor to ADJUSTABLE_DPI (0x2201): newer gaming mice (the PRO X
// Superlight family and onward) implement it *instead of* 0x2201, so
// without it they'd expose no DPI control at all. On top of a single
// per-sensor DPI it adds independent X/Y DPI and lift-off distance, and
// describes the supported DPI as a mix of fixed values and stepped
// ranges. Byte layouts verified against OpenLogi's independently written
// implementation (github.com/AprilNEA/OpenLogi, extended_dpi), the same
// way the rest of this package was verified against Solaar.
const (
	extDPIFuncGetSensorCapabilities byte = 0x10
	extDPIFuncGetDpiRanges          byte = 0x20
	extDPIFuncGetDpiParameters      byte = 0x50
	extDPIFuncSetDpiParameters      byte = 0x60
)

// getSensorCapabilities capability bits.
const (
	extDPICapHasY byte = 1 << 0 // sensor supports an independent Y-axis DPI
)

// extDPIRangeHyphen tags a word in the supported-DPI stream as a range
// step ("hyphen") rather than a literal DPI value: the preceding literal
// is the range's low end, the following literal its high end, and the
// low 13 bits of the tagged word are the step.
const extDPIRangeHyphen uint16 = 0b111 << 13

// extDPIMaxRangePages bounds how many getSensorDpiRanges pages are
// fetched before the device is considered to be returning a malformed,
// unterminated list.
const extDPIMaxRangePages = 16

// extDPIDirectionX selects the X axis in range/list queries. The GUI
// exposes one DPI value, applied to both axes, so Y's ranges (identical
// on known sensors) are never queried.
const extDPIDirectionX byte = 0

// extDPIInfo is what discoverExtendedDPI learns about the sensor.
type extDPIInfo struct {
	hasY           bool
	min, max, step int
}

// discoverExtendedDPI reads the sensor's capabilities and its supported
// DPI range. The range comes back as a stream of 16-bit words — fixed
// values and step-tagged ranges, 0x0000-terminated, possibly split
// across pages — which is collapsed to the min/max/step shape
// device.DPIControl wants.
func discoverExtendedDPI(conn *hidpp.Conn, deviceIndex, featureIndex byte) (extDPIInfo, error) {
	caps, err := conn.Call(deviceIndex, featureIndex, extDPIFuncGetSensorCapabilities, []byte{dpiSensorIndex})
	if err != nil {
		return extDPIInfo{}, fmt.Errorf("gpro: get extended DPI sensor capabilities: %w", err)
	}
	if len(caps) < 3 {
		return extDPIInfo{}, fmt.Errorf("gpro: short extended DPI capabilities response")
	}
	info := extDPIInfo{hasY: caps[2]&extDPICapHasY != 0}

	var stream []byte
	for page := 0; page < extDPIMaxRangePages; page++ {
		resp, err := conn.Call(deviceIndex, featureIndex, extDPIFuncGetDpiRanges,
			[]byte{dpiSensorIndex, extDPIDirectionX, byte(page)})
		if err != nil {
			return extDPIInfo{}, fmt.Errorf("gpro: get extended DPI ranges (page %d): %w", page, err)
		}
		// The response echoes the addressing before the page body; verify
		// it so a mismatched page can't corrupt the accumulated stream.
		if len(resp) < 4 || resp[0] != dpiSensorIndex || resp[1] != extDPIDirectionX || resp[2] != byte(page) {
			return extDPIInfo{}, fmt.Errorf("gpro: unexpected extended DPI ranges response")
		}
		stream = append(stream, resp[3:]...)

		min, max, step, done, err := parseExtendedDPIRanges(stream)
		if err != nil {
			return extDPIInfo{}, err
		}
		if done {
			info.min, info.max, info.step = min, max, step
			return info, nil
		}
	}
	return extDPIInfo{}, fmt.Errorf("gpro: extended DPI range list never terminated")
}

// parseExtendedDPIRanges decodes the supported-DPI word stream. done is
// false (with no error) while the 0x0000 terminator hasn't arrived yet,
// telling the caller to fetch another page.
func parseExtendedDPIRanges(stream []byte) (min, max, step int, done bool, err error) {
	var words []uint16
	terminated := false
	for off := 0; off+2 <= len(stream); off += 2 {
		w := binary.BigEndian.Uint16(stream[off : off+2])
		if w == 0 {
			terminated = true
			break
		}
		words = append(words, w)
	}
	if !terminated {
		return 0, 0, 0, false, nil
	}

	note := func(v int) {
		if min == 0 || v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	pending := 0
	hasPending := false
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w >= extDPIRangeHyphen {
			s := int(w &^ extDPIRangeHyphen)
			if !hasPending || s == 0 || i+1 >= len(words) || words[i+1] >= extDPIRangeHyphen {
				return 0, 0, 0, false, fmt.Errorf("gpro: malformed extended DPI range stream")
			}
			to := int(words[i+1])
			if to < pending {
				return 0, 0, 0, false, fmt.Errorf("gpro: malformed extended DPI range stream")
			}
			note(to)
			if step == 0 || s < step {
				step = s
			}
			pending = to
			i++
		} else {
			note(int(w))
			pending = int(w)
			hasPending = true
		}
	}

	if max == 0 {
		return 0, 0, 0, false, fmt.Errorf("gpro: empty extended DPI range list")
	}
	if step == 0 {
		// A list of fixed values only; 50 is the granularity every known
		// gaming sensor uses, and it only shapes the GUI's slider anyway.
		step = 50
	}
	return min, max, step, true, nil
}

// extendedDPI reads the sensor's current X-axis DPI (falling back to its
// default when no override is set, mirroring the 0x2201 path).
func (m *Mouse) extendedDPI() (int, error) {
	resp, err := m.conn.Call(m.deviceIndex, m.extDPIFeatureIndex, extDPIFuncGetDpiParameters, []byte{dpiSensorIndex})
	if err != nil {
		return 0, fmt.Errorf("gpro: get extended DPI: %w", err)
	}
	// [sensor, dpiX:2, defaultX:2, dpiY:2, defaultY:2, lod]
	if len(resp) < 10 {
		return 0, fmt.Errorf("gpro: short extended DPI response")
	}
	current := int(binary.BigEndian.Uint16(resp[1:3]))
	if current == 0 {
		current = int(binary.BigEndian.Uint16(resp[3:5]))
	}
	return current, nil
}

// setExtendedDPI writes dpi to the X axis (and the Y axis too when the
// sensor has one — the GUI exposes a single linked value, like G HUB's
// default). The set call writes DPI and lift-off distance together, so
// the current LOD is read first and preserved.
func (m *Mouse) setExtendedDPI(dpi int) error {
	dpi = clampToStep(dpi, m.extDPI.min, m.extDPI.max, m.extDPI.step)

	var lod byte
	if resp, err := m.conn.Call(m.deviceIndex, m.extDPIFeatureIndex, extDPIFuncGetDpiParameters, []byte{dpiSensorIndex}); err == nil && len(resp) >= 10 {
		lod = resp[9]
	}

	y := 0
	if m.extDPI.hasY {
		y = dpi
	}
	params := []byte{
		dpiSensorIndex,
		byte(dpi >> 8), byte(dpi),
		byte(y >> 8), byte(y),
		lod,
	}
	if _, err := m.conn.Call(m.deviceIndex, m.extDPIFeatureIndex, extDPIFuncSetDpiParameters, params); err != nil {
		return fmt.Errorf("gpro: set extended DPI: %w", err)
	}
	return nil
}
