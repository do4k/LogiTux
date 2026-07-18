#!/usr/bin/env bash
# Builds and installs LogiTux: the binary (to ~/.local/bin), a desktop
# launcher entry, and the udev rule that lets LogiTux talk to supported
# devices without running as root.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

echo "==> LogiTux installer"

if ! command -v go >/dev/null 2>&1; then
	echo "==> Go toolchain not found."
	if command -v apt-get >/dev/null 2>&1; then
		echo "==> Installing golang-go via apt..."
		sudo apt-get update
		sudo apt-get install -y golang-go
	else
		echo "Please install Go 1.22 or newer from https://go.dev/dl/ and re-run this script."
		exit 1
	fi
fi
echo "==> Using $(go version)"

if command -v apt-get >/dev/null 2>&1; then
	echo "==> Ensuring Fyne's build dependencies are installed (gcc, libgl1-mesa-dev, xorg-dev)..."
	sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev
else
	echo "==> No apt-get found; skipping automatic dependency install."
	echo "    Fyne needs a C compiler and OpenGL/X11 dev headers - see https://docs.fyne.io/started/"
fi

echo "==> Building and installing LogiTux..."
make install

cat <<'EOF'

Done.

Unplug and replug your Logitech device so the new udev rule takes effect,
then launch LogiTux from your application menu, or run:

    ~/.local/bin/logitux

(Add ~/.local/bin to your PATH if that command isn't found.)
EOF
