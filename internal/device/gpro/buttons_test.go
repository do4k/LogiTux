package gpro

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

// fakeEmitter is a uinputEmitter test double recording every EmitKey call
// instead of touching a real /dev/uinput.
type fakeEmitter struct {
	mu     sync.Mutex
	events []struct {
		code uint16
		down bool
	}
	closed bool
}

func (e *fakeEmitter) EmitKey(code uint16, down bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, struct {
		code uint16
		down bool
	}{code, down})
	return nil
}

func (e *fakeEmitter) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}

func (e *fakeEmitter) snapshot() []struct {
	code uint16
	down bool
} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]struct {
		code uint16
		down bool
	}{}, e.events...)
}

// withFakeUinput swaps openUinputFunc for one that returns a fakeEmitter,
// restoring the original on test cleanup.
func withFakeUinput(t *testing.T) *fakeEmitter {
	t.Helper()
	emitter := &fakeEmitter{}
	orig := openUinputFunc
	openUinputFunc = func(name string, keyCodes []uint16) (uinputEmitter, error) {
		return emitter, nil
	}
	t.Cleanup(func() { openUinputFunc = orig })
	return emitter
}

func TestButtonsListsOnlyDivertableControls(t *testing.T) {
	m, _, _ := newTestMouse(t)
	defer m.Close()

	buttons, err := m.Buttons()
	if err != nil {
		t.Fatalf("Buttons: %v", err)
	}
	if len(buttons) != 1 || buttons[0].ID != 0x53 {
		t.Fatalf("expected only the divertable Back Button (0x53), got %+v", buttons)
	}
	if buttons[0].Name != "Back Button" {
		t.Errorf("expected name %q, got %q", "Back Button", buttons[0].Name)
	}
}

func TestRemapButtonRejectsNonDivertableControl(t *testing.T) {
	withFakeUinput(t)
	m, _, _ := newTestMouse(t)
	defer m.Close()

	if err := m.RemapButton(0x50, uinputCode(t)); err == nil {
		t.Fatal("expected an error remapping a non-divertable control (Left Button, 0x50)")
	}
}

func TestRemapButtonRejectsUnknownControl(t *testing.T) {
	withFakeUinput(t)
	m, _, _ := newTestMouse(t)
	defer m.Close()

	if err := m.RemapButton(0x9999, uinputCode(t)); err == nil {
		t.Fatal("expected an error remapping an unknown control")
	}
}

func TestRemapButtonDivertsAndCreatesUinputDeviceLazily(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()

	m.remap.mu.Lock()
	hasDevice := m.remap.dev != nil
	m.remap.mu.Unlock()
	if hasDevice {
		t.Fatal("expected no uinput device before any remap")
	}

	withFakeUinput(t)
	if err := m.RemapButton(0x53, uinputCode(t)); err != nil {
		t.Fatalf("RemapButton: %v", err)
	}

	fm.mu.Lock()
	diverted := fm.diverted[0x53]
	fm.mu.Unlock()
	if !diverted {
		t.Error("expected the device to be told to divert control 0x53")
	}

	m.remap.mu.Lock()
	hasDevice = m.remap.dev != nil
	m.remap.mu.Unlock()
	if !hasDevice {
		t.Error("expected a uinput device to have been created")
	}
}

func TestRemapButtonZeroTargetRestoresDefault(t *testing.T) {
	withFakeUinput(t)
	m, fm, _ := newTestMouse(t)
	defer m.Close()

	if err := m.RemapButton(0x53, uinputCode(t)); err != nil {
		t.Fatalf("RemapButton: %v", err)
	}
	if err := m.RemapButton(0x53, 0); err != nil {
		t.Fatalf("RemapButton(0): %v", err)
	}

	fm.mu.Lock()
	diverted := fm.diverted[0x53]
	fm.mu.Unlock()
	if diverted {
		t.Error("expected diversion to be cleared after remapping to 0 (default)")
	}
}

func TestCloseRevertsActiveRemaps(t *testing.T) {
	withFakeUinput(t)
	m, fm, _ := newTestMouse(t)

	if err := m.RemapButton(0x53, uinputCode(t)); err != nil {
		t.Fatalf("RemapButton: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fm.mu.Lock()
	diverted := fm.diverted[0x53]
	fm.mu.Unlock()
	if diverted {
		t.Error("expected Close to revert diversion for actively remapped buttons")
	}
}

func TestDivertedNotificationEmitsKeyPressAndRelease(t *testing.T) {
	emitter := withFakeUinput(t)
	m, _, h := newTestMouse(t)
	defer m.Close()

	target := uinputCode(t)
	if err := m.RemapButton(0x53, target); err != nil {
		t.Fatalf("RemapButton: %v", err)
	}

	// Press: notification reports CID 0x53 held.
	h.pushReport(notificationReport(m.deviceIndex, m.buttonsFeatureIndex, 0x53, 0, 0, 0))
	waitForEvents(t, emitter, 1)

	// Release: notification reports no CIDs held.
	h.pushReport(notificationReport(m.deviceIndex, m.buttonsFeatureIndex, 0, 0, 0, 0))
	waitForEvents(t, emitter, 2)

	events := emitter.snapshot()
	if events[0].code != target || !events[0].down {
		t.Errorf("expected first event to be a press of 0x%x, got %+v", target, events[0])
	}
	if events[1].code != target || events[1].down {
		t.Errorf("expected second event to be a release of 0x%x, got %+v", target, events[1])
	}
}

func TestDivertedNotificationIgnoresOtherDeviceIndex(t *testing.T) {
	emitter := withFakeUinput(t)
	m, _, h := newTestMouse(t)
	defer m.Close()

	if err := m.RemapButton(0x53, uinputCode(t)); err != nil {
		t.Fatalf("RemapButton: %v", err)
	}

	h.pushReport(notificationReport(m.deviceIndex+1, m.buttonsFeatureIndex, 0x53, 0, 0, 0))
	time.Sleep(50 * time.Millisecond)

	if len(emitter.snapshot()) != 0 {
		t.Error("expected notifications for a different device index to be ignored")
	}
}

// notificationReport builds a raw REPROG_CONTROLS_V4 function-0
// (diverted-controls) notification report.
func notificationReport(deviceIndex, featureIndex byte, cid1, cid2, cid3, cid4 uint16) []byte {
	report := make([]byte, 20)
	report[0] = 0x11 // long report
	report[1] = deviceIndex
	report[2] = featureIndex
	report[3] = 0x00 // function 0, software ID 0 (spontaneous notification)
	binary.BigEndian.PutUint16(report[4:6], cid1)
	binary.BigEndian.PutUint16(report[6:8], cid2)
	binary.BigEndian.PutUint16(report[8:10], cid3)
	binary.BigEndian.PutUint16(report[10:12], cid4)
	return report
}

func uinputCode(t *testing.T) uint16 {
	t.Helper()
	return 30 // KEY_A; the exact code doesn't matter for these tests
}

func waitForEvents(t *testing.T, e *fakeEmitter, n int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if len(e.snapshot()) >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d emitted events, got %d", n, len(e.snapshot()))
		case <-time.After(5 * time.Millisecond):
		}
	}
}
