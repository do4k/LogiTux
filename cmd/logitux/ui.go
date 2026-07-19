package main

import (
	"fmt"
	"image/color"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"logitux/internal/config"
	"logitux/internal/device"
	"logitux/internal/uinput"
)

func loadingPlaceholder() fyne.CanvasObject {
	return container.NewCenter(widget.NewLabel("Looking for Logitech devices..."))
}

// buildDeviceList renders one tab per connected device, or an explanatory
// message when nothing was found. Tabs appear and disappear automatically
// as devices connect/disconnect, since this is rebuilt from the live
// device list on every discovery tick (see appState.refresh); there's
// nothing separate to reconcile.
func buildDeviceList(a *appState, devices []device.Device) fyne.CanvasObject {
	if len(devices) == 0 {
		msg := widget.NewLabel("No supported Logitech devices found.\n\n" +
			"Make sure the device is plugged in and the udev rule installed by " +
			"LogiTux's installer has been applied (see the README).")
		msg.Wrapping = fyne.TextWrapWord
		msg.Alignment = fyne.TextAlignCenter
		return container.NewCenter(msg)
	}

	labels := make([]string, len(devices))
	seen := make(map[string]int, len(devices))
	for _, d := range devices {
		seen[d.Info().Name]++
	}
	for i, d := range devices {
		name := d.Info().Name
		if seen[name] > 1 {
			// Disambiguate same-model devices with a short serial suffix.
			serial := d.Info().Serial
			if len(serial) > 6 {
				serial = serial[len(serial)-6:]
			}
			name = fmt.Sprintf("%s (%s)", name, serial)
		}
		labels[i] = name
	}

	deviceItems := make([]*container.TabItem, len(devices))
	for i, d := range devices {
		deviceItems[i] = container.NewTabItem(labels[i], container.NewVScroll(buildDeviceCard(a, d)))
	}

	tabs := container.NewAppTabs()
	dashboardItem := container.NewTabItem("Dashboard", buildDashboard(devices, func(index int) {
		tabs.Select(deviceItems[index])
	}))
	tabs.Append(dashboardItem)
	for _, item := range deviceItems {
		tabs.Append(item)
	}
	tabs.OnSelected = func(item *container.TabItem) {
		a.selectedTab = item.Text
	}

	// Restore whichever tab was selected before this rebuild (a fresh
	// start, with no prior selection, lands on the Dashboard).
	selectItem := dashboardItem
	for i, l := range labels {
		if l == a.selectedTab {
			selectItem = deviceItems[i]
		}
	}
	tabs.Select(selectItem)
	a.selectedTab = selectItem.Text

	return tabs
}

