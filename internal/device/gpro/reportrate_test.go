package gpro

import (
	"reflect"
	"testing"
)

func TestReportRateOptionsFromBitmask(t *testing.T) {
	m, _, _ := newTestMouse(t)
	defer m.Close()

	// fakeMouse's default bitmask (0b10000001) advertises 1ms and 8ms.
	want := []int{1000, 125}
	if !reflect.DeepEqual(m.ReportRateOptions(), want) {
		t.Errorf("ReportRateOptions() = %v, want %v", m.ReportRateOptions(), want)
	}
}

func TestReportRateReadsCurrentValue(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()
	fm.reportRateMs = 8

	hz, err := m.ReportRate()
	if err != nil {
		t.Fatalf("ReportRate: %v", err)
	}
	if hz != 125 {
		t.Errorf("ReportRate() = %d, want 125", hz)
	}
}

func TestSetReportRateSnapsToClosestOption(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()

	if err := m.SetReportRate(1000); err != nil {
		t.Fatalf("SetReportRate(1000): %v", err)
	}
	if fm.reportRateMs != 1 {
		t.Errorf("device interval = %dms, want 1ms for 1000Hz", fm.reportRateMs)
	}

	// 900Hz isn't a supported option (only 1000Hz/125Hz are); should snap
	// to whichever is closer, i.e. 1000Hz -> 1ms (distance 100 vs 775).
	if err := m.SetReportRate(900); err != nil {
		t.Fatalf("SetReportRate(900): %v", err)
	}
	if fm.reportRateMs != 1 {
		t.Errorf("device interval = %dms, want snapped to 1ms (1000Hz)", fm.reportRateMs)
	}

	if err := m.SetReportRate(100); err != nil {
		t.Fatalf("SetReportRate(100): %v", err)
	}
	if fm.reportRateMs != 8 {
		t.Errorf("device interval = %dms, want snapped to 8ms (125Hz)", fm.reportRateMs)
	}
}
