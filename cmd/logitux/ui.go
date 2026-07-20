package main

import (
	"fmt"
	"image/color"
	"log"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"logitux/internal/config"
	"logitux/internal/device"
	"logitux/internal/uinput"
)

func loadingPlaceholder() fyne.CanvasObject {
	return container.NewCenter(widget.NewLabel("Looking for Logitech devices..."))
}

// buildMainView renders the app's single-screen navigation, G HUB-style:
// the dashboard of device cards is the home screen, tapping a card opens
// that device's page full-window, and a back arrow in the page's top-left
// returns to the dashboard. Rebuilt from the live device list on every
// discovery tick (see appState.refresh); a.selectedSerial carries which
// screen is showing across rebuilds, and falls back to the dashboard if
// the open device's page unplugs.
func buildMainView(a *appState, devices []device.Device) fyne.CanvasObject {
	if d := a.selectedDevice(devices); d != nil {
		return buildDevicePage(a, devices, d)
	}
	a.selectedSerial = ""
	return buildDashboard(devices, func(index int) {
		a.selectedSerial = devices[index].Info().Serial
		a.window.SetContent(buildMainView(a, devices))
	})
}

// selectedDevice resolves a.selectedSerial against the current device
// list: the device whose page should be showing, or nil for the
// dashboard (nothing selected, or the selected device is gone).
func (a *appState) selectedDevice(devices []device.Device) device.Device {
	if a.selectedSerial == "" {
		return nil
	}
	for _, d := range devices {
		if d.Info().Serial == a.selectedSerial {
			return d
		}
	}
	return nil
}

// pageSection is one entry in a device page's left-hand icon rail —
// Sensitivity, Assignments, Lighting, Sound — with the settings panel it
// opens. A device only gets the sections its capabilities call for.
type pageSection struct {
	id   string
	icon fyne.Resource
	body fyne.CanvasObject
}

// buildDevicePage renders one device's page, laid out like G HUB's: a
// back arrow and the device name across the top, an icon rail on the far
// left selecting a section, that section's settings in a panel beside
// it, and the rest of the window given to a showcase — battery readout
// and a large product render. Devices with no configurable sections
// (nothing beyond battery) just get the showcase.
func buildDevicePage(a *appState, devices []device.Device, d device.Device) fyne.CanvasObject {
	info := d.Info()

	back := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		a.selectedSerial = ""
		a.window.SetContent(buildMainView(a, devices))
	})
	back.Importance = widget.LowImportance

	title := canvas.NewText(strings.ToUpper(info.Name), colorForeground)
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.TextSize = theme.Size(theme.SizeNameSubHeadingText)
	topBar := container.NewHBox(back, container.NewPadded(title))

	// Lights get a halo behind their showcase render that tracks the
	// power/brightness/temperature controls live, like G HUB's Litra
	// page, where the render actually glows.
	var glow *lightGlow
	if info.Kind == device.KindLight {
		saved, _ := a.store.Get(info.Serial)
		glow = newLightGlow(saved)
	}

	showcase := deviceShowcase(a, d, glow)
	sections := deviceSections(a, d, glow)
	if len(sections) == 0 {
		return container.NewBorder(topBar, nil, nil, nil, showcase)
	}

	// Restore this device's previously selected section across rebuilds,
	// defaulting to its first.
	selected := 0
	for i, s := range sections {
		if s.id == a.pageSection[info.Serial] {
			selected = i
		}
	}
	a.pageSection[info.Serial] = sections[selected].id

	railButtons := make([]fyne.CanvasObject, len(sections))
	for i, s := range sections {
		s := s
		b := widget.NewButtonWithIcon("", s.icon, func() {
			a.pageSection[info.Serial] = s.id
			a.window.SetContent(buildMainView(a, devices))
		})
		b.Importance = widget.LowImportance
		if i == selected {
			b.Importance = widget.HighImportance // accent square, like G HUB's rail
		}
		railButtons[i] = b
	}
	rail := container.NewVBox(railButtons...)

	panelBG := canvas.NewRectangle(colorCard)
	panelBG.CornerRadius = 10
	panelSizer := canvas.NewRectangle(color.Transparent)
	panelSizer.SetMinSize(fyne.NewSize(300, 0))
	panel := container.NewStack(panelBG, panelSizer,
		container.NewVScroll(container.NewPadded(container.NewPadded(sections[selected].body))))

	left := container.NewHBox(container.NewPadded(rail), panel)
	content := container.NewBorder(nil, nil, left, nil, showcase)
	return container.NewBorder(topBar, nil, nil, nil, container.NewPadded(content))
}

