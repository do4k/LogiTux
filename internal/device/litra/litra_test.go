package litra

import (
	"bytes"
	"io"
	"testing"

	"logitux/internal/device"
	"logitux/internal/hid"
)

type fakeWriter struct {
	writes [][]byte
	closed bool
}

func (w *fakeWriter) Write(data []byte) (int, error) {
	cp := make([]byte, len(data))
	copy(cp, data)
	w.writes = append(w.writes, cp)
	return len(data), nil
}

func (w *fakeWriter) Close() error {
	w.closed = true
	return nil
}

// Read is never called by the Litra plugin (it's write-only), but is
// required to satisfy hid.Handle.
func (w *fakeWriter) Read(data []byte) (int, error) {
	return 0, io.EOF
}

type fakeBackend struct {
	writer *fakeWriter
}

func (b *fakeBackend) Enumerate(vendorID, productID uint16) ([]hid.Info, error) { return nil, nil }
func (b *fakeBackend) Open(info hid.Info) (hid.Handle, error)                   { return b.writer, nil }

func newTestLight(t *testing.T) (*Light, *fakeWriter) {
	t.Helper()
	w := &fakeWriter{}
	d, err := open(&fakeBackend{writer: w}, hid.Info{
		Path:      "/dev/hidraw0",
		VendorID:  vendorID,
		ProductID: productGlow,
		Serial:    "SN1",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	light, ok := d.(*Light)
	if !ok {
		t.Fatalf("expected *Light, got %T", d)
	}
	return light, w
}

func TestOpenSetsInfo(t *testing.T) {
	light, _ := newTestLight(t)
	info := light.Info()
	if info.Name != "Litra Glow" || info.Serial != "SN1" || info.ProductID != productGlow {
		t.Errorf("unexpected info: %+v", info)
	}
}

func TestSetPowerOnOff(t *testing.T) {
	light, w := newTestLight(t)

	if err := light.SetPower(true); err != nil {
		t.Fatalf("SetPower(true): %v", err)
	}
	want := []byte{0x11, 0xff, 0x04, 0x1c, 0x01}
	got := w.writes[len(w.writes)-1]
	if !bytes.Equal(got[:len(want)], want) || len(got) != 20 {
		t.Errorf("SetPower(true) report = % x, want prefix % x padded to 20 bytes", got, want)
	}

	if err := light.SetPower(false); err != nil {
		t.Fatalf("SetPower(false): %v", err)
	}
	want = []byte{0x11, 0xff, 0x04, 0x1c, 0x00}
	got = w.writes[len(w.writes)-1]
	if !bytes.Equal(got[:len(want)], want) {
		t.Errorf("SetPower(false) report = % x, want prefix % x", got, want)
	}
}

func TestSetBrightnessScalesAndClamps(t *testing.T) {
	light, w := newTestLight(t)

	cases := []struct {
		percent int
		wantRaw byte
	}{
		{0, minBrightnessRaw},
		{100, maxBrightnessRaw},
		{-10, minBrightnessRaw}, // clamped
		{200, maxBrightnessRaw}, // clamped
		{50, minBrightnessRaw + byte((maxBrightnessRaw-minBrightnessRaw)/2)},
	}
	for _, c := range cases {
		if err := light.SetBrightness(c.percent); err != nil {
			t.Fatalf("SetBrightness(%d): %v", c.percent, err)
		}
		got := w.writes[len(w.writes)-1]
		want := []byte{0x11, 0xff, 0x04, 0x4c, 0x00, c.wantRaw}
		if !bytes.Equal(got[:len(want)], want) {
			t.Errorf("SetBrightness(%d) report = % x, want prefix % x", c.percent, got, want)
		}
	}
}

func TestSetTemperatureEncodesBigEndianAndClamps(t *testing.T) {
	light, w := newTestLight(t)

	if err := light.SetTemperature(4000); err != nil {
		t.Fatalf("SetTemperature(4000): %v", err)
	}
	got := w.writes[len(w.writes)-1]
	want := []byte{0x11, 0xff, 0x04, 0x9c, 0x0f, 0xa0} // 4000 = 0x0FA0
	if !bytes.Equal(got[:len(want)], want) {
		t.Errorf("SetTemperature(4000) report = % x, want prefix % x", got, want)
	}

	if err := light.SetTemperature(100); err != nil {
		t.Fatalf("SetTemperature(100): %v", err)
	}
	got = w.writes[len(w.writes)-1]
	if got[4] != 0x0a || got[5] != 0x8c { // clamped to 2700 = 0x0A8C
		t.Errorf("SetTemperature(100) should clamp to %d, got % x", minTemperatureK, got)
	}

	if err := light.SetTemperature(10000); err != nil {
		t.Fatalf("SetTemperature(10000): %v", err)
	}
	got = w.writes[len(w.writes)-1]
	if got[4] != 0x19 || got[5] != 0x64 { // clamped to 6500 = 0x1964
		t.Errorf("SetTemperature(10000) should clamp to %d, got % x", maxTemperatureK, got)
	}
}

func TestTemperatureRange(t *testing.T) {
	light, _ := newTestLight(t)
	min, max := light.TemperatureRange()
	if min != 2700 || max != 6500 {
		t.Errorf("TemperatureRange() = (%d, %d), want (2700, 6500)", min, max)
	}
}

func TestCloseClosesConnection(t *testing.T) {
	light, w := newTestLight(t)
	if err := light.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !w.closed {
		t.Error("expected underlying writer to be closed")
	}
}

var _ device.Device = (*Light)(nil)
var _ device.PowerControl = (*Light)(nil)
var _ device.BrightnessControl = (*Light)(nil)
var _ device.TemperatureControl = (*Light)(nil)
