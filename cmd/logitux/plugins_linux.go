//go:build linux

package main

// The webcam plugin talks to Video4Linux2, which is Linux-only, so it is
// registered here rather than in main.go's cross-platform import block.
// Its package has no non-Linux build, so importing it anywhere that
// compiles on other OSes would break those builds.
import _ "logitux/internal/device/webcam"