// lightGlow is the halo behind a light's showcase render. The lighting
// panel's controls update it as the user adjusts them, so the render
// reflects the light's actual state: off means no halo, and the halo's
// tint and strength follow color temperature and brightness.
type lightGlow struct {
	gradient   *canvas.RadialGradient
	on         bool
	brightness int
	kelvin     int
}

func newLightGlow(saved config.DeviceState) *lightGlow {
	g := &lightGlow{
		gradient:   canvas.NewRadialGradient(color.Transparent, color.Transparent),
		on:         saved.Power,
		brightness: saved.Brightness,
		kelvin:     saved.Temperature,
	}
	// Same defaults the panel's widgets seed with, so halo and controls
	// agree before the user touches anything.
	if g.brightness <= 0 {
		g.brightness = 50
	}
	if g.kelvin <= 0 {
		g.kelvin = (minLitraKelvin + maxLitraKelvin) / 2
	}
	g.apply()
	return g
}

func (g *lightGlow) apply() {
	if g.on {
		// Halfway between the Kelvin tint and pure white: a raw warm/cool
		// tint over the black background reads gray, not glowing.
		c := kelvinColor(g.kelvin)
		c.R += (0xff - c.R) / 2
		c.G += (0xff - c.G) / 2
		c.B += (0xff - c.B) / 2
		c.A = uint8(0x24 + g.brightness*0x8c/100)
		g.gradient.StartColor = c
	} else {
		g.gradient.StartColor = color.NRGBA{}
	}
	g.gradient.Refresh()
}

func (g *lightGlow) setPower(on bool)          { g.on = on; g.apply() }
func (g *lightGlow) setBrightness(pct int)     { g.brightness = pct; g.apply() }
func (g *lightGlow) setTemperature(kelvin int) { g.kelvin = kelvin; g.apply() }

// The Litra range; kelvinColor interpolates across it. Fine for other
// future lights too — tints outside it just clamp to the endpoints.
const (
	minLitraKelvin = 2700
	maxLitraKelvin = 6500
)

var (
	warmColor = color.NRGBA{R: 0xff, G: 0xc2, B: 0x70, A: 0xff} // ~2700K
	coolColor = color.NRGBA{R: 0xcf, G: 0xe2, B: 0xff, A: 0xff} // ~6500K
)

// kelvinColor approximates a color temperature as an RGB tint by
// interpolating between a warm and a cool endpoint — plenty for a UI
// halo; nobody is color-grading against it.
func kelvinColor(kelvin int) color.NRGBA {
	t := float64(kelvin-minLitraKelvin) / float64(maxLitraKelvin-minLitraKelvin)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	lerp := func(a, b uint8) uint8 { return uint8(float64(a) + t*(float64(b)-float64(a))) }
	return color.NRGBA{
		R: lerp(warmColor.R, coolColor.R),
		G: lerp(warmColor.G, coolColor.G),
		B: lerp(warmColor.B, coolColor.B),
		A: 0xff,
	}
}

