package hidpp

import "testing"

// fakeNameDevice simulates DEVICE_TYPE_AND_NAME (0x0005) at a fixed
// feature index, chunked the way real firmware does: getName returns up
// to 16 bytes starting at the requested offset.
func nameResponder(featureIndex byte, name string) func([]byte) []byte {
	return func(req []byte) []byte {
		deviceIndex, reqFeatureIndex, funcSwID := req[1], req[2], req[3]
		function := funcSwID & 0xf0
		params := req[4:]

		resp := make([]byte, 20)
		resp[0], resp[1], resp[2], resp[3] = req[0], deviceIndex, reqFeatureIndex, funcSwID

		switch reqFeatureIndex {
		case RootFeatureIndex:
			if function == 0x00 {
				featureID := uint16(params[0])<<8 | uint16(params[1])
				if featureID == FeatureDeviceTypeAndName {
					resp[4] = featureIndex
				}
				return resp
			}
		case featureIndex:
			switch function {
			case nameFuncGetCount & 0xf0:
				resp[4] = byte(len(name))
				return resp
			case nameFuncGetName & 0xf0:
				offset := int(params[0])
				if offset < len(name) {
					copy(resp[4:], name[offset:])
				}
				return resp
			}
		}
		resp = make([]byte, 7)
		resp[0], resp[1], resp[2], resp[3], resp[4], resp[5] = 0x10, deviceIndex, 0xff, reqFeatureIndex, funcSwID, 0x02
		return resp
	}
}

func TestGetDeviceName(t *testing.T) {
	h := newFakeHandle()
	h.responder = nameResponder(0x09, "PRO X SUPERLIGHT 2")
	conn := Open(h)
	defer conn.Close()

	name, err := GetDeviceName(conn, DeviceIndexDirect)
	if err != nil {
		t.Fatalf("GetDeviceName: %v", err)
	}
	if name != "PRO X SUPERLIGHT 2" {
		t.Errorf("GetDeviceName() = %q, want %q", name, "PRO X SUPERLIGHT 2")
	}
}

func TestGetDeviceNameSpansMultipleChunks(t *testing.T) {
	// Longer than one 16-byte getName response, so GetDeviceName must
	// issue more than one call and stitch the pieces together.
	long := "A Very Long Product Name Indeed"
	if len(long) <= 16 {
		t.Fatalf("test name too short to exercise chunking: %d bytes", len(long))
	}

	h := newFakeHandle()
	h.responder = nameResponder(0x09, long)
	conn := Open(h)
	defer conn.Close()

	name, err := GetDeviceName(conn, DeviceIndexDirect)
	if err != nil {
		t.Fatalf("GetDeviceName: %v", err)
	}
	if name != long {
		t.Errorf("GetDeviceName() = %q, want %q", name, long)
	}
}

func TestGetDeviceNameUnsupported(t *testing.T) {
	h := newFakeHandle()
	h.responder = func(req []byte) []byte {
		// No feature ever resolves (Root always answers index 0 = "not found").
		resp := make([]byte, 20)
		resp[0], resp[1], resp[2], resp[3] = req[0], req[1], req[2], req[3]
		return resp
	}
	conn := Open(h)
	defer conn.Close()

	if _, err := GetDeviceName(conn, DeviceIndexDirect); err == nil {
		t.Fatal("expected an error when the device has no DEVICE_TYPE_AND_NAME feature")
	}
}
