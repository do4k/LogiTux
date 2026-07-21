//go:build ignore

// Command genicon rasterizes assets/icon.svg to Icon.png (512x512), the
// app icon used when packaging LogiTux (fyne package for the macOS .app,
// and the Debian package's desktop icon). It's a build-ignored developer
// tool, run manually when the icon changes:
//
//	go run ./tools/genicon
//
// It reuses oksvg/rasterx (already in the module via Fyne) so it needs no
// external rasterizer like rsvg-convert or ImageMagick.
package main

import (
	"image"
	"image/png"
	"log"
	"os"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

const size = 512

func main() {
	in, err := os.Open("assets/icon.svg")
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	icon, err := oksvg.ReadIconStream(in)
	if err != nil {
		log.Fatal(err)
	}
	icon.SetTarget(0, 0, size, size)

	rgba := image.NewRGBA(image.Rect(0, 0, size, size))
	scanner := rasterx.NewScannerGV(size, size, rgba, rgba.Bounds())
	icon.Draw(rasterx.NewDasher(size, size, scanner), 1.0)

	out, err := os.Create("Icon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, rgba); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote Icon.png (%dx%d)", size, size)
}
