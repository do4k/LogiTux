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
	KindLight Kind = "light"
	KindMouse Kind = "mouse"
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

// OpenFunc opens a specific discovered hidraw device as a Device. Some
// physical devices expose more than one hidraw node for the same product
// (e.g. a wireless receiver has one hidraw interface per USB interface);
// OpenFunc may return (nil, nil) to say "not an error, but this particular
// node isn't the one to use," and Discover will silently skip it rather
// than treating it as a device or an error.
type OpenFunc func(backend hid.Backend, info hid.Info) (Device, error)

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

// Discover enumerates hidraw devices via backend and opens every one that
// matches a registered plugin. It does not fail on individual device
// errors (e.g. a permission problem on one light); those are returned
// alongside any devices that opened successfully.
func Discover(backend hid.Backend) ([]Device, []error) {
	var devices []Device
	var errs []error

	for _, p := range plugins {
		for _, productID := range p.productIDs {
			infos, err := backend.Enumerate(p.vendorID, productID)
			if err != nil {
				errs = append(errs, fmt.Errorf("device: enumerate %04x:%04x: %w", p.vendorID, productID, err))
				continue
			}
			for _, info := range infos {
				d, err := p.open(backend, info)
				if err != nil {
					errs = append(errs, fmt.Errorf("device: open %s: %w", info.Path, err))
					continue
				}
				if d == nil {
					continue // OpenFunc chose to skip this node; not an error
				}
				devices = append(devices, d)
			}
		}
	}

	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Info().Serial < devices[j].Info().Serial
	})
	return devices, errs
}
