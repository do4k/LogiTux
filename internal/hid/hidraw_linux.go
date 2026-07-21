//go:build linux

// This file is the Linux hidraw implementation of the Backend interface
// declared in hid.go: device discovery via /sys/class/hidraw and I/O via
// /dev/hidrawN, using only the standard library (no libhidapi/cgo).

package hid

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// On Linux, Default is the real sysfs/dev-backed Backend.
func init() {
	Default = &sysfsBackend{classPath: "/sys/class/hidraw", devPath: "/dev"}
}

type sysfsBackend struct {
	classPath string
	devPath   string
}

func (b *sysfsBackend) Enumerate(vendorID, productID uint16) ([]Info, error) {
	entries, err := os.ReadDir(b.classPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("hid: read %s: %w", b.classPath, err)
	}

	var infos []Info
	for _, e := range entries {
		devDir := filepath.Join(b.classPath, e.Name(), "device")
		u, err := readUevent(devDir)
		if err != nil {
			continue
		}
		if u.vendorID != vendorID || (productID != 0 && u.productID != productID) {
			continue
		}
		serial, err := readSerial(devDir)
		if err != nil {
			// No USB iSerialNumber descriptor (common on receivers, and on
			// the per-peripheral virtual hidraw nodes some kernels expose
			// for devices paired to one). Fall back to HID_UNIQ, which the
			// HID core sometimes populates from the device's own protocol
			// (e.g. a wireless peripheral's hardware ID) even when the USB
			// layer has nothing.
			serial = u.uniq
		}
		infos = append(infos, Info{
			Path:      filepath.Join(b.devPath, e.Name()),
			VendorID:  u.vendorID,
			ProductID: u.productID,
			Serial:    serial,
		})
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return infos, nil
}

func (b *sysfsBackend) Open(info Info) (Handle, error) {
	f, err := os.OpenFile(info.Path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("hid: open %s: %w", info.Path, err)
	}
	return f, nil
}

// deviceUevent holds the fields this package cares about from a hidraw
// device's uevent file.
type deviceUevent struct {
	vendorID, productID uint16
	uniq                string // HID_UNIQ; often empty, see readUevent
}

// readUevent parses vendor/product IDs from the HID_ID field
// ("<bus>:<vendor>:<product>", all hex, e.g. "0003:0000046D:0000C900") and
// captures HID_UNIQ, a per-device identifier the HID core sometimes sets
// from the device's own protocol (used as a serial-number fallback; see
// sysfsBackend.Enumerate).
func readUevent(devDir string) (deviceUevent, error) {
	f, err := os.Open(filepath.Join(devDir, "uevent"))
	if err != nil {
		return deviceUevent{}, err
	}
	defer f.Close()

	var u deviceUevent
	var haveID bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, "HID_ID="); ok {
			parts := strings.Split(rest, ":")
			if len(parts) == 3 {
				v, err1 := strconv.ParseUint(parts[1], 16, 32)
				p, err2 := strconv.ParseUint(parts[2], 16, 32)
				if err1 == nil && err2 == nil {
					u.vendorID, u.productID = uint16(v), uint16(p)
					haveID = true
				}
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "HID_UNIQ="); ok {
			u.uniq = rest
		}
	}
	if !haveID {
		return deviceUevent{}, fmt.Errorf("hid: HID_ID not found under %s", devDir)
	}
	return u, nil
}

// readSerial walks up the sysfs hierarchy from the HID device directory to
// find the owning USB device's directory (identified by an "idVendor"
// file, which only a genuine USB device directory has) and reads its
// "serial" attribute (the iSerialNumber string), the same attribute hidapi
// reads on Linux.
//
// It stops at the first such directory whether or not "serial" exists
// there: many receivers (e.g. Logitech's Lightspeed/Unifying dongles)
// simply have no serial descriptor. Climbing past that boundary looking
// for a hit is wrong, not just unnecessary — parent directories are USB
// hubs and host controllers, which can have their own unrelated same-named
// "serial" file (e.g. a PCI bus address), silently producing a bogus,
// non-unique value instead of a clear "no serial" result.
func readSerial(devDir string) (string, error) {
	real, err := filepath.EvalSymlinks(devDir)
	if err != nil {
		return "", err
	}

	dir := real
	for i := 0; i < 8 && dir != "/" && dir != "."; i++ {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			data, err := os.ReadFile(filepath.Join(dir, "serial"))
			if err != nil {
				return "", fmt.Errorf("hid: USB device at %s has no serial attribute", dir)
			}
			return strings.TrimSpace(string(data)), nil
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("hid: no USB device directory (with idVendor) found above %s", devDir)
}
