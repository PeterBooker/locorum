//go:build !darwin

package platform

// isUnderRosetta is the non-darwin stub. Rosetta is a macOS-only translator;
// other operating systems are never under it.
func isUnderRosetta() bool { return false }