// buildDeviceCard renders the controls a device supports, based on which
// capability interfaces it implements. Initial widget values come from the
// last-known state in config.Store; they are set directly on the widget
// fields (not via SetValue/SetChecked) so seeding the UI never sends a
// command to the device before the user interacts with it.
//
// Controls are split into two groups: the handful someone actually
// adjusts often (power, primary brightness/DPI, battery) stay directly on
// the card; everything else (color temperature, report rate, RGB, button
// remapping, sidetone, equalizer) lives in a collapsed "Advanced" section,
// since a card listing every capability at once got overwhelming once a
// device had several of them (e.g. a headset's ten-band equalizer).
func buildDeviceCard(a *appState, d device.Device) fyne.CanvasObject {
	info := d.Info()
	serial := info.Serial
	saved, _ := a.store.Get(serial)

	main := container.NewVBox()
	if serial != "" {
		// Some devices have no USB serial descriptor and nothing else to
		// derive one from (e.g. this PRO X Wireless's dongle); nothing
		// useful to show in that case, so the row is omitted rather than
		// left dangling as "Serial: ".
		main.Add(widget.NewLabel(fmt.Sprintf("Serial: %s", serial)))
	}
	var advanced []fyne.CanvasObject
	addAdvanced := func(items ...fyne.CanvasObject) { advanced = append(advanced, items...) }

	if pc, ok := d.(device.PowerControl); ok {
		check := widget.NewCheck("Power", nil)
		check.Checked = saved.Power
		check.OnChanged = func(on bool) {
			if err := pc.SetPower(on); err != nil {
				log.Printf("logitux: set power on %s: %v", info.Name, err)
				return
			}
			a.saveState(serial, func(s *config.DeviceState) { s.Power = on })
		}
		main.Add(check)
	}

	if bc, ok := d.(device.BrightnessControl); ok {
		initial := saved.Brightness
		if initial <= 0 {
			initial = 50
		}
		label := widget.NewLabel(fmt.Sprintf("Brightness: %d%%", initial))
		slider := widget.NewSlider(0, 100)
		slider.Value = float64(initial)
		slider.Step = 1
		slider.OnChanged = func(v float64) {
			percent := int(v)
			label.SetText(fmt.Sprintf("Brightness: %d%%", percent))
			if err := bc.SetBrightness(percent); err != nil {
				log.Printf("logitux: set brightness on %s: %v", info.Name, err)
				return
			}
			a.saveState(serial, func(s *config.DeviceState) { s.Brightness = percent })
		}
		main.Add(label)
		main.Add(slider)
	}

	if dc, ok := d.(device.DPIControl); ok {
		min, max, step := dc.DPIRange()
		initial := saved.DPI
		if live, err := dc.DPI(); err == nil {
			initial = live // the device can report this, unlike brightness/temperature
		}
		if initial <= 0 {
			initial = min
		}
		label := widget.NewLabel(fmt.Sprintf("DPI: %d", initial))
		slider := widget.NewSlider(float64(min), float64(max))
		slider.Value = float64(initial)
		slider.Step = float64(step)
		slider.OnChanged = func(v float64) {
			dpi := int(v)
			label.SetText(fmt.Sprintf("DPI: %d", dpi))
			if err := dc.SetDPI(dpi); err != nil {
				log.Printf("logitux: set DPI on %s: %v", info.Name, err)
				return
			}
			a.saveState(serial, func(s *config.DeviceState) { s.DPI = dpi })
		}
		main.Add(label)
		main.Add(slider)
	}

	if bs, ok := d.(device.BatteryStatus); ok {
		text := "Battery: unavailable"
		if percent, charging, err := bs.Battery(); err == nil {
			state := "Discharging"
			if charging {
				state = "Charging"
			}
			text = fmt.Sprintf("Battery: %d%% (%s)", percent, state)
		}
		main.Add(widget.NewLabel(text))
	}

	if tc, ok := d.(device.TemperatureControl); ok {
		min, max := tc.TemperatureRange()
		initial := saved.Temperature
		if initial <= 0 {
			initial = (min + max) / 2
		}
		label := widget.NewLabel(fmt.Sprintf("Color Temperature: %dK", initial))
		slider := widget.NewSlider(float64(min), float64(max))
		slider.Value = float64(initial)
		slider.Step = 100
		slider.OnChanged = func(v float64) {
			kelvin := int(v)
			label.SetText(fmt.Sprintf("Color Temperature: %dK", kelvin))
			if err := tc.SetTemperature(kelvin); err != nil {
				log.Printf("logitux: set temperature on %s: %v", info.Name, err)
				return
			}
			a.saveState(serial, func(s *config.DeviceState) { s.Temperature = kelvin })
		}
		addAdvanced(label, slider)
	}

	if rrc, ok := d.(device.ReportRateControl); ok {
		options := rrc.ReportRateOptions()
		if len(options) > 0 {
			labels := make([]string, len(options))
			hzByLabel := make(map[string]int, len(options))
			for i, hz := range options {
				l := fmt.Sprintf("%d Hz", hz)
				labels[i] = l
				hzByLabel[l] = hz
			}

			initial := options[0]
			if live, err := rrc.ReportRate(); err == nil {
				initial = live
			} else if saved.ReportRate > 0 {
				initial = saved.ReportRate
			}
			initialLabel := fmt.Sprintf("%d Hz", initial)

			selectWidget := widget.NewSelect(labels, nil)
			selectWidget.Selected = initialLabel
			selectWidget.OnChanged = func(l string) {
				hz := hzByLabel[l]
				if err := rrc.SetReportRate(hz); err != nil {
					log.Printf("logitux: set report rate on %s: %v", info.Name, err)
					return
				}
				a.saveState(serial, func(s *config.DeviceState) { s.ReportRate = hz })
			}
			addAdvanced(widget.NewLabel("Report Rate"), selectWidget)
		}
	}

	if stc, ok := d.(device.SidetoneControl); ok {
		initial := saved.Sidetone
		if live, err := stc.Sidetone(); err == nil {
			initial = live
		}
		label := widget.NewLabel(fmt.Sprintf("Sidetone: %d%%", initial))
		slider := widget.NewSlider(0, 100)
		slider.Value = float64(initial)
		slider.Step = 1
		slider.OnChanged = func(v float64) {
			percent := int(v)
			label.SetText(fmt.Sprintf("Sidetone: %d%%", percent))
			if err := stc.SetSidetone(percent); err != nil {
				log.Printf("logitux: set sidetone on %s: %v", info.Name, err)
				return
			}
			a.saveState(serial, func(s *config.DeviceState) { s.Sidetone = percent })
		}
		addAdvanced(label, slider)
	}

	if eq, ok := d.(device.EqualizerControl); ok {
		if bands := eq.EqualizerBands(); len(bands) > 0 {
			addAdvanced(equalizerSection(a, eq, info, serial, saved, bands))
		}
	}

	if rgb, ok := d.(device.RGBControl); ok {
		initial := color.NRGBA{R: saved.Red, G: saved.Green, B: saved.Blue, A: 0xff}
		if saved.Red == 0 && saved.Green == 0 && saved.Blue == 0 {
			initial = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // default to white, not invisible black
		}

		swatch := canvas.NewRectangle(initial)
		swatch.SetMinSize(fyne.NewSize(28, 28))

		chooseButton := widget.NewButton("Choose Logo Color...", nil)
		chooseButton.OnTapped = func() {
			picker := dialog.NewColorPicker("Logo Color", "Pick a color for the mouse's logo LED", func(c color.Color) {
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

		addAdvanced(container.NewHBox(widget.NewLabel("Logo Color:"), swatch, chooseButton))
	}

	if brc, ok := d.(device.ButtonRemapControl); ok {
		if buttons, err := brc.Buttons(); err == nil && len(buttons) > 0 {
			addAdvanced(buttonRemapSection(a, brc, info, serial, saved, buttons))
		}
	}

	if len(advanced) > 0 {
		main.Add(advancedSection(a, serial, advanced))
	}

	return container.NewPadded(main)
}

// advancedSection is a manually-toggled collapsible section (rather than
// widget.Accordion) so its open/closed state can be persisted in
// appState.advancedOpen and restored across rebuilds — device cards are
// rebuilt from scratch on every discovery tick, which would otherwise
// re-collapse an Accordion (whose open state lives only in the discarded
// widget instance) out from under the user every few seconds.
func advancedSection(a *appState, serial string, items []fyne.CanvasObject) fyne.CanvasObject {
	body := container.NewVBox(items...)
	body.Hidden = !a.advancedOpen[serial]

	toggle := widget.NewButton(advancedButtonLabel(body.Hidden), nil)
	toggle.OnTapped = func() {
		open := !a.advancedOpen[serial]
		a.advancedOpen[serial] = open
		body.Hidden = !open
		toggle.SetText(advancedButtonLabel(body.Hidden))
	}

	return container.NewVBox(toggle, body)
}

func advancedButtonLabel(hidden bool) string {
	if hidden {
		return "▸ Advanced"
	}
	return "▾ Advanced"
}

// equalizerSection renders one slider per equalizer band. All sliders
// share a single in-memory levels snapshot (seeded from a live read, or
// last-saved values if that fails) rather than re-reading the device on
// every change, since SetEqualizerLevels writes every band's level at
// once and a slider can fire OnChanged many times during a single drag.
func equalizerSection(a *appState, eq device.EqualizerControl, info device.Info, serial string, saved config.DeviceState, bands []device.EqualizerBand) fyne.CanvasObject {
	min, max := eq.EqualizerRange()

	levels := make([]int, len(bands))
	if live, err := eq.EqualizerLevels(); err == nil && len(live) == len(bands) {
		copy(levels, live)
	} else if len(saved.EqualizerLevels) == len(bands) {
		copy(levels, saved.EqualizerLevels)
	}

	section := container.NewVBox(widget.NewLabel("Equalizer"))
	for i, band := range bands {
		i := i
		freqLabel := formatFrequency(band.FrequencyHz)

		label := widget.NewLabel(fmt.Sprintf("%s: %ddB", freqLabel, levels[i]))
		slider := widget.NewSlider(float64(min), float64(max))
		slider.Value = float64(levels[i])
		slider.Step = 1
		slider.OnChanged = func(v float64) {
			levels[i] = int(v)
			label.SetText(fmt.Sprintf("%s: %ddB", freqLabel, levels[i]))
			if err := eq.SetEqualizerLevels(levels); err != nil {
				log.Printf("logitux: set equalizer on %s: %v", info.Name, err)
				return
			}
			saveLevels := append([]int(nil), levels...)
			a.saveState(serial, func(s *config.DeviceState) { s.EqualizerLevels = saveLevels })
		}
		section.Add(container.NewHBox(label, slider))
	}
	return section
}

// formatFrequency renders a band frequency compactly, e.g. 125 -> "125Hz",
// 8000 -> "8kHz".
func formatFrequency(hz int) string {
	if hz >= 1000 {
		return fmt.Sprintf("%gkHz", float64(hz)/1000)
	}
	return fmt.Sprintf("%dHz", hz)
}

// buttonRemapSection renders one dropdown per remappable button, each
// offering "Default" plus every target in uinput.Targets.
func buttonRemapSection(a *appState, brc device.ButtonRemapControl, info device.Info, serial string, saved config.DeviceState, buttons []device.ButtonInfo) fyne.CanvasObject {
	labels := make([]string, 0, len(uinput.Targets)+1)
	labels = append(labels, "Default")
	targetByLabel := map[string]uint16{"Default": 0}
	labelByTarget := map[uint16]string{0: "Default"}
	for _, t := range uinput.Targets {
		labels = append(labels, t.Label)
		targetByLabel[t.Label] = t.Code
		labelByTarget[t.Code] = t.Label
	}

	section := container.NewVBox(widget.NewLabel("Button Remapping"))
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
		section.Add(container.NewHBox(widget.NewLabel(b.Name+":"), selectWidget))
	}
	return section
}
