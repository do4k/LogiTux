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

type fakeLightDevice struct{ fakeDevice }

func TestGeneratePreview(t *testing.T) {
	if os.Getenv("LOGITUX_PREVIEW") == "" {
		t.Skip("preview generation only")
	}
	a := test.NewApp()
	a.Settings().SetTheme(ghubTheme{})

	devices := []device.Device{
		&fakeBattDevice{fakeDevice{info: device.Info{Name: "G Pro Wireless", Kind: device.KindMouse, Serial: "A1B2C3"}, percent: 93, charging: true}},
		&fakeLightDevice{fakeDevice{info: device.Info{Name: "Litra Glow", Kind: device.KindLight, Serial: "D4E5F6"}}},
		&fakeDevice{info: device.Info{Name: "Litra Beam", Kind: device.KindLight, Serial: "D4E5F7"}},
		&fakeHeadsetDevice{fakeDevice{info: device.Info{Name: "PRO X Wireless Gaming Headset", Kind: device.KindHeadset, Serial: "G7H8I9"}, percent: 44}},
		&fakeWebcamDevice{fakeDevice: fakeDevice{info: device.Info{Name: "C922 Pro Stream", Kind: device.KindWebcam, Serial: "J1K2L3"}}, values: map[string]int{"Zoom": 130}},
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

	// Litra page, with saved state so the power pill reads ON and the
	// showcase halo is lit.
	litra := devices[1]
	if err := store.Set(litra.Info().Serial, config.DeviceState{Power: true, Brightness: 65, Temperature: 5000}); err != nil {
		t.Fatal(err)
	}
	state.selectedSerial = litra.Info().Serial
	w3 := test.NewWindow(buildMainView(state, devices))
	w3.Resize(fyne.NewSize(980, 620))
	save(t, w3.Canvas().Capture(), "preview-litra.png")

	state.selectedSerial = devices[3].Info().Serial
	w4 := test.NewWindow(buildMainView(state, devices))
	w4.Resize(fyne.NewSize(980, 620))
	save(t, w4.Canvas().Capture(), "preview-headset.png")

	state.selectedSerial = devices[4].Info().Serial
	w5 := test.NewWindow(buildMainView(state, devices))
	w5.Resize(fyne.NewSize(980, 620))
	save(t, w5.Canvas().Capture(), "preview-webcam.png")
}

func (f *fakeLightDevice) SetPower(bool) error          { return nil }
func (f *fakeLightDevice) SetBrightness(int) error      { return nil }
func (f *fakeLightDevice) SetTemperature(int) error     { return nil }
func (f *fakeLightDevice) TemperatureRange() (int, int) { return 2700, 6500 }

type fakeHeadsetDevice struct{ fakeDevice }

func (f *fakeHeadsetDevice) Battery() (int, bool, error) { return f.percent, f.charging, nil }
func (f *fakeHeadsetDevice) Sidetone() (int, error)      { return 30, nil }
func (f *fakeHeadsetDevice) SetSidetone(int) error       { return nil }
func (f *fakeHeadsetDevice) EqualizerBands() []device.EqualizerBand {
	bands := make([]device.EqualizerBand, 0, 10)
	for _, hz := range []int{32, 64, 125, 250, 500, 1000, 2000, 4000, 8000, 16000} {
		bands = append(bands, device.EqualizerBand{FrequencyHz: hz})
	}
	return bands
}
func (f *fakeHeadsetDevice) EqualizerRange() (int, int) { return -12, 12 }
func (f *fakeHeadsetDevice) EqualizerLevels() ([]int, error) {
	return []int{0, 0, 0, 2, 4, 6, 6, 7, 7, 7}, nil
}
func (f *fakeHeadsetDevice) SetEqualizerLevels([]int) error { return nil }
func (f *fakeHeadsetDevice) NoiseReduction() (bool, error)  { return true, nil }
func (f *fakeHeadsetDevice) SetNoiseReduction(bool) error   { return nil }

type fakeWebcamDevice struct {
	fakeDevice
	values map[string]int
}

func (f *fakeWebcamDevice) CameraControls() []device.CameraControl {
	return []device.CameraControl{
		{Name: "Zoom", Min: 100, Max: 500, Step: 1, Default: 100},
		{Name: "Pan", Min: -36000, Max: 36000, Step: 3600},
		{Name: "Tilt", Min: -36000, Max: 36000, Step: 3600},
		{Name: "Auto Focus", Min: 0, Max: 1, Step: 1, Default: 1},
		{Name: "Focus", Min: 0, Max: 250, Step: 5},
		{Name: "Auto Exposure", Min: 0, Max: 1, Step: 1, Default: 1},
		{Name: "Exposure", Min: 3, Max: 2047, Step: 1, Default: 250},
		{Name: "Brightness", Min: 0, Max: 255, Step: 1, Default: 128},
		{Name: "Contrast", Min: 0, Max: 255, Step: 1, Default: 128},
		{Name: "Saturation", Min: 0, Max: 255, Step: 1, Default: 128},
		{Name: "Sharpness", Min: 0, Max: 255, Step: 1, Default: 128},
	}
}

func (f *fakeWebcamDevice) CameraControl(name string) (int, error) {
	if v, ok := f.values[name]; ok {
		return v, nil
	}
	for _, c := range f.CameraControls() {
		if c.Name == name {
			return c.Default, nil
		}
	}
	return 0, nil
}

func (f *fakeWebcamDevice) SetCameraControl(name string, value int) error {
	if f.values == nil {
		f.values = map[string]int{}
	}
	f.values[name] = value
	return nil
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
