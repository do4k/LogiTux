# LogiTux

A native Linux GUI for controlling Logitech devices — the Logitech G HUB
equivalent for Linux. Written in Go.

LogiTux talks to hardware directly over Linux's `hidraw` interface (no
`libhidapi` dependency, no cgo for device I/O), so installation only needs
a Go toolchain and Fyne's usual GUI build dependencies.

## Status

v1 supports the **Litra Glow** and **Litra Beam** key lights: power,
brightness, and color temperature, from a single window with a toggle and
two sliders per device, plus a system tray icon for quick on/off.

The device layer is a plugin architecture (see [Architecture](#architecture)
below), so support for other Logitech hardware — mice, keyboards, headsets,
webcams — can be added incrementally without changing the GUI or core app.
Most of those devices speak Logitech's HID++ protocol rather than the
Litra's simple vendor HID commands, and are not implemented yet.

## Quick start

```bash
git clone <this-repo>
cd LogiTux
./install.sh
```

`install.sh` will, on Debian/Ubuntu-based distros (including Linux Mint):

1. Install the Go toolchain (`golang-go`) if it's missing.
2. Install Fyne's build dependencies (`gcc`, `libgl1-mesa-dev`, `xorg-dev`).
3. Build LogiTux and install it to `~/.local/bin/logitux`.
4. Install a udev rule granting your user access to supported devices'
   `hidraw` nodes, and a `.desktop` launcher entry.

On other distros, install Go 1.22+ and Fyne's build dependencies yourself
(see the [Fyne getting-started guide](https://docs.fyne.io/started/)) and
then run `./install.sh` — it will skip the apt-specific steps.

After installing, **unplug and replug your device** so the new udev rule
applies, then launch LogiTux from your application menu or run
`~/.local/bin/logitux`.

To remove everything `install.sh` set up, run `./uninstall.sh`.

## Usage

LogiTux polls for supported devices every few seconds. Each connected
device gets its own card with a power checkbox and, where applicable,
brightness and color-temperature sliders. Changes are applied immediately.

Closing the window minimizes LogiTux to the system tray rather than
quitting; use the tray menu's "Quit" item to actually exit, or "All Lights
On"/"All Lights Off" to control every connected light at once.

Litra devices can't report their current state back over USB, so LogiTux
remembers the last values it sent (in
`$XDG_CONFIG_HOME/logitux/state.json`, typically `~/.config/logitux/state.json`)
and uses them to pre-fill the sliders on the next launch. It does not
automatically re-apply that state to the device on startup.

## Architecture

```
cmd/logitux/            GUI entry point (Fyne): window, systray, widgets
internal/hid/           Pure-Go hidraw backend: enumerate + open /dev/hidrawN
internal/device/        Plugin registry and capability interfaces
internal/device/litra/  Litra Glow/Beam plugin
internal/config/        JSON-backed last-known-state store
install/                udev rule and .desktop launcher entry
```

Device support is added as a plugin: a package registers the vendor/product
IDs it handles with `internal/device.Register` in an `init()` function (see
`internal/device/litra/litra.go`), and implements whichever capability
interfaces the hardware supports (`PowerControl`, `BrightnessControl`,
`TemperatureControl`, ...). The GUI never references a specific product —
it type-asserts each discovered `device.Device` against the capability
interfaces and renders whatever controls apply. Adding a new light, mouse,
or keyboard means writing a new plugin package and importing it (for its
`init()` side effect) from `cmd/logitux/main.go`; no other files need to
change.

## Development

```bash
make build   # -> bin/logitux
make test    # go vet + go test ./...
make run     # build and run
```

## Credit

The Litra USB protocol was originally reverse-engineered by
[kharyam/go-litra-driver](https://github.com/kharyam/go-litra-driver) (and
its Python predecessor, [kharyam/litra-driver](https://github.com/kharyam/litra-driver)).
LogiTux's Litra plugin is an independent implementation of that protocol,
built on LogiTux's own pure-Go hidraw backend rather than `libhidapi`.

## License

MIT — see [LICENSE](LICENSE).
