//go:build linux

// Package webcam is the device plugin for Logitech UVC webcams (C920
// family, C922, C930e). Unlike LogiTux's other plugins these are not
// HID devices — camera controls live behind the Video4Linux2 API — so
// the plugin registers a device.Discoverer that scans
// /sys/class/video4linux rather than a hidraw vendor/product match, and
// talks to /dev/videoN through internal/v4l2.
//
// Only controls are supported (zoom, pan/tilt, focus, exposure, image
// tuning); there is no video preview. Which controls exist is queried
// from the driver per device, not assumed — the GUI renders whatever is
// reported, exactly like the headset equalizer's band layout.
package webcam

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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
}

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

func discover() ([]device.Device, []error) {
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

	var devices []device.Device
	var errs []error
	for _, name := range names {
		vendor, product, ok := usbIDs(filepath.Join(classPath, name, "device", "modalias"))
		if !ok || vendor != vendorLogitech {
			continue
		}
		productName, known := productNames[product]
		if !known {
			continue
		}

		w, err := open(name, vendor, product, productName)
		if err != nil {
			errs = append(errs, fmt.Errorf("webcam: open %s: %w", name, err))
			continue
		}
		if w != nil {
			devices = append(devices, w)
		}
	}
	return devices, errs
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
}

var _ device.CameraControlSet = (*Webcam)(nil)

func (w *Webcam) Info() device.Info { return w.info }
func (w *Webcam) Close() error      { return w.dev.Close() }

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
