package prox

import (
	"encoding/binary"
	"fmt"

	"logitux/internal/device"
	"logitux/internal/hidpp"
)

// EQUALIZER (0x8310) function numbers.
const (
	eqFuncGetInfo            byte = 0x00
	eqFuncGetBandFrequencies byte = 0x10
	eqFuncGetLevels          byte = 0x20
	eqFuncSetLevels          byte = 0x30
	eqFuncGetNoiseReduction  byte = 0x40
	eqFuncSetNoiseReduction  byte = 0x50
)

// The get/set-levels calls take a leading selector byte. Originally these
// were cargo-culted as "required prefix" from the reference implementation
// this plugin was verified against; OpenLogi's 0x8310 implementation
// documents their actual meaning:
//
//   - get: a gain *location* — 0 reads the custom EQ stored in EEPROM,
//     1 reads the active EQ in RAM.
//   - set: a gain *persistence* — 0 applies to RAM only (volatile),
//     1 applies to RAM and stores to EEPROM, 2 stores to EEPROM only.
//
// The values themselves are unchanged from what was verified against real
// hardware.
const (
	eqGainLocationEEPROM      byte = 0x00
	eqPersistNonVolatileWrite byte = 0x02
)

// discoverEqualizer reads the device's fixed band layout (frequencies)
// and dB range. Band levels themselves are read live by EqualizerLevels,
// not cached here, since they can change on the device side (e.g. a
// physical control, or another app) between calls.
func discoverEqualizer(conn *hidpp.Conn, featureIndex byte) (bands []device.EqualizerBand, minDB, maxDB int, err error) {
	info, err := conn.Call(hidpp.DeviceIndexDirect, featureIndex, eqFuncGetInfo, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("prox: get equalizer info: %w", err)
	}
	if len(info) < 5 {
		return nil, 0, 0, fmt.Errorf("prox: short equalizer info response")
	}
	count := int(info[0])
	dbRange := int(int8(info[1]))
	minDB = int(int8(info[3]))
	maxDB = int(int8(info[4]))
	if minDB == 0 {
		minDB = -dbRange
	}
	if maxDB == 0 {
		maxDB = dbRange
	}

	bands = make([]device.EqualizerBand, 0, count)
	for group := 0; group*7 < count; group++ {
		resp, err := conn.Call(hidpp.DeviceIndexDirect, featureIndex, eqFuncGetBandFrequencies, []byte{byte(group * 7)})
		if err != nil {
			return nil, 0, 0, fmt.Errorf("prox: get equalizer band frequencies (group %d): %w", group, err)
		}
		for b := 0; b < 7; b++ {
			bandIndex := group*7 + b
			if bandIndex >= count {
				break
			}
			offset := 1 + 2*b
			if offset+2 > len(resp) {
				return nil, 0, 0, fmt.Errorf("prox: short equalizer band frequency response")
			}
			freq := int(binary.BigEndian.Uint16(resp[offset : offset+2]))
			bands = append(bands, device.EqualizerBand{FrequencyHz: freq})
		}
	}

	return bands, minDB, maxDB, nil
}

// EqualizerBands implements device.EqualizerControl.
func (h *Headset) EqualizerBands() []device.EqualizerBand {
	return h.eqBands
}

// EqualizerRange implements device.EqualizerControl.
func (h *Headset) EqualizerRange() (min, max int) {
	return h.eqMinDB, h.eqMaxDB
}

// EqualizerLevels implements device.EqualizerControl.
func (h *Headset) EqualizerLevels() ([]int, error) {
	if h.eqFeatureIndex == 0 {
		return nil, fmt.Errorf("prox: device has no equalizer feature")
	}
	resp, err := h.conn.Call(h.deviceIndex, h.eqFeatureIndex, eqFuncGetLevels, []byte{eqGainLocationEEPROM})
	if err != nil {
		return nil, fmt.Errorf("prox: get equalizer levels: %w", err)
	}
	count := len(h.eqBands)
	if len(resp) < count {
		return nil, fmt.Errorf("prox: short equalizer levels response")
	}
	levels := make([]int, count)
	for i := 0; i < count; i++ {
		levels[i] = int(int8(resp[i]))
	}
	return levels, nil
}

// NoiseReduction implements device.NoiseReductionControl: whether the
// headset's hardware microphone noise reduction is enabled. Functions
// 4/5 of the same EQUALIZER feature; units whose firmware predates them
// answer with a HID++ error, which callers treat as "not supported"
// (the GUI probes with a read and hides the toggle on error).
func (h *Headset) NoiseReduction() (bool, error) {
	if h.eqFeatureIndex == 0 {
		return false, fmt.Errorf("prox: device has no equalizer feature")
	}
	resp, err := h.conn.Call(h.deviceIndex, h.eqFeatureIndex, eqFuncGetNoiseReduction, nil)
	if err != nil {
		return false, fmt.Errorf("prox: get noise reduction: %w", err)
	}
	if len(resp) < 1 {
		return false, fmt.Errorf("prox: short noise reduction response")
	}
	return resp[0] != 0, nil
}

// SetNoiseReduction implements device.NoiseReductionControl.
func (h *Headset) SetNoiseReduction(enabled bool) error {
	if h.eqFeatureIndex == 0 {
		return fmt.Errorf("prox: device has no equalizer feature")
	}
	var v byte
	if enabled {
		v = 1
	}
	if _, err := h.conn.Call(h.deviceIndex, h.eqFeatureIndex, eqFuncSetNoiseReduction, []byte{v}); err != nil {
		return fmt.Errorf("prox: set noise reduction: %w", err)
	}
	return nil
}

// SetEqualizerLevels implements device.EqualizerControl.
func (h *Headset) SetEqualizerLevels(levelsDB []int) error {
	if h.eqFeatureIndex == 0 {
		return fmt.Errorf("prox: device has no equalizer feature")
	}
	if len(levelsDB) != len(h.eqBands) {
		return fmt.Errorf("prox: expected %d equalizer levels, got %d", len(h.eqBands), len(levelsDB))
	}

	params := make([]byte, 1+len(levelsDB))
	params[0] = eqPersistNonVolatileWrite
	for i, lvl := range levelsDB {
		lvl = clamp(lvl, h.eqMinDB, h.eqMaxDB)
		params[1+i] = byte(int8(lvl))
	}
	if _, err := h.conn.Call(h.deviceIndex, h.eqFeatureIndex, eqFuncSetLevels, params); err != nil {
		return fmt.Errorf("prox: set equalizer levels: %w", err)
	}
	return nil
}
