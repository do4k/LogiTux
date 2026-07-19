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
		tiles[i] = newDeviceTile(d, func() { onSelect(index) })
	}
	grid := container.NewGridWrap(fyne.NewSize(140, 160), tiles...)
	return container.NewVScroll(container.NewPadded(grid))
}

// deviceTile is a small tappable card: an icon, the device's name, its
// battery level if it has one, and its serial. Fyne's widget.Card isn't
// itself tappable, so this is a minimal custom widget (BaseWidget +
// Tapped) wrapping one instead.
type deviceTile struct {
	widget.BaseWidget
	d     device.Device
	onTap func()
}

func newDeviceTile(d device.Device, onTap func()) *deviceTile {
	t := &deviceTile{d: d, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *deviceTile) CreateRenderer() fyne.WidgetRenderer {
	info := t.d.Info()

	icon := canvas.NewImageFromResource(iconForKind(info.Kind))
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(56, 56))

	name := widget.NewLabel(info.Name)
	name.Alignment = fyne.TextAlignCenter
	name.Wrapping = fyne.TextWrapWord

	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	bg.CornerRadius = theme.Size(theme.SizeNameInputRadius)

	tileRows := []fyne.CanvasObject{container.NewCenter(icon), name}

	if bs, ok := t.d.(device.BatteryStatus); ok {
		if percent, charging, err := bs.Battery(); err == nil {
			text := fmt.Sprintf("%d%%", percent)
			if charging {
				text = "⚡ " + text // a bolt is the universal "charging" signal, not decorative
			}
			battery := widget.NewLabel(text)
			battery.Alignment = fyne.TextAlignCenter
			tileRows = append(tileRows, battery)
		}
	}

	content := container.NewPadded(container.NewVBox(tileRows...))
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
