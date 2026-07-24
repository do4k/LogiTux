//go:build linux

// Package webcam is the device plugin for Logitech UVC webcams (C920
// family, C922, C930e). Unlike LogiTux's other plugins these are not
// HID devices — camera controls live behind the Video4Linux2 API — so
// the plugin registers a device.Discoverer that scans
// /sys/class/video4linux rather than a hidraw vendor/product match, and
// talks to /dev/videoN through internal/v4l2.
//
// Controls (zoom, pan/tilt, focus, exposure, image tuning) are queried
// from the driver per device, not assumed — the GUI renders whatever is
// reported, exactly like the headset equalizer's band layout. It also
// implements device.Previewer, streaming a live preview over the same
// fd via internal/v4l2's mmap capture support.
package webcam

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"logitux/internal/device"
	"logitux/internal/v4l2"
)

const vendorLogitech = 0x046d

// Known Logitech UVC webcams. Purely a product-name/allowlist table;
// every control range still comes from the driver.
var productNames = map[uint16]string{
	0x082d: "C920",
	0x0892: "C920 HD Pro",
	0x085c: "C922 Pro Stream",
	0x0843: "C930e",
}

// Overridable in tests.
var (
	classPath = "/sys/class/video4linux"
	devPath   = "/dev"

	openControlDevice = func(path string) (controlDevice, error) { return v4l2.Open(path) }
)

// controlDevice is the slice of *v4l2.Device the plugin uses, as an
// interface so tests can fake the kernel side.
type controlDevice interface {
	QueryCap() (card string, deviceCaps uint32, err error)
	QueryControl(id uint32) (v4l2.ControlInfo, error)
	Get(id uint32) (int32, error)
	Set(id uint32, value int32) error
	Close() error

	SetFormat(width, height int, pixelFormat uint32) (v4l2.Format, error)
	GetFormat() (v4l2.Format, error)
	RequestBuffers(count int) (int, error)
	MapBuffer(index int) ([]byte, error)
	UnmapBuffer(mem []byte) error
	QueueBuffer(index int) error
	DequeueBuffer() (index, bytesUsed int, err error)
	StreamOn() error
	StreamOff() error
}

// previewBufferCount is how many mmap buffers the capture ring uses;
// the driver may grant fewer.
const previewBufferCount = 4

func init() {
	device.RegisterDiscoverer(discover)
}

// candidateControls is every control LogiTux knows how to render, in
// display order. Names are the stable identifiers the GUI keys on (see
// device.CameraControl).
var candidateControls = []struct {
	name string
	id   uint32
}{
	{"Zoom", v4l2.CtrlZoomAbsolute},
	{"Pan", v4l2.CtrlPanAbsolute},
	{"Tilt", v4l2.CtrlTiltAbsolute},
	{"Auto Focus", v4l2.CtrlFocusAuto},
	{"Focus", v4l2.CtrlFocusAbsolute},
	{"Auto Exposure", v4l2.CtrlExposureAuto},
	{"Exposure", v4l2.CtrlExposureAbsolute},
	{"Brightness", v4l2.CtrlBrightness},
	{"Contrast", v4l2.CtrlContrast},
	{"Saturation", v4l2.CtrlSaturation},
	{"Sharpness", v4l2.CtrlSharpness},
}

// discover enumerates candidate video nodes cheaply (reading each node's
// modalias to match a known Logitech camera) and defers the actual open —
// which does the V4L2 QUERYCAP/QUERYCTRL ioctls — to each Candidate's Open
// thunk, so a node LogiTux already holds isn't reopened on every tick.
func discover() ([]device.Candidate, []error) {
	entries, err := os.ReadDir(classPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("webcam: read %s: %w", classPath, err)}
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var candidates []device.Candidate
	for _, name := range names {
		vendor, product, ok := usbIDs(filepath.Join(classPath, name, "device", "modalias"))
		if !ok || vendor != vendorLogitech {
			continue
		}
		productName, known := productNames[product]
		if !known {
			continue
		}

		name, vendor, product, productName := name, vendor, product, productName
		candidates = append(candidates, device.Candidate{
			Key: filepath.Join(devPath, name),
			Open: func() (device.Device, error) {
				w, err := open(name, vendor, product, productName)
				if err != nil {
					return nil, fmt.Errorf("webcam: open %s: %w", name, err)
				}
				if w == nil {
					return nil, nil // e.g. a UVC metadata node, not the capture interface
				}
				return w, nil
			},
		})
	}
	return candidates, nil
}

