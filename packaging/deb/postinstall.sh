#!/bin/sh
# Reload udev rules after install so LogiTux's HID access rule takes
# effect without waiting for a reboot. Best-effort: a headless or
# container environment may have no running udev, which is not a failure.
set -e

if command -v udevadm >/dev/null 2>&1; then
	udevadm control --reload-rules || true
	udevadm trigger || true
fi

exit 0
