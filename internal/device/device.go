// Package device defines the plugin model that lets LogiTux control
// different kinds of Logitech hardware through a common interface.
//
// A device plugin (e.g. internal/device/litra) registers itself in an
// init() function by calling Register with the vendor/product IDs it
// handles and a factory that turns a discovered hidraw device into a
// Device. The core application (internal/app) never needs to know about
// specific product lines: it calls Discover and works with whatever
// capability interfaces (PowerControl, BrightnessControl, ...) each
// returned Device happens to implement.
package device

import (
	"fmt"
	"sort"

	"logitux/internal/hid"
)

// Kind categorizes a device for display purposes (e.g. choosing a
// dashboard icon). Freeform and cosmetic only — nothing in this package
// branches on it.
type Kind string

const (
	KindLight   Kind = "light"
	KindMouse   Kind = "mouse"
	KindHeadset Kind = "headset"
	KindWebcam  Kind = "webcam"
)

// Info identifies a discovered device for display purposes.
type Info struct {
	Name      string // human-readable product name, e.g. "Litra Glow"
	Kind      Kind
	Serial    string
	VendorID  uint16
	ProductID uint16
}

// Device is the minimum any supported piece of hardware must implement.
// Most functionality is exposed through optional capability interfaces
// below, which a Device implements only if the hardware supports it.
type Device interface {
	Info() Info
	Close() error
}

// PowerControl is implemented by devices that can be switched on/off.
type PowerControl interface {
	SetPower(on bool) error
}

// BrightnessControl is implemented by devices with adjustable brightness,
// expressed as a percentage from 0 to 100.
type BrightnessControl interface {
	SetBrightness(percent int) error
}

// TemperatureControl is implemented by devices with adjustable color
// temperature, expressed in Kelvin.
type TemperatureControl interface {
	SetTemperature(kelvin int) error
	TemperatureRange() (min, max int)
}

// DPIControl is implemented by devices with an adjustable sensor DPI
// (e.g. mice). Min/max/step describe the device's supported range so the
// GUI can build an appropriately bounded control.
type DPIControl interface {
	DPIRange() (min, max, step int)
	SetDPI(dpi int) error
	DPI() (int, error)
}

// BatteryStatus is implemented by battery-powered wireless devices.
type BatteryStatus interface {
	Battery() (percent int, charging bool, err error)
}

// ReportRateControl is implemented by devices with a selectable USB
// report ("polling") rate, in Hz.
type ReportRateControl interface {
	ReportRateOptions() []int
	SetReportRate(hz int) error
	ReportRate() (int, error)
}

// RGBControl is implemented by devices with an addressable single-color
// RGB light (e.g. a logo).
type RGBControl interface {
	SetColor(r, g, b uint8) error
}

// ButtonInfo describes one of a device's remappable physical controls.
type ButtonInfo struct {
	ID   uint16 // opaque, device-defined control ID
	Name string // human-readable name, e.g. "Back Button"
}

// ButtonRemapControl is implemented by devices whose physical buttons can
// be remapped to a different action. RemapButton's target values come
// from internal/uinput's Targets list (KEY_*/BTN_* codes); passing 0
// restores a button's native behavior.
//
// Remapping works by diverting the button so the device reports presses
// as raw events instead of its normal click, which the implementation
// then translates into a synthetic input event. That means a remapped
// button only works while the device is open and being actively
// translated — see each implementation's docs for exactly what happens if
// the controlling process isn't running.
type ButtonRemapControl interface {
	Buttons() ([]ButtonInfo, error)
	RemapButton(buttonID uint16, target uint16) error
}

// SidetoneControl is implemented by headsets with adjustable sidetone
// (how much of the mic is played back into the headset's own speakers),
// expressed as a percentage from 0 to 100.
type SidetoneControl interface {
	SetSidetone(percent int) error
	Sidetone() (int, error)
}

// EqualizerBand describes one fixed band of a device's equalizer.
type EqualizerBand struct {
	FrequencyHz int
}

// EqualizerControl is implemented by devices with a hardware equalizer.
// The band layout (count and frequencies) and dB range are fixed
// properties of the hardware, discovered at connect time; SetLevels takes
// one dB value per band, in the same order as Bands, clamped to Range.
type EqualizerControl interface {
	EqualizerBands() []EqualizerBand
	EqualizerRange() (min, max int) // dB
	EqualizerLevels() ([]int, error)
	SetEqualizerLevels(levelsDB []int) error
}