// usbIDs parses a sysfs modalias like "usb:v046Dp085Cd0016..." into its
// vendor and product IDs.
func usbIDs(modaliasPath string) (vendor, product uint16, ok bool) {
	data, err := os.ReadFile(modaliasPath)
	if err != nil {
		return 0, 0, false
	}
	s := strings.TrimSpace(string(data))
	if !strings.HasPrefix(s, "usb:v") || len(s) < len("usb:vXXXXpXXXX") || s[9] != 'p' {
		return 0, 0, false
	}
	v, err1 := strconv.ParseUint(s[5:9], 16, 16)
	p, err2 := strconv.ParseUint(s[10:14], 16, 16)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return uint16(v), uint16(p), true
}

// open opens one videoN node, returning (nil, nil) for nodes that
// belong to a matching camera but aren't its video-capture interface —
// UVC devices also expose e.g. metadata nodes, same shape as the
// multi-node situation hidraw plugins handle via OpenFunc.
func open(node string, vendor, product uint16, productName string) (*Webcam, error) {
	d, err := openControlDevice(filepath.Join(devPath, node))
	if err != nil {
		return nil, err
	}

	_, caps, err := d.QueryCap()
	if err != nil {
		d.Close()
		return nil, err
	}
	if caps&v4l2.CapVideoCapture == 0 {
		d.Close()
		return nil, nil
	}

	w := &Webcam{
		dev: d,
		ids: make(map[string]uint32, len(candidateControls)),
		info: device.Info{
			Name:      productName,
			Kind:      device.KindWebcam,
			Serial:    serialFor(node),
			VendorID:  vendor,
			ProductID: product,
		},
	}
	for _, c := range candidateControls {
		ci, err := d.QueryControl(c.id)
		if err != nil {
			continue // this unit doesn't have the control; not an error
		}
		ctrl := device.CameraControl{
			Name:    c.name,
			Min:     int(ci.Min),
			Max:     int(ci.Max),
			Step:    int(ci.Step),
			Default: int(ci.Default),
		}
		if c.id == v4l2.CtrlExposureAuto {
			// The driver reports this as a menu (1=manual,
			// 3=aperture-priority auto); present it as a boolean.
			ctrl.Min, ctrl.Max, ctrl.Step, ctrl.Default = 0, 1, 1, 1
		}
		w.controls = append(w.controls, ctrl)
		w.ids[c.name] = c.id
	}
	if len(w.controls) == 0 {
		d.Close()
		return nil, fmt.Errorf("no usable camera controls")
	}
	return w, nil
}

// serialFor reads the USB device's serial from sysfs. The videoN
// device symlink points at the USB *interface* directory; the serial
// lives one level up on the USB device itself. Falls back to the node
// name so the device still has a stable-enough identity within a boot.
func serialFor(node string) string {
	data, err := os.ReadFile(filepath.Join(classPath, node, "device", "..", "serial"))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return node
	}
	return strings.TrimSpace(string(data))
}

// Webcam is a device.Device for a single connected Logitech UVC webcam.
type Webcam struct {
	dev      controlDevice
	info     device.Info
	controls []device.CameraControl
	ids      map[string]uint32

	// previewMu guards the streaming state below, since the capture
	// goroutine started by StartPreview writes frame/frameSeq while the
	// GUI's preview ticker concurrently calls Frame.
	previewMu sync.Mutex
	streaming bool
	buffers   [][]byte
	frame     image.Image
	frameSeq  uint64
	stopped   chan struct{} // closed by capture() when it exits
}

