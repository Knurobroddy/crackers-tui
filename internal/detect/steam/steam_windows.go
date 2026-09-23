//go:build windows

package steam

import (
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// defaultRoots reads the Steam install path from the registry:
// HKCU\Software\Valve\Steam\SteamPath, then HKLM\SOFTWARE\WOW6432Node\Valve\Steam\InstallPath.
func defaultRoots() []string {
	var roots []string
	if p := readRegString(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath"); p != "" {
		roots = append(roots, filepath.Clean(filepath.FromSlash(p))) // SteamPath may use forward slashes
	}
	if p := readRegString(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"); p != "" {
		roots = append(roots, filepath.Clean(filepath.FromSlash(p)))
	}
	return roots
}

func readRegString(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}
