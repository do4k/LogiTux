package gpro

import (
	"testing"
	"time"

	"logitux/internal/hid"
)

func init() {
	probeTimeout = 20 * time.Millisecond
}

// fakeBackend simulates enough of hid.Backend to exercise openViaReceiver
// and openDirect: multiple hidraw nodes can share a serial (as a
// receiver's USB interfaces do), and each node's Open call is wired to
// its own responder so only the "real" HID++ interface answers.
type fakeBackend struct {
	infos     map[uint16][]hid.Info
	responder map[string]func([]byte) []byte // keyed by Info.Path
}

func (b *fakeBackend) Enumerate(vendorID, productID uint16) ([]hid.Info, error) {
	return b.infos[productID], nil
}

func (b *fakeBackend) Open(info hid.Info) (hid.Handle, error) {
	h := newFakeHandle()
	h.responder = b.responder[info.Path]
	return h, nil
}

func TestOpenViaReceiverFindsTheRightInterfaceAndDeviceIndex(t *testing.T) {
	fm := newFakeMouse()

	backend := &fakeBackend{
		infos: map[uint16][]hid.Info{
			productReceiver: {
				{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"},
				{Path: "/dev/hidraw1", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"},
				{Path: "/dev/hidraw2", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"},
			},
		},
		responder: map[string]func([]byte) []byte{
			// hidraw0/1 are present but don't speak HID++ (e.g. plain boot
			// mouse/keyboard interfaces): they never answer.
			"/dev/hidraw0": func([]byte) []byte { return nil },
			"/dev/hidraw1": func([]byte) []byte { return nil },
			// hidraw2 is the real HID++ interface, with the mouse paired
			// at device index 3.
			"/dev/hidraw2": func(req []byte) []byte {
				if req[1] != 0x03 {
					return nil // nothing paired at other indices
				}
				return fm.respond(req)
			},
		},
	}

	// device.Discover would call open() once per matching hid.Info; only
	// the canonical (lowest-path) one should do real work per the (nil,
	// nil)-skip contract, so simulate that here directly.
	d, err := open(backend, hid.Info{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if d == nil {
		t.Fatal("expected the canonical node to open a device")
	}
	defer d.Close()

	if d.Info().Name != "G Pro Wireless" {
		t.Errorf("unexpected info: %+v", d.Info())
	}
	if d.Info().Serial != "RCV1-3" {
		t.Errorf("expected composite serial RCV1-3, got %q", d.Info().Serial)
	}
}

// TestOpenViaReceiverPrefersKernelExposedSerial covers a real-world case:
// some kernels expose a second, virtual hidraw node for the same physical
// mouse (product 0x4079) purely for identification, with a real
// HID_UNIQ-derived serial even when the receiver itself has none. That
// should be preferred over the receiver-serial-plus-index composite.
func TestOpenViaReceiverPrefersKernelExposedSerial(t *testing.T) {
	fm := newFakeMouse()

	backend := &fakeBackend{
		infos: map[uint16][]hid.Info{
			productReceiver: {
				{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productReceiver, Serial: ""}, // no USB serial, as observed on real Lightspeed receivers
			},
			// Kernel-exposed virtual per-device node, not used for I/O here,
			// only consulted for its Serial.
			productWired: {
				{Path: "/dev/hidraw10", VendorID: vendorID, ProductID: productWired, Serial: "5d-93-7b-24"},
			},
		},
		responder: map[string]func([]byte) []byte{
			"/dev/hidraw0": func(req []byte) []byte {
				if req[1] != 0x01 {
					return nil
				}
				return fm.respond(req)
			},
		},
	}

	d, err := open(backend, hid.Info{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productReceiver, Serial: ""})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if d.Info().Serial != "5d-93-7b-24" {
		t.Errorf("expected the kernel-exposed virtual node's serial, got %q", d.Info().Serial)
	}
}

func TestOpenViaReceiverSkipsNonCanonicalSiblings(t *testing.T) {
	backend := &fakeBackend{
		infos: map[uint16][]hid.Info{
			productReceiver: {
				{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"},
				{Path: "/dev/hidraw1", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"},
			},
		},
	}

	d, err := open(backend, hid.Info{Path: "/dev/hidraw1", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if d != nil {
		t.Fatal("expected the non-canonical sibling to skip via (nil, nil)")
	}
}

func TestOpenViaReceiverErrorsWhenNothingResponds(t *testing.T) {
	backend := &fakeBackend{
		infos: map[uint16][]hid.Info{
			productReceiver: {
				{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"},
			},
		},
		responder: map[string]func([]byte) []byte{
			"/dev/hidraw0": func([]byte) []byte { return nil },
		},
	}

	_, err := open(backend, hid.Info{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productReceiver, Serial: "RCV1"})
	if err == nil {
		t.Fatal("expected an error when no device index responds")
	}
}

func TestOpenDirectWiredMouse(t *testing.T) {
	fm := newFakeMouse()
	backend := &fakeBackend{
		responder: map[string]func([]byte) []byte{
			"/dev/hidraw0": func(req []byte) []byte {
				if req[1] != 0xff {
					return nil
				}
				return fm.respond(req)
			},
		},
	}

	d, err := open(backend, hid.Info{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productWired, Serial: "DIRECTSN"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if d.Info().Serial != "DIRECTSN" {
		t.Errorf("expected direct connection to use the real USB serial, got %q", d.Info().Serial)
	}
}

func TestOpenRejectsUnexpectedProductID(t *testing.T) {
	backend := &fakeBackend{}
	_, err := open(backend, hid.Info{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: 0x9999})
	if err == nil {
		t.Fatal("expected an error for an unrecognized product ID")
	}
}
