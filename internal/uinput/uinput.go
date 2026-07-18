//go:build linux

// Package uinput creates a Linux virtual input device via /dev/uinput.
// LogiTux uses this to re-emit a diverted mouse button press as whatever
// action the user configured (a different mouse button, or a keyboard
// key): once a HID++ control is diverted, the mouse itself stops sending
// its normal click and only reports the press as a raw HID++ notification
// (see internal/device/gpro's button remapping), so something has to turn
// that back into an event the rest of the desktop understands.
package uinput

import (
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	evSyn = 0x00
	evKey = 0x01

	synReport = 0

	uinputMaxNameSize = 80
	absCnt             = 64

	uinputIoctlBase = 0x55 // 'U', per linux/uinput.h
)

// ioctl request-code construction, mirroring the _IO/_IOW macros in
// linux/ioctl.h, so these values are derived rather than copied magic
// numbers.
const (
	iocNRBits   = 8
	iocTypeBits = 8
	iocSizeBits = 14

	iocNRShift   = 0
	iocTypeShift = iocNRShift + iocNRBits
	iocSizeShift = iocTypeShift + iocTypeBits
	iocDirShift  = iocSizeShift + iocSizeBits

	iocWrite = 1
)

func iocEncode(dir, typ, nr, size uint) uint {
	return (dir << iocDirShift) | (typ << iocTypeShift) | (nr << iocNRShift) | (size << iocSizeShift)
}

func ioNoArg(typ, nr uint) uint     { return iocEncode(0, typ, nr, 0) }
func iowInt(typ, nr uint) uint      { return iocEncode(iocWrite, typ, nr, 4) } // sizeof(int) on this platform

var (
	uiDevCreate  = ioNoArg(uinputIoctlBase, 1)
	uiDevDestroy = ioNoArg(uinputIoctlBase, 2)
	uiSetEvBit   = iowInt(uinputIoctlBase, 100)
	uiSetKeyBit  = iowInt(uinputIoctlBase, 101)
)

// inputID and userDev mirror linux/uinput.h's struct input_id and the
// legacy struct uinput_user_dev, used to describe and create the device
// in one ioctl-free step (versus the newer UI_DEV_SETUP ioctl, which needs
// a kernel new enough to matter less for us than broad compatibility).
type inputID struct {
	Bustype uint16
	Vendor  uint16
	Product uint16
	Version uint16
}

type userDev struct {
	Name         [uinputMaxNameSize]byte
	ID           inputID
	FFEffectsMax uint32
	Absmax       [absCnt]int32
	Absmin       [absCnt]int32
	Absfuzz      [absCnt]int32
	Absflat      [absCnt]int32
}

// timeval and inputEvent mirror linux/input.h's struct input_event as laid
// out on 64-bit Linux (amd64/arm64), where both timeval fields are 8-byte
// integers, for a 24-byte struct. The kernel doesn't require a meaningful
// timestamp from a uinput client, so these are always sent zeroed.
type timeval struct {
	Sec  int64
	Usec int64
}

type inputEvent struct {
	Time  timeval
	Type  uint16
	Code  uint16
	Value int32
}

// Device is a virtual input device that the rest of the desktop sees as
// an ordinary keyboard/mouse.
type Device struct {
	f *os.File
}

// Open creates and registers a virtual input device named name, capable
// of emitting the given key/button codes (linux/input-event-codes.h
// KEY_*/BTN_* values — see internal/uinput/keycodes.go). Call Close to
// remove the device.
func Open(name string, keyCodes []uint16) (*Device, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("uinput: open /dev/uinput: %w", err)
	}

	if err := unix.IoctlSetInt(int(f.Fd()), uiSetEvBit, evKey); err != nil {
		f.Close()
		return nil, fmt.Errorf("uinput: enable EV_KEY: %w", err)
	}
	for _, code := range keyCodes {
		if err := unix.IoctlSetInt(int(f.Fd()), uiSetKeyBit, int(code)); err != nil {
			f.Close()
			return nil, fmt.Errorf("uinput: enable key 0x%x: %w", code, err)
		}
	}

	dev := userDev{
		// A distinct vendor/product (rather than reusing Logitech's 0x046d
		// products) so this virtual device is never confused with a real
		// Logitech peripheral by anything inspecting connected hardware.
		ID: inputID{Bustype: 0x03, Vendor: 0x0001, Product: 0x0001, Version: 1}, // BUS_USB
	}
	copy(dev.Name[:], name)

	if err := binary.Write(f, binary.NativeEndian, &dev); err != nil {
		f.Close()
		return nil, fmt.Errorf("uinput: write device descriptor: %w", err)
	}

	if err := unix.IoctlSetInt(int(f.Fd()), uiDevCreate, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("uinput: create device: %w", err)
	}

	return &Device{f: f}, nil
}

// EmitKey sends a key/button press (down=true) or release, followed by the
// sync report that tells listeners the event is complete.
func (d *Device) EmitKey(code uint16, down bool) error {
	value := int32(0)
	if down {
		value = 1
	}
	if err := d.emit(evKey, code, value); err != nil {
		return fmt.Errorf("uinput: emit key event: %w", err)
	}
	if err := d.emit(evSyn, synReport, 0); err != nil {
		return fmt.Errorf("uinput: emit sync report: %w", err)
	}
	return nil
}

func (d *Device) emit(evType, code uint16, value int32) error {
	ev := inputEvent{Type: evType, Code: code, Value: value}
	return binary.Write(d.f, binary.NativeEndian, &ev)
}

// Close destroys the virtual device. The kernel also tears it down
// automatically when the fd closes even without the explicit destroy
// ioctl, which is why its error is ignored here.
func (d *Device) Close() error {
	_ = unix.IoctlSetInt(int(d.f.Fd()), uiDevDestroy, 0)
	return d.f.Close()
}
