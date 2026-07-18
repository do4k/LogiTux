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

// Info identifies a discovered device for display purposes.
type Info struct {
	Name      string // human-readable product name, e.g. "Litra Glow"
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

// OpenFunc opens a specific discovered hidraw device as a Device.
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
				devices = append(devices, d)
			}
		}
	}

	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Info().Serial < devices[j].Info().Serial
	})
	return devices, errs
}
