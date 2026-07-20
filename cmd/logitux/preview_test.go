package main

// Visual-preview helper for iterating on the UI without hardware:
// renders the dashboard and a device page with fake devices to PNGs,
// using Fyne's software canvas (no GL context or display needed).
// Skipped in normal test runs; to generate the images:
//   LOGITUX_PREVIEW=<output-dir> go test ./cmd/logitux -run TestGeneratePreview

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"logitux/internal/config"
	"logitux/internal/device"
)

type fakeDevice struct {
	info     device.Info
	percent  int
	charging bool
	hasBatt  bool
}

func (f *fakeDevice) Info() device.Info { return f.info }
func (f *fakeDevice) Close() error      { return nil }

type fakeBattDevice struct{ fakeDevice }

func (f *fakeBattDevice) Battery() (int, bool, error) { return f.percent, f.charging, nil }

func TestGeneratePreview(t *testing.T) {
	if os.Getenv("LOGITUX_PREVIEW") == "" {
		t.Skip("preview generation only")
	}
	a := test.NewApp()
	a.Settings().SetTheme(ghubTheme{})

	devices := []device.Device{
		&fakeBattDevice{fakeDevice{info: device.Info{Name: "G Pro Wireless", Kind: device.KindMouse, Serial: "A1B2C3"}, percent: 93, charging: true}},
		&fakeDevice{info: device.Info{Name: "Litra Glow", Kind: device.KindLight, Serial: "D4E5F6"}},
		&fakeDevice{info: device.Info{Name: "Litra Beam", Kind: device.KindLight, Serial: "D4E5F7"}},
		&fakeBattDevice{fakeDevice{info: device.Info{Name: "PRO X Wireless Gaming Headset", Kind: device.KindHeadset, Serial: "G7H8I9"}, percent: 44}},
	}

	store, err := config.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	state := &appState{store: store, pageSection: map[string]string{}}

	w := test.NewWindow(buildMainView(state, devices))
	w.Resize(fyne.NewSize(980, 620))
	save(t, w.Canvas().Capture(), "preview-dashboard.png")

	state.selectedSerial = devices[0].Info().Serial
	w2 := test.NewWindow(buildMainView(state, devices))
	w2.Resize(fyne.NewSize(980, 620))
	save(t, w2.Canvas().Capture(), "preview-device.png")
}

func (f *fakeBattDevice) DPIRange() (int, int, int)    { return 100, 25600, 50 }
func (f *fakeBattDevice) SetDPI(int) error             { return nil }
func (f *fakeBattDevice) DPI() (int, error)            { return 1600, nil }
func (f *fakeBattDevice) ReportRateOptions() []int     { return []int{125, 250, 500, 1000} }
func (f *fakeBattDevice) SetReportRate(int) error      { return nil }
func (f *fakeBattDevice) ReportRate() (int, error)     { return 1000, nil }
func (f *fakeBattDevice) SetColor(r, g, b uint8) error { return nil }
func (f *fakeBattDevice) Buttons() ([]device.ButtonInfo, error) {
	return []device.ButtonInfo{{ID: 1, Name: "Back Button"}, {ID: 2, Name: "Forward Button"}}, nil
}
func (f *fakeBattDevice) RemapButton(uint16, uint16) error { return nil }

func save(t *testing.T, img image.Image, name string) {
	t.Helper()
	dir := os.Getenv("LOGITUX_PREVIEW")
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
