package uinput

// Standard Linux input event codes (linux/input-event-codes.h), stable
// across kernel versions. This is a curated subset covering common remap
// targets, not the full table.
const (
	BtnLeft    uint16 = 0x110
	BtnRight   uint16 = 0x111
	BtnMiddle  uint16 = 0x112
	BtnSide    uint16 = 0x113
	BtnExtra   uint16 = 0x114
	BtnForward uint16 = 0x115
	BtnBack    uint16 = 0x116
)

const (
	KeyEsc        uint16 = 1
	KeyBackspace  uint16 = 14
	KeyTab        uint16 = 15
	KeyEnter      uint16 = 28
	KeyLeftCtrl   uint16 = 29
	KeyLeftShift  uint16 = 42
	KeyRightShift uint16 = 54
	KeyLeftAlt    uint16 = 56
	KeySpace      uint16 = 57
	KeyCapsLock   uint16 = 58
	KeyRightCtrl  uint16 = 97
	KeyRightAlt   uint16 = 100
	KeyHome       uint16 = 102
	KeyUp         uint16 = 103
	KeyPageUp     uint16 = 104
	KeyLeft       uint16 = 105
	KeyRight      uint16 = 106
	KeyEnd        uint16 = 107
	KeyDown       uint16 = 108
	KeyPageDown   uint16 = 109
	KeyInsert     uint16 = 110
	KeyDelete     uint16 = 111
	KeyLeftMeta   uint16 = 125
	KeyRightMeta  uint16 = 126

	KeyF1  uint16 = 59
	KeyF2  uint16 = 60
	KeyF3  uint16 = 61
	KeyF4  uint16 = 62
	KeyF5  uint16 = 63
	KeyF6  uint16 = 64
	KeyF7  uint16 = 65
	KeyF8  uint16 = 66
	KeyF9  uint16 = 67
	KeyF10 uint16 = 68
	KeyF11 uint16 = 87
	KeyF12 uint16 = 88
)

// Letter/digit codes follow the physical QWERTY key position, not
// alphabetic/numeric order.
const (
	KeyQ uint16 = 16
	KeyW uint16 = 17
	KeyE uint16 = 18
	KeyR uint16 = 19
	KeyT uint16 = 20
	KeyY uint16 = 21
	KeyU uint16 = 22
	KeyI uint16 = 23
	KeyO uint16 = 24
	KeyP uint16 = 25
	KeyA uint16 = 30
	KeyS uint16 = 31
	KeyD uint16 = 32
	KeyF uint16 = 33
	KeyG uint16 = 34
	KeyH uint16 = 35
	KeyJ uint16 = 36
	KeyK uint16 = 37
	KeyL uint16 = 38
	KeyZ uint16 = 44
	KeyX uint16 = 45
	KeyC uint16 = 46
	KeyV uint16 = 47
	KeyB uint16 = 48
	KeyN uint16 = 49
	KeyM uint16 = 50

	Key1 uint16 = 2
	Key2 uint16 = 3
	Key3 uint16 = 4
	Key4 uint16 = 5
	Key5 uint16 = 6
	Key6 uint16 = 7
	Key7 uint16 = 8
	Key8 uint16 = 9
	Key9 uint16 = 10
	Key0 uint16 = 11
)

// Target is a named remap destination for a GUI dropdown.
type Target struct {
	Label string
	Code  uint16
}

// Targets lists the remap destinations LogiTux offers, in display order.
var Targets = []Target{
	{"Left Click", BtnLeft},
	{"Right Click", BtnRight},
	{"Middle Click", BtnMiddle},
	{"Back", BtnBack},
	{"Forward", BtnForward},
	{"Side Button 1", BtnSide},
	{"Side Button 2", BtnExtra},

	{"Escape", KeyEsc},
	{"Tab", KeyTab},
	{"Enter", KeyEnter},
	{"Space", KeySpace},
	{"Backspace", KeyBackspace},
	{"Left Ctrl", KeyLeftCtrl},
	{"Right Ctrl", KeyRightCtrl},
	{"Left Shift", KeyLeftShift},
	{"Right Shift", KeyRightShift},
	{"Left Alt", KeyLeftAlt},
	{"Right Alt", KeyRightAlt},
	{"Left Meta (Super)", KeyLeftMeta},
	{"Right Meta (Super)", KeyRightMeta},
	{"Up Arrow", KeyUp},
	{"Down Arrow", KeyDown},
	{"Left Arrow", KeyLeft},
	{"Right Arrow", KeyRight},
	{"Home", KeyHome},
	{"End", KeyEnd},
	{"Page Up", KeyPageUp},
	{"Page Down", KeyPageDown},
	{"Insert", KeyInsert},
	{"Delete", KeyDelete},
	{"Caps Lock", KeyCapsLock},

	{"F1", KeyF1}, {"F2", KeyF2}, {"F3", KeyF3}, {"F4", KeyF4},
	{"F5", KeyF5}, {"F6", KeyF6}, {"F7", KeyF7}, {"F8", KeyF8},
	{"F9", KeyF9}, {"F10", KeyF10}, {"F11", KeyF11}, {"F12", KeyF12},

	{"A", KeyA}, {"B", KeyB}, {"C", KeyC}, {"D", KeyD}, {"E", KeyE},
	{"F", KeyF}, {"G", KeyG}, {"H", KeyH}, {"I", KeyI}, {"J", KeyJ},
	{"K", KeyK}, {"L", KeyL}, {"M", KeyM}, {"N", KeyN}, {"O", KeyO},
	{"P", KeyP}, {"Q", KeyQ}, {"R", KeyR}, {"S", KeyS}, {"T", KeyT},
	{"U", KeyU}, {"V", KeyV}, {"W", KeyW}, {"X", KeyX}, {"Y", KeyY},
	{"Z", KeyZ},

	{"0", Key0}, {"1", Key1}, {"2", Key2}, {"3", Key3}, {"4", Key4},
	{"5", Key5}, {"6", Key6}, {"7", Key7}, {"8", Key8}, {"9", Key9},
}

// AllCodes returns every code in Targets, for registering with Open.
func AllCodes() []uint16 {
	codes := make([]uint16, len(Targets))
	for i, t := range Targets {
		codes[i] = t.Code
	}
	return codes
}