// deviceShowcase fills a device page's main area, G HUB-style: battery
// level (and serial) in the top-left corner, and — under a faded
// oversized product name — either the device's equalizer (G HUB gives
// headsets' ADVANCED EQ the main area, in place of the render) or a
// large product render. glow, non-nil only for lights, is layered behind
// the render.
func deviceShowcase(a *appState, d device.Device, glow *lightGlow) fyne.CanvasObject {
	info := d.Info()

	var infoRows []fyne.CanvasObject
	if bs, ok := d.(device.BatteryStatus); ok {
		if percent, charging, err := bs.Battery(); err == nil {
			infoRows = append(infoRows, panelCaption("Battery Level"))
			value := canvas.NewText(fmt.Sprintf("%d%%", percent), colorForeground)
			value.TextStyle = fyne.TextStyle{Bold: true}
			value.TextSize = theme.Size(theme.SizeNameHeadingText)
			row := container.NewHBox(value)
			if charging {
				bolt := canvas.NewImageFromResource(boltIcon)
				bolt.FillMode = canvas.ImageFillContain
				bolt.SetMinSize(fyne.NewSize(14, 19))
				row.Add(container.NewVBox(layout.NewSpacer(), bolt, layout.NewSpacer()))
			}
			infoRows = append(infoRows, row)
		}
	}
	if info.Serial != "" {
		infoRows = append(infoRows, panelCaption("Serial"), panelValue(info.Serial))
	}
	// Keep the block hugging the top-left corner rather than stretching.
	corner := container.NewVBox(container.NewHBox(container.NewPadded(container.NewVBox(infoRows...))))

	ghost := canvas.NewText(strings.ToUpper(info.Name), color.NRGBA{R: 0x28, G: 0x28, B: 0x2c, A: 0xff})
	ghost.TextStyle = fyne.TextStyle{Bold: true}
	ghost.TextSize = theme.Size(theme.SizeNameHeadingText) * 1.7
	if len(info.Name) > 18 {
		// Long names ("PRO X WIRELESS GAMING HEADSET") would clip and
		// run under the corner info block at full display size.
		ghost.TextSize = theme.Size(theme.SizeNameHeadingText)
	}

	var feature fyne.CanvasObject
	if eq, ok := d.(device.EqualizerControl); ok && len(eq.EqualizerBands()) > 0 {
		saved, _ := a.store.Get(info.Serial)
		feature = equalizerShowcase(a, eq, info, info.Serial, saved, eq.EqualizerBands())
	} else {
		art := canvas.NewImageFromResource(artForDevice(info))
		art.FillMode = canvas.ImageFillContain
		art.SetMinSize(fyne.NewSize(340, 300))

		feature = art
		if glow != nil {
			glow.gradient.SetMinSize(fyne.NewSize(400, 360))
			feature = container.NewStack(glow.gradient, container.NewCenter(art))
		}
	}

	center := container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(ghost),
		container.NewCenter(feature),
		layout.NewSpacer(),
	)
	return container.NewStack(center, corner)
}

// deviceSections assembles the page sections a device's capabilities
// call for. Initial widget values come from the last-known state in
// config.Store (or a live read where the hardware supports one); they
// are set directly on the widget fields — not via SetValue/SetChecked —
// so seeding the UI never sends a command to the device before the user
// interacts with it.
func deviceSections(a *appState, d device.Device, glow *lightGlow) []pageSection {
	info := d.Info()
	serial := info.Serial
	saved, _ := a.store.Get(serial)

	var sections []pageSection
	add := func(id string, icon fyne.Resource, body fyne.CanvasObject) {
		if body != nil {
			sections = append(sections, pageSection{id: id, icon: icon, body: body})
		}
	}
	add("sensitivity", sensitivityIcon, sensitivityPanel(a, d, info, serial, saved))
	add("assignments", assignmentsIcon, assignmentsPanel(a, d, info, serial, saved))
	add("lighting", lightingIcon, lightingPanel(a, d, info, serial, saved, glow))
	add("sound", soundIcon, soundPanel(a, d, info, serial, saved))
	return sections
}

// --- panel building blocks -------------------------------------------------

