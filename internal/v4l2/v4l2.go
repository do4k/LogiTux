//go:build linux

// Package v4l2 provides the minimal slice of the Video4Linux2 API that
// LogiTux needs to adjust webcams: querying and setting *controls*
// (zoom, pan/tilt, focus, exposure, image tuning) on /dev/videoN via
// the VIDIOC_QUERYCAP / QUERYCTRL / G_CTRL / S_CTRL ioctls, mirroring
// linux/videodev2.h. Frame capture/streaming is deliberately not
// implemented. Like internal/hid and internal/uinput, this is pure Go
// (x/sys/unix) — no libv4l, no cgo.
package v4l2

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Control IDs, from linux/v4l2-controls.h. User class is 0x00980900 + n;
// camera class is 0x009a0900 + n.
const (
	CtrlBrightness = 0x00980900 + 0
	CtrlContrast   = 0x00980900 + 1
	CtrlSaturation = 0x00980900 + 2
	CtrlSharpness  = 0x00980900 + 27

	CtrlExposureAuto     = 0x009a0900 + 1
	CtrlExposureAbsolute = 0x009a0900 + 2
	CtrlPanAbsolute      = 0x009a0900 + 8
	CtrlTiltAbsolute     = 0x009a0900 + 9
	CtrlFocusAbsolute    = 0x009a0900 + 10
	CtrlFocusAuto        = 0x009a0900 + 12
	CtrlZoomAbsolute     = 0x009a0900 + 13
)

// v4l2_exposure_auto_type menu values for CtrlExposureAuto. UVC webcams
// implement exactly these two: fully manual, and auto ("aperture
// priority", since a webcam's aperture is fixed).
const (
	ExposureManual       = 1
	ExposureAperturePrio = 3
)

// Capability flags (v4l2_capability.device_caps). A UVC webcam exposes
// several /dev/videoN nodes; only the one with CapVideoCapture set is
// the camera itself (the others are e.g. metadata nodes).
const CapVideoCapture = 0x00000001

// Control flags (v4l2_queryctrl.flags).
const flagDisabled = 0x0001

// ioctl request-code construction, mirroring the _IOC macro in
// linux/ioctl.h, so these values are derived rather than copied magic
// numbers. 'V' (0x56) is the videodev2 ioctl type.
const (
	iocWrite = 1
	iocRead  = 2
)

func vidioc(dir, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | 'V'<<8 | nr
}

// Sizes of the marshaled structs below, fixed by the kernel ABI.
const (
	sizeofCapability = 104
	sizeofQueryctrl  = 68
	sizeofControl    = 8
)

var (
	vidiocQuerycap  = vidioc(iocRead, 0, sizeofCapability)
	vidiocGCtrl     = vidioc(iocRead|iocWrite, 27, sizeofControl)
	vidiocSCtrl     = vidioc(iocRead|iocWrite, 28, sizeofControl)
	vidiocQueryctrl = vidioc(iocRead|iocWrite, 36, sizeofQueryctrl)
)

// ioctlFunc is swapped out by tests to fake the kernel side.
type ioctlFunc func(fd int, req uintptr, arg unsafe.Pointer) error

func realIoctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// ControlInfo describes one control's range as reported by the driver.
type ControlInfo struct {
	Min, Max, Step, Default int32
}

// Device is an open V4L2 device node.
type Device struct {
	f     *os.File
	ioctl ioctlFunc
}

// Open opens a /dev/videoN node for control access.
func Open(path string) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("v4l2: open %s: %w", path, err)
	}
	return &Device{f: f, ioctl: realIoctl}, nil
}

func (d *Device) Close() error { return d.f.Close() }

// QueryCap returns the device's card name and device_caps flags
// (v4l2_capability layout: driver[16], card[32], bus_info[32], version
// u32, capabilities u32, device_caps u32, reserved u32[3]).
func (d *Device) QueryCap() (card string, deviceCaps uint32, err error) {
	var buf [sizeofCapability]byte
	if err := d.ioctl(int(d.f.Fd()), vidiocQuerycap, unsafe.Pointer(&buf[0])); err != nil {
		return "", 0, fmt.Errorf("v4l2: QUERYCAP %s: %w", d.f.Name(), err)
	}
	return cString(buf[16:48]), binary.LittleEndian.Uint32(buf[88:92]), nil
}

// QueryControl reports a control's range, or an error if the device
// doesn't have it (v4l2_queryctrl layout: id u32, type u32, name[32],
// minimum s32, maximum s32, step s32, default_value s32, flags u32,
// reserved u32[2]).
func (d *Device) QueryControl(id uint32) (ControlInfo, error) {
	var buf [sizeofQueryctrl]byte
	binary.LittleEndian.PutUint32(buf[0:4], id)
	if err := d.ioctl(int(d.f.Fd()), vidiocQueryctrl, unsafe.Pointer(&buf[0])); err != nil {
		return ControlInfo{}, fmt.Errorf("v4l2: QUERYCTRL %#x on %s: %w", id, d.f.Name(), err)
	}
	if binary.LittleEndian.Uint32(buf[56:60])&flagDisabled != 0 {
		return ControlInfo{}, fmt.Errorf("v4l2: control %#x on %s is disabled", id, d.f.Name())
	}
	s32 := func(b []byte) int32 { return int32(binary.LittleEndian.Uint32(b)) }
	return ControlInfo{
		Min:     s32(buf[40:44]),
		Max:     s32(buf[44:48]),
		Step:    s32(buf[48:52]),
		Default: s32(buf[52:56]),
	}, nil
}

// Get reads a control's current value (v4l2_control layout: id u32,
// value s32).
func (d *Device) Get(id uint32) (int32, error) {
	var buf [sizeofControl]byte
	binary.LittleEndian.PutUint32(buf[0:4], id)
	if err := d.ioctl(int(d.f.Fd()), vidiocGCtrl, unsafe.Pointer(&buf[0])); err != nil {
		return 0, fmt.Errorf("v4l2: G_CTRL %#x on %s: %w", id, d.f.Name(), err)
	}
	return int32(binary.LittleEndian.Uint32(buf[4:8])), nil
}

// Set writes a control's value.
func (d *Device) Set(id uint32, value int32) error {
	var buf [sizeofControl]byte
	binary.LittleEndian.PutUint32(buf[0:4], id)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(value))
	if err := d.ioctl(int(d.f.Fd()), vidiocSCtrl, unsafe.Pointer(&buf[0])); err != nil {
		return fmt.Errorf("v4l2: S_CTRL %#x=%d on %s: %w", id, value, d.f.Name(), err)
	}
	return nil
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
