//go:build !windows && !linux

package steam

// defaultRoots returns nothing: only Windows and Linux are supported.
func defaultRoots() []string { return nil }
