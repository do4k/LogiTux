//go:build !linux

// Button remapping works by synthesizing input events through a virtual
// device, which on Linux is /dev/uinput. There is no portable equivalent
// wired up on Windows/macOS yet, so this stub lets the package (and thus
// the gpro plugin, which uses it) build everywhere; Open just reports
// that remapping is unavailable, which the caller already logs and
// degrades gracefully around. The Targets/keycodes tables in keycodes.go
// are plain data and remain shared across platforms.

package uinput

import "runtime"

// Device is the non-Linux placeholder for the virtual input device. Its
// methods are never reached because Open always fails first.
type Device struct{}

func (*Device) EmitKey(code uint16, down bool) error { return errUnsupported }
func (*Device) Close() error                         { return nil }

// Open reports that virtual-input emission isn't supported on this OS.
func Open(name string, keyCodes []uint16) (*Device, error) {
	return nil, errUnsupported
}

var errUnsupported = &unsupportedError{}

type unsupportedError struct{}

func (*unsupportedError) Error() string {
	return "uinput: button remapping is not supported on " + runtime.GOOS + " yet"
}
