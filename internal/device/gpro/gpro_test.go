package gpro

import (
	"encoding/binary"
	"io"
	"sync"
	"testing"

	"logitux/internal/hid"
	"logitux/internal/hidpp"
)

// fakeHandle simulates a hidraw device for hidpp.Conn to talk to. Same
// shape as internal/hidpp's own test double, duplicated here since that
// one is unexported.
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

// fakeMouse simulates a G Pro Wireless's HID++ responses: feature
// discovery via Root, and the DPI/battery/report-rate/LED features this
// plugin uses, closely enough to exercise the plugin's request encoding
// and response decoding end to end without real hardware.
type fakeMouse struct {
	mu sync.Mutex

	features map[uint16]byte // featureID -> assigned featureIndex

	dpi, dpiDefault uint16

	batteryPercent int
	batteryStatus  byte

	batteryMillivolts int
	batteryVoltFlags  byte

	reportRateBitmask byte
	reportRateMs      byte

	ledControl byte
	ledColor   [3]byte

	buttonCIDs       []uint16
	buttonDivertable map[uint16]bool
	diverted         map[uint16]bool // last value set via setCidReporting, for test assertions
}

func newFakeMouse() *fakeMouse {
	return &fakeMouse{
		features: map[uint16]byte{
			featureAdjustableDPI:        0x01,
			hidpp.FeatureUnifiedBattery: 0x02,
			featureReportRate:           0x03,
			featureColorLEDEffects:      0x04,
			featureReprogControlsV4:     0x05,
		},
		dpi:               800,
		dpiDefault:        800,
		batteryPercent:    72,
		batteryStatus:     hidpp.BatteryStatusDischarging,
		reportRateBitmask: 0b10000001, // 1ms (1000Hz) and 8ms (125Hz)
		reportRateMs:      1,
		// 0x53 = Back Button (divertable), 0x50 = Left Button (not
		// divertable — every real device refuses to divert its primary
		// click), matching the real CID assignments this plugin was
		// verified against.
		buttonCIDs:       []uint16{0x50, 0x53},
		buttonDivertable: map[uint16]bool{0x50: false, 0x53: true},
		diverted:         make(map[uint16]bool),
	}
}

// newFakeMouseWithLegacyBattery simulates a mouse that only implements the
// older BATTERY_STATUS (0x1000), not UNIFIED_BATTERY (0x1004) — the real
// case this plugin was validated against on actual G Pro Wireless
// hardware.
func newFakeMouseWithLegacyBattery() *fakeMouse {
	fm := newFakeMouse()
	delete(fm.features, hidpp.FeatureUnifiedBattery)
	fm.features[hidpp.FeatureBatteryStatus] = 0x02
	return fm
}

// newFakeMouseWithVoltageBattery simulates a mouse with neither modern
// battery feature, only BATTERY_VOLTAGE (0x1001).
func newFakeMouseWithVoltageBattery() *fakeMouse {
	fm := newFakeMouse()
	delete(fm.features, hidpp.FeatureUnifiedBattery)
	fm.features[hidpp.FeatureBatteryVoltage] = 0x02
	fm.batteryMillivolts = 3922 // ~70% per the discharge curve
	return fm
}

// newFakeMouseWithADCBattery simulates a mouse with only ADC_MEASUREMENT
// (0x1F20).
func newFakeMouseWithADCBattery() *fakeMouse {
	fm := newFakeMouse()
	delete(fm.features, hidpp.FeatureUnifiedBattery)
	fm.features[hidpp.FeatureADCMeasurement] = 0x02
	fm.batteryMillivolts = 3922
	fm.batteryVoltFlags = 0x01 // valid reading, discharging
	return fm
}

