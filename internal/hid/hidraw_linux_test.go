//go:build linux

package hid

import (
	"os"
	"path/filepath"
	"testing"
)

// writeUevent creates a fake /sys/class/hidraw/hidrawN/device/uevent file.
func writeUevent(t *testing.T, dir, hidID string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "DRIVER=hid-generic\nHID_ID=" + hidID + "\nHID_NAME=Fake Device\n"
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnumerateMatchesVendorAndProduct(t *testing.T) {
	root := t.TempDir()
	classPath := filepath.Join(root, "sys", "class", "hidraw")
	devPath := filepath.Join(root, "dev")

	// hidraw0: matches
	dev0 := filepath.Join(classPath, "hidraw0", "device")
	writeUevent(t, dev0, "0003:0000046D:0000C900")
	if err := os.WriteFile(filepath.Join(dev0, "serial"), []byte("ABC123\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// hidraw1: different product, should not match
	dev1 := filepath.Join(classPath, "hidraw1", "device")
	writeUevent(t, dev1, "0003:0000046D:0000C901")

	// hidraw2: different vendor, should not match
	dev2 := filepath.Join(classPath, "hidraw2", "device")
	writeUevent(t, dev2, "0003:00001234:00005678")

	b := &sysfsBackend{classPath: classPath, devPath: devPath}
	infos, err := b.Enumerate(0x046d, 0xc900)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(infos), infos)
	}
	got := infos[0]
	if got.VendorID != 0x046d || got.ProductID != 0xc900 {
		t.Errorf("unexpected IDs: %+v", got)
	}
	if got.Path != filepath.Join(devPath, "hidraw0") {
		t.Errorf("unexpected path: %s", got.Path)
	}
	if got.Serial != "ABC123" {
		t.Errorf("expected serial ABC123 without the trailing serial-attribute read failing, got %q", got.Serial)
	}
}

func TestEnumerateProductZeroMatchesAnyProduct(t *testing.T) {
	root := t.TempDir()
	classPath := filepath.Join(root, "sys", "class", "hidraw")
	devPath := filepath.Join(root, "dev")

	writeUevent(t, filepath.Join(classPath, "hidraw0", "device"), "0003:0000046D:0000C900")
	writeUevent(t, filepath.Join(classPath, "hidraw1", "device"), "0003:0000046D:0000C901")

	b := &sysfsBackend{classPath: classPath, devPath: devPath}
	infos, err := b.Enumerate(0x046d, 0)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(infos), infos)
	}
}

func TestEnumerateNoHidrawClassIsNotAnError(t *testing.T) {
	root := t.TempDir()
	b := &sysfsBackend{classPath: filepath.Join(root, "does-not-exist"), devPath: filepath.Join(root, "dev")}
	infos, err := b.Enumerate(0x046d, 0xc900)
	if err != nil {
		t.Fatalf("expected no error when hidraw class is absent, got %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected no devices, got %+v", infos)
	}
}

func TestReadSerialMissingFallsBackToEmpty(t *testing.T) {
	root := t.TempDir()
	classPath := filepath.Join(root, "sys", "class", "hidraw")
	devPath := filepath.Join(root, "dev")

	writeUevent(t, filepath.Join(classPath, "hidraw0", "device"), "0003:0000046D:0000C900")

	b := &sysfsBackend{classPath: classPath, devPath: devPath}
	infos, err := b.Enumerate(0x046d, 0xc900)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 match, got %d", len(infos))
	}
	if infos[0].Serial != "" {
		t.Errorf("expected empty serial when no serial attribute exists, got %q", infos[0].Serial)
	}
}

// TestReadSerialWalksUpToUSBDevice mimics real sysfs topology, where the
// hidraw "device" symlink points at the HID interface directory and the
// "serial" attribute lives a couple of directories up, on the owning USB
// device directory.
func TestReadSerialWalksUpToUSBDevice(t *testing.T) {
	root := t.TempDir()
	classPath := filepath.Join(root, "sys", "class", "hidraw")
	devPath := filepath.Join(root, "dev")

	// usbDev is the real USB device directory that carries "serial".
	usbDev := filepath.Join(root, "sys", "devices", "usb1", "1-1")
	if err := os.MkdirAll(usbDev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(usbDev, "serial"), []byte("XYZ789\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// hidDev is the HID interface directory nested under the USB device.
	hidDev := filepath.Join(usbDev, "1-1:1.0", "0003:046D:C900.0001")
	writeUevent(t, hidDev, "0003:0000046D:0000C900")

	// hidraw0/device is a symlink to the HID interface directory, as it is
	// on real hardware.
	hidrawDir := filepath.Join(classPath, "hidraw0")
	if err := os.MkdirAll(hidrawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hidDev, filepath.Join(hidrawDir, "device")); err != nil {
		t.Fatal(err)
	}

	b := &sysfsBackend{classPath: classPath, devPath: devPath}
	infos, err := b.Enumerate(0x046d, 0xc900)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(infos), infos)
	}
	if infos[0].Serial != "XYZ789" {
		t.Errorf("expected serial XYZ789 found by walking up to the USB device dir, got %q", infos[0].Serial)
	}
}