// panelHeading is a section's title, e.g. "Sensitivity (DPI)".
func panelHeading(text string) fyne.CanvasObject {
	t := canvas.NewText(text, colorForeground)
	t.TextStyle = fyne.TextStyle{Bold: true}
	t.TextSize = theme.Size(theme.SizeNameSubHeadingText)
	return t
}

// panelNote is short explanatory text under a heading.
func panelNote(lines ...string) fyne.CanvasObject {
	box := container.NewVBox()
	for _, l := range lines {
		t := canvas.NewText(l, colorSecondary)
		t.TextSize = theme.Size(theme.SizeNameCaptionText)
		box.Add(t)
	}
	return box
}

// panelCaption is a small uppercase gray field label, G HUB-style
// (e.g. "REPORT RATE (PER SECOND)").
func panelCaption(text string) fyne.CanvasObject {
	t := canvas.NewText(strings.ToUpper(text), colorSecondary)
	t.TextSize = theme.Size(theme.SizeNameCaptionText)
	return t
}

// panelValue is a plain value line under a caption.
func panelValue(text string) fyne.CanvasObject {
	t := canvas.NewText(text, colorForeground)
	t.TextSize = theme.Size(theme.SizeNameText)
	return t
}

// labeledSlider renders a caption row with a live bold readout of the
// current value right-aligned (G HUB-style), the slider, and the range's
// ends labeled beneath it. onChanged receives each new value after the
// readout has updated.
func labeledSlider(captionText string, format func(int) string, initial, min, max, step int, onChanged func(int)) fyne.CanvasObject {
	value := canvas.NewText(format(initial), colorForeground)
	value.TextStyle = fyne.TextStyle{Bold: true}
	value.TextSize = theme.Size(theme.SizeNameText)

	slider := widget.NewSlider(float64(min), float64(max))
	slider.Value = float64(initial)
	slider.Step = float64(step)
	slider.OnChanged = func(v float64) {
		value.Text = format(int(v))
		value.Refresh()
		onChanged(int(v))
	}

	left := canvas.NewText(format(min), colorSecondary)
	left.TextSize = theme.Size(theme.SizeNameCaptionText)
	right := canvas.NewText(format(max), colorSecondary)
	right.TextSize = theme.Size(theme.SizeNameCaptionText)

	return container.NewVBox(
		container.NewHBox(panelCaption(captionText), layout.NewSpacer(), value),
		slider,
		container.NewHBox(left, layout.NewSpacer(), right),
	)
}

// gradientSlider is labeledSlider plus what G HUB's light sliders have:
// a gradient strip under the track previewing what the ends mean, with
// end labels (e.g. "Cool" ... "Warm"). The current value still gets a
// bold readout, right-aligned on the caption row.
func gradientSlider(captionText string, format func(int) string, leftLabel, rightLabel string, start, end color.Color, initial, min, max, step int, onChanged func(int)) fyne.CanvasObject {
	value := canvas.NewText(format(initial), colorForeground)
	value.TextStyle = fyne.TextStyle{Bold: true}
	value.TextSize = theme.Size(theme.SizeNameText)

	slider := widget.NewSlider(float64(min), float64(max))
	slider.Value = float64(initial)
	slider.Step = float64(step)
	slider.OnChanged = func(v float64) {
		value.Text = format(int(v))
		value.Refresh()
		onChanged(int(v))
	}

	strip := canvas.NewHorizontalGradient(start, end)
	strip.SetMinSize(fyne.NewSize(0, 5))

	left := canvas.NewText(leftLabel, colorSecondary)
	left.TextSize = theme.Size(theme.SizeNameCaptionText)
	right := canvas.NewText(rightLabel, colorSecondary)
	right.TextSize = theme.Size(theme.SizeNameCaptionText)

	return container.NewVBox(
		container.NewHBox(panelCaption(captionText), layout.NewSpacer(), value),
		slider,
		strip,
		container.NewHBox(left, layout.NewSpacer(), right),
	)
}