func (fm *fakeMouse) respond(req []byte) []byte {
	fm.mu.Lock()
	defer fm.mu.Unlock()

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
			resp[4] = fm.features[featureID] // 0 if unknown, matching the real protocol
		case 0x10: // Ping / GetProtocolVersion
			resp[4], resp[5] = 0x04, 0x02
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[featureAdjustableDPI]:
		switch function {
		case dpiFuncGetDpi & 0xf0:
			resp[4] = params[0]
			binary.BigEndian.PutUint16(resp[5:7], fm.dpi)
			binary.BigEndian.PutUint16(resp[7:9], fm.dpiDefault)
		case dpiFuncSetDpi & 0xf0:
			fm.dpi = binary.BigEndian.Uint16(params[1:3])
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[hidpp.FeatureUnifiedBattery]:
		switch function {
		case hidpp.BatteryFuncGetStatusUnified & 0xf0:
			resp[4] = byte(fm.batteryPercent)
			resp[5] = 0x00
			resp[6] = fm.batteryStatus
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[hidpp.FeatureBatteryStatus]:
		switch function {
		case hidpp.BatteryFuncGetStatusLegacy & 0xf0:
			resp[4] = byte(fm.batteryPercent)
			resp[5] = 0x00 // next threshold, unused by our code
			resp[6] = fm.batteryStatus
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[hidpp.FeatureBatteryVoltage]:
		switch function {
		case hidpp.BatteryFuncGetVoltage & 0xf0:
			binary.BigEndian.PutUint16(resp[4:6], uint16(fm.batteryMillivolts))
			resp[6] = fm.batteryVoltFlags
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[hidpp.FeatureADCMeasurement]:
		switch function {
		case hidpp.BatteryFuncGetADC & 0xf0:
			binary.BigEndian.PutUint16(resp[4:6], uint16(fm.batteryMillivolts))
			resp[6] = fm.batteryVoltFlags
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[featureReportRate]:
		switch function {
		case reportRateFuncGetList & 0xf0:
			resp[4] = fm.reportRateBitmask
		case reportRateFuncGet & 0xf0:
			resp[4] = fm.reportRateMs
		case reportRateFuncSet & 0xf0:
			fm.reportRateMs = params[0]
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[featureColorLEDEffects]:
		switch function {
		case ledFuncGetInfo & 0xf0:
			resp[4] = 0x02 // 2 zones: Primary(0), Logo(1)
		case ledFuncGetZoneInfo & 0xf0:
			zone := params[0]
			if zone == 0 {
				resp[5], resp[6], resp[7] = 0x00, 0x01, 0x01 // location Primary, 1 effect
			} else {
				resp[5], resp[6], resp[7] = 0x00, 0x02, 0x02 // location Logo, 2 effects
			}
		case ledFuncGetZoneEffectInfo & 0xf0:
			zone, effect := params[0], params[1]
			resp[4], resp[5] = zone, effect
			if zone == 1 && effect == 1 {
				binary.BigEndian.PutUint16(resp[6:8], ledEffectIDStatic)
			} else {
				binary.BigEndian.PutUint16(resp[6:8], 0x00) // Disabled
			}
		case ledFuncSetZoneEffect & 0xf0:
			copy(fm.ledColor[:], params[2:5])
		case ledFuncSetControl & 0xf0:
			fm.ledControl = params[0]
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	case fm.features[featureReprogControlsV4]:
		switch function {
		case buttonsFuncGetCount & 0xf0:
			resp[4] = byte(len(fm.buttonCIDs))
		case buttonsFuncGetCidInfo & 0xf0:
			index := int(params[0])
			if index < 0 || index >= len(fm.buttonCIDs) {
				return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
			}
			cid := fm.buttonCIDs[index]
			binary.BigEndian.PutUint16(resp[4:6], cid)
			if fm.buttonDivertable[cid] {
				resp[8] = byte(keyFlagDivertable) // fits in flags1's low byte
			}
		case buttonsFuncSetCidReporting & 0xf0:
			cid := binary.BigEndian.Uint16(params[0:2])
			bfield := params[2]
			fm.diverted[cid] = bfield&mappingFlagDiverted != 0
		default:
			return fm.errorResponse(deviceIndex, featureIndex, funcSwID)
		}

	default:
		return fm.errorResponse(deviceIndex, featureIndex, funcSwID) // unknown feature index
	}
	return resp
}

func (fm *fakeMouse) errorResponse(deviceIndex, featureIndex, funcSwID byte) []byte {
	resp := make([]byte, 7)
	resp[0] = 0x10
	resp[1] = deviceIndex
	resp[2] = 0xff
	resp[3] = featureIndex
	resp[4] = funcSwID
	resp[5] = 0x02 // HID++ 2.0 "invalid function" (illustrative code for tests)
	return resp
}

// newTestMouse builds a *Mouse wired to a fakeMouse via the real discovery
// path (buildMouse), so feature-index resolution is exercised too, not
// just the individual feature calls.
func newTestMouse(t *testing.T) (*Mouse, *fakeMouse, *fakeHandle) {
	t.Helper()
	return newTestMouseFrom(t, newFakeMouse())
}

func newTestMouseFrom(t *testing.T, fm *fakeMouse) (*Mouse, *fakeMouse, *fakeHandle) {
	t.Helper()
	h := newFakeHandle()
	h.responder = fm.respond
	conn := hidpp.Open(h)

	m, err := buildMouse(conn, 0x01, "TESTSERIAL-1")
	if err != nil {
		conn.Close()
		t.Fatalf("buildMouse: %v", err)
	}
	return m, fm, h
}

func TestBuildMouseResolvesAllFeatures(t *testing.T) {
	m, _, _ := newTestMouse(t)
	defer m.Close()

	if m.Info().Name != "G Pro Wireless" || m.Info().Serial != "TESTSERIAL-1" {
		t.Errorf("unexpected info: %+v", m.Info())
	}
	if m.dpiFeatureIndex != 0x01 || m.batteryFeatureIndex != 0x02 ||
		m.reportRateFeatureIndex != 0x03 || m.ledFeatureIndex != 0x04 {
		t.Errorf("unexpected resolved feature indexes: %+v", m)
	}
	if m.ledZoneIndex != 0x01 || m.ledStaticEffectIndex != 0x01 {
		t.Errorf("expected Logo zone (index 1) with Static at effect-list index 1, got zone=%d effect=%d",
			m.ledZoneIndex, m.ledStaticEffectIndex)
	}
}

func TestBuildMouseFailsWhenARequiredFeatureIsMissing(t *testing.T) {
	fm := newFakeMouse()
	delete(fm.features, featureAdjustableDPI) // DPI is required, unlike battery (see below)
	h := newFakeHandle()
	h.responder = fm.respond
	conn := hidpp.Open(h)
	defer conn.Close()

	_, err := buildMouse(conn, 0x01, "SN")
	if err == nil {
		t.Fatal("expected an error when a required feature is missing")
	}
}

// TestBuildMouseToleratesMissingBattery mirrors a real unit found during
// hardware testing: it answered "not supported" for both known battery
// feature IDs. DPI/report-rate/RGB should still work; only Battery()
// should report an error.
func TestBuildMouseToleratesMissingBattery(t *testing.T) {
	fm := newFakeMouse()
	delete(fm.features, hidpp.FeatureUnifiedBattery) // and no hidpp.FeatureBatteryStatus either

	m, _, _ := newTestMouseFrom(t, fm)
	defer m.Close()

	if _, _, err := m.Battery(); err == nil {
		t.Error("expected Battery() to report an error when no battery feature was found")
	}
	// DPI should still work.
	if _, err := m.DPI(); err != nil {
		t.Errorf("expected DPI() to still work despite missing battery, got: %v", err)
	}
}
