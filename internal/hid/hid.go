// Package hid provides minimal access to per-platform HID devices behind
// a single Backend interface, so device plugins (internal/device/*) are
// written once against Info/Handle/Backend regardless of OS.
//
// The Linux implementation (hidraw_linux.go) talks to /sys/class/hidraw
// and /dev/hidrawN using only the standard library — no libhidapi or cgo,
// keeping LogiTux a single static-ish binary. Other platforms currently
// get a stub Backend (hid_stub.go) that finds no devices, so the app
// builds and runs everywhere while real Windows/macOS HID backends can be
// slotted in behind Default later without touching any plugin.
package hid

// Info describes a discovered HID device.
type Info struct {
	Path      string // opaque per-backend device path, e.g. /dev/hidraw0 on Linux
	VendorID  uint16
	ProductID uint16
	Serial    string
}

// Handle is an open HID device. Write-only protocols (e.g. the Litra
// plugin) only ever call Write/Close; request/response protocols (e.g.
// HID++, used by the gpro plugin) also read reports back.
type Handle interface {
	Read(data []byte) (int, error)
	Write(data []byte) (int, error)
	Close() error
}

// Backend abstracts HID discovery and access so device plugins can be
// unit tested without real hardware, and so each OS can supply its own
// implementation behind the same interface.
type Backend interface {
	// Enumerate returns all HID devices matching vendorID/productID.
	// A productID of 0 matches any product for the given vendor.
	Enumerate(vendorID, productID uint16) ([]Info, error)
	// Open opens the HID device described by info for reading/writing.
	Open(info Info) (Handle, error)
}

// Default is the platform's HID backend, set by the build-tagged file
// that compiles for the target OS (hidraw_linux.go / hid_stub.go).
var Default Backend
