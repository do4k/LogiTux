// Package gpro implements the device plugin for the Logitech G Pro
// Wireless mouse, connected either directly (wired/Bluetooth) or through
// its Lightspeed USB receiver.
//
// Unlike Litra lights, this mouse speaks Logitech's HID++ 2.0 protocol
// (see internal/hidpp): every capability here is a feature discovered at
// runtime via the Root feature, not a fixed byte sequence. Feature byte
// layouts used across this package were verified against Solaar's
// hidpp20.py/settings_templates.py (github.com/pwr-Solaar/Solaar), the
// most complete existing Linux HID++ implementation, and against a real
// `solaar show` dump for this exact mouse (WPID 4079 behind receiver
// 046d:c539) rather than guessed from general HID++ documentation.
package gpro

import (
	"fmt"
	"sort"
	"time"

	"logitux/internal/device"
	"logitux/internal/hid"
	"logitux/internal/hidpp"
)

const vendorID = 0x046d

const (
	// productReceiver is the Logitech Lightspeed USB receiver the mouse is
	// typically paired to.
	productReceiver uint16 = 0xc539
	// productWired is the mouse's own product ID when connected directly
	// (wired or Bluetooth), addressed at HID++ device index 0xFF.
	productWired uint16 = 0x4079
)

// HID++ feature IDs used by this plugin. Feature *indexes* (which slot in
// a given device's feature table implements them) are discovered at
// runtime via hidpp.GetFeatureIndex; see buildMouse.
const (
	featureAdjustableDPI    uint16 = 0x2201
	featureUnifiedBattery   uint16 = 0x1004
	featureBatteryStatus    uint16 = 0x1000 // legacy fallback; see resolveBatteryFeature
	featureReportRate       uint16 = 0x8060
	featureColorLEDEffects  uint16 = 0x8070
	featureReprogControlsV4 uint16 = 0x1b04
)

// probeTimeout bounds each Ping while hunting for which hidraw interface
// and device index the mouse is reachable on. It's much shorter than the
// normal operation timeout because most attempts are expected to go
// nowhere (wrong interface, or an index nothing is paired to).
// A var (not const) so tests can shrink it instead of waiting out several
// real probe timeouts.
var probeTimeout = 300 * time.Millisecond

func init() {
	device.Register(vendorID, []uint16{productReceiver, productWired}, open)
}

// Mouse is a device.Device for a connected G Pro Wireless.
type Mouse struct {
	conn        *hidpp.Conn
	deviceIndex byte
	info        device.Info

	dpiFeatureIndex byte

	batteryFeatureIndex byte
	batteryKind         batteryKind

	reportRateFeatureIndex byte
	reportRateOptions      []int // Hz, fastest (highest Hz) first

	ledFeatureIndex      byte
	ledZoneIndex         byte
	ledStaticEffectIndex byte

	buttonsFeatureIndex byte
	buttons             []buttonControl
	remap               remapState

	// notifyDone closes once watchButtonNotifications has exited, so Close
	// can wait for it before tearing down the uinput device it might still
	// be using.
	notifyDone chan struct{}
}

func (m *Mouse) Info() device.Info { return m.info }

// Close reverts any active button remaps before closing the connection
// (both need the connection alive), waits for the notification-watching
// goroutine to exit, and releases the virtual input device if one was
// created.
func (m *Mouse) Close() error {
	m.revertAllRemaps()
	err := m.conn.Close()
	<-m.notifyDone

	m.remap.mu.Lock()
	if m.remap.dev != nil {
		m.remap.dev.Close()
		m.remap.dev = nil
	}
	m.remap.mu.Unlock()

	return err
}

func open(backend hid.Backend, info hid.Info) (device.Device, error) {
	switch info.ProductID {
	case productReceiver:
		return openViaReceiver(backend, info)
	case productWired:
		return openDirect(backend, info)
	default:
		return nil, fmt.Errorf("gpro: unexpected product ID %04x", info.ProductID)
	}
}

