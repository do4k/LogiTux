// Command logitux is a GUI for controlling Logitech devices. It targets
// Linux (full device support) and also builds and runs on Windows and
// macOS, where the HID backend is currently a stub (see internal/hid) so
// the UI comes up but finds no devices yet.
package main

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"logitux/internal/config"
	"logitux/internal/device"

	// Registers itself with the internal/device registry on import. Add
	// further device plugins the same way to extend LogiTux. These three
	// are HID-based and build on every OS; the Linux-only webcam plugin
	// (V4L2) is imported from plugins_linux.go instead.
	_ "logitux/internal/device/gpro"
	_ "logitux/internal/device/litra"
	_ "logitux/internal/device/prox"
	"logitux/internal/hid"
)

const discoveryInterval = 3 * time.Second

// previewInterval paces repainting the live camera preview — much
// faster than discoveryInterval, since a 3s cadence would make a
// "live" feed an obvious slideshow. ~15fps is plenty for a settings
// preview and keeps continuous JPEG decode cheap.
const previewInterval = 66 * time.Millisecond

// appState owns the set of currently-open devices and the widgets built
// for them. Discover is called periodically to pick up hot-plugged
// devices; we keep already-open handles alive across refreshes rather
// than reopening them every tick.
type appState struct {
	fyneApp fyne.App
	window  fyne.Window
	backend hid.Backend
	store   *config.Store

	// trayApp is non-nil when the platform supports a system tray; the
	// tray menu is rebuilt on every refresh (see updateSystemTrayMenu)
	// since it has a submenu per connected device.
	trayApp desktop.App

	mu sync.Mutex
	// current holds every open device, keyed by its Candidate.Key (a
	// stable per-interface identity, e.g. the /dev node path) rather than
	// its serial — serials aren't always unique or even present, and the
	// key is what lets refresh reuse an already-open device instead of
	// reopening it.
	current map[string]device.Device
	// skip holds keys that enumerate as present but shouldn't be (re)opened:
	// interfaces that opened to no device (a receiver's non-primary node)
	// or that errored. Cleared for a key once it disappears, so a replug
	// retries. Prevents a per-tick open storm on those nodes.
	skip map[string]bool

	// selectedSerial is the serial of the device whose page is open, or
	// "" when the dashboard (the home screen) is showing. It survives
	// rebuilds (buildMainView runs on every discovery tick) so the open
	// page stays open; if that device unplugs, the UI falls back to the
	// dashboard. Only ever touched from the main/UI goroutine — inside
	// fyne.Do in refresh, or from a widget callback — so it needs no
	// lock, unlike current above.
	selectedSerial string

	// pageSection remembers, per device serial, which section of that
	// device's page (sensitivity/assignments/lighting/sound) is selected.
	// Pages are rebuilt from scratch on every refresh tick, which would
	// otherwise silently snap back to the first section out from under
	// the user; same no-lock-needed reasoning as selectedSerial.
	pageSection map[string]string

	// previewer/previewImage/previewSeq belong to the live camera preview
	// on a webcam's Camera/Image page (see cameraPreviewShowcase in
	// ui.go): previewer is the device.Previewer whose frames should be
	// painted into previewImage, and previewSeq is the sequence number
	// last painted, so the preview ticker below only repaints on an
	// actual new frame. Set from cameraPreviewShowcase, which — like
	// buildMainView generally — only ever runs on the main/UI goroutine
	// (a nav callback, or inside refresh's fyne.Do), so like
	// selectedSerial these need no lock.
	previewer    device.Previewer
	previewImage *canvas.Image
	previewSeq   uint64
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

	// All UI updates triggered from background goroutines (the discovery
	// ticker in main, below) go through fyne.Do; this declares that so
	// Fyne doesn't print its "not migrated" warning on every startup.
	app.SetMetadata(fyne.AppMetadata{
		ID:         "io.github.logitux",
		Name:       "LogiTux",
		Migrations: map[string]bool{"fyneDo": true},
	})

	a := app.NewWithID("io.github.logitux")
	a.SetIcon(theme.FyneLogo())
	a.Settings().SetTheme(ghubTheme{})
	window := a.NewWindow("LogiTux")
	window.Resize(fyne.NewSize(980, 620))

	state := &appState{
		fyneApp:     a,
		window:      window,
		backend:     hid.Default,
		store:       store,
		current:     make(map[string]device.Device),
		skip:        make(map[string]bool),
		pageSection: make(map[string]string),
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

	go func() {
		ticker := time.NewTicker(previewInterval)
		defer ticker.Stop()
		for range ticker.C {
			state.tickPreview()
		}
	}()

	window.ShowAndRun()

	state.closeAll()
}

// refresh re-enumerates connected devices, opens newly connected ones,
// closes handles for devices that disappeared, and rebuilds the UI. It
// deliberately does NOT reopen devices it already holds: enumeration is
// cheap (device.Discover returns candidates without opening them), and
// re-opening a wireless HID++ device every tick floods it with a fresh
// feature-discovery burst on a second hidraw fd, which stalls the live
// connection — that was what made opening the headset hang. Safe to call
// from any goroutine.
func (a *appState) refresh() {
	candidates, errs := device.Discover(a.backend)
	for _, err := range errs {
		log.Printf("logitux: %v", err)
	}

	present := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		present[c.Key] = true
	}

	a.mu.Lock()
	// Close devices whose interface disappeared, and forget stale skip
	// marks so a replug of the same node is retried.
	for key, d := range a.current {
		if !present[key] {
			d.Close()
			delete(a.current, key)
		}
	}
	for key := range a.skip {
		if !present[key] {
			delete(a.skip, key)
		}
	}

	// Open only interfaces we don't already hold (and haven't marked as
	// non-openable).
	var newlyOpened []device.Device
	for _, c := range candidates {
		if _, alreadyOpen := a.current[c.Key]; alreadyOpen {
			continue
		}
		if a.skip[c.Key] {
			continue
		}
		d, err := c.Open()
		if err != nil {
			log.Printf("logitux: %v", err)
			a.skip[c.Key] = true // don't retry every tick; a replug clears it
			continue
		}
		if d == nil {
			a.skip[c.Key] = true // e.g. a receiver's non-primary hidraw node
			continue
		}
		a.current[c.Key] = d
		newlyOpened = append(newlyOpened, d)
	}

	devices := make([]device.Device, 0, len(a.current))
	for _, d := range a.current {
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Info().Serial < devices[j].Info().Serial
	})
	a.mu.Unlock()

	// Outside the lock: applySavedRemaps does blocking device I/O
	// (RemapButton is a HID++ round-trip), which shouldn't hold up
	// anything else that needs a.mu meanwhile.
	for _, d := range newlyOpened {
		a.applySavedRemaps(d)
	}

	fyne.Do(func() {
		a.window.SetContent(buildMainView(a, devices))
		a.updateSystemTrayMenu(devices)
	})
}

