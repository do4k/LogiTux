package prox

import (
	"encoding/binary"
	"io"
	"sync"
	"testing"
	"time"

	"logitux/internal/hid"
	"logitux/internal/hidpp"
)

func init() {
	probeTimeout = 20 * time.Millisecond
}

// fakeHandle simulates a hidraw device for hidpp.Conn to talk to. Same
// shape as internal/hidpp's and internal/device/gpro's own test doubles.
type fakeHandle struct {
	mu        sync.Mutex
	respQueue [][]byte
	responder func(request []byte) []byte

	notify chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newFakeHandle() *fakeHandle {
	return &fakeHandle{notify: make(chan struct{}, 1), closed: make(chan struct{})}
}

func (f *fakeHandle) Write(data []byte) (int, error) {
	cp := append([]byte(nil), data...)
	f.mu.Lock()
	var resp []byte
	if f.responder != nil {
		resp = f.responder(cp)
	}
	f.mu.Unlock()
	if resp != nil {
		f.pushReport(resp)
	}
	return len(data), nil
}

func (f *fakeHandle) pushReport(data []byte) {
	f.mu.Lock()
	f.respQueue = append(f.respQueue, data)
	f.mu.Unlock()
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

func (f *fakeHandle) Read(buf []byte) (int, error) {
	for {
		f.mu.Lock()
		if len(f.respQueue) > 0 {
			data := f.respQueue[0]
			f.respQueue = f.respQueue[1:]
			f.mu.Unlock()
			return copy(buf, data), nil
		}
		f.mu.Unlock()

		select {
		case <-f.notify:
			continue
		case <-f.closed:
			return 0, io.EOF
		}
	}
}

func (f *fakeHandle) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

var _ hid.Handle = (*fakeHandle)(nil)

// fakeHeadset simulates a PRO X Wireless's HID++ responses closely enough
// to exercise this plugin's request encoding and response decoding
// end-to-end without real hardware.
type fakeHeadset struct {
	mu sync.Mutex

	features map[uint16]byte

	batteryPercent int
	batteryStatus  byte

	sidetone byte

	eqCount   int
	eqDBRange int8
	eqFreqs   []uint16
	eqLevels  []int8
}

func newFakeHeadset() *fakeHeadset {
	return &fakeHeadset{
		features: map[uint16]byte{
			hidpp.FeatureUnifiedBattery: 0x02,
			featureSidetone:             0x03,
			featureEqualizer:            0x04,
		},
		batteryPercent: 80,
		batteryStatus:  hidpp.BatteryStatusDischarging,
		sidetone:       25,
		eqCount:        10,
		eqDBRange:      12,
		eqFreqs:        []uint16{32, 64, 125, 250, 500, 1000, 2000, 4000, 8000, 16000},
		eqLevels:       []int8{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
}

func (fh *fakeHeadset) respond(req []byte) []byte {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	deviceIndex, featureIndex, funcSwID := req[1], req[2], req[3]
	function := funcSwID & 0xf0
	params := req[4:]

	resp := make([]byte, 20)
	resp[0], resp[1], resp[2], resp[3] = req[0], deviceIndex, featureIndex, funcSwID

	switch featureIndex {
	case hidpp.RootFeatureIndex:
		switch function {
		case 0x00: // GetFeature
			featureID := binary.BigEndian.Uint16(params[0:2])
			resp[4] = fh.features[featureID]
		case 0x10: // Ping
			resp[4], resp[5] = 0x04, 0x02
		default:
			return fh.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fh.features[hidpp.FeatureUnifiedBattery]:
		switch function {
		case hidpp.BatteryFuncGetStatusUnified & 0xf0:
			resp[4] = byte(fh.batteryPercent)
			resp[5] = 0x00
			resp[6] = fh.batteryStatus
		default:
			return fh.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fh.features[featureSidetone]:
		switch function {
		case sidetoneFuncGet & 0xf0:
			resp[4] = fh.sidetone
		case sidetoneFuncSet & 0xf0:
			fh.sidetone = params[0]
		default:
			return fh.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fh.features[featureEqualizer]:
		switch function {
		case eqFuncGetInfo & 0xf0:
			resp[4] = byte(fh.eqCount)
			resp[5] = byte(fh.eqDBRange)
			resp[6] = 0x00
			resp[7] = 0x00 // dbMin: 0 means "derive from dbRange"
			resp[8] = 0x00 // dbMax: 0 means "derive from dbRange"
		case eqFuncGetBandFrequencies & 0xf0:
			start := int(params[0])
			for b := 0; b < 7; b++ {
				idx := start + b
				if idx >= len(fh.eqFreqs) {
					break
				}
				binary.BigEndian.PutUint16(resp[5+2*b:7+2*b], fh.eqFreqs[idx])
			}
		case eqFuncGetLevels & 0xf0:
			for i, lvl := range fh.eqLevels {
				resp[4+i] = byte(lvl)
			}
		case eqFuncSetLevels & 0xf0:
			for i := range fh.eqLevels {
				if 1+i >= len(params) {
					break
				}
				fh.eqLevels[i] = int8(params[1+i])
			}
		default:
			return fh.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	default:
		return fh.errorResponse(deviceIndex, featureIndex, funcSwID)
	}
	return resp
}

func (fh *fakeHeadset) errorResponse(deviceIndex, featureIndex, funcSwID byte) []byte {
	resp := make([]byte, 7)
	resp[0] = 0x10
	resp[1] = deviceIndex
	resp[2] = 0xff
	resp[3] = featureIndex
	resp[4] = funcSwID
	resp[5] = 0x02
	return resp
}

func newTestHeadset(t *testing.T) (*Headset, *fakeHeadset, *fakeHandle) {
	t.Helper()
	fh := newFakeHeadset()
	h := newFakeHandle()
	h.responder = fh.respond
	conn := hidpp.Open(h)

	headset, err := buildHeadset(conn, "TESTSN")
	if err != nil {
		conn.Close()
		t.Fatalf("buildHeadset: %v", err)
	}
	return headset, fh, h
}

func TestOpenRejectsWrongProductID(t *testing.T) {
	backend := &fakeBackend{}
	_, err := open(backend, hid.Info{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: 0x9999})
	if err == nil {
		t.Fatal("expected an error for an unrecognized product ID")
	}
}

type fakeBackend struct {
	responder func([]byte) []byte
}

func (b *fakeBackend) Enumerate(vendorID, productID uint16) ([]hid.Info, error) { return nil, nil }
func (b *fakeBackend) Open(info hid.Info) (hid.Handle, error) {
	h := newFakeHandle()
	h.responder = b.responder
	return h, nil
}

func TestOpenDirectHeadset(t *testing.T) {
	fh := newFakeHeadset()
	backend := &fakeBackend{responder: fh.respond}

	d, err := open(backend, hid.Info{Path: "/dev/hidraw0", VendorID: vendorID, ProductID: productID, Serial: "DIRECTSN"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if d.Info().Serial != "DIRECTSN" || d.Info().Name != "PRO X Wireless Gaming Headset" {
		t.Errorf("unexpected info: %+v", d.Info())
	}
}

func TestBattery(t *testing.T) {
	h, fh, _ := newTestHeadset(t)
	defer h.Close()
	fh.batteryPercent = 42

	percent, charging, err := h.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if percent != 42 || charging {
		t.Errorf("Battery() = (%d, %v), want (42, false)", percent, charging)
	}
}

func TestSidetoneGetSet(t *testing.T) {
	h, fh, _ := newTestHeadset(t)
	defer h.Close()

	got, err := h.Sidetone()
	if err != nil {
		t.Fatalf("Sidetone: %v", err)
	}
	if got != 25 {
		t.Errorf("Sidetone() = %d, want 25", got)
	}

	if err := h.SetSidetone(60); err != nil {
		t.Fatalf("SetSidetone: %v", err)
	}
	if fh.sidetone != 60 {
		t.Errorf("device sidetone = %d, want 60", fh.sidetone)
	}
}

func TestSetSidetoneClamps(t *testing.T) {
	h, fh, _ := newTestHeadset(t)
	defer h.Close()

	if err := h.SetSidetone(500); err != nil {
		t.Fatalf("SetSidetone: %v", err)
	}
	if fh.sidetone != 100 {
		t.Errorf("expected clamp to 100, got %d", fh.sidetone)
	}

	if err := h.SetSidetone(-10); err != nil {
		t.Fatalf("SetSidetone: %v", err)
	}
	if fh.sidetone != 0 {
		t.Errorf("expected clamp to 0, got %d", fh.sidetone)
	}
}

func TestEqualizerBandsDiscovered(t *testing.T) {
	h, fh, _ := newTestHeadset(t)
	defer h.Close()

	bands := h.EqualizerBands()
	if len(bands) != fh.eqCount {
		t.Fatalf("expected %d bands, got %d", fh.eqCount, len(bands))
	}
	for i, b := range bands {
		if b.FrequencyHz != int(fh.eqFreqs[i]) {
			t.Errorf("band %d frequency = %d, want %d", i, b.FrequencyHz, fh.eqFreqs[i])
		}
	}

	min, max := h.EqualizerRange()
	if min != -12 || max != 12 {
		t.Errorf("EqualizerRange() = (%d, %d), want (-12, 12)", min, max)
	}
}

func TestEqualizerLevelsGetSet(t *testing.T) {
	h, fh, _ := newTestHeadset(t)
	defer h.Close()
	fh.eqLevels = []int8{-3, 0, 2, 5, -8, 0, 0, 0, 0, 0}

	levels, err := h.EqualizerLevels()
	if err != nil {
		t.Fatalf("EqualizerLevels: %v", err)
	}
	want := []int{-3, 0, 2, 5, -8, 0, 0, 0, 0, 0}
	for i, v := range want {
		if levels[i] != v {
			t.Errorf("level %d = %d, want %d", i, levels[i], v)
		}
	}

	newLevels := make([]int, len(fh.eqLevels))
	for i := range newLevels {
		newLevels[i] = 4
	}
	if err := h.SetEqualizerLevels(newLevels); err != nil {
		t.Fatalf("SetEqualizerLevels: %v", err)
	}
	for i, lvl := range fh.eqLevels {
		if lvl != 4 {
			t.Errorf("device level %d = %d, want 4", i, lvl)
		}
	}
}

func TestSetEqualizerLevelsClampsToRange(t *testing.T) {
	h, fh, _ := newTestHeadset(t)
	defer h.Close()

	newLevels := make([]int, len(fh.eqLevels))
	for i := range newLevels {
		newLevels[i] = 100 // way outside +/-12dB
	}
	if err := h.SetEqualizerLevels(newLevels); err != nil {
		t.Fatalf("SetEqualizerLevels: %v", err)
	}
	for i, lvl := range fh.eqLevels {
		if lvl != 12 {
			t.Errorf("device level %d = %d, want clamped to 12", i, lvl)
		}
	}
}

func TestSetEqualizerLevelsWrongCount(t *testing.T) {
	h, _, _ := newTestHeadset(t)
	defer h.Close()

	if err := h.SetEqualizerLevels([]int{1, 2, 3}); err == nil {
		t.Fatal("expected an error when the level count doesn't match the band count")
	}
}
