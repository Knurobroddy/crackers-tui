//go:build !windows && !linux

package steam

// DefaultRoots returns nothing: only Windows and Linux are supported.
func DefaultRoots() []string { return nil }
