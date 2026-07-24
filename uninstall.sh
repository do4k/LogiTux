#!/usr/bin/env bash
# Removes the LogiTux binary, desktop launcher entry, udev rule, and .app bundle.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

# Detect OS
OS=""
if command -v apt-get >/dev/null 2>&1; then
	OS="linux"
elif [[ "$OSTYPE" == "darwin"* ]]; then
	OS="macos"
fi

if [[ "$OS" == "linux" ]]; then
	make uninstall
	echo "LogiTux has been uninstalled."
elif [[ "$OS" == "macos" ]]; then
	# Remove .app from /Applications or ~/.local/Applications
	APP="LogiTux.app"
	for DIR in "/Applications" "$HOME/.local/Applications"; do
		if [[ -d "$DIR/$APP" ]]; then
			echo "==> Removing $DIR/$APP..."
			rm -rf "$DIR/$APP"
		fi
	done
	echo "LogiTux has been uninstalled."
else
	echo "==> Unsupported OS. Please remove LogiTux manually."
fi
