package gpro

import (
	"fmt"

	"logitux/internal/hidpp"
)

// Battery implements device.BatteryStatus.
func (m *Mouse) Battery() (percent int, charging bool, err error) {
	if m.batteryFeatureIndex == 0 {
		return 0, false, fmt.Errorf("gpro: device has no supported battery feature")
	}
	return hidpp.ReadBattery(m.conn, m.deviceIndex, m.batteryFeatureIndex, m.batteryKind)
}
