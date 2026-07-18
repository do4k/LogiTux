package main

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"logitux/internal/config"
	"logitux/internal/device"
)

func loadingPlaceholder() fyne.CanvasObject {
	return container.NewCenter(widget.NewLabel("Looking for Logitech devices..."))
}

// buildDeviceList renders one card per connected device, or an
// explanatory message when nothing was found.
func buildDeviceList(a *appState, devices []device.Device) fyne.CanvasObject {
	if len(devices) == 0 {
		msg := widget.NewLabel("No supported Logitech devices found.\n\n" +
			"Make sure the device is plugged in and the udev rule installed by " +
			"LogiTux's installer has been applied (see the README).")
		msg.Wrapping = fyne.TextWrapWord
		msg.Alignment = fyne.TextAlignCenter
		return container.NewCenter(msg)
	}

	cards := make([]fyne.CanvasObject, 0, len(devices))
	for _, d := range devices {
		cards = append(cards, buildDeviceCard(a, d))
	}
	return container.NewVScroll(container.NewVBox(cards...))
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

	rows := container.NewVBox()

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

	subtitle := fmt.Sprintf("Serial: %s", serial)
	return widget.NewCard(info.Name, subtitle, rows)
}
