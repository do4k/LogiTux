package device

import (
	"errors"
	"testing"

	"logitux/internal/hid"
)

type fakeBackend struct {
	infos    map[uint16][]hid.Info // keyed by productID
	openErrs map[string]error      // keyed by path
}

func (f *fakeBackend) Enumerate(vendorID, productID uint16) ([]hid.Info, error) {
	return f.infos[productID], nil
}

func (f *fakeBackend) Open(info hid.Info) (hid.Writer, error) {
	if err, ok := f.openErrs[info.Path]; ok {
		return nil, err
	}
	return nil, nil
}

type fakeDevice struct {
	info Info
}

func (d *fakeDevice) Info() Info   { return d.info }
func (d *fakeDevice) Close() error { return nil }

func TestDiscoverOpensMatchingDevices(t *testing.T) {
	plugins = nil // reset global registry between tests
	Register(0x046d, []uint16{0xc900, 0xc901}, func(backend hid.Backend, info hid.Info) (Device, error) {
		return &fakeDevice{info: Info{Name: "Test Light", Serial: info.Serial, VendorID: info.VendorID, ProductID: info.ProductID}}, nil
	})

	backend := &fakeBackend{
		infos: map[uint16][]hid.Info{
			0xc900: {{Path: "/dev/hidraw0", VendorID: 0x046d, ProductID: 0xc900, Serial: "B"}},
			0xc901: {{Path: "/dev/hidraw1", VendorID: 0x046d, ProductID: 0xc901, Serial: "A"}},
		},
	}

	devices, errs := Discover(backend)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}
	// Sorted by serial: "A" before "B".
	if devices[0].Info().Serial != "A" || devices[1].Info().Serial != "B" {
		t.Errorf("expected devices sorted by serial, got %s, %s", devices[0].Info().Serial, devices[1].Info().Serial)
	}
}

func TestDiscoverCollectsOpenErrorsWithoutFailingOthers(t *testing.T) {
	plugins = nil
	Register(0x046d, []uint16{0xc900}, func(backend hid.Backend, info hid.Info) (Device, error) {
		if info.Path == "/dev/hidraw0" {
			return nil, errors.New("permission denied")
		}
		return &fakeDevice{info: Info{Name: "Test Light", Serial: info.Serial}}, nil
	})

	backend := &fakeBackend{
		infos: map[uint16][]hid.Info{
			0xc900: {
				{Path: "/dev/hidraw0", VendorID: 0x046d, ProductID: 0xc900, Serial: "BAD"},
				{Path: "/dev/hidraw1", VendorID: 0x046d, ProductID: 0xc900, Serial: "GOOD"},
			},
		},
	}

	devices, errs := Discover(backend)
	if len(devices) != 1 || devices[0].Info().Serial != "GOOD" {
		t.Fatalf("expected 1 device (GOOD), got %+v", devices)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
}
