// Package litra implements the device plugin for the Logitech Litra Glow
// and Litra Beam key lights.
//
// These lights speak a simple vendor-defined USB HID protocol (not the
// HID++ protocol used by most other Logitech peripherals): each command is
// a fixed 20-byte output report starting with report ID 0x11 and a
// 0xff 0x04 feature prefix, followed by a one-byte command and up to two
// payload bytes. There is no way to read current state back from the
// device, which is why LogiTux tracks last-known state itself
// (see internal/config).
package litra

import (
	"encoding/binary"
	"fmt"
	"math"

	"logitux/internal/device"
	"logitux/internal/hid"
)

const vendorID = 0x046d

const (
	productGlow uint16 = 0xc900
	productBeam uint16 = 0xc901
)

// Raw brightness values the device accepts; LogiTux exposes these to the
// rest of the app as a 0-100 percentage and scales at the boundary.
const (
	minBrightnessRaw = 0x14
	maxBrightnessRaw = 0xfa
)

const (
	minTemperatureK = 2700
	maxTemperatureK = 6500
)

var productNames = map[uint16]string{
	productGlow: "Litra Glow",
	productBeam: "Litra Beam",
}

func init() {
	device.Register(vendorID, []uint16{productGlow, productBeam}, open)
}

// Light is a device.Device for a single connected Litra Glow or Beam.
type Light struct {
	conn hid.Handle
	info device.Info
}

func open(backend hid.Backend, info hid.Info) (device.Device, error) {
	w, err := backend.Open(info)
	if err != nil {
		return nil, err
	}
	name := productNames[info.ProductID]
	if name == "" {
		name = fmt.Sprintf("Litra (%04x)", info.ProductID)
	}
	return &Light{
		conn: w,
		info: device.Info{
			Name:      name,
			Serial:    info.Serial,
			VendorID:  info.VendorID,
			ProductID: info.ProductID,
		},
	}, nil
}

func (l *Light) Info() device.Info { return l.info }
func (l *Light) Close() error      { return l.conn.Close() }

// SetPower implements device.PowerControl.
func (l *Light) SetPower(on bool) error {
	var powerByte byte
	if on {
		powerByte = 0x01
	}
	return l.send(0x1c, powerByte)
}

// SetBrightness implements device.BrightnessControl. percent is clamped to
// 0-100 and scaled to the device's raw brightness range.
func (l *Light) SetBrightness(percent int) error {
	percent = clamp(percent, 0, 100)
	span := float64(maxBrightnessRaw - minBrightnessRaw)
	raw := byte(minBrightnessRaw + math.Round(float64(percent)/100*span))
	return l.send(0x4c, 0x00, raw)
}

// SetTemperature implements device.TemperatureControl. kelvin is clamped
// to the device's supported range before being sent.
func (l *Light) SetTemperature(kelvin int) error {
	kelvin = clamp(kelvin, minTemperatureK, maxTemperatureK)
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(kelvin))
	return l.send(0x9c, buf[0], buf[1])
}

// TemperatureRange implements device.TemperatureControl.
func (l *Light) TemperatureRange() (min, max int) {
	return minTemperatureK, maxTemperatureK
}

// send builds and writes a 20-byte Litra report: report ID 0x11, the
// 0xff 0x04 lighting-feature prefix, a one-byte command, then payload
// bytes zero-padded to the device's fixed report length.
func (l *Light) send(command byte, payload ...byte) error {
	report := make([]byte, 20)
	report[0] = 0x11
	report[1] = 0xff
	report[2] = 0x04
	report[3] = command
	copy(report[4:], payload)
	_, err := l.conn.Write(report)
	return err
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
