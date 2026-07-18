//go:build linux

package uinput

import "testing"

// TestIoctlConstants checks the hand-derived _IO/_IOW request codes against
// their well-known values (as widely documented for linux/uinput.h on a
// 4-byte-int platform), to catch a mistake in the shift/macro arithmetic
// without needing a real /dev/uinput to exercise.
func TestIoctlConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint
		want uint
	}{
		{"UI_DEV_CREATE", uiDevCreate, 0x5501},
		{"UI_DEV_DESTROY", uiDevDestroy, 0x5502},
		{"UI_SET_EVBIT", uiSetEvBit, 0x40045564},
		{"UI_SET_KEYBIT", uiSetKeyBit, 0x40045565},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = 0x%x, want 0x%x", c.name, c.got, c.want)
		}
	}
}

func TestTargetsHaveUniqueLabelsAndCodes(t *testing.T) {
	labels := make(map[string]bool)
	for _, tgt := range Targets {
		if labels[tgt.Label] {
			t.Errorf("duplicate target label %q", tgt.Label)
		}
		labels[tgt.Label] = true
		if tgt.Code == 0 {
			t.Errorf("target %q has zero code", tgt.Label)
		}
	}
}

func TestAllCodesMatchesTargets(t *testing.T) {
	codes := AllCodes()
	if len(codes) != len(Targets) {
		t.Fatalf("AllCodes() returned %d codes, want %d (one per Target)", len(codes), len(Targets))
	}
	for i, tgt := range Targets {
		if codes[i] != tgt.Code {
			t.Errorf("AllCodes()[%d] = 0x%x, want 0x%x (%s)", i, codes[i], tgt.Code, tgt.Label)
		}
	}
}
