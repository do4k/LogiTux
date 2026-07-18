package gpro

import (
	"encoding/binary"
	"fmt"
	"sync"

	"logitux/internal/device"
	"logitux/internal/hidpp"
	"logitux/internal/uinput"
)

// REPROG_CONTROLS_V4 (0x1B04) function numbers.
const (
	buttonsFuncGetCount        byte = 0x00
	buttonsFuncGetCidInfo      byte = 0x10
	buttonsFuncSetCidReporting byte = 0x30
)

// Bits from getCidInfo's capability flags and setCidReporting's bfield
// parameter. Both pack a value bit and its adjacent "this bit is being
// explicitly written" bit; mappingFlagDiverted<<1 is that write-mask bit
// for the diverted flag specifically.
const (
	keyFlagDivertable   uint16 = 0x0020
	mappingFlagDiverted byte   = 0x01
)

// controlNames maps known control IDs to human-readable labels; unlisted
// IDs fall back to "Control 0xNNNN". Curated from Solaar's
// special_keys.py CONTROL table (github.com/pwr-Solaar/Solaar), limited
// to controls relevant to a mouse.
var controlNames = map[uint16]string{
	0x0050: "Left Button",
	0x0051: "Right Button",
	0x0052: "Middle Button",
	0x0053: "Back Button",
	0x0056: "Forward Button",
	0x0059: "Button 6",
	0x005a: "Button 7",
	0x005b: "Left Tilt",
	0x005c: "Button 8",
	0x005d: "Right Tilt",
	0x00c3: "Gesture Button",
	0x00ed: "DPI Change",
	0x00fd: "DPI Switch",
}

func controlName(cid uint16) string {
	if name, ok := controlNames[cid]; ok {
		return name
	}
	return fmt.Sprintf("Control 0x%04x", cid)
}

// buttonControl is a control discovered via getCidInfo.
type buttonControl struct {
	cid        uint16
	name       string
	divertable bool
}

// discoverButtonsFeature resolves REPROG_CONTROLS_V4 and enumerates its
// controls. Like battery, this is optional: an error or "not supported"
// here shouldn't block the rest of the mouse from working, so callers
// treat any returned error as "no button remapping on this unit."
func discoverButtonsFeature(conn *hidpp.Conn, deviceIndex byte) (featureIndex byte, controls []buttonControl, err error) {
	idx, ok, err := hidpp.GetFeatureIndex(conn, deviceIndex, featureReprogControlsV4)
	if err != nil {
		return 0, nil, fmt.Errorf("gpro: look up Reprogrammable Controls feature: %w", err)
	}
	if !ok {
		return 0, nil, fmt.Errorf("gpro: device does not support the Reprogrammable Controls feature (0x%04x)", featureReprogControlsV4)
	}

	resp, err := conn.Call(deviceIndex, idx, buttonsFuncGetCount, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("gpro: get control count: %w", err)
	}
	if len(resp) < 1 {
		return 0, nil, fmt.Errorf("gpro: short control count response")
	}
	count := int(resp[0])

	controls = make([]buttonControl, 0, count)
	for i := 0; i < count; i++ {
		info, err := conn.Call(deviceIndex, idx, buttonsFuncGetCidInfo, []byte{byte(i)})
		if err != nil {
			return 0, nil, fmt.Errorf("gpro: get control info(%d): %w", i, err)
		}
		if len(info) < 9 {
			continue
		}
		cid := binary.BigEndian.Uint16(info[0:2])
		flags := uint16(info[4]) | uint16(info[8])<<8
		controls = append(controls, buttonControl{
			cid:        cid,
			name:       controlName(cid),
			divertable: flags&keyFlagDivertable != 0,
		})
	}
	return idx, controls, nil
}

// uinputEmitter is the subset of *uinput.Device this package uses,
// abstracted so tests can substitute a fake instead of touching a real
// /dev/uinput.
type uinputEmitter interface {
	EmitKey(code uint16, down bool) error
	Close() error
}

// openUinputFunc creates the virtual input device backing remaps; a var
// so tests can substitute a fake emitter.
var openUinputFunc = func(name string, keyCodes []uint16) (uinputEmitter, error) {
	return uinput.Open(name, keyCodes)
}

// remapState is the mutable, remap-specific state for a Mouse: the active
// cid->target mappings, the lazily-created uinput device used to emit
// them (nothing is created until the first remap, to avoid an unused
// virtual input device existing just because LogiTux is running), and the
// currently-held set of diverted CIDs, needed because HID++ reports that
// as a snapshot rather than discrete press/release edges (see
// handleNotification).
type remapState struct {
	mu      sync.Mutex
	targets map[uint16]uint16 // cid -> uinput target code
	pressed map[uint16]bool
	dev     uinputEmitter
}

// Buttons implements device.ButtonRemapControl, listing only controls the
// device reports as divertable — the rest can't be remapped no matter
// what LogiTux does, so there's no point offering them.
func (m *Mouse) Buttons() ([]device.ButtonInfo, error) {
	if m.buttonsFeatureIndex == 0 {
		return nil, fmt.Errorf("gpro: device has no reprogrammable controls feature")
	}
	infos := make([]device.ButtonInfo, 0, len(m.buttons))
	for _, b := range m.buttons {
		if b.divertable {
			infos = append(infos, device.ButtonInfo{ID: b.cid, Name: b.name})
		}
	}
	return infos, nil
}

