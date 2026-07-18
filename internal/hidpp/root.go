package hidpp

import "fmt"

// Root feature (0x0000) function numbers. The Root feature is always at
// feature index 0, so these calls never need a prior lookup.
const (
	rootFuncGetFeature         byte = 0x00
	rootFuncGetProtocolVersion byte = 0x10
)

// GetFeatureIndex asks the Root feature which feature index implements
// featureID on deviceIndex. ok is false if the device doesn't support that
// feature at all (a legitimate, expected outcome — not an error).
func GetFeatureIndex(c *Conn, deviceIndex byte, featureID uint16) (featureIndex byte, ok bool, err error) {
	params := []byte{byte(featureID >> 8), byte(featureID)}
	resp, err := c.Call(deviceIndex, RootFeatureIndex, rootFuncGetFeature, params)
	if err != nil {
		return 0, false, err
	}
	if len(resp) < 1 || resp[0] == 0 {
		return 0, false, nil
	}
	return resp[0], true, nil
}

// Ping confirms deviceIndex is present and speaks HID++ 2.0, returning its
// protocol version. It's also the standard way to probe which device
// indices behind a receiver currently have a paired device.
func Ping(c *Conn, deviceIndex byte) (major, minor byte, err error) {
	resp, err := c.Call(deviceIndex, RootFeatureIndex, rootFuncGetProtocolVersion, []byte{0x00, 0x00, 0xaa})
	if err != nil {
		return 0, 0, err
	}
	if len(resp) < 2 {
		return 0, 0, fmt.Errorf("hidpp: short ping response from device 0x%02x", deviceIndex)
	}
	return resp[0], resp[1], nil
}