// tickPreview repaints the live camera preview, if a webcam's page is
// open and has produced a frame since the last tick. Runs on its own
// faster ticker than refresh's discovery-driven rebuilds (see
// previewInterval) so the feed doesn't visibly stutter, and is a no-op
// whenever no preview is showing.
func (a *appState) tickPreview() {
	fyne.Do(func() {
		if a.previewer == nil || a.previewImage == nil {
			return
		}
		img, seq := a.previewer.Frame()
		if img == nil || seq == a.previewSeq {
			return
		}
		a.previewSeq = seq
		a.previewImage.Image = img
		a.previewImage.Refresh()
	})
}

func (a *appState) closeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.current {
		d.Close()
	}
}

// setPower is the tray menu's per-device "Turn On"/"Turn Off" action.
func (a *appState) setPower(d device.Device, pc device.PowerControl, on bool) {
	if err := pc.SetPower(on); err != nil {
		log.Printf("logitux: set power on %s: %v", d.Info().Name, err)
		return
	}
	a.saveState(d.Info().Serial, func(s *config.DeviceState) { s.Power = on })
	// Reflect the change immediately (in the window and the tray menu's
	// checkmark-free labels) rather than waiting for the next tick.
	go a.refresh()
}

// setDPI is the tray menu's per-device DPI preset action.
func (a *appState) setDPI(d device.Device, dc device.DPIControl, dpi int) {
	if err := dc.SetDPI(dpi); err != nil {
		log.Printf("logitux: set DPI on %s: %v", d.Info().Name, err)
		return
	}
	a.saveState(d.Info().Serial, func(s *config.DeviceState) { s.DPI = dpi })
	go a.refresh()
}

