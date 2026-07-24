package gpro

import (
	"encoding/binary"
	"fmt"

	"logitux/internal/hidpp"
)

// EXTENDED_ADJUSTABLE_REPORT_RATE (0x8061) function numbers. Like
// EXTENDED_ADJUSTABLE_DPI, newer mice implement this *instead of*
// REPORT_RATE (0x8060); it extends the range to 8000 Hz and models rates
// as an index into a fixed table rather than a millisecond interval.
// Byte layouts verified against OpenLogi (extended_report_rate).
const (
	extRateFuncGetRateList byte = 0x10 // rates available on the current connection
	extRateFuncGetRate     byte = 0x20 // takes a connection type
	extRateFuncSetRate     byte = 0x30 // applies to the current connection
)

// Connection types for extRateFuncGetRate.
const (
	extRateConnWired          byte = 0
	extRateConnGamingWireless byte = 1 // Lightspeed
)

// extRateHz maps the feature's rate index (0-6) to Hz.
var extRateHz = []int{125, 250, 500, 1000, 2000, 4000, 8000}

// getExtendedReportRateOptions reads the bitmask of rates supported on
// the current connection (bit i = extRateHz[i]) and returns them as Hz,
// fastest first — the same ordering getReportRateOptions uses.
func getExtendedReportRateOptions(conn *hidpp.Conn, deviceIndex, featureIndex byte) ([]int, error) {
	resp, err := conn.Call(deviceIndex, featureIndex, extRateFuncGetRateList, nil)
	if err != nil {
		return nil, fmt.Errorf("gpro: get extended report rate list: %w", err)
	}
	if len(resp) < 2 {
		return nil, fmt.Errorf("gpro: short extended report rate list response")
	}
	mask := binary.BigEndian.Uint16(resp[0:2])

	var options []int
	for i := len(extRateHz) - 1; i >= 0; i-- {
		if mask&(1<<uint(i)) != 0 {
			options = append(options, extRateHz[i])
		}
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("gpro: device reports no supported report rates")
	}
	return options, nil
}

// extendedReportRate reads the active rate. The read takes a connection
// type; the likely one is tried first based on how the mouse is
// addressed (direct = wired, receiver-paired = Lightspeed), falling back
// to the other, since a direct hidraw node can still belong to a
// receiver-paired unit (see openDirect).
func (m *Mouse) extendedReportRate() (int, error) {
	first, second := extRateConnGamingWireless, extRateConnWired
	if m.deviceIndex == hidpp.DeviceIndexDirect {
		first, second = second, first
	}

	var lastErr error
	for _, connType := range []byte{first, second} {
		resp, err := m.conn.Call(m.deviceIndex, m.extRateFeatureIndex, extRateFuncGetRate, []byte{connType})
		if err != nil {
			lastErr = err
			continue
		}
		if len(resp) < 1 || int(resp[0]) >= len(extRateHz) {
			lastErr = fmt.Errorf("gpro: invalid extended report rate response")
			continue
		}
		return extRateHz[resp[0]], nil
	}
	return 0, fmt.Errorf("gpro: get extended report rate: %w", lastErr)
}

// setExtendedReportRate snaps hz to the closest supported option and
// writes its rate index.
func (m *Mouse) setExtendedReportRate(hz int) error {
	hz = closestOption(hz, m.reportRateOptions)
	index := -1
	for i, v := range extRateHz {
		if v == hz {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("gpro: unsupported report rate %d Hz", hz)
	}
	if _, err := m.conn.Call(m.deviceIndex, m.extRateFeatureIndex, extRateFuncSetRate, []byte{byte(index)}); err != nil {
		return fmt.Errorf("gpro: set extended report rate: %w", err)
	}
	return nil
}
