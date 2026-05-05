//go:build !darwin

package main

// runPreflight is the non-darwin stub. Rosetta is macOS-only; other
// platforms have their own pre-init concerns (the Wayland/X11 switch
// for WSL) but those are handled inside main() proper.
func runPreflight() {}
