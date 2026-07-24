//go:build linux

package webcam

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// fakeControlDevice fakes the V4L2 side of one /dev/videoN node,
// including the streaming (preview) ioctls: frames pushed onto the
// frames channel are what DequeueBuffer hands back, and StreamOff
// unblocks a pending DequeueBuffer exactly like the real vb2 core does
// (see internal/v4l2's StreamOff doc comment) so StopPreview's
// wait-for-capture-to-exit logic gets real coverage.
type fakeControlDevice struct {
	caps     uint32
	controls map[uint32]v4l2.ControlInfo
	values   map[uint32]int32
	closed   bool

	// negotiatedFormat overrides the pixel format SetFormat reports back,
	// simulating a driver that substitutes something other than what was
	// requested. Zero means "report back whatever was requested".
	negotiatedFormat uint32
	setFormatErr     error
	getFormatErr     error
	activeFormat     v4l2.Format // what GetFormat reports; set by SetFormat, or directly by a test to simulate an already-streaming device

	buffers   [][]byte
	streaming bool
	streamOff chan struct{}
	frames    chan []byte
}

func (f *fakeControlDevice) SetFormat(width, height int, pixelFormat uint32) (v4l2.Format, error) {
	if f.setFormatErr != nil {
		return v4l2.Format{}, f.setFormatErr
	}
	pf := pixelFormat
	if f.negotiatedFormat != 0 {
		pf = f.negotiatedFormat
	}
	f.activeFormat = v4l2.Format{Width: width, Height: height, PixelFormat: pf, BytesPerLine: width * 2, SizeImage: width * height * 2}
	return f.activeFormat, nil
}

func (f *fakeControlDevice) GetFormat() (v4l2.Format, error) {
	if f.getFormatErr != nil {
		return v4l2.Format{}, f.getFormatErr
	}
	return f.activeFormat, nil
}

func (f *fakeControlDevice) RequestBuffers(count int) (int, error) {
	if count == 0 {
		f.buffers = nil
		return 0, nil
	}
	f.buffers = make([][]byte, count)
	for i := range f.buffers {
		f.buffers[i] = make([]byte, 1<<20)
	}
	return count, nil
}

func (f *fakeControlDevice) MapBuffer(index int) ([]byte, error) { return f.buffers[index], nil }
func (f *fakeControlDevice) UnmapBuffer(mem []byte) error        { return nil }
func (f *fakeControlDevice) QueueBuffer(index int) error         { return nil }

func (f *fakeControlDevice) DequeueBuffer() (index, bytesUsed int, err error) {
	select {
	case data := <-f.frames:
		return 0, copy(f.buffers[0], data), nil
	case <-f.streamOff:
		return 0, 0, errors.New("v4l2: stream off")
	}
}

func (f *fakeControlDevice) StreamOn() error {
	f.streaming = true
	f.streamOff = make(chan struct{})
	return nil
}

func (f *fakeControlDevice) StreamOff() error {
	f.streaming = false
	close(f.streamOff)
	return nil
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

func openWebcam(t *testing.T, fake *fakeControlDevice) *Webcam {
	t.Helper()
	root := t.TempDir()
	writeSysfs(t, root, "video0", "usb:v046Dp085Cd0016...", "S1")
	withFakes(t, root, map[string]*fakeControlDevice{"video0": fake})
	devices, errs := openAll2(discover())
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(devices) != 1 {
		t.Fatal("no device")
	}
	return devices[0].(*Webcam)
}

func newFakeControlDevice() *fakeControlDevice {
	return &fakeControlDevice{
		caps: v4l2.CapVideoCapture, controls: c922Controls(), values: map[uint32]int32{},
		frames: make(chan []byte, 4),
	}
}

func encodeTestJPEG(t *testing.T, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, size, size)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPreviewLifecycle(t *testing.T) {
	fake := newFakeControlDevice()
	w := openWebcam(t, fake)

	if img, seq := w.Frame(); img != nil || seq != 0 {
		t.Fatalf("Frame before StartPreview = (%v, %d), want (nil, 0)", img, seq)
	}

	if err := w.StartPreview(); err != nil {
		t.Fatal(err)
	}
	// Idempotent: the GUI calls this on every page rebuild (every few
	// seconds) while a webcam's page stays open, not just once.
	if err := w.StartPreview(); err != nil {
		t.Fatal(err)
	}
	if !fake.streaming {
		t.Fatal("StartPreview didn't stream on")
	}

	fake.frames <- encodeTestJPEG(t, 8)

	var img image.Image
	deadline := time.Now().Add(2 * time.Second)
	for img == nil && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
		img, _ = w.Frame()
	}
	if img == nil {
		t.Fatal("timed out waiting for a decoded frame")
	}
	if _, seq := w.Frame(); seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}
	if img.Bounds().Dx() != 8 || img.Bounds().Dy() != 8 {
		t.Errorf("decoded bounds = %v, want 8x8", img.Bounds())
	}

	w.StopPreview()
	w.StopPreview() // idempotent
	if fake.streaming {
		t.Error("StopPreview left the device streaming")
	}
	if img, _ := w.Frame(); img != nil {
		t.Error("Frame after StopPreview should be nil")
	}

	// Close on an already-stopped preview must not hang or panic.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !fake.closed {
		t.Error("Close didn't close the underlying device")
	}
}

