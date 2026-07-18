package gpro

import "testing"

func TestDPIRange(t *testing.T) {
	m, _, _ := newTestMouse(t)
	defer m.Close()

	min, max, step := m.DPIRange()
	if min != 100 || max != 25600 || step != 50 {
		t.Errorf("DPIRange() = (%d, %d, %d), want (100, 25600, 50)", min, max, step)
	}
}

func TestDPIReadsCurrentValue(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()
	fm.dpi = 1600

	got, err := m.DPI()
	if err != nil {
		t.Fatalf("DPI: %v", err)
	}
	if got != 1600 {
		t.Errorf("DPI() = %d, want 1600", got)
	}
}

func TestDPIFallsBackToDefaultWhenCurrentIsZero(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()
	fm.dpi = 0
	fm.dpiDefault = 800

	got, err := m.DPI()
	if err != nil {
		t.Fatalf("DPI: %v", err)
	}
	if got != 800 {
		t.Errorf("DPI() = %d, want fallback to default 800", got)
	}
}

func TestSetDPIRoundsToStepAndClamps(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()

	cases := []struct {
		set  int
		want uint16
	}{
		{1600, 1600},   // already on-step
		{1624, 1600},   // rounds down to nearest 50
		{1626, 1650},   // rounds up to nearest 50
		{-100, 100},    // clamped to min
		{99999, 25600}, // clamped to max
	}
	for _, c := range cases {
		if err := m.SetDPI(c.set); err != nil {
			t.Fatalf("SetDPI(%d): %v", c.set, err)
		}
		if fm.dpi != c.want {
			t.Errorf("SetDPI(%d): device DPI = %d, want %d", c.set, fm.dpi, c.want)
		}
	}
}