// powerPill is G HUB's "POWER [ON]" row: caption on the left, a pill
// toggle showing the current state on the right (accent-filled while
// on). onChanged only runs when the device accepted the change, so the
// pill never shows a state the hardware refused.
func powerPill(initial bool, apply func(on bool) error, onChanged func(on bool)) fyne.CanvasObject {
	on := initial
	toggle := widget.NewButton("", nil)
	render := func() {
		if on {
			toggle.SetText("ON")
			toggle.Importance = widget.HighImportance
		} else {
			toggle.SetText("OFF")
			toggle.Importance = widget.MediumImportance
		}
		toggle.Refresh()
	}
	render()
	toggle.OnTapped = func() {
		if err := apply(!on); err != nil {
			return // logged by the caller's apply; keep showing the real state
		}
		on = !on
		render()
		onChanged(on)
	}

	caption := container.NewVBox(layout.NewSpacer(), panelCaption("Power"), layout.NewSpacer())
	return container.NewHBox(caption, layout.NewSpacer(), toggle)
}

// --- capability panels -----------------------------------------------------

// sensitivityPanel covers DPIControl and ReportRateControl, or nil if
// the device has neither.
func sensitivityPanel(a *appState, d device.Device, info device.Info, serial string, saved config.DeviceState) fyne.CanvasObject {
	dc, hasDPI := d.(device.DPIControl)
	rrc, hasRate := d.(device.ReportRateControl)
	if !hasDPI && !hasRate {
		return nil
	}

	body := container.NewVBox(
		panelHeading("Sensitivity (DPI)"),
		panelNote("DPI is the speed of your mouse", "on the screen."),
		widget.NewLabel(""), // breathing room, G HUB-style sparse panel
	)

	if hasDPI {
		min, max, step := dc.DPIRange()
		initial := saved.DPI
		if live, err := dc.DPI(); err == nil {
			initial = live // the device can report this, unlike e.g. brightness
		}
		if initial <= 0 {
			initial = min
		}
		body.Add(labeledSlider("DPI Speed", func(v int) string { return fmt.Sprintf("%d", v) },
			initial, min, max, step, func(dpi int) {
				if err := dc.SetDPI(dpi); err != nil {
					log.Printf("logitux: set DPI on %s: %v", info.Name, err)
					return
				}
				a.saveState(serial, func(s *config.DeviceState) { s.DPI = dpi })
			}))
	}

	if hasRate {
		if options := rrc.ReportRateOptions(); len(options) > 0 {
			labels := make([]string, len(options))
			hzByLabel := make(map[string]int, len(options))
			for i, hz := range options {
				l := fmt.Sprintf("%d", hz)
				labels[i] = l
				hzByLabel[l] = hz
			}

			initial := options[0]
			if live, err := rrc.ReportRate(); err == nil {
				initial = live
			} else if saved.ReportRate > 0 {
				initial = saved.ReportRate
			}

			radio := widget.NewRadioGroup(labels, nil)
			radio.Horizontal = true
			radio.Required = true
			radio.Selected = fmt.Sprintf("%d", initial)
			radio.OnChanged = func(l string) {
				hz := hzByLabel[l]
				if err := rrc.SetReportRate(hz); err != nil {
					log.Printf("logitux: set report rate on %s: %v", info.Name, err)
					return
				}
				a.saveState(serial, func(s *config.DeviceState) { s.ReportRate = hz })
			}
			body.Add(widget.NewLabel(""))
			body.Add(panelCaption("Report Rate (per second)"))
			body.Add(radio)
		}
	}

	return body
}