// CameraControl describes one adjustable camera control, discovered
// from the hardware at open time (the set varies by model and even
// firmware). Name is a stable identifier the GUI recognizes for special
// treatment — e.g. "Zoom", "Pan", "Tilt", "Auto Focus" — mirroring how
// EqualizerControl reports its band layout rather than assuming one.
// Boolean controls (e.g. "Auto Focus") have Min 0 and Max 1.
type CameraControl struct {
	Name                    string
	Min, Max, Step, Default int
}

// CameraControlSet is implemented by camera devices (webcams). Values
// are read from and written to the hardware live; there is no cached
// state to get stale.
type CameraControlSet interface {
	CameraControls() []CameraControl
	CameraControl(name string) (int, error)
	SetCameraControl(name string, value int) error
}

// OpenFunc opens a specific discovered hidraw device as a Device. Some
// physical devices expose more than one hidraw node for the same product
// (e.g. a wireless receiver has one hidraw interface per USB interface);
// OpenFunc may return (nil, nil) to say "not an error, but this particular
// node isn't the one to use," and the caller should silently skip it
// rather than treating it as a device or an error.
type OpenFunc func(backend hid.Backend, info hid.Info) (Device, error)

// Candidate is a present-but-not-yet-opened device: a cheap, stable Key
// (unique per physical interface, e.g. its /dev node path) plus an Open
// thunk that performs the possibly-expensive construction — for a HID++
// device, that means feature discovery, a burst of round-trips to the
// hardware. Separating "present" from "opened" lets the caller skip
// re-opening devices it already holds; re-opening a wireless HID++ device
// on a timer floods it with discovery traffic on a second hidraw fd and
// stalls the live connection.
type Candidate struct {
	Key  string
	Open func() (Device, error)
}

type plugin struct {
	vendorID   uint16
	productIDs []uint16
	open       OpenFunc
}

var plugins []plugin

// Register adds a device plugin to the registry. It is meant to be called
// from a plugin package's init() function.
func Register(vendorID uint16, productIDs []uint16, open OpenFunc) {
	plugins = append(plugins, plugin{vendorID: vendorID, productIDs: productIDs, open: open})
}

// Discoverer finds devices that don't live behind the hidraw backend at
// all — e.g. webcams, which are V4L2 devices. Registered via
// RegisterDiscoverer from a plugin package's init(), and run on every
// Discover alongside the hidraw plugins. Like the hidraw path, a
// Discoverer enumerates cheaply and defers the actual open to each
// Candidate's Open thunk.
type Discoverer func() ([]Candidate, []error)

var discoverers []Discoverer

// RegisterDiscoverer adds a non-hidraw device discoverer to the registry.
func RegisterDiscoverer(fn Discoverer) {
	discoverers = append(discoverers, fn)
}

// Discover enumerates present supported devices without opening them,
// returning one Candidate per device. Enumeration is cheap — hidraw/sysfs
// listing for the HID plugins, plus each registered Discoverer's own scan
// — and the potentially expensive open (and the device I/O it entails)
// happens only when the caller invokes a Candidate's Open. Enumeration
// errors (e.g. a backend that can't list a vendor's devices) are returned
// here; per-device open errors surface from Open instead.
//
// Candidates are sorted by Key for a stable order; the caller decides
// display order (LogiTux sorts opened devices by serial).
func Discover(backend hid.Backend) ([]Candidate, []error) {
	var candidates []Candidate
	var errs []error

	for _, p := range plugins {
		open := p.open
		for _, productID := range p.productIDs {
			infos, err := backend.Enumerate(p.vendorID, productID)
			if err != nil {
				errs = append(errs, fmt.Errorf("device: enumerate %04x:%04x: %w", p.vendorID, productID, err))
				continue
			}
			for _, info := range infos {
				info := info
				candidates = append(candidates, Candidate{
					Key: info.Path,
					Open: func() (Device, error) {
						d, err := open(backend, info)
						if err != nil {
							return nil, fmt.Errorf("device: open %s: %w", info.Path, err)
						}
						return d, nil
					},
				})
			}
		}
	}

	for _, discover := range discoverers {
		found, ferrs := discover()
		candidates = append(candidates, found...)
		errs = append(errs, ferrs...)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Key < candidates[j].Key
	})
	return candidates, errs
}
