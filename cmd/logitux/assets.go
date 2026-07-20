package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"

	"logitux/internal/device"
)

//go:embed assets/mouse.svg
var mouseIconSVG []byte

//go:embed assets/light.svg
var lightIconSVG []byte

//go:embed assets/headset.svg
var headsetIconSVG []byte

//go:embed assets/gpro.svg
var gproArtSVG []byte

//go:embed assets/litra-glow.svg
var litraGlowArtSVG []byte

//go:embed assets/litra-beam.svg
var litraBeamArtSVG []byte

//go:embed assets/prox.svg
var proxArtSVG []byte

//go:embed assets/bolt.svg
var boltIconSVG []byte

// boltIcon marks a charging battery on dashboard cards. It's an SVG
// rather than the "⚡" character because the bundled font has no glyph
// for it — it silently renders as nothing.
var boltIcon = fyne.NewStaticResource("bolt.svg", boltIconSVG)

var (
	mouseIcon   = fyne.NewStaticResource("mouse.svg", mouseIconSVG)
	lightIcon   = fyne.NewStaticResource("light.svg", lightIconSVG)
	headsetIcon = fyne.NewStaticResource("headset.svg", headsetIconSVG)
	genericIcon = mouseIcon // fallback for any future device.Kind without dedicated art
)

// productArtByName maps exact product names (device.Info.Name) to a
// G HUB-style render of that product. All of these are original artwork
// drawn for LogiTux — not Logitech's copyrighted marketing renders, which
// an MIT-licensed repo can't redistribute. Products without an entry fall
// back to a generic icon for their Kind.
var productArtByName = map[string]fyne.Resource{
	"G Pro Wireless":                fyne.NewStaticResource("gpro.svg", gproArtSVG),
	"Litra Glow":                    fyne.NewStaticResource("litra-glow.svg", litraGlowArtSVG),
	"Litra Beam":                    fyne.NewStaticResource("litra-beam.svg", litraBeamArtSVG),
	"PRO X Wireless Gaming Headset": fyne.NewStaticResource("prox.svg", proxArtSVG),
}

// artForDevice returns the product image to show for a device: a
// user-supplied override if one exists (see userArtOverride), else
// LogiTux's own render of that product, else a generic icon for its Kind.
func artForDevice(info device.Info) fyne.Resource {
	if res := userArtOverride(info.Name); res != nil {
		return res
	}
	if res, ok := productArtByName[info.Name]; ok {
		return res
	}
	return iconForKind(info.Kind)
}

// userArtOverride lets users supply their own product image — e.g. an
// official Logitech render they've downloaded themselves, which LogiTux
// can use locally but must not ship. It looks for
// $XDG_CONFIG_HOME/logitux/images/<slug>.<ext> where <slug> is the
// product name lowercased with spaces as dashes (e.g.
// "g-pro-wireless.png") and <ext> is png, jpg, jpeg, or svg. Results —
// including "no override" — are cached, so adding an image takes effect
// on the next start rather than the next 3-second refresh tick.
func userArtOverride(name string) fyne.Resource {
	artCacheMu.Lock()
	defer artCacheMu.Unlock()
	if res, ok := artCache[name]; ok {
		return res
	}

	var res fyne.Resource
	if dir, err := os.UserConfigDir(); err == nil {
		slug := artSlug(name)
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".svg"} {
			path := filepath.Join(dir, "logitux", "images", slug+ext)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			res = fyne.NewStaticResource(filepath.Base(path), data)
			break
		}
	}
	artCache[name] = res
	return res
}

var (
	artCacheMu sync.Mutex
	artCache   = map[string]fyne.Resource{}
)

// artSlug turns a product name into an override filename stem:
// lowercased, spaces to dashes, everything else non-alphanumeric dropped.
func artSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// iconForKind returns the generic icon for a device kind, used when a
// product has no dedicated render (e.g. a future device type added before
// its own art is drawn).
func iconForKind(k device.Kind) fyne.Resource {
	switch k {
	case device.KindLight:
		return lightIcon
	case device.KindMouse:
		return mouseIcon
	case device.KindHeadset:
		return headsetIcon
	default:
		return genericIcon
	}
}
