#!/usr/bin/env bash
# Removes the LogiTux binary, desktop launcher entry, and udev rule.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

make uninstall
echo "LogiTux has been uninstalled."
