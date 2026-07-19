package main

import (
	_ "embed"

	"fyne.io/fyne/v2"

	"logitux/internal/device"
)

//go:embed assets/mouse.svg
var mouseIconSVG []byte

//go:embed assets/light.svg
var lightIconSVG []byte

var (
	mouseIcon   = fyne.NewStaticResource("mouse.svg", mouseIconSVG)
	lightIcon   = fyne.NewStaticResource("light.svg", lightIconSVG)
	genericIcon = mouseIcon // fallback for any future device.Kind without dedicated art
)

// iconForKind returns the dashboard icon for a device kind, falling back
// to a generic icon for kinds without dedicated art (e.g. a future device
// type added before its own icon is drawn).
func iconForKind(k device.Kind) fyne.Resource {
	switch k {
	case device.KindLight:
		return lightIcon
	case device.KindMouse:
		return mouseIcon
	default:
		return genericIcon
	}
}
