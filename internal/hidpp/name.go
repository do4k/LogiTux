package hidpp

import "fmt"

// FeatureDeviceTypeAndName is DEVICE_TYPE_AND_NAME (0x0005): the
// device's own marketing name, readable in fixed-size chunks.
const FeatureDeviceTypeAndName uint16 = 0x0005

const (
	nameFuncGetCount byte = 0x00 // -> [nameLength]
	nameFuncGetName  byte = 0x10 // [charIndex] -> up to 16 name bytes
)

// maxDeviceNameLen guards against a device reporting an implausible name
// length and this loop issuing dozens of calls for it.
const maxDeviceNameLen = 64

// GetDeviceName reads the device's marketing name (e.g.
// "PRO X SUPERLIGHT") via DEVICE_TYPE_AND_NAME, paging through
// getDeviceName until the reported length is collected. Returns an error
// if the device doesn't expose the feature; callers treat the name as a
// nice-to-have and fall back to a product-ID-derived name.
func GetDeviceName(conn *Conn, deviceIndex byte) (string, error) {
	idx, ok, err := GetFeatureIndex(conn, deviceIndex, FeatureDeviceTypeAndName)
	if err != nil {
		return "", fmt.Errorf("hidpp: look up DEVICE_TYPE_AND_NAME: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("hidpp: device has no DEVICE_TYPE_AND_NAME feature")
	}

	resp, err := conn.Call(deviceIndex, idx, nameFuncGetCount, nil)
	if err != nil {
		return "", fmt.Errorf("hidpp: get device name length: %w", err)
	}
	if len(resp) < 1 {
		return "", fmt.Errorf("hidpp: short device name length response")
	}
	count := int(resp[0])
	if count == 0 || count > maxDeviceNameLen {
		return "", fmt.Errorf("hidpp: implausible device name length %d", count)
	}

	name := make([]byte, 0, count)
	for len(name) < count {
		resp, err := conn.Call(deviceIndex, idx, nameFuncGetName, []byte{byte(len(name))})
		if err != nil {
			return "", fmt.Errorf("hidpp: get device name chunk: %w", err)
		}
		if len(resp) == 0 {
			return "", fmt.Errorf("hidpp: empty device name chunk")
		}
		if remaining := count - len(name); len(resp) > remaining {
			resp = resp[:remaining]
		}
		name = append(name, resp...)
	}

	// Some firmwares pad the advertised length with NULs; trim them.
	end := len(name)
	for end > 0 && name[end-1] == 0 {
		end--
	}
	if end == 0 {
		return "", fmt.Errorf("hidpp: device reported an empty name")
	}
	return string(name[:end]), nil
}