// assignmentsPanel covers ButtonRemapControl: one dropdown per
// remappable button, each offering "Default" plus every target in
// uinput.Targets. Nil if the device has no remappable buttons.
func assignmentsPanel(a *appState, d device.Device, info device.Info, serial string, saved config.DeviceState) fyne.CanvasObject {
	brc, ok := d.(device.ButtonRemapControl)
	if !ok {
		return nil
	}
	buttons, err := brc.Buttons()
	if err != nil || len(buttons) == 0 {
		return nil
	}

	labels := make([]string, 0, len(uinput.Targets)+1)
	labels = append(labels, "Default")
	targetByLabel := map[string]uint16{"Default": 0}
	labelByTarget := map[uint16]string{0: "Default"}
	for _, t := range uinput.Targets {
		labels = append(labels, t.Label)
		targetByLabel[t.Label] = t.Code
		labelByTarget[t.Code] = t.Label
	}

	body := container.NewVBox(
		panelHeading("Assignments"),
		panelNote("Reassign a button to another", "mouse button or a keyboard key."),
		widget.NewLabel(""),
	)
	for _, b := range buttons {
		initialLabel := "Default"
		if target, ok := saved.ButtonRemaps[b.ID]; ok {
			if l, ok := labelByTarget[target]; ok {
				initialLabel = l
			}
		}

		selectWidget := widget.NewSelect(labels, nil)
		selectWidget.Selected = initialLabel
		selectWidget.OnChanged = func(l string) {
			target := targetByLabel[l]
			if err := brc.RemapButton(b.ID, target); err != nil {
				log.Printf("logitux: remap %s on %s: %v", b.Name, info.Name, err)
				return
			}
			a.saveState(serial, func(s *config.DeviceState) {
				if s.ButtonRemaps == nil {
					s.ButtonRemaps = make(map[uint16]uint16)
				}
				if target == 0 {
					delete(s.ButtonRemaps, b.ID)
				} else {
					s.ButtonRemaps[b.ID] = target
				}
			})
		}
		body.Add(panelCaption(b.Name))
		body.Add(selectWidget)
	}
	return body
}

// lightingPanel covers the light-shaped capabilities: power, color
// temperature, and brightness (Litra lights, laid out in G HUB's order
// with its gradient sliders), and RGB logo color (mice). Nil if the
// device has none of them. glow (non-nil only for lights) is kept in
// sync so the showcase render reflects each change.
func lightingPanel(a *appState, d device.Device, info device.Info, serial string, saved config.DeviceState, glow *lightGlow) fyne.CanvasObject {
	pc, hasPower := d.(device.PowerControl)
	bc, hasBrightness := d.(device.BrightnessControl)
	tc, hasTemperature := d.(device.TemperatureControl)
	rgb, hasRGB := d.(device.RGBControl)
	if !hasPower && !hasBrightness && !hasTemperature && !hasRGB {
		return nil
	}

	heading := "Lighting"
	if info.Kind == device.KindLight {
		heading = "Light" // G HUB's own heading on the Litra page
	}
	body := container.NewVBox(
		panelHeading(heading),
		widget.NewLabel(""),
	)

	if hasPower {
		body.Add(powerPill(saved.Power,
			func(on bool) error {
				if err := pc.SetPower(on); err != nil {
					log.Printf("logitux: set power on %s: %v", info.Name, err)
					return err
				}
				return nil
			},
			func(on bool) {
				if glow != nil {
					glow.setPower(on)
				}
				a.saveState(serial, func(s *config.DeviceState) { s.Power = on })
			}))
		body.Add(widget.NewLabel(""))
	}

	if hasTemperature {
		min, max := tc.TemperatureRange()
		initial := saved.Temperature
		if initial <= 0 {
			initial = (min + max) / 2
		}
		// Left end = min Kelvin = warm, so the strip runs warm-to-cool —
		// mirrored from G HUB's, whose slider runs the other direction.
		body.Add(gradientSlider("Temperature", func(v int) string { return fmt.Sprintf("%dK", v) },
			"Warm", "Cool", warmColor, coolColor,
			initial, min, max, 100, func(kelvin int) {
				if err := tc.SetTemperature(kelvin); err != nil {
					log.Printf("logitux: set temperature on %s: %v", info.Name, err)
					return
				}
				if glow != nil {
					glow.setTemperature(kelvin)
				}
				a.saveState(serial, func(s *config.DeviceState) { s.Temperature = kelvin })
			}))
		body.Add(widget.NewLabel(""))
	}

	if hasBrightness {
		initial := saved.Brightness
		if initial <= 0 {
			initial = 50
		}
		body.Add(gradientSlider("Brightness", func(v int) string { return fmt.Sprintf("%d%%", v) },
			"Dim", "Bright", color.NRGBA{R: 0x3a, G: 0x3a, B: 0x40, A: 0xff}, color.NRGBA{R: 0xf2, G: 0xf2, B: 0xf2, A: 0xff},
			initial, 0, 100, 1, func(percent int) {
				if err := bc.SetBrightness(percent); err != nil {
					log.Printf("logitux: set brightness on %s: %v", info.Name, err)
					return
				}
				if glow != nil {
					glow.setBrightness(percent)
				}
				a.saveState(serial, func(s *config.DeviceState) { s.Brightness = percent })
			}))
	}

	if hasRGB {
		initial := color.NRGBA{R: saved.Red, G: saved.Green, B: saved.Blue, A: 0xff}
		if saved.Red == 0 && saved.Green == 0 && saved.Blue == 0 {
			initial = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // default to white, not invisible black
		}

		swatch := canvas.NewRectangle(initial)
		swatch.CornerRadius = 6
		swatch.SetMinSize(fyne.NewSize(28, 28))

		chooseButton := widget.NewButton("Choose...", nil)
		chooseButton.OnTapped = func() {
			picker := dialog.NewColorPicker("Logo Color", "Pick a color for the logo LED", func(c color.Color) {
				swatch.FillColor = c
				swatch.Refresh()
				r, g, b, _ := c.RGBA() // 16-bit-per-channel components
				r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
				if err := rgb.SetColor(r8, g8, b8); err != nil {
					log.Printf("logitux: set LED color on %s: %v", info.Name, err)
					return
				}
				a.saveState(serial, func(s *config.DeviceState) { s.Red, s.Green, s.Blue = r8, g8, b8 })
			}, a.window)
			picker.Advanced = true
			picker.Show()
		}

		body.Add(panelCaption("Logo Color"))
		body.Add(container.NewHBox(swatch, chooseButton))
	}

	return body
}

