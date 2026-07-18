//go:build linux

// Package hid provides minimal access to Linux hidraw character devices.
//
// It talks to /sys/class/hidraw for device discovery and /dev/hidrawN for
// I/O, using only the standard library. This avoids a build- and run-time
// dependency on libhidapi (as opposed to a cgo binding), which keeps
// LogiTux to a single static-ish binary.
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

// Info describes a discovered hidraw device.
type Info struct {
	Path      string // e.g. /dev/hidraw0
	VendorID  uint16
	ProductID uint16
	Serial    string
}

// Writer is an open handle to a hidraw device.
type Writer interface {
	Write(data []byte) (int, error)
	Close() error
}

// Backend abstracts hidraw discovery and access so device plugins can be
// unit tested without real hardware or sysfs.
type Backend interface {
	// Enumerate returns all hidraw devices matching vendorID/productID.
	// A productID of 0 matches any product for the given vendor.
	Enumerate(vendorID, productID uint16) ([]Info, error)
	// Open opens the hidraw device described by info for reading/writing.
	Open(info Info) (Writer, error)
}

// Default is the real sysfs/dev-backed Backend.
var Default Backend = &sysfsBackend{classPath: "/sys/class/hidraw", devPath: "/dev"}

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
		vid, pid, err := readIDs(devDir)
		if err != nil {
			continue
		}
		if vid != vendorID || (productID != 0 && pid != productID) {
			continue
		}
		serial, _ := readSerial(devDir)
		infos = append(infos, Info{
			Path:      filepath.Join(b.devPath, e.Name()),
			VendorID:  vid,
			ProductID: pid,
			Serial:    serial,
		})
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return infos, nil
}

func (b *sysfsBackend) Open(info Info) (Writer, error) {
	f, err := os.OpenFile(info.Path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("hid: open %s: %w", info.Path, err)
	}
	return f, nil
}

// readIDs parses vendor/product IDs from the HID_ID field of the hidraw
// device's uevent file. The field has the form "<bus>:<vendor>:<product>",
// all hex, e.g. "0003:0000046D:0000C900".
func readIDs(devDir string) (vendorID, productID uint16, err error) {
	f, err := os.Open(filepath.Join(devDir, "uevent"))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		rest, ok := strings.CutPrefix(line, "HID_ID=")
		if !ok {
			continue
		}
		parts := strings.Split(rest, ":")
		if len(parts) != 3 {
			continue
		}
		v, err1 := strconv.ParseUint(parts[1], 16, 32)
		p, err2 := strconv.ParseUint(parts[2], 16, 32)
		if err1 != nil || err2 != nil {
			continue
		}
		return uint16(v), uint16(p), nil
	}
	return 0, 0, fmt.Errorf("hid: HID_ID not found under %s", devDir)
}

// readSerial walks up the sysfs hierarchy from the HID device directory
// looking for the owning USB device's "serial" attribute (its
// iSerialNumber string), the same attribute hidapi reads on Linux.
func readSerial(devDir string) (string, error) {
	real, err := filepath.EvalSymlinks(devDir)
	if err != nil {
		return "", err
	}

	dir := real
	for i := 0; i < 8 && dir != "/" && dir != "."; i++ {
		data, err := os.ReadFile(filepath.Join(dir, "serial"))
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("hid: no serial attribute found above %s", devDir)
}
