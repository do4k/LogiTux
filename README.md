# LogiTux

A native GUI for controlling Logitech devices — the Logitech G HUB
equivalent, built for Linux first. Written in Go. It also builds and runs
on Windows and macOS (with a device-support caveat — see [Prebuilt
packages](#prebuilt-packages)).

On Linux, LogiTux talks to hardware directly over the `hidraw` interface
(no `libhidapi` dependency, no cgo for device I/O), so installation only
needs a Go toolchain and Fyne's usual GUI build dependencies.

![LogiTux's Dashboard, styled after Logitech G HUB: one dark card per connected device with a product render (original artwork, not Logitech's — see Credit), battery level, and a settings button](images/screenshot-dashboard.png)
![LogiTux's device page for a G Pro Wireless: a row of DPI stage presets and report rate in the panel, with a DPI Speeds track plotting each stage in the showcase](images/screenshot-mouse.png)
![LogiTux's Litra Glow page: a power pill, temperature and brightness gradient sliders, and a render whose halo follows the light's state](images/screenshot-litra.png)
![LogiTux's C922 page: zoom slider, pan/tilt position pad, and autofocus/auto-exposure pills with their manual sliders](images/screenshot-webcam.png)

## Status

v1 supports:

- **Litra Glow / Litra Beam** key lights — power, brightness, color
  temperature.
- **G Pro Wireless** mouse (direct or via its Lightspeed receiver) — DPI,
  report (polling) rate, and logo color always; battery and button
  remapping on units whose firmware exposes the corresponding HID++
  feature (see [Usage](#usage) — one of the two units this was tested
  against doesn't expose either, evidently handling them through the
  receiver's own protocol layer instead, so both are implemented as
  optional and degrade gracefully rather than being guaranteed).
- **PRO X Wireless Gaming Headset** — battery, sidetone, a full per-band
  equalizer (band count/frequencies/dB range are read from the device,
  not assumed), and a hardware mic noise-reduction toggle on units whose
  firmware supports it (probed with a read; the toggle hides otherwise).
- **C920 / C920 HD Pro / C922 Pro Stream / C930e** webcams — zoom,
  pan/tilt position, autofocus and manual focus, auto and manual
  exposure, and image tuning (brightness, contrast, saturation,
  sharpness). Webcams aren't HID devices, so these go through a small
  pure-Go V4L2 layer rather than hidraw; which controls exist is queried
  from the driver per device, not assumed. Controls only — no video
  preview yet. **Caveat:** implemented against the documented V4L2 UVC
  control API with unit-tested ioctl marshaling, but not yet verified
  against physical hardware, unlike everything above.

The UI is styled after Logitech's own G HUB, navigation included: the
**Dashboard** — a near-black home screen with a cyan-blue accent — shows
every connected device as a clickable card (product render, name,
battery level if it has one — a bolt marks charging, and a settings
button); clicking one opens that device's page with its full controls,
and the back arrow in the page's top-left returns to the Dashboard. A
device's page only exists while it's actually connected — unplugging
drops you back on the Dashboard. Device pages are laid out like G HUB's:
an icon rail on the left groups the device's settings into sections —
Sensitivity (DPI, report rate), Assignments (button remapping), Lighting
(power, brightness, color temperature, RGB logo color), and Sound
(sidetone, mic noise reduction, equalizer) — and only the sections a
device actually supports
appear. The panel beside the rail holds the selected section's controls,
and the rest of the page shows the battery level and a section-specific
showcase. On a mouse's Sensitivity section, DPI is presented as G HUB's
**DPI stages**: a row of preset buttons (the active one highlighted;
click to switch the mouse to that DPI), with a *DPI Speeds* track in the
showcase plotting each stage as a dot along a non-linear axis. On a
headset's page the showcase is the vertical ADVANCED EQ; on a light's
page the product render actually glows, like G HUB's Litra page — a halo
behind it follows the power pill and the temperature and brightness
gradient sliders live. The selected section sticks across
LogiTux's periodic re-polling
rather than snapping back to the first every few seconds. A system
tray icon offers quick per-device actions (power, DPI presets) without
opening the window.

The device layer is a plugin architecture (see [Architecture](#architecture)
below), so support for further Logitech hardware — other mice, keyboards,
headsets, webcams — can be added incrementally without changing the GUI or
core app.

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
   `hidraw` nodes (and `/dev/uinput`, used only if you remap a mouse
   button), and a `.desktop` launcher entry.

On other distros, install Go 1.22+ and Fyne's build dependencies yourself
(see the [Fyne getting-started guide](https://docs.fyne.io/started/)) and
then run `./install.sh` — it will skip the apt-specific steps.

After installing, **unplug and replug your device** so the new udev rule
applies, then launch LogiTux from your application menu or run
`~/.local/bin/logitux`.

To remove everything `install.sh` set up, run `./uninstall.sh`.

### Prebuilt packages

Every push builds installable packages for all three platforms in CI (see
`.github/workflows/build.yml`), and tagging a release (`vX.Y.Z`) attaches
them to a GitHub Release:

- **Debian/Ubuntu** — a `.deb` (`sudo apt install ./logitux_*_amd64.deb`),
  which drops the binary in `/usr/bin`, installs the udev rule and desktop
  entry, and reloads udev for you. A plain binary tarball is also built.
- **Windows** — a zipped `logitux.exe`.
- **macOS** — a zipped `LogiTux.app`. It's unsigned, so the first launch
  needs a right-click → **Open** to get past Gatekeeper.

> **Platform support caveat.** LogiTux was built for Linux, where device
> support is complete and hardware-tested. It now also **builds and runs**
> on Windows and macOS — the full G HUB-style UI comes up — but those
> builds currently ship a *stub* HID backend, so they find no devices yet
> (`internal/hid`). The device plugins are already cross-platform; only a
> native Windows (setupapi/hid.dll) or macOS (IOKit) HID backend behind
> `hid.Default` is needed to light them up. Webcam control stays
> Linux-only regardless, since it uses V4L2.

## Usage

LogiTux polls for supported devices every few seconds. Each connected
device gets its own page, opened from its Dashboard card (and gone once
the device unplugs), with whichever controls it supports — a power
checkbox, sliders, dropdowns, a color picker. Changes are applied
immediately.

Closing the window minimizes LogiTux to the system tray rather than
quitting; use the tray menu's "Quit" item to actually exit. The tray also
has a submenu per connected device for one-click actions (e.g. a light's
"Turn On"/"Turn Off", or a mouse's DPI presets) without opening the window.

Devices that can't report their own state back over USB (Litra lights;
the G Pro Wireless's DPI/report-rate/battery/color often *can* be read
back live and are) fall back to the last value LogiTux sent, kept in
`$XDG_CONFIG_HOME/logitux/state.json` (typically
`~/.config/logitux/state.json`). Most settings are only ever *applied* when
you interact with them — opening LogiTux never pushes a stale value to a
light that's currently off, for example. Button remaps are the one
exception (see below): they're re-applied automatically whenever a mouse
reconnects, since a remap that silently stopped working every time the app
restarted would defeat the point of the feature.

### Custom product images

Each supported product ships with an original render drawn for LogiTux
(Logitech's own product photography is copyrighted, so an MIT-licensed
project can't redistribute it). If you'd rather see the official images,
you can supply your own: drop a file into
`$XDG_CONFIG_HOME/logitux/images/` (typically `~/.config/logitux/images/`)
named after the product — lowercased, spaces as dashes — with a `.png`,
`.jpg`, `.jpeg`, or `.svg` extension, e.g. `g-pro-wireless.png`,
`litra-glow.png`, or `pro-x-wireless-gaming-headset.png`. LogiTux picks
these up at startup and uses them everywhere the built-in render would
appear. Such images are for your own local use; don't commit them to a
public fork.

### Button remapping

If the mouse's firmware exposes the `REPROG_CONTROLS` feature (0x1B00
through 0x1B04) — not universal; see [Status](#status) — its remappable
buttons each get a dropdown to retarget them to another mouse button or a
keyboard key, or restore "Default". If the feature isn't present, this
section just doesn't appear; the rest of the mouse's controls are
unaffected.

This works by telling the mouse to stop sending that button's normal click
and instead report presses as a raw event, which LogiTux then translates
into a synthetic key/button press via a virtual input device
(`/dev/uinput`). That means **a remapped button only works while LogiTux is
running** — if LogiTux isn't running (not started yet, crashed, or killed
without a chance to clean up), that button does nothing at all, unlike
every other setting here, which simply stops being adjustable rather than
breaking the button. LogiTux reverts every active remap when it exits
normally (including from the tray's "Quit"), so a clean shutdown always
restores normal clicking.

## Architecture

```
cmd/logitux/             GUI entry point (Fyne): window, dashboard, device pages, systray
internal/hid/            HID Backend interface (hid.go) + Linux hidraw impl
                           (hidraw_linux.go) and a no-op stub for other OSes (hid_stub.go)
internal/hidpp/          HID++ 2.0 transport: feature calls, notifications, shared battery logic
internal/uinput/         Virtual input device for button remapping (Linux; stub elsewhere)
internal/device/         Plugin registry and capability interfaces
internal/device/litra/   Litra Glow/Beam plugin (simple vendor HID protocol)
internal/device/gpro/    G Pro Wireless plugin (HID++ 2.0 protocol)
internal/device/prox/    PRO X Wireless Gaming Headset plugin (HID++ 2.0 protocol)
internal/device/webcam/  C920/C922/C930e webcam plugin (V4L2 controls, not HID; Linux only)
internal/v4l2/           Pure-Go V4L2 control ioctls: query/get/set on /dev/videoN
internal/config/         JSON-backed last-known-state store
packaging/               nfpm .deb config, Debian postinstall, macOS Info.plist template
install/                 udev rules and .desktop launcher entry
.github/workflows/       CI: vet/test + per-OS builds (.deb, .exe, .app) and tag releases
```

**Cross-platform structure.** The HID-facing types (`Info`, `Handle`,
`Backend`, `Default`) live in the build-tag-free `internal/hid/hid.go`, so
every device plugin compiles on all three OSes. `hidraw_linux.go` supplies
the real Linux backend; `hid_stub.go` (`//go:build !linux`) supplies a
no-op one so Windows/macOS builds run but find nothing until a native
backend is written. The `uinput` package is split the same way (real
Linux `/dev/uinput` device vs. a stub `Open` that reports remapping
unavailable), and the Linux-only webcam plugin is imported from
`cmd/logitux/plugins_linux.go` rather than the shared import block.

Device support is added as a plugin: a package registers the vendor/product
IDs it handles with `internal/device.Register` in an `init()` function (see
`internal/device/litra/litra.go`, `internal/device/gpro/gpro.go`, or
`internal/device/prox/prox.go`), and implements whichever capability
interfaces the hardware supports (`PowerControl`, `DPIControl`,
`ButtonRemapControl`, `EqualizerControl`, ...). The GUI never references a
specific product — it type-asserts each discovered `device.Device` against
the capability interfaces and renders whatever controls apply. Adding a new
device means writing a new plugin package and importing it (for its
`init()` side effect) from `cmd/logitux/main.go`; no other files need to
change. Hardware that doesn't live behind hidraw at all can instead
register a whole discovery function with `device.RegisterDiscoverer` —
that's how the webcam plugin scans `/sys/class/video4linux` for V4L2
devices while everything else goes through the hidraw backend.

Most Logitech peripherals beyond simple lights (mice, keyboards, headsets)
speak Logitech's **HID++ 2.0** protocol: a request/response feature-call
system rather than the Litra's fixed byte sequences. `internal/hidpp`
implements that transport (feature discovery via the Root feature,
request/response matching, unsolicited notifications), so a new HID++
device plugin only needs to know which feature IDs it uses and their byte
layouts — see `internal/device/gpro` for a worked example covering DPI,
report rate, RGB, and button remapping, and `internal/device/prox` for a
per-band equalizer. Battery support (a four-tier feature-ID fallback, since
different units implement different ones — verified the hard way, against
real hardware that needed each of the fallback tiers) lives once in
`internal/hidpp/battery.go` and both device plugins just call into it,
rather than each having its own copy.

## Development

```bash
make build   # -> bin/logitux
make test    # go vet + go test ./...
make run     # build and run
```

## Credit

All product artwork in LogiTux (the dashboard renders and generic device
icons) is original, drawn for this project; none of it is Logitech's
imagery. "Logitech", "G HUB", and the product names are Logitech
trademarks, used here only to identify the hardware being controlled.

The Litra USB protocol was originally reverse-engineered by
[kharyam/go-litra-driver](https://github.com/kharyam/go-litra-driver) (and
its Python predecessor, [kharyam/litra-driver](https://github.com/kharyam/litra-driver)).
LogiTux's Litra plugin is an independent implementation of that protocol,
built on LogiTux's own pure-Go hidraw backend rather than `libhidapi`.

HID++ 2.0 feature byte layouts used by the G Pro Wireless plugin were
verified against [pwr-Solaar/Solaar](https://github.com/pwr-Solaar/Solaar),
the most complete existing Linux HID++ implementation, rather than guessed
from general protocol documentation.

## License

MIT — see [LICENSE](LICENSE).