// soundPanel covers SidetoneControl and EqualizerControl (headsets), or
// nil if the device has neither. The equalizer itself renders in the
// showcase area (see equalizerShowcase), like G HUB's ADVANCED EQ; the
// panel holds the sidetone slider.
func soundPanel(a *appState, d device.Device, info device.Info, serial string, saved config.DeviceState) fyne.CanvasObject {
	stc, hasSidetone := d.(device.SidetoneControl)
	_, hasEQ := d.(device.EqualizerControl)
	if !hasSidetone && !hasEQ {
		return nil
	}

	heading := "Sound"
	if info.Kind == device.KindHeadset {
		heading = "Headphones" // G HUB's own heading on headset pages
	}
	body := container.NewVBox(
		panelHeading(heading),
		widget.NewLabel(""),
	)

	if hasSidetone {
		initial := saved.Sidetone
		if live, err := stc.Sidetone(); err == nil {
			initial = live
		}
		body.Add(labeledSlider("Sidetone", func(v int) string { return fmt.Sprintf("%d%%", v) },
			initial, 0, 100, 1, func(percent int) {
				if err := stc.SetSidetone(percent); err != nil {
					log.Printf("logitux: set sidetone on %s: %v", info.Name, err)
					return
				}
				a.saveState(serial, func(s *config.DeviceState) { s.Sidetone = percent })
			}))
	}

	if hasEQ {
		body.Add(widget.NewLabel(""))
		body.Add(panelNote("The advanced EQ is on the right;", "changes apply immediately."))
	}

	return body
}