// openDirect handles the mouse connected directly (wired or Bluetooth), or
// via a kernel-level per-peripheral hidraw node some Linux versions expose
// even for a receiver-paired mouse (product 0x4079 but device index
// 0xFF). Either way it's addressed at a fixed device index, so unlike
// openViaReceiver there's no index to hunt for — but it may simply not be
// live right now (mouse asleep, or this kernel doesn't expose that node),
// so the initial liveness check uses the same short probeTimeout rather
// than the full operation timeout.
func openDirect(backend hid.Backend, info hid.Info) (device.Device, error) {
	h, err := backend.Open(info)
	if err != nil {
		return nil, fmt.Errorf("gpro: open %s: %w", info.Path, err)
	}
	conn := hidpp.Open(h)
	conn.SetTimeout(probeTimeout)

	if _, _, err := hidpp.Ping(conn, hidpp.DeviceIndexDirect); err != nil {
		conn.Close()
		return nil, fmt.Errorf("gpro: ping direct-connected mouse: %w", err)
	}
	conn.SetTimeout(operationTimeout)

	m, err := buildMouse(conn, hidpp.DeviceIndexDirect, info.Serial)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return m, nil
}

// openViaReceiver handles the mouse paired to a Lightspeed receiver. The
// receiver exposes one hidraw node per USB interface, all sharing the same
// vendor/product ID and serial; only one of them actually carries HID++
// long reports, and the mouse's HID++ device index (1-6) is assigned by
// the receiver at pairing time, so both must be discovered by probing.
//
// Known limitation: Lightspeed/Unifying receivers commonly have no USB
// serial descriptor at all (internal/hid's readSerial then reports an
// empty string), in which case every sibling interface groups under the
// same empty serial. That's fine with exactly one receiver connected, but
// two serial-less receivers would be indistinguishable and could collide
// in config.Store. Not addressed here since it doesn't apply to a
// single-receiver setup; a real fix would need a physical-USB-path-based
// identifier as a fallback grouping key instead of serial.
func openViaReceiver(backend hid.Backend, info hid.Info) (device.Device, error) {
	siblings, err := backend.Enumerate(vendorID, productReceiver)
	if err != nil {
		return nil, fmt.Errorf("gpro: enumerate receiver interfaces: %w", err)
	}

	var group []hid.Info
	for _, s := range siblings {
		if s.Serial == info.Serial {
			group = append(group, s)
		}
	}
	sort.Slice(group, func(i, j int) bool { return group[i].Path < group[j].Path })
	if len(group) == 0 || group[0].Path != info.Path {
		// Another call in this Discover pass, for the canonical (lowest
		// path) sibling, will do the real work; skip the rest silently.
		return nil, nil
	}

	for _, candidate := range group {
		h, err := backend.Open(candidate)
		if err != nil {
			continue
		}
		conn := hidpp.Open(h)
		conn.SetTimeout(probeTimeout)

		deviceIndex, ok := findPairedDevice(conn)
		if !ok {
			conn.Close()
			continue
		}
		conn.SetTimeout(operationTimeout)

		serial := resolveMouseSerial(backend, info.Serial, deviceIndex)
		m, err := buildMouse(conn, deviceIndex, serial)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return m, nil
	}

	return nil, fmt.Errorf("gpro: no paired device responded on receiver %s (is the mouse asleep? try moving it)", info.Serial)
}

// findPairedDevice probes HID++ device indices 1-6 (the range a receiver
// assigns paired peripherals) and returns the first one that answers a
// ping, i.e. is actually connected right now.
func findPairedDevice(conn *hidpp.Conn) (byte, bool) {
	for idx := byte(1); idx <= 6; idx++ {
		if _, _, err := hidpp.Ping(conn, idx); err == nil {
			return idx, true
		}
	}
	return 0, false
}

// resolveMouseSerial picks the best available identifier for a
// receiver-paired mouse. Some Linux kernels expose a second, virtual
// hidraw node for the same physical mouse (product 0x4079, driver
// logitech-hidpp-device) purely for identification purposes, and that
// node's HID_UNIQ is often a real hardware ID even when the receiver
// itself (receiverSerial) has no USB serial descriptor at all — see
// internal/hid's HID_UNIQ fallback. When available, that's a far more
// useful identifier than the receiver-serial-plus-device-index composite
// this falls back to.
func resolveMouseSerial(backend hid.Backend, receiverSerial string, deviceIndex byte) string {
	if wiredInfos, err := backend.Enumerate(vendorID, productWired); err == nil {
		for _, wi := range wiredInfos {
			if wi.Serial != "" {
				return wi.Serial
			}
		}
	}
	return fmt.Sprintf("%s-%d", receiverSerial, deviceIndex)
}

