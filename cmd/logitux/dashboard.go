package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"logitux/internal/device"
)

// buildDashboard renders one tappable tile per connected device, with an
// icon (see assets.go) matching its Kind and its name/serial below.
// Tapping a tile calls onSelect with that device's index in devices, so
// the caller can switch to its full control tab.
func buildDashboard(devices []device.Device, onSelect func(index int)) fyne.CanvasObject {
	if len(devices) == 0 {
		msg := widget.NewLabel("No supported Logitech devices found.\n\n" +
			"Make sure the device is plugged in and the udev rule installed by " +
			"LogiTux's installer has been applied (see the README).")
		msg.Wrapping = fyne.TextWrapWord
		msg.Alignment = fyne.TextAlignCenter
		return container.NewCenter(msg)
	}

	tiles := make([]fyne.CanvasObject, len(devices))
	for i, d := range devices {
		index := i // captured per-iteration (Go 1.22+ loop semantics)
		tiles[i] = newDeviceTile(d.Info(), func() { onSelect(index) })
	}
	grid := container.NewGridWrap(fyne.NewSize(140, 160), tiles...)
	return container.NewVScroll(container.NewPadded(grid))
}

// deviceTile is a small tappable card: an icon, the device's name, and its
// serial. Fyne's widget.Card isn't itself tappable, so this is a minimal
// custom widget (BaseWidget + Tapped) wrapping one instead.
type deviceTile struct {
	widget.BaseWidget
	info  device.Info
	onTap func()
}

func newDeviceTile(info device.Info, onTap func()) *deviceTile {
	t := &deviceTile{info: info, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *deviceTile) CreateRenderer() fyne.WidgetRenderer {
	icon := canvas.NewImageFromResource(iconForKind(t.info.Kind))
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(56, 56))

	name := widget.NewLabel(t.info.Name)
	name.Alignment = fyne.TextAlignCenter
	name.Wrapping = fyne.TextWrapWord

	// Shortened rather than relying on wrapping alone: some serials (e.g.
	// the receiver-serial-plus-index composite for a mouse behind a
	// receiver with no USB serial of its own) are long enough that even
	// wrapped text would crowd a small tile.
	shortSerial := t.info.Serial
	if len(shortSerial) > 10 {
		shortSerial = shortSerial[len(shortSerial)-10:]
	}
	serial := widget.NewLabel(fmt.Sprintf("Serial: %s", shortSerial))
	serial.Alignment = fyne.TextAlignCenter
	serial.Wrapping = fyne.TextWrapWord
	serial.TextStyle = fyne.TextStyle{Italic: true}

	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	bg.CornerRadius = theme.Size(theme.SizeNameInputRadius)

	content := container.NewPadded(container.NewVBox(
		container.NewCenter(icon),
		name,
		serial,
	))
	return widget.NewSimpleRenderer(container.NewStack(bg, content))
}

func (t *deviceTile) Tapped(_ *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *deviceTile) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}
