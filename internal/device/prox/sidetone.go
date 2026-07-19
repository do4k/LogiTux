package prox

import "fmt"

// SIDETONE (0x8300) function numbers (both are HID++'s defaults: read 0,
// write 0x10 — this feature doesn't override them).
const (
	sidetoneFuncGet byte = 0x00
	sidetoneFuncSet byte = 0x10
)

// Sidetone implements device.SidetoneControl.
func (h *Headset) Sidetone() (int, error) {
	if h.sidetoneFeatureIndex == 0 {
		return 0, fmt.Errorf("prox: device has no sidetone feature")
	}
	resp, err := h.conn.Call(h.deviceIndex, h.sidetoneFeatureIndex, sidetoneFuncGet, nil)
	if err != nil {
		return 0, fmt.Errorf("prox: get sidetone: %w", err)
	}
	if len(resp) < 1 {
		return 0, fmt.Errorf("prox: short sidetone response")
	}
	return int(resp[0]), nil
}

// SetSidetone implements device.SidetoneControl.
func (h *Headset) SetSidetone(percent int) error {
	if h.sidetoneFeatureIndex == 0 {
		return fmt.Errorf("prox: device has no sidetone feature")
	}
	percent = clamp(percent, 0, 100)
	if _, err := h.conn.Call(h.deviceIndex, h.sidetoneFeatureIndex, sidetoneFuncSet, []byte{byte(percent)}); err != nil {
		return fmt.Errorf("prox: set sidetone: %w", err)
	}
	return nil
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
