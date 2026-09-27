//go:build windows

package steam

import (
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// DefaultRoots reads the Steam install path from the registry:
// HKCU\Software\Valve\Steam\SteamPath, then HKLM\SOFTWARE\WOW6432Node\Valve\Steam\InstallPath.
func DefaultRoots() []string {
	var roots []string
	if path := readRegString(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath"); path != "" {
		roots = append(roots, filepath.Clean(filepath.FromSlash(path))) // SteamPath may use forward slashes
	}
	if path := readRegString(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"); path != "" {
		roots = append(roots, filepath.Clean(filepath.FromSlash(path)))
	}
	return roots
}

func readRegString(root registry.Key, path, name string) string {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = key.Close() }()
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return value
}