// equalizerShowcase renders a device's equalizer the way G HUB does:
// a bank of vertical sliders across the page's main area, one per band,
// with the frequency above and the current dB below each, a dB scale on
// the left, and a reset button. All sliders share a single in-memory
// levels snapshot (seeded from a live read, or last-saved values if that
// fails) rather than re-reading the device on every change, since
// SetEqualizerLevels writes every band's level at once and a slider can
// fire OnChanged many times during a single drag.
func equalizerShowcase(a *appState, eq device.EqualizerControl, info device.Info, serial string, saved config.DeviceState, bands []device.EqualizerBand) fyne.CanvasObject {
	min, max := eq.EqualizerRange()

	levels := make([]int, len(bands))
	if live, err := eq.EqualizerLevels(); err == nil && len(live) == len(bands) {
		copy(levels, live)
	} else if len(saved.EqualizerLevels) == len(bands) {
		copy(levels, saved.EqualizerLevels)
	}

	writeLevels := func() bool {
		if err := eq.SetEqualizerLevels(levels); err != nil {
			log.Printf("logitux: set equalizer on %s: %v", info.Name, err)
			return false
		}
		saveLevels := append([]int(nil), levels...)
		a.saveState(serial, func(s *config.DeviceState) { s.EqualizerLevels = saveLevels })
		return true
	}

	small := func(text string) *canvas.Text {
		t := canvas.NewText(text, colorSecondary)
		t.TextSize = theme.Size(theme.SizeNameCaptionText)
		return t
	}

	const sliderHeight = 230
	sliders := make([]*widget.Slider, len(bands))
	values := make([]*canvas.Text, len(bands))
	columns := make([]fyne.CanvasObject, 0, len(bands)+1)

	// dB scale, aligned with the sliders' track area.
	scale := container.NewVBox(small(fmt.Sprintf("+%ddB", max)), layout.NewSpacer(), small("0"), layout.NewSpacer(), small(fmt.Sprintf("%ddB", min)))
	scaleSizer := canvas.NewRectangle(color.Transparent)
	scaleSizer.SetMinSize(fyne.NewSize(0, sliderHeight))
	columns = append(columns, container.NewVBox(small(""), container.NewStack(scaleSizer, scale), small("")))

	for i, band := range bands {
		i := i
		value := canvas.NewText(fmt.Sprintf("%d", levels[i]), colorForeground)
		value.TextStyle = fyne.TextStyle{Bold: true}
		value.TextSize = theme.Size(theme.SizeNameCaptionText)

		slider := widget.NewSlider(float64(min), float64(max))
		slider.Orientation = widget.Vertical
		slider.Step = 1
		slider.Value = float64(levels[i])
		slider.OnChanged = func(v float64) {
			levels[i] = int(v)
			value.Text = fmt.Sprintf("%d", levels[i])
			value.Refresh()
			writeLevels()
		}
		sliders[i] = slider
		values[i] = value

		sizer := canvas.NewRectangle(color.Transparent)
		sizer.SetMinSize(fyne.NewSize(34, sliderHeight))
		columns = append(columns, container.NewVBox(
			container.NewCenter(small(formatFrequency(band.FrequencyHz))),
			container.NewStack(sizer, slider),
			container.NewCenter(value),
		))
	}

	reset := widget.NewButton("Reset", func() {
		for i := range levels {
			levels[i] = 0
		}
		if !writeLevels() {
			return
		}
		for i := range sliders {
			sliders[i].Value = 0
			sliders[i].Refresh()
			values[i].Text = "0"
			values[i].Refresh()
		}
	})

	return container.NewVBox(
		container.NewCenter(panelCaption("Advanced EQ")),
		widget.NewLabel(""),
		container.NewCenter(container.NewHBox(columns...)),
		widget.NewLabel(""),
		container.NewCenter(reset),
	)
}

// formatFrequency renders a band frequency compactly, e.g. 125 -> "125Hz",
// 8000 -> "8kHz".
func formatFrequency(hz int) string {
	if hz >= 1000 {
		return fmt.Sprintf("%gkHz", float64(hz)/1000)
	}
	return fmt.Sprintf("%dHz", hz)
}
