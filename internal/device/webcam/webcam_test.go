//go:build linux

package webcam

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"logitux/internal/device"
	"logitux/internal/v4l2"
)

// openAll2 resolves discover()'s candidates into opened devices, the way
// appState.refresh does: skip nil results and combine enumeration errors
// with per-candidate open errors. Takes discover()'s two return values so
// call sites can write openAll2(discover()).
func openAll2(candidates []device.Candidate, enumErrs []error) ([]device.Device, []error) {
	devices := []device.Device{}
	errs := append([]error(nil), enumErrs...)
	for _, c := range candidates {
		d, err := c.Open()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if d != nil {
			devices = append(devices, d)
		}
	}
	return devices, errs
}

// fakeControlDevice fakes the V4L2 side of one /dev/videoN node.
type fakeControlDevice struct {
	caps     uint32
	controls map[uint32]v4l2.ControlInfo
	values   map[uint32]int32
	closed   bool
}

func (f *fakeControlDevice) QueryCap() (string, uint32, error) {
	return "C922 Pro Stream Webcam", f.caps, nil
}
func (f *fakeControlDevice) QueryControl(id uint32) (v4l2.ControlInfo, error) {
	ci, ok := f.controls[id]
	if !ok {
		return v4l2.ControlInfo{}, errors.New("EINVAL")
	}
	return ci, nil
}
func (f *fakeControlDevice) Get(id uint32) (int32, error) {
	v, ok := f.values[id]
	if !ok {
		return 0, errors.New("EINVAL")
	}
	return v, nil
}
func (f *fakeControlDevice) Set(id uint32, v int32) error {
	if _, ok := f.controls[id]; !ok {
		return errors.New("EINVAL")
	}
	f.values[id] = v
	return nil
}
func (f *fakeControlDevice) Close() error { f.closed = true; return nil }

func c922Controls() map[uint32]v4l2.ControlInfo {
	return map[uint32]v4l2.ControlInfo{
		v4l2.CtrlZoomAbsolute: {Min: 100, Max: 500, Step: 1, Default: 100},
		v4l2.CtrlPanAbsolute:  {Min: -36000, Max: 36000, Step: 3600},
		v4l2.CtrlTiltAbsolute: {Min: -36000, Max: 36000, Step: 3600},
		v4l2.CtrlFocusAuto:    {Min: 0, Max: 1, Step: 1, Default: 1},
		v4l2.CtrlExposureAuto: {Min: 0, Max: 3, Step: 1, Default: 3},
		v4l2.CtrlBrightness:   {Min: 0, Max: 255, Step: 1, Default: 128},
	}
}

