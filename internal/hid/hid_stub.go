//go:build !linux

// This file supplies a do-nothing HID Backend on platforms without a
// native implementation yet (Windows, macOS). It lets the whole app
// build and run there — the UI comes up and simply reports no devices —
// so cross-platform builds and CI work today, and a real Windows/macOS
// backend can replace stubBackend behind hid.Default without any change
// to the device plugins that consume the interface.

package hid

import "runtime"

func init() {
	Default = stubBackend{}
}

type stubBackend struct{}

// Enumerate finds nothing: there is no native HID access on this OS yet.
func (stubBackend) Enumerate(vendorID, productID uint16) ([]Info, error) {
	return nil, nil
}

func (stubBackend) Open(info Info) (Handle, error) {
	return nil, &unsupportedError{}
}

type unsupportedError struct{}

func (*unsupportedError) Error() string {
	return "hid: no native HID backend on " + runtime.GOOS + " yet"
}