const operationTimeout = 2 * time.Second

// buildMouse resolves every HID++ feature this plugin uses and returns a
// ready-to-use Mouse, or an error naming whichever feature couldn't be
// found. DPI, report rate, and the logo LED are expected on every G Pro
// Wireless and fail buildMouse outright if missing. Battery is treated as
// optional instead: real hardware testing found a unit that answers "not
// supported" for all four known battery feature IDs (0x1004, 0x1000,
// 0x1001, 0x1F20) even though the kernel's own hid-logitech-hidpp driver
// does report a battery percentage for it (visible via upower) — evidently
// through the receiver's wireless connection-status notifications rather
// than any feature this mouse's own HID++ table advertises. Replicating
// that would need reverse-engineering the DJ receiver's notification
// format; not attempted here. Either way, there's no reason a missing
// "nice to have" battery reading should block DPI/report-rate/RGB from
// working, so Battery() just reports a clear error on such a unit rather
// than the device failing to open at all.
func buildMouse(conn *hidpp.Conn, deviceIndex byte, serial string) (*Mouse, error) {
	dpiIdx, err := requireFeature(conn, deviceIndex, featureAdjustableDPI, "Adjustable DPI")
	if err != nil {
		return nil, err
	}
	batteryIdx, battKind, err := resolveBatteryFeature(conn, deviceIndex)
	if err != nil {
		batteryIdx, battKind = 0, batteryKindNone
	}
	reportRateIdx, err := requireFeature(conn, deviceIndex, featureReportRate, "Report Rate")
	if err != nil {
		return nil, err
	}
	ledIdx, err := requireFeature(conn, deviceIndex, featureColorLEDEffects, "Color LED Effects")
	if err != nil {
		return nil, err
	}

	rateOptions, err := getReportRateOptions(conn, deviceIndex, reportRateIdx)
	if err != nil {
		return nil, err
	}

	ledZone, err := discoverLogoZone(conn, deviceIndex, ledIdx)
	if err != nil {
		return nil, err
	}

	// Button remapping is optional like battery: not every unit exposes
	// REPROG_CONTROLS_V4 (verified against real hardware: a G Pro Wireless
	// unit that supports none of REPROG_CONTROLS through _V4, 0x1B00-0x1B04
	// — this mouse's remapping, if Logitech exposes it at all for this
	// model, evidently goes through the receiver's own protocol layer
	// rather than a feature on the mouse's own HID++ table), and a missing
	// "nice to have" shouldn't block the rest of the mouse.
	buttonsIdx, buttons, err := discoverButtonsFeature(conn, deviceIndex)
	if err != nil {
		buttonsIdx, buttons = 0, nil
	}

	m := &Mouse{
		conn:        conn,
		deviceIndex: deviceIndex,
		info: device.Info{
			Name:      "G Pro Wireless",
			Kind:      device.KindMouse,
			Serial:    serial,
			VendorID:  vendorID,
			ProductID: productWired,
		},
		dpiFeatureIndex:        dpiIdx,
		batteryFeatureIndex:    batteryIdx,
		batteryKind:            battKind,
		reportRateFeatureIndex: reportRateIdx,
		reportRateOptions:      rateOptions,
		ledFeatureIndex:        ledIdx,
		ledZoneIndex:           ledZone.zoneIndex,
		ledStaticEffectIndex:   ledZone.staticEffectIndex,
		buttonsFeatureIndex:    buttonsIdx,
		buttons:                buttons,
		remap: remapState{
			targets: make(map[uint16]uint16),
			pressed: make(map[uint16]bool),
		},
		notifyDone: make(chan struct{}),
	}
	go m.watchButtonNotifications()
	return m, nil
}

func requireFeature(conn *hidpp.Conn, deviceIndex byte, featureID uint16, name string) (byte, error) {
	idx, ok, err := hidpp.GetFeatureIndex(conn, deviceIndex, featureID)
	if err != nil {
		return 0, fmt.Errorf("gpro: look up %s feature: %w", name, err)
	}
	if !ok {
		return 0, fmt.Errorf("gpro: device does not support the %s feature (0x%04x)", name, featureID)
	}
	return idx, nil
}
