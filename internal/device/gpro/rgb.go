package gpro

import (
	"fmt"

	"logitux/internal/hidpp"
)

// COLOR_LED_EFFECTS (0x8070) function numbers.
const (
	ledFuncGetInfo           byte = 0x00
	ledFuncGetZoneInfo       byte = 0x10
	ledFuncGetZoneEffectInfo byte = 0x20
	ledFuncSetZoneEffect     byte = 0x30
	ledFuncSetControl        byte = 0x80
)

const (
	ledZoneLocationLogo uint16 = 0x02
	ledEffectIDStatic   uint16 = 0x01
)

// ledZoneTarget locates the mouse's Logo LED zone and its "Static" effect
// slot, resolved once during discovery: SetZoneEffect addresses an effect
// by its position in that zone's own effect list, not by the effect's ID.
type ledZoneTarget struct {
	zoneIndex         byte
	staticEffectIndex byte
}

// discoverLogoZone walks COLOR_LED_EFFECTS' GetInfo -> GetZoneInfo ->
// GetZoneEffectInfo calls to find the Logo zone and its Static effect.
func discoverLogoZone(conn *hidpp.Conn, deviceIndex, featureIndex byte) (ledZoneTarget, error) {
	info, err := conn.Call(deviceIndex, featureIndex, ledFuncGetInfo, nil)
	if err != nil {
		return ledZoneTarget{}, fmt.Errorf("gpro: LED GetInfo: %w", err)
	}
	if len(info) < 1 {
		return ledZoneTarget{}, fmt.Errorf("gpro: short LED GetInfo response")
	}
	zoneCount := int(info[0])

	for zone := 0; zone < zoneCount; zone++ {
		zi, err := conn.Call(deviceIndex, featureIndex, ledFuncGetZoneInfo, []byte{byte(zone), 0xff, 0x00})
		if err != nil {
			return ledZoneTarget{}, fmt.Errorf("gpro: LED GetZoneInfo(%d): %w", zone, err)
		}
		if len(zi) < 4 {
			continue
		}
		location := uint16(zi[1])<<8 | uint16(zi[2])
		if location != ledZoneLocationLogo {
			continue
		}
		effectCount := int(zi[3])

		for e := 0; e < effectCount; e++ {
			ei, err := conn.Call(deviceIndex, featureIndex, ledFuncGetZoneEffectInfo, []byte{byte(zone), byte(e), 0x00})
			if err != nil {
				return ledZoneTarget{}, fmt.Errorf("gpro: LED GetZoneEffectInfo(%d,%d): %w", zone, e, err)
			}
			if len(ei) < 4 {
				continue
			}
			effectID := uint16(ei[2])<<8 | uint16(ei[3])
			if effectID == ledEffectIDStatic {
				return ledZoneTarget{zoneIndex: byte(zone), staticEffectIndex: byte(e)}, nil
			}
		}
		return ledZoneTarget{}, fmt.Errorf("gpro: Logo LED zone has no Static effect")
	}
	return ledZoneTarget{}, fmt.Errorf("gpro: no Logo LED zone found")
}

// claimLEDControl switches the LED zones to host control. Without this,
// the device's onboard profile keeps driving the LED and a written color
// may not become visible. Called before every write rather than once at
// open time so it's unaffected by the device resetting control mode
// across a sleep/wake cycle.
func claimLEDControl(conn *hidpp.Conn, deviceIndex, featureIndex byte) error {
	if _, err := conn.Call(deviceIndex, featureIndex, ledFuncSetControl, []byte{0x01}); err != nil {
		return fmt.Errorf("gpro: claim LED control: %w", err)
	}
	return nil
}

// SetColor implements device.RGBControl, setting the Logo LED to a static
// color.
func (m *Mouse) SetColor(r, g, b uint8) error {
	if err := claimLEDControl(m.conn, m.deviceIndex, m.ledFeatureIndex); err != nil {
		return err
	}

	// zoneIndex, effectListIndex, then a 10-byte effect parameter block
	// (color R,G,B, ramp, and 6 zero-padding bytes for the Static effect).
	params := make([]byte, 12)
	params[0] = m.ledZoneIndex
	params[1] = m.ledStaticEffectIndex
	params[2] = r
	params[3] = g
	params[4] = b
	params[5] = 0x00 // ramp: Default

	if _, err := m.conn.Call(m.deviceIndex, m.ledFeatureIndex, ledFuncSetZoneEffect, params); err != nil {
		return fmt.Errorf("gpro: set LED color: %w", err)
	}
	return nil
}
