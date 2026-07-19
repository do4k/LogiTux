package hidpp

import (
	"encoding/binary"
	"testing"
)

// batteryResponder simulates a device exposing exactly one battery
// feature at featureIndex 0x05, answering Root.GetFeature for featureID
// and dispatching any call on that feature index to handle.
func batteryResponder(featureID uint16, handle func(function byte, params []byte) []byte) func([]byte) []byte {
	const idx = 0x05
	return func(req []byte) []byte {
		deviceIndex, featureIndex, funcSwID := req[1], req[2], req[3]
		function := funcSwID & 0xf0
		params := req[4:]

		resp := make([]byte, longReportLen)
		resp[0], resp[1], resp[3] = reportIDLong, deviceIndex, funcSwID

		if featureIndex == RootFeatureIndex {
			if function != 0x00 {
				return nil
			}
			resp[2] = RootFeatureIndex
			if binary.BigEndian.Uint16(params[0:2]) == featureID {
				resp[4] = idx
			}
			return resp
		}

		if featureIndex != idx {
			return nil
		}
		payload := handle(function, params)
		if payload == nil {
			return nil
		}
		resp[2] = idx
		copy(resp[4:], payload)
		return resp
	}
}

func TestResolveBatteryFeaturePrefersUnified(t *testing.T) {
	h := newFakeHandle()
	h.responder = batteryResponder(FeatureUnifiedBattery, func(byte, []byte) []byte { return []byte{} })
	c := Open(h)
	defer c.Close()

	idx, kind, err := ResolveBatteryFeature(c, 0x01)
	if err != nil {
		t.Fatalf("ResolveBatteryFeature: %v", err)
	}
	if idx != 0x05 || kind != BatteryKindUnified {
		t.Errorf("got idx=%d kind=%v, want idx=5 kind=Unified", idx, kind)
	}
}

func TestResolveBatteryFeatureFallsBackToLegacy(t *testing.T) {
	h := newFakeHandle()
	h.responder = batteryResponder(FeatureBatteryStatus, func(byte, []byte) []byte { return []byte{} })
	c := Open(h)
	defer c.Close()

	_, kind, err := ResolveBatteryFeature(c, 0x01)
	if err != nil {
		t.Fatalf("ResolveBatteryFeature: %v", err)
	}
	if kind != BatteryKindLegacyStatus {
		t.Errorf("got kind=%v, want LegacyStatus", kind)
	}
}

func TestResolveBatteryFeatureNoneFound(t *testing.T) {
	h := newFakeHandle()
	h.responder = func(req []byte) []byte {
		deviceIndex, featureIndex, funcSwID := req[1], req[2], req[3]
		if featureIndex != RootFeatureIndex || funcSwID&0xf0 != 0x00 {
			return nil
		}
		resp := make([]byte, longReportLen)
		resp[0], resp[1], resp[2], resp[3] = reportIDLong, deviceIndex, RootFeatureIndex, funcSwID
		return resp // featureIndex byte (resp[4]) stays 0: "not supported"
	}
	c := Open(h)
	defer c.Close()

	_, kind, err := ResolveBatteryFeature(c, 0x01)
	if err != nil {
		t.Fatalf("ResolveBatteryFeature: %v", err)
	}
	if kind != BatteryKindNone {
		t.Errorf("expected BatteryKindNone, got %v", kind)
	}
}

func TestReadBatteryUnified(t *testing.T) {
	h := newFakeHandle()
	h.responder = batteryResponder(FeatureUnifiedBattery, func(function byte, params []byte) []byte {
		if function != BatteryFuncGetStatusUnified {
			return nil
		}
		return []byte{55, 0x00, BatteryStatusRecharging, 0x00}
	})
	c := Open(h)
	defer c.Close()

	idx, kind, err := ResolveBatteryFeature(c, 0x01)
	if err != nil {
		t.Fatalf("ResolveBatteryFeature: %v", err)
	}
	percent, charging, err := ReadBattery(c, 0x01, idx, kind)
	if err != nil {
		t.Fatalf("ReadBattery: %v", err)
	}
	if percent != 55 || !charging {
		t.Errorf("ReadBattery() = (%d, %v), want (55, true)", percent, charging)
	}
}

func TestReadBatteryVoltageEstimatesPercent(t *testing.T) {
	h := newFakeHandle()
	h.responder = batteryResponder(FeatureBatteryVoltage, func(function byte, params []byte) []byte {
		if function != BatteryFuncGetVoltage {
			return nil
		}
		buf := make([]byte, 3)
		binary.BigEndian.PutUint16(buf[0:2], 3922)
		buf[2] = 0x00
		return buf
	})
	c := Open(h)
	defer c.Close()

	idx, kind, err := ResolveBatteryFeature(c, 0x01)
	if err != nil {
		t.Fatalf("ResolveBatteryFeature: %v", err)
	}
	percent, _, err := ReadBattery(c, 0x01, idx, kind)
	if err != nil {
		t.Fatalf("ReadBattery: %v", err)
	}
	if percent != 70 {
		t.Errorf("ReadBattery() percent = %d, want 70", percent)
	}
}

func TestReadBatteryUnknownKind(t *testing.T) {
	h := newFakeHandle()
	c := Open(h)
	defer c.Close()

	if _, _, err := ReadBattery(c, 0x01, 0x00, BatteryKindNone); err == nil {
		t.Fatal("expected an error for BatteryKindNone")
	}
}
