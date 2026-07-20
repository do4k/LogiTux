package main

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"logitux/internal/device"
)

// Dashboard card geometry, sized so a product render dominates the card
// the way G HUB's device tiles do.
const (
	tileWidth  float32 = 230
	tileHeight float32 = 260
)

// buildDashboard renders one tappable G HUB-style card per connected
// device: bold uppercase name, battery status, a large product render
// (see assets.go), and a settings button. Tapping a card (or its gear)
// calls onSelect with that device's index in devices, so the caller can
// switch to its full control tab.
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
	grid := container.NewGridWrap(fyne.NewSize(tileWidth, tileHeight), tiles...)
	return container.NewVScroll(container.NewPadded(grid))
}

// deviceTile is one dashboard card, mimicking a G HUB device tile: name
// top-left, battery below it, product image filling the middle, gear
// bottom-right. Fyne's widget.Card isn't tappable (or hoverable), so this
// is a custom widget; the whole card and the gear both open the device's
// tab.
type deviceTile struct {
	widget.BaseWidget
	d     device.Device
	onTap func()

	bg *canvas.Rectangle // kept for the hover highlight
}

func newDeviceTile(d device.Device, onTap func()) *deviceTile {
	t := &deviceTile{d: d, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *deviceTile) CreateRenderer() fyne.WidgetRenderer {
	info := t.d.Info()

	t.bg = canvas.NewRectangle(colorCard)
	t.bg.CornerRadius = 10

	name := canvas.NewText(strings.ToUpper(info.Name), colorForeground)
	name.TextStyle = fyne.TextStyle{Bold: true}
	name.TextSize = theme.Size(theme.SizeNameText)

	top := container.NewVBox(name, batteryRow(t.d))

	art := canvas.NewImageFromResource(artForDevice(info))
	art.FillMode = canvas.ImageFillContain
	art.SetMinSize(fyne.NewSize(150, 130))

	gear := widget.NewButtonWithIcon("", theme.SettingsIcon(), t.onTap)
	gear.Importance = widget.LowImportance
	bottom := container.NewHBox(layout.NewSpacer(), gear)

	content := container.NewBorder(top, bottom, nil, nil, art)
	pad := container.NewPadded(container.NewPadded(content))
	return widget.NewSimpleRenderer(container.NewStack(t.bg, pad))
}

// batteryRow renders a card's status line: battery percentage plus a
// bolt icon while charging (the universal "charging" signal), or a blank
// placeholder for devices without a battery — the line stays either way,
// keeping every card's layout identical, again like G HUB.
func batteryRow(d device.Device) fyne.CanvasObject {
	text := " "
	charging := false
	if bs, ok := d.(device.BatteryStatus); ok {
		if percent, chg, err := bs.Battery(); err == nil {
			text = fmt.Sprintf("%d%%", percent)
			charging = chg
		}
	}

	label := canvas.NewText(text, colorSecondary)
	label.TextSize = theme.Size(theme.SizeNameCaptionText)
	row := container.NewHBox(label)
	if charging {
		bolt := canvas.NewImageFromResource(boltIcon)
		bolt.FillMode = canvas.ImageFillContain
		bolt.SetMinSize(fyne.NewSize(9, 12))
		row.Add(bolt)
	}
	return row
}

func (t *deviceTile) Tapped(_ *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *deviceTile) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

// MouseIn/MouseOut give the card G HUB's subtle lighten-on-hover.
func (t *deviceTile) MouseIn(_ *desktop.MouseEvent) { t.setHovered(true) }

func (t *deviceTile) MouseMoved(_ *desktop.MouseEvent) {}

func (t *deviceTile) MouseOut() { t.setHovered(false) }

func (t *deviceTile) setHovered(hovered bool) {
	if t.bg == nil {
		return
	}
	if hovered {
		t.bg.FillColor = colorCardHover
	} else {
		t.bg.FillColor = colorCard
	}
	t.bg.Refresh()
}