var _ device.CameraControlSet = (*Webcam)(nil)
var _ device.Previewer = (*Webcam)(nil)

func (w *Webcam) Info() device.Info { return w.info }

func (w *Webcam) Close() error {
	w.StopPreview()
	return w.dev.Close()
}

func (w *Webcam) CameraControls() []device.CameraControl {
	return append([]device.CameraControl(nil), w.controls...)
}

func (w *Webcam) CameraControl(name string) (int, error) {
	id, ok := w.ids[name]
	if !ok {
		return 0, fmt.Errorf("webcam: %s has no %q control", w.info.Name, name)
	}
	v, err := w.dev.Get(id)
	if err != nil {
		return 0, err
	}
	if id == v4l2.CtrlExposureAuto {
		if v == v4l2.ExposureManual {
			return 0, nil
		}
		return 1, nil
	}
	return int(v), nil
}

func (w *Webcam) SetCameraControl(name string, value int) error {
	id, ok := w.ids[name]
	if !ok {
		return fmt.Errorf("webcam: %s has no %q control", w.info.Name, name)
	}
	if id == v4l2.CtrlExposureAuto {
		if value == 0 {
			value = v4l2.ExposureManual
		} else {
			value = v4l2.ExposureAperturePrio
		}
	}
	return w.dev.Set(id, int32(value))
}

// StartPreview begins capturing frames for a live preview, negotiating
// MJPEG (falling back to whatever the driver substitutes, e.g. YUYV —
// see decodeFrame) at device.PreviewWidth x device.PreviewHeight and
// setting up an mmap buffer ring. Idempotent: the GUI's page rebuilds
// every few seconds while a webcam's page is open, and this is a no-op
// on every call after the first.
func (w *Webcam) StartPreview() error {
	w.previewMu.Lock()
	defer w.previewMu.Unlock()
	if w.streaming {
		return nil
	}

	format, err := w.dev.SetFormat(device.PreviewWidth, device.PreviewHeight, v4l2.PixFmtMJPEG)
	if err != nil {
		// Some drivers — and V4L2 compatibility shims some desktops put
		// in front of the real device, e.g. PipeWire's — refuse to
		// renegotiate the format at all (ENOTTY), but still report
		// whatever's already active via G_FMT. Use that rather than
		// giving up outright, if it's something we can decode.
		fallback, gErr := w.dev.GetFormat()
		if gErr != nil {
			return fmt.Errorf("webcam: set preview format on %s: %w", w.info.Name, err)
		}
		format = fallback
	}
	if format.PixelFormat != v4l2.PixFmtMJPEG && format.PixelFormat != v4l2.PixFmtYUYV {
		return fmt.Errorf("webcam: %s has no usable preview pixel format (negotiated %#x)", w.info.Name, format.PixelFormat)
	}

	granted, err := w.dev.RequestBuffers(previewBufferCount)
	if err != nil {
		return fmt.Errorf("webcam: request preview buffers on %s: %w", w.info.Name, err)
	}
	if granted == 0 {
		return fmt.Errorf("webcam: %s granted no preview buffers", w.info.Name)
	}

	buffers := make([][]byte, granted)
	for i := range buffers {
		mem, err := w.dev.MapBuffer(i)
		if err != nil {
			releaseBuffers(w.dev, buffers[:i])
			return fmt.Errorf("webcam: map preview buffer %d on %s: %w", i, w.info.Name, err)
		}
		buffers[i] = mem
		if err := w.dev.QueueBuffer(i); err != nil {
			releaseBuffers(w.dev, buffers)
			return fmt.Errorf("webcam: queue preview buffer %d on %s: %w", i, w.info.Name, err)
		}
	}

	if err := w.dev.StreamOn(); err != nil {
		releaseBuffers(w.dev, buffers)
		return fmt.Errorf("webcam: stream on %s: %w", w.info.Name, err)
	}

	w.buffers = buffers
	w.streaming = true
	w.stopped = make(chan struct{})
	go w.capture(format, w.buffers, w.stopped)
	return nil
}