func TestStartPreviewSetFormatError(t *testing.T) {
	fake := newFakeControlDevice()
	fake.setFormatErr = errors.New("EINVAL")
	w := openWebcam(t, fake)
	if err := w.StartPreview(); err == nil {
		t.Error("expected an error")
	}
}

// TestStartPreviewFallsBackToGetFormat covers the case seen with a
// PipeWire-mediated /dev/videoN: SetFormat is refused outright (ENOTTY),
// but the device is already streaming some usable format, reported by
// GetFormat, that StartPreview should use instead of giving up.
func TestStartPreviewFallsBackToGetFormat(t *testing.T) {
	fake := newFakeControlDevice()
	fake.setFormatErr = errors.New("inappropriate ioctl for device")
	fake.activeFormat = v4l2.Format{Width: 1280, Height: 720, PixelFormat: v4l2.PixFmtMJPEG, BytesPerLine: 2560, SizeImage: 1280 * 720 * 2}
	w := openWebcam(t, fake)

	if err := w.StartPreview(); err != nil {
		t.Fatalf("StartPreview: %v", err)
	}
	defer w.StopPreview()

	fake.frames <- encodeTestJPEG(t, 8)
	var img image.Image
	deadline := time.Now().Add(2 * time.Second)
	for img == nil && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
		img, _ = w.Frame()
	}
	if img == nil {
		t.Fatal("timed out waiting for a decoded frame")
	}
}

// TestStartPreviewFallbackAlsoUnusable covers G_FMT reporting a pixel
// format LogiTux can't decode (e.g. a compressed format other than
// MJPEG): StartPreview should still fail rather than silently limping
// along with no frames.
func TestStartPreviewFallbackAlsoUnusable(t *testing.T) {
	fake := newFakeControlDevice()
	fake.setFormatErr = errors.New("inappropriate ioctl for device")
	fake.activeFormat = v4l2.Format{Width: 1280, Height: 720, PixelFormat: 0x34363248 /* H264 */}
	w := openWebcam(t, fake)
	if err := w.StartPreview(); err == nil {
		t.Error("expected an error")
	}
}

func TestStartPreviewUnsupportedFormat(t *testing.T) {
	fake := newFakeControlDevice()
	fake.negotiatedFormat = 0x44454144 // "DEAD", not MJPEG or YUYV
	w := openWebcam(t, fake)
	if err := w.StartPreview(); err == nil {
		t.Error("expected an error for an unsupported negotiated pixel format")
	}
}

func TestDecodeYUYV(t *testing.T) {
	// One row of two YUYV macropixels: (Y0,U,Y1,V) x2.
	data := []byte{
		10, 20, 30, 40, 50, 60, 70, 80,
		11, 21, 31, 41, 51, 61, 71, 81,
	}
	img, err := decodeFrame(data, v4l2.Format{Width: 4, Height: 2, PixelFormat: v4l2.PixFmtYUYV})
	if err != nil {
		t.Fatal(err)
	}
	ycbcr, ok := img.(*image.YCbCr)
	if !ok {
		t.Fatalf("got %T, want *image.YCbCr", img)
	}
	if got := ycbcr.YCbCrAt(0, 0); got.Y != 10 || got.Cb != 20 || got.Cr != 40 {
		t.Errorf("pixel (0,0) = %+v", got)
	}
	if got := ycbcr.YCbCrAt(1, 0); got.Y != 30 || got.Cb != 20 || got.Cr != 40 {
		t.Errorf("pixel (1,0) (shares chroma with (0,0)) = %+v", got)
	}
	if got := ycbcr.YCbCrAt(2, 1); got.Y != 51 || got.Cb != 61 || got.Cr != 81 {
		t.Errorf("pixel (2,1) = %+v", got)
	}
	if got := ycbcr.YCbCrAt(3, 1); got.Y != 71 || got.Cb != 61 || got.Cr != 81 {
		t.Errorf("pixel (3,1) = %+v", got)
	}
}

func TestDecodeYUYVShortFrame(t *testing.T) {
	_, err := decodeFrame([]byte{1, 2, 3}, v4l2.Format{Width: 4, Height: 2, PixelFormat: v4l2.PixFmtYUYV})
	if err == nil {
		t.Error("short frame should error")
	}
}
