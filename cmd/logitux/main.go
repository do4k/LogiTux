// Command logitux is a GUI for controlling Logitech devices on Linux.
package main

import (
	"log"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"logitux/internal/config"
	"logitux/internal/device"

	// Registers itself with the internal/device registry on import. Add
	// further device plugins the same way to extend LogiTux.
	_ "logitux/internal/device/litra"
	"logitux/internal/hid"
)

const discoveryInterval = 3 * time.Second

// appState owns the set of currently-open devices and the widgets built
// for them. Discover is called periodically to pick up hot-plugged
// devices; we keep already-open handles alive across refreshes rather
// than reopening them every tick.
type appState struct {
	fyneApp fyne.App
	window  fyne.Window
	backend hid.Backend
	store   *config.Store

	mu      sync.Mutex
	current map[string]device.Device // keyed by serial
}

func main() {
	statePath, err := config.DefaultPath()
	if err != nil {
		log.Fatalf("logitux: %v", err)
	}
	store, err := config.Open(statePath)
	if err != nil {
		log.Fatalf("logitux: %v", err)
	}

	a := app.NewWithID("io.github.logitux")
	a.SetIcon(theme.FyneLogo())
	window := a.NewWindow("LogiTux")
	window.Resize(fyne.NewSize(420, 480))

	state := &appState{
		fyneApp: a,
		window:  window,
		backend: hid.Default,
		store:   store,
		current: make(map[string]device.Device),
	}

	setUpSystemTray(state)

	// Minimize to tray instead of quitting when the window is closed, so
	// LogiTux keeps running in the background like the systray menu implies.
	window.SetCloseIntercept(func() {
		window.Hide()
	})

	window.SetContent(loadingPlaceholder())
	state.refresh() // populate immediately instead of waiting for the first tick

	go func() {
		ticker := time.NewTicker(discoveryInterval)
		defer ticker.Stop()
		for range ticker.C {
			state.refresh()
		}
	}()

	window.ShowAndRun()

	state.closeAll()
}

// refresh re-enumerates hidraw devices, opens newly connected ones, closes
// handles for devices that disappeared, and rebuilds the device list UI.
// Safe to call from any goroutine.
func (a *appState) refresh() {
	discovered, errs := device.Discover(a.backend)
	for _, err := range errs {
		log.Printf("logitux: %v", err)
	}

	a.mu.Lock()
	bySerial := make(map[string]device.Device, len(discovered))
	for _, d := range discovered {
		bySerial[d.Info().Serial] = d
	}

	for serial, d := range a.current {
		if _, stillPresent := bySerial[serial]; !stillPresent {
			d.Close()
			delete(a.current, serial)
		}
	}
	for serial, d := range bySerial {
		if _, alreadyOpen := a.current[serial]; alreadyOpen {
			d.Close() // Discover opened a fresh handle; we already have one
			continue
		}
		a.current[serial] = d
	}

	devices := make([]device.Device, 0, len(a.current))
	for _, d := range a.current {
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Info().Serial < devices[j].Info().Serial
	})
	a.mu.Unlock()

	fyne.Do(func() {
		a.window.SetContent(buildDeviceList(a, devices))
	})
}

func (a *appState) closeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.current {
		d.Close()
	}
}

func (a *appState) setAllPower(on bool) {
	a.mu.Lock()
	devices := make([]device.Device, 0, len(a.current))
	for _, d := range a.current {
		devices = append(devices, d)
	}
	a.mu.Unlock()

	for _, d := range devices {
		pc, ok := d.(device.PowerControl)
		if !ok {
			continue
		}
		if err := pc.SetPower(on); err != nil {
			log.Printf("logitux: set power on %s: %v", d.Info().Name, err)
			continue
		}
		a.saveState(d.Info().Serial, func(s *config.DeviceState) { s.Power = on })
	}
	// Reflect the change immediately rather than waiting for the next tick.
	go a.refresh()
}

// saveState reads the current persisted state for serial (if any), applies
// mutate, and writes it back. Errors are logged, not fatal: losing the
// last-known-state cache never affects live device control.
func (a *appState) saveState(serial string, mutate func(*config.DeviceState)) {
	state, _ := a.store.Get(serial)
	mutate(&state)
	if err := a.store.Set(serial, state); err != nil {
		log.Printf("logitux: save state for %s: %v", serial, err)
	}
}

func setUpSystemTray(a *appState) {
	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		return // platform has no systray support; window Show/Hide still works
	}

	menu := fyne.NewMenu("LogiTux",
		fyne.NewMenuItem("Show", func() { a.window.Show() }),
		fyne.NewMenuItem("Hide", func() { a.window.Hide() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("All Lights On", func() { a.setAllPower(true) }),
		fyne.NewMenuItem("All Lights Off", func() { a.setAllPower(false) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { a.fyneApp.Quit() }),
	)
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(theme.FyneLogo())
}