// RemapButton implements device.ButtonRemapControl. target is a
// linux/input-event-codes.h KEY_*/BTN_* code from internal/uinput's
// Targets list; pass 0 to restore buttonID's native behavior.
//
// While a remap is active, the button stops sending its normal click:
// the device only reports presses as a HID++ notification LogiTux
// translates into a synthetic input event via a virtual uinput device. If
// LogiTux isn't running when a remapped button is pressed, that press
// does nothing — see Close, which reverts every active remap so a clean
// exit restores normal behavior.
func (m *Mouse) RemapButton(buttonID uint16, target uint16) error {
	if m.buttonsFeatureIndex == 0 {
		return fmt.Errorf("gpro: device has no reprogrammable controls feature")
	}
	control := m.findButton(buttonID)
	if control == nil {
		return fmt.Errorf("gpro: unknown control 0x%04x", buttonID)
	}

	if target == 0 {
		if err := m.setDiversion(buttonID, false); err != nil {
			return fmt.Errorf("gpro: restore default for %s: %w", control.name, err)
		}
		m.remap.mu.Lock()
		delete(m.remap.targets, buttonID)
		delete(m.remap.pressed, buttonID)
		m.remap.mu.Unlock()
		return nil
	}

	if !control.divertable {
		return fmt.Errorf("gpro: %s cannot be remapped on this device", control.name)
	}
	if err := m.ensureUinputDevice(); err != nil {
		return err
	}
	if err := m.setDiversion(buttonID, true); err != nil {
		return fmt.Errorf("gpro: divert %s: %w", control.name, err)
	}

	m.remap.mu.Lock()
	m.remap.targets[buttonID] = target
	m.remap.mu.Unlock()
	return nil
}

func (m *Mouse) findButton(cid uint16) *buttonControl {
	for i := range m.buttons {
		if m.buttons[i].cid == cid {
			return &m.buttons[i]
		}
	}
	return nil
}

// setDiversion is setCidReporting with only the DIVERTED flag touched,
// and no device-side remap target (remap=0): LogiTux always translates
// diverted presses itself via uinput rather than asking the device to
// natively remap to another control.
func (m *Mouse) setDiversion(cid uint16, divert bool) error {
	bfield := mappingFlagDiverted << 1 // the "I am specifying this bit" write-mask bit
	if divert {
		bfield |= mappingFlagDiverted
	}
	params := make([]byte, 5)
	binary.BigEndian.PutUint16(params[0:2], cid)
	params[2] = bfield
	binary.BigEndian.PutUint16(params[3:5], 0)
	_, err := m.conn.Call(m.deviceIndex, m.buttonsFeatureIndex, buttonsFuncSetCidReporting, params)
	return err
}

func (m *Mouse) ensureUinputDevice() error {
	m.remap.mu.Lock()
	defer m.remap.mu.Unlock()
	if m.remap.dev != nil {
		return nil
	}
	dev, err := openUinputFunc("LogiTux Virtual Input ("+m.info.Name+")", uinput.AllCodes())
	if err != nil {
		return fmt.Errorf("gpro: create virtual input device: %w", err)
	}
	m.remap.dev = dev
	return nil
}

// watchButtonNotifications translates diverted-control notifications into
// uinput events for as long as the connection stays open; it exits when
// m.conn's Notifications channel closes, which happens as soon as the
// connection is closed (see Mouse.Close).
func (m *Mouse) watchButtonNotifications() {
	defer close(m.notifyDone)
	for n := range m.conn.Notifications() {
		m.handleNotification(n)
	}
}

// handleNotification processes one REPROG_CONTROLS_V4 function-0
// notification: [cid1, cid2, cid3, cid4] (2 bytes each, BE, 0 = empty
// slot), the full set of currently-held diverted controls. Since this is
// a snapshot rather than a press/release event, presses/releases are
// derived by diffing against the previous snapshot.
func (m *Mouse) handleNotification(n hidpp.Notification) {
	if n.DeviceIndex != m.deviceIndex || len(n.Data) < 12 {
		return
	}
	// n.Data is the full report: [reportID, deviceIndex, featureIndex, funcSwID, ...payload].
	if n.Data[2] != m.buttonsFeatureIndex || n.Data[3]&0xf0 != 0x00 {
		return
	}
	payload := n.Data[4:]

	current := make(map[uint16]bool, 4)
	for i := 0; i < 4; i++ {
		cid := binary.BigEndian.Uint16(payload[i*2 : i*2+2])
		if cid != 0 {
			current[cid] = true
		}
	}

	m.remap.mu.Lock()
	var toPress, toRelease []uint16
	for cid := range current {
		if !m.remap.pressed[cid] {
			toPress = append(toPress, cid)
		}
	}
	for cid := range m.remap.pressed {
		if !current[cid] {
			toRelease = append(toRelease, cid)
		}
	}
	m.remap.pressed = current
	dev := m.remap.dev
	targets := make(map[uint16]uint16, len(m.remap.targets))
	for cid, target := range m.remap.targets {
		targets[cid] = target
	}
	m.remap.mu.Unlock()

	if dev == nil {
		return
	}
	for _, cid := range toPress {
		if target, ok := targets[cid]; ok {
			_ = dev.EmitKey(target, true)
		}
	}
	for _, cid := range toRelease {
		if target, ok := targets[cid]; ok {
			_ = dev.EmitKey(target, false)
		}
	}
}

// revertAllRemaps clears diversion for every actively remapped button.
// Best-effort: called during Close, when the device may already be
// unreachable (e.g. unplugged), so individual failures aren't surfaced.
func (m *Mouse) revertAllRemaps() {
	m.remap.mu.Lock()
	cids := make([]uint16, 0, len(m.remap.targets))
	for cid := range m.remap.targets {
		cids = append(cids, cid)
	}
	m.remap.mu.Unlock()

	for _, cid := range cids {
		_ = m.setDiversion(cid, false)
	}
}
