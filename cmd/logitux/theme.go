package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// ghubTheme is LogiTux's dark theme, styled after Logitech G HUB: a
// near-black window, dark gray cards, light text, and a Logitech-G-style
// cyan-blue accent for anything interactive. It is always dark — the
// requested variant is ignored, exactly like G HUB itself.
type ghubTheme struct{}

var _ fyne.Theme = (*ghubTheme)(nil)

var (
	colorBackground = color.NRGBA{R: 0x0d, G: 0x0d, B: 0x0e, A: 0xff}
	colorCard       = color.NRGBA{R: 0x1b, G: 0x1b, B: 0x1e, A: 0xff}
	colorCardHover  = color.NRGBA{R: 0x25, G: 0x25, B: 0x29, A: 0xff}
	colorForeground = color.NRGBA{R: 0xec, G: 0xec, B: 0xec, A: 0xff}
	colorSecondary  = color.NRGBA{R: 0x9b, G: 0x9b, B: 0xa0, A: 0xff}
	colorAccent     = color.NRGBA{R: 0x00, G: 0xb0, B: 0xf5, A: 0xff}
	colorButton     = color.NRGBA{R: 0x2a, G: 0x2a, B: 0x2e, A: 0xff}
	colorSeparator  = color.NRGBA{R: 0x2c, G: 0x2c, B: 0x30, A: 0xff}
)

func (ghubTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return colorBackground
	case theme.ColorNameForeground:
		return colorForeground
	case theme.ColorNameInputBackground:
		// A step lighter than colorCard: sliders draw their track and
		// inputs their field with this, and panels/cards are colorCard —
		// identical values would make tracks invisible on them.
		return color.NRGBA{R: 0x2e, G: 0x2e, B: 0x33, A: 0xff}
	case theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return colorCard
	case theme.ColorNameButton:
		return colorButton
	case theme.ColorNameHover:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x14}
	case theme.ColorNamePrimary, theme.ColorNameFocus, theme.ColorNameHyperlink:
		return colorAccent
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0x00, G: 0xb0, B: 0xf5, A: 0x55}
	case theme.ColorNameSeparator:
		return colorSeparator
	case theme.ColorNamePlaceHolder:
		return colorSecondary
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 0x5c, G: 0x5c, B: 0x60, A: 0xff}
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x40}
	case theme.ColorNameShadow:
		return color.NRGBA{A: 0x80}
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (ghubTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (ghubTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (ghubTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}
