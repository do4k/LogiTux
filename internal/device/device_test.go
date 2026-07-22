package device

import (
	"errors"
	"sort"
	"testing"

	"logitux/internal/hid"
)

// openAll resolves the candidates from Discover into opened devices,
// mirroring what appState.refresh does: skip nil results, collect open
// errors, and sort the survivors by serial.
func openAll(candidates []Candidate) ([]Device, []error) {
	var devices []Device
	var errs []error
	for _, c := range candidates {
		d, err := c.Open()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if d == nil {
			continue
		}
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Info().Serial < devices[j].Info().Serial
	})
	return devices, errs
}

type fakeBackend struct {
	infos    map[uint16][]hid.Info // keyed by productID
	openErrs map[string]error      // keyed by path
}

func (f *fakeBackend) Enumerate(vendorID, productID uint16) ([]hid.Info, error) {
	return f.infos[productID], nil
}

func (f *fakeBackend) Open(info hid.Info) (hid.Handle, error) {
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

	candidates, errs := Discover(backend)
	devices, openErrs := openAll(candidates)
	errs = append(errs, openErrs...)
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

	candidates, errs := Discover(backend)
	devices, openErrs := openAll(candidates)
	errs = append(errs, openErrs...)
	if len(devices) != 1 || devices[0].Info().Serial != "GOOD" {
		t.Fatalf("expected 1 device (GOOD), got %+v", devices)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
}

func TestDiscoverDefersOpen(t *testing.T) {
	plugins = nil
	opens := 0
	Register(0x046d, []uint16{0xc900}, func(backend hid.Backend, info hid.Info) (Device, error) {
		opens++
		return &fakeDevice{info: Info{Serial: info.Serial}}, nil
	})

	backend := &fakeBackend{infos: map[uint16][]hid.Info{
		0xc900: {
			{Path: "/dev/hidraw0", VendorID: 0x046d, ProductID: 0xc900, Serial: "X"},
			{Path: "/dev/hidraw1", VendorID: 0x046d, ProductID: 0xc900, Serial: "Y"},
		},
	}}

	candidates, errs := Discover(backend)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	// The whole point of the candidate split: enumerating must not open
	// anything (that's what let refresh reopen a wireless device every
	// tick and hang). Opening happens only on demand.
	if opens != 0 {
		t.Fatalf("Discover opened %d devices before Open was called; want 0", opens)
	}
	if _, err := candidates[0].Open(); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("after one Open, opens = %d; want 1", opens)
	}
}

func TestDiscoverSkipsNilNilWithoutError(t *testing.T) {
	plugins = nil
	// Simulates a multi-interface physical device (e.g. a receiver): only
	// the canonical node (hidraw0) should produce a device.
	Register(0x046d, []uint16{0xc539}, func(backend hid.Backend, info hid.Info) (Device, error) {
		if info.Path != "/dev/hidraw0" {
			return nil, nil
		}
		return &fakeDevice{info: Info{Name: "Test Receiver", Serial: info.Serial}}, nil
	})

	backend := &fakeBackend{
		infos: map[uint16][]hid.Info{
			0xc539: {
				{Path: "/dev/hidraw0", VendorID: 0x046d, ProductID: 0xc539, Serial: "R1"},
				{Path: "/dev/hidraw1", VendorID: 0x046d, ProductID: 0xc539, Serial: "R1"},
				{Path: "/dev/hidraw2", VendorID: 0x046d, ProductID: 0xc539, Serial: "R1"},
			},
		},
	}

	candidates, errs := Discover(backend)
	devices, openErrs := openAll(candidates)
	errs = append(errs, openErrs...)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device (the rest skipped via nil,nil), got %d", len(devices))
	}
}
