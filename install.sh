#!/usr/bin/env bash
# Builds and installs LogiTux for Linux and macOS.
# On Linux: binary to ~/.local/bin, desktop entry, and udev rule.
# On macOS: .app bundle to /Applications (or ~/.local/Applications).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

echo "==> LogiTux installer"

# Detect OS
OS=""
if command -v apt-get >/dev/null 2>&1; then
	OS="linux"
elif [[ "$OSTYPE" == "darwin"* ]]; then
	OS="macos"
else
	OS="unknown"
fi

echo "==> Detected OS: $OS"

# Ensure Go toolchain is available
if ! command -v go >/dev/null 2>&1; then
	echo "==> Go toolchain not found."
	if [[ "$OS" == "linux" ]]; then
		echo "==> Installing golang-go via apt..."
		sudo apt-get update
		sudo apt-get install -y golang-go
	elif [[ "$OS" == "macos" ]]; then
		echo "Please install Go 1.22 or newer from https://go.dev/dl/ or via Homebrew:"
		echo "    brew install go"
		exit 1
	else
		echo "Please install Go 1.22 or newer from https://go.dev/dl/ and re-run this script."
		exit 1
	fi
fi
echo "==> Using $(go version)"

# Install build dependencies based on OS
if [[ "$OS" == "linux" ]]; then
	echo "==> Ensuring Fyne's build dependencies are installed (gcc, libgl1-mesa-dev, xorg-dev)..."
	sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev
elif [[ "$OS" == "macos" ]]; then
	# Ensure Xcode command line tools are installed (provides clang for CGO)
	if ! xcode-select -p >/dev/null 2>&1; then
		echo "==> Xcode command line tools not found."
		echo "    Installing via xcode-select..."
		xcode-select --install
		echo "    After installation completes, re-run this script."
		exit 1
	fi
	echo "==> Xcode command line tools: $(xcode-select -p)"
else
	echo "==> Fyne needs a C compiler and OpenGL headers."
	echo "    On Linux: install gcc, libgl1-mesa-dev, xorg-dev (or equivalent)."
	echo "    On macOS: install Xcode command line tools."
	echo "    See https://docs.fyne.io/started/ for details."
fi

echo "==> Building and installing LogiTux..."

if [[ "$OS" == "linux" ]]; then
	make install

	cat <<'EOF'

Done.

Unplug and replug your Logitech device so the new udev rule takes effect,
then launch LogiTux from your application menu, or run:

    ~/.local/bin/logitux

(Add ~/.local/bin to your PATH if that command isn't found.)
EOF

elif [[ "$OS" == "macos" ]]; then
	# Build .app bundle
	VERSION="0.0.0"
	CGO_ENABLED=1 go build -ldflags "-s -w" -o logitux ./cmd/logitux

	# Create .app bundle
	APP="LogiTux.app"
	rm -rf "$APP" AppIcon.iconset
	mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
	cp logitux "$APP/Contents/MacOS/logitux"

	# Create Info.plist
	sed "s/__VERSION__/${VERSION}/g" packaging/macos/Info.plist.template > "$APP/Contents/Info.plist"

	# Build .icns from Icon.png
	mkdir AppIcon.iconset
	for pair in "16 16x16" "32 16x16@2x" "32 32x32" "64 32x32@2x" \
	            "128 128x128" "256 128x128@2x" "256 256x256" "512 256x256@2x" "512 512x512"; do
		set -- $pair
		sips -z "$1" "$1" Icon.png --out "AppIcon.iconset/icon_$2.png" >/dev/null
	done
	iconutil -c icns AppIcon.iconset -o "$APP/Contents/Resources/AppIcon.icns"

	# Clean up
	rm -rf AppIcon.iconset logitux

	# Install to /Applications (requires sudo) or ~/.local/Applications
	INSTALL_DIR="/Applications"
	if [[ ! -w "$INSTALL_DIR" ]]; then
		INSTALL_DIR="$HOME/.local/Applications"
		mkdir -p "$INSTALL_DIR"
	fi

	echo "==> Installing LogiTux.app to $INSTALL_DIR..."
	cp -R "$APP" "$INSTALL_DIR/"
	rm -rf "$APP"

	cat <<EOF

Done.

LogiTux has been installed to $INSTALL_DIR/LogiTux.app

To run: open $INSTALL_DIR/LogiTux.app

Note: On first launch, right-click the app and select Open to bypass Gatekeeper.
EOF

else
	echo "==> Unsupported OS. Please build manually:"
	echo "    make build"
fi