// releaseBuffers unmaps every non-nil buffer, best-effort, for
// StartPreview's rollback on a mid-setup failure.
func releaseBuffers(dev controlDevice, buffers [][]byte) {
	for _, b := range buffers {
		if b != nil {
			dev.UnmapBuffer(b)
		}
	}
}

// capture runs on its own goroutine for the lifetime of one preview
// session: dequeue a filled buffer, decode it, requeue, repeat. It
// exits when DequeueBuffer errors, which is what StopPreview's
// StreamOff call causes to happen to a pending DQBUF — see StreamOff's
// doc comment in internal/v4l2. buffers and format are passed in
// (rather than read from w) so this goroutine never has to touch
// w.buffers, which StopPreview clears and reassigns.
func (w *Webcam) capture(format v4l2.Format, buffers [][]byte, stopped chan struct{}) {
	defer close(stopped)
	for {
		index, n, err := w.dev.DequeueBuffer()
		if err != nil {
			return
		}
		if index >= 0 && index < len(buffers) {
			if img, err := decodeFrame(buffers[index][:n], format); err == nil {
				w.previewMu.Lock()
				w.frame = img
				w.frameSeq++
				w.previewMu.Unlock()
			}
		}
		if err := w.dev.QueueBuffer(index); err != nil {
			return
		}
	}
}

// StopPreview halts capture and releases the streaming buffers. Safe
// to call whether or not a preview is running.
func (w *Webcam) StopPreview() {
	w.previewMu.Lock()
	if !w.streaming {
		w.previewMu.Unlock()
		return
	}
	w.streaming = false
	stopped := w.stopped
	buffers := w.buffers
	w.buffers = nil
	w.frame = nil
	w.previewMu.Unlock()

	if err := w.dev.StreamOff(); err != nil {
		log.Printf("webcam: stream off %s: %v", w.info.Name, err)
	}
	<-stopped // wait for capture() to stop touching buffers before unmapping them
	releaseBuffers(w.dev, buffers)
	if _, err := w.dev.RequestBuffers(0); err != nil {
		log.Printf("webcam: release preview buffers on %s: %v", w.info.Name, err)
	}
}

// Frame returns the most recently decoded preview frame and a sequence
// number that changes with each new one, or (nil, 0) before the first
// frame has decoded.
func (w *Webcam) Frame() (image.Image, uint64) {
	w.previewMu.Lock()
	defer w.previewMu.Unlock()
	return w.frame, w.frameSeq
}

// decodeFrame turns one captured frame's raw bytes into an image.Image,
// according to whichever pixel format StartPreview actually negotiated.
func decodeFrame(data []byte, format v4l2.Format) (image.Image, error) {
	switch format.PixelFormat {
	case v4l2.PixFmtMJPEG:
		return jpeg.Decode(bytes.NewReader(data))
	case v4l2.PixFmtYUYV:
		return decodeYUYV(data, format.Width, format.Height)
	default:
		return nil, fmt.Errorf("webcam: unsupported preview pixel format %#x", format.PixelFormat)
	}
}

// decodeYUYV converts a packed YUYV (YUY2) frame — two pixels per
// 4-byte macropixel, sharing one Cb/Cr sample — into an image.YCbCr,
// for the drivers/resolutions that don't offer MJPEG.
func decodeYUYV(data []byte, width, height int) (image.Image, error) {
	if width <= 0 || height <= 0 || len(data) < width*height*2 {
		return nil, fmt.Errorf("webcam: short YUYV frame: %d bytes for %dx%d", len(data), width, height)
	}
	img := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio422)
	for y := 0; y < height; y++ {
		row := data[y*width*2:]
		for x := 0; x+1 < width; x += 2 {
			o := x * 2
			y0, u, y1, v := row[o], row[o+1], row[o+2], row[o+3]
			img.Y[img.YOffset(x, y)] = y0
			img.Y[img.YOffset(x+1, y)] = y1
			ci := img.COffset(x, y)
			img.Cb[ci] = u
			img.Cr[ci] = v
		}
	}
	return img, nil
}
