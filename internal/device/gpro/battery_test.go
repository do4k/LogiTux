package gpro

import (
	"testing"

	"logitux/internal/hidpp"
)

func TestBatteryReportsPercentAndDischargingState(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()
	fm.batteryPercent = 55
	fm.batteryStatus = hidpp.BatteryStatusDischarging

	percent, charging, err := m.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if percent != 55 || charging {
		t.Errorf("Battery() = (%d, %v), want (55, false)", percent, charging)
	}
}

func TestBatteryReportsChargingStates(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()

	chargingStates := []byte{hidpp.BatteryStatusRecharging, hidpp.BatteryStatusAlmostFull, hidpp.BatteryStatusFull, hidpp.BatteryStatusSlowRecharge}
	for _, status := range chargingStates {
		fm.batteryStatus = status
		_, charging, err := m.Battery()
		if err != nil {
			t.Fatalf("Battery: %v", err)
		}
		if !charging {
			t.Errorf("status 0x%02x: expected charging=true", status)
		}
	}
}

// TestBatteryFallsBackToLegacyFeature covers the real hardware case this
// plugin was validated against: a G Pro Wireless that only implements the
// older BATTERY_STATUS (0x1000), not UNIFIED_BATTERY (0x1004).
func TestBatteryFallsBackToLegacyFeature(t *testing.T) {
	m, fm, _ := newTestMouseFrom(t, newFakeMouseWithLegacyBattery())
	defer m.Close()
	fm.batteryPercent = 40
	fm.batteryStatus = hidpp.BatteryStatusDischarging

	percent, charging, err := m.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if percent != 40 || charging {
		t.Errorf("Battery() = (%d, %v), want (40, false)", percent, charging)
	}
}

// TestBatteryLegacyZeroPercentMeansUnknown covers BATTERY_STATUS's (but
// not UNIFIED_BATTERY's) special case: a reported percent of 0 means
// "unavailable", not literally empty.
func TestBatteryLegacyZeroPercentMeansUnknown(t *testing.T) {
	m, fm, _ := newTestMouseFrom(t, newFakeMouseWithLegacyBattery())
	defer m.Close()
	fm.batteryPercent = 0

	_, _, err := m.Battery()
	if err == nil {
		t.Fatal("expected an error when the legacy feature reports 0%% (unknown)")
	}
}

// TestBatteryFallsBackToVoltageFeature covers a mouse with neither modern
// battery feature, only BATTERY_VOLTAGE (0x1001), estimating a percentage
// from the reported millivolts via the discharge curve.
func TestBatteryFallsBackToVoltageFeature(t *testing.T) {
	m, _, _ := newTestMouseFrom(t, newFakeMouseWithVoltageBattery())
	defer m.Close()

	percent, charging, err := m.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if percent != 70 || charging {
		t.Errorf("Battery() = (%d, %v), want (70, false) for 3922mV", percent, charging)
	}
}

// TestBatteryVoltageChargingFlag covers BATTERY_VOLTAGE's charging bit
// (bit 7 of the flags byte).
func TestBatteryVoltageChargingFlag(t *testing.T) {
	fm := newFakeMouseWithVoltageBattery()
	fm.batteryVoltFlags = 0x80
	m, _, _ := newTestMouseFrom(t, fm)
	defer m.Close()

	_, charging, err := m.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if !charging {
		t.Error("expected charging=true when the voltage flags' bit 7 is set")
	}
}

// TestBatteryFallsBackToADCFeature covers a mouse with only ADC_MEASUREMENT
// (0x1F20), the last-resort fallback.
func TestBatteryFallsBackToADCFeature(t *testing.T) {
	m, _, _ := newTestMouseFrom(t, newFakeMouseWithADCBattery())
	defer m.Close()

	percent, charging, err := m.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if percent != 70 || charging {
		t.Errorf("Battery() = (%d, %v), want (70, false)", percent, charging)
	}
}

// TestBatteryADCInvalidReading covers ADC_MEASUREMENT's "valid" bit: a
// clear bit 0 means no usable reading yet, not a literal error.
func TestBatteryADCInvalidReading(t *testing.T) {
	fm := newFakeMouseWithADCBattery()
	fm.batteryVoltFlags = 0x00 // valid bit clear
	m, _, _ := newTestMouseFrom(t, fm)
	defer m.Close()

	if _, _, err := m.Battery(); err == nil {
		t.Fatal("expected an error when the ADC reading isn't marked valid")
	}
}

func TestEstimateBatteryPercentClampsAndInterpolates(t *testing.T) {
	cases := []struct {
		mv   int
		want int
	}{
		{5000, 100}, // above table range, clamps to max
		{4186, 100},
		{3811, 50},
		{3500, 0},
		{3000, 0},  // below table range, clamps to min
		{3946, 74}, // interpolated between 3922(70%) and 3989(80%)
	}
	for _, c := range cases {
		if got := hidpp.EstimateBatteryPercent(c.mv); got != c.want {
			t.Errorf("hidpp.EstimateBatteryPercent(%d) = %d, want %d", c.mv, got, c.want)
		}
	}
}
