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
func buildDeviceCard(a *appState, d device.Device) fyne.CanvasObject {
	info := d.Info()
	serial := info.Serial
	saved, _ := a.store.Get(serial)

	rows := container.NewVBox(widget.NewLabel(fmt.Sprintf("Serial: %s", serial)))

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
		rows.Add(check)
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
		rows.Add(label)
		rows.Add(slider)
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
		rows.Add(label)
		rows.Add(slider)
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
		rows.Add(label)
		rows.Add(slider)
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
			rows.Add(widget.NewLabel("Report Rate"))
			rows.Add(selectWidget)
		}
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
		rows.Add(widget.NewLabel(text))
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

		rows.Add(container.NewHBox(widget.NewLabel("Logo Color:"), swatch, chooseButton))
	}

	if brc, ok := d.(device.ButtonRemapControl); ok {
		if buttons, err := brc.Buttons(); err == nil && len(buttons) > 0 {
			rows.Add(buttonRemapSection(a, brc, info, serial, saved, buttons))
		}
	}

	return container.NewPadded(rows)
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