// applySavedRemaps re-applies any persisted button remaps to a
// newly-connected device. Unlike brightness/DPI/etc., which only seed the
// GUI and wait for the user to interact, remaps are re-applied
// proactively: a remap that silently stopped working every time the
// device reconnects would defeat the point of the feature, and — unlike a
// light turning on unexpectedly — reapplying a remap is inert until the
// user actually presses that button, so it's not a surprising thing to do
// automatically.
func (a *appState) applySavedRemaps(d device.Device) {
	brc, ok := d.(device.ButtonRemapControl)
	if !ok {
		return
	}
	saved, ok := a.store.Get(d.Info().Serial)
	if !ok {
		return
	}
	for cid, target := range saved.ButtonRemaps {
		if err := brc.RemapButton(cid, target); err != nil {
			log.Printf("logitux: reapply button remap 0x%x on %s: %v", cid, d.Info().Name, err)
		}
	}
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
	a.trayApp = desk
	desk.SetSystemTrayIcon(theme.FyneLogo())
	a.updateSystemTrayMenu(nil) // Show/Hide/Quit only, until the first refresh finds devices
}

// updateSystemTrayMenu rebuilds the tray menu so it has a submenu per
// currently connected device (e.g. "Litra Glow > Turn On / Turn Off")
// instead of one global action across every light. Called on every
// refresh, since the device list changes over time. No-op if the platform
// has no tray support. Must run on the main/UI goroutine.
func (a *appState) updateSystemTrayMenu(devices []device.Device) {
	if a.trayApp == nil {
		return
	}

	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Show", func() { a.window.Show() }),
		fyne.NewMenuItem("Hide", func() { a.window.Hide() }),
	}

	if len(devices) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		for _, d := range devices {
			if item := deviceTrayMenuItem(a, d); item != nil {
				items = append(items, item)
			}
		}
	}

	items = append(items, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("Quit", func() { a.fyneApp.Quit() }))
	a.trayApp.SetSystemTrayMenu(fyne.NewMenu("LogiTux", items...))
}

// dpiPresets are the quick-pick values offered in the tray for devices
// with adjustable DPI, filtered to whatever range the device reports.
var dpiPresets = []int{400, 800, 1600, 3200, 6400}

// deviceTrayMenuItem builds a device's tray submenu from whichever quick
// actions its capabilities support (power, DPI presets, ...), or nil if it
// has none. Sliders/pickers for finer control stay in the main window;
// this is only for one-click actions.
func deviceTrayMenuItem(a *appState, d device.Device) *fyne.MenuItem {
	var subItems []*fyne.MenuItem

	if pc, ok := d.(device.PowerControl); ok {
		subItems = append(subItems,
			fyne.NewMenuItem("Turn On", func() { a.setPower(d, pc, true) }),
			fyne.NewMenuItem("Turn Off", func() { a.setPower(d, pc, false) }),
		)
	}

	if dc, ok := d.(device.DPIControl); ok {
		min, max, _ := dc.DPIRange()
		var dpiItems []*fyne.MenuItem
		for _, dpi := range dpiPresets {
			if dpi < min || dpi > max {
				continue
			}
			dpiItems = append(dpiItems, fyne.NewMenuItem(fmt.Sprintf("%d DPI", dpi), func() { a.setDPI(d, dc, dpi) }))
		}
		if len(dpiItems) > 0 {
			if len(subItems) > 0 {
				subItems = append(subItems, fyne.NewMenuItemSeparator())
			}
			subItems = append(subItems, dpiItems...)
		}
	}

	if len(subItems) == 0 {
		return nil
	}

	item := fyne.NewMenuItem(d.Info().Name, nil)
	item.ChildMenu = fyne.NewMenu("", subItems...)
	return item
}
