package gpro

import "testing"

func TestSetColorClaimsHostControlAndWritesLogoZone(t *testing.T) {
	m, fm, _ := newTestMouse(t)
	defer m.Close()

	if err := m.SetColor(0x11, 0x22, 0x33); err != nil {
		t.Fatalf("SetColor: %v", err)
	}

	if fm.ledControl != 0x01 {
		t.Errorf("expected LED control to be claimed for the host (0x01), got 0x%02x", fm.ledControl)
	}
	want := [3]byte{0x11, 0x22, 0x33}
	if fm.ledColor != want {
		t.Errorf("device LED color = % x, want % x", fm.ledColor, want)
	}
}

func TestDiscoverLogoZoneSkipsNonLogoZones(t *testing.T) {
	m, _, _ := newTestMouse(t)
	defer m.Close()

	// fakeMouse zone 0 is "Primary" (location 0x01) with only a Disabled
	// effect; zone 1 is "Logo" (location 0x02) with Disabled + Static.
	// buildMouse must have picked zone 1, not zone 0.
	if m.ledZoneIndex != 1 {
		t.Errorf("ledZoneIndex = %d, want 1 (Logo)", m.ledZoneIndex)
	}
}
