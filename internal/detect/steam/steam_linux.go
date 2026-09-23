//go:build linux

package steam

import "os"

// defaultRoots returns the Linux Steam root candidates under $HOME
// (native, legacy symlinks and Flatpak).
func defaultRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return linuxRootCandidates(home)
}