// writeSysfs fabricates a /sys/class/video4linux tree with one videoN
// entry whose USB device has the given modalias and serial.
func writeSysfs(t *testing.T, root, node, modalias, serial string) {
	t.Helper()
	// The real layout reaches the serial via device/../serial; using a
	// real directory (not a symlink) keeps the test simple while
	// exercising the same relative path.
	usbDir := filepath.Join(root, node)
	ifaceDir := filepath.Join(usbDir, "device")
	if err := os.MkdirAll(ifaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ifaceDir, "modalias"), []byte(modalias+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if serial != "" {
		if err := os.WriteFile(filepath.Join(usbDir, "serial"), []byte(serial+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func withFakes(t *testing.T, root string, devs map[string]*fakeControlDevice) {
	t.Helper()
	oldClass, oldOpen := classPath, openControlDevice
	classPath = root
	openControlDevice = func(path string) (controlDevice, error) {
		d, ok := devs[filepath.Base(path)]
		if !ok {
			return nil, errors.New("no such device")
		}
		return d, nil
	}
	t.Cleanup(func() { classPath, openControlDevice = oldClass, oldOpen })
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "video0", "usb:v046Dp085Cd0016dc00...", "ABC123")
	writeSysfs(t, root, "video1", "usb:v046Dp085Cd0016dc00...", "ABC123") // metadata node
	writeSysfs(t, root, "video2", "usb:v1BCFp2C99d0001...", "")           // non-Logitech

	withFakes(t, root, map[string]*fakeControlDevice{
		"video0": {caps: v4l2.CapVideoCapture, controls: c922Controls(), values: map[uint32]int32{}},
		"video1": {caps: 0x00800000 /* metadata */, controls: c922Controls(), values: map[uint32]int32{}},
	})

	devices, errs := openAll2(discover())
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(devices) != 1 {
		t.Fatalf("found %d devices, want 1", len(devices))
	}
	info := devices[0].Info()
	if info.Name != "C922 Pro Stream" || info.Kind != "webcam" || info.Serial != "ABC123" {
		t.Errorf("info = %+v", info)
	}
	if info.VendorID != 0x046d || info.ProductID != 0x085c {
		t.Errorf("ids = %04x:%04x", info.VendorID, info.ProductID)
	}
}

func TestDiscoverNoSysfs(t *testing.T) {
	withFakes(t, filepath.Join(t.TempDir(), "missing"), nil)
	devices, errs := openAll2(discover())
	if len(devices) != 0 || len(errs) != 0 {
		t.Errorf("devices=%v errs=%v, want none", devices, errs)
	}
}

func TestControls(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "video0", "usb:v046Dp085Cd0016...", "S1")
	fake := &fakeControlDevice{caps: v4l2.CapVideoCapture, controls: c922Controls(), values: map[uint32]int32{
		v4l2.CtrlZoomAbsolute: 100,
		v4l2.CtrlExposureAuto: v4l2.ExposureAperturePrio,
	}}
	withFakes(t, root, map[string]*fakeControlDevice{"video0": fake})

	devices, _ := openAll2(discover())
	if len(devices) != 1 {
		t.Fatal("no device")
	}
	w := devices[0].(*Webcam)

	controls := w.CameraControls()
	byName := map[string]bool{}
	for _, c := range controls {
		byName[c.Name] = true
	}
	for _, want := range []string{"Zoom", "Pan", "Tilt", "Auto Focus", "Auto Exposure", "Brightness"} {
		if !byName[want] {
			t.Errorf("missing control %q in %v", want, controls)
		}
	}
	if byName["Focus"] || byName["Exposure"] {
		t.Errorf("controls the fake doesn't expose should be absent: %v", controls)
	}

	// Auto Exposure is presented as a boolean over the driver's menu.
	for _, c := range controls {
		if c.Name == "Auto Exposure" && (c.Min != 0 || c.Max != 1 || c.Default != 1) {
			t.Errorf("Auto Exposure range = %+v", c)
		}
	}
	if v, err := w.CameraControl("Auto Exposure"); err != nil || v != 1 {
		t.Errorf("Auto Exposure = %d, %v; want 1", v, err)
	}
	if err := w.SetCameraControl("Auto Exposure", 0); err != nil {
		t.Fatal(err)
	}
	if fake.values[v4l2.CtrlExposureAuto] != v4l2.ExposureManual {
		t.Errorf("raw exposure_auto = %d, want manual (%d)", fake.values[v4l2.CtrlExposureAuto], v4l2.ExposureManual)
	}

	if err := w.SetCameraControl("Zoom", 250); err != nil {
		t.Fatal(err)
	}
	if v, err := w.CameraControl("Zoom"); err != nil || v != 250 {
		t.Errorf("Zoom = %d, %v; want 250", v, err)
	}

	if _, err := w.CameraControl("Nonexistent"); err == nil {
		t.Error("unknown control name should error")
	}
}

func TestSerialFallsBackToNode(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "video0", "usb:v046Dp082Dd0011...", "")
	withFakes(t, root, map[string]*fakeControlDevice{
		"video0": {caps: v4l2.CapVideoCapture, controls: c922Controls(), values: map[uint32]int32{}},
	})
	devices, _ := openAll2(discover())
	if len(devices) != 1 {
		t.Fatal("no device")
	}
	if got := devices[0].Info(); got.Serial != "video0" || got.Name != "C920" {
		t.Errorf("info = %+v", got)
	}
}
