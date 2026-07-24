package gpro

import (
	"fmt"

	"logitux/internal/hidpp"
)

const (
	reportRateFuncGetList byte = 0x00
	reportRateFuncGet     byte = 0x10
	reportRateFuncSet     byte = 0x20
)

// getReportRateOptions queries which report intervals (1-8 ms, as a
// bitmask over bit i = interval i+1) the device supports and returns them
// as Hz values, ordered by interval ascending (so fastest/highest Hz
// first).
func getReportRateOptions(conn *hidpp.Conn, deviceIndex, featureIndex byte) ([]int, error) {
	resp, err := conn.Call(deviceIndex, featureIndex, reportRateFuncGetList, nil)
	if err != nil {
		return nil, fmt.Errorf("gpro: get report rate list: %w", err)
	}
	if len(resp) < 1 {
		return nil, fmt.Errorf("gpro: short report rate list response")
	}

	bitmask := resp[0]
	var options []int
	for i := 0; i < 8; i++ {
		if bitmask&(1<<uint(i)) == 0 {
			continue
		}
		intervalMs := i + 1
		options = append(options, 1000/intervalMs)
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("gpro: device reports no supported report rates")
	}
	return options, nil
}

// ReportRateOptions implements device.ReportRateControl.
func (m *Mouse) ReportRateOptions() []int {
	return m.reportRateOptions
}

// ReportRate implements device.ReportRateControl.
func (m *Mouse) ReportRate() (int, error) {
	if m.extRateFeatureIndex != 0 {
		return m.extendedReportRate()
	}
	resp, err := m.conn.Call(m.deviceIndex, m.reportRateFeatureIndex, reportRateFuncGet, nil)
	if err != nil {
		return 0, fmt.Errorf("gpro: get report rate: %w", err)
	}
	if len(resp) < 1 || resp[0] == 0 {
		return 0, fmt.Errorf("gpro: invalid report rate response")
	}
	return 1000 / int(resp[0]), nil
}

// SetReportRate implements device.ReportRateControl. hz is snapped to
// whichever supported option is closest.
func (m *Mouse) SetReportRate(hz int) error {
	if m.extRateFeatureIndex != 0 {
		return m.setExtendedReportRate(hz)
	}
	intervalMs := 1000 / closestOption(hz, m.reportRateOptions)
	if _, err := m.conn.Call(m.deviceIndex, m.reportRateFeatureIndex, reportRateFuncSet, []byte{byte(intervalMs)}); err != nil {
		return fmt.Errorf("gpro: set report rate: %w", err)
	}
	return nil
}

func closestOption(hz int, options []int) int {
	best := options[0]
	for _, o := range options {
		if abs(o-hz) < abs(best-hz) {
			best = o
		}
	}
	return best
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
