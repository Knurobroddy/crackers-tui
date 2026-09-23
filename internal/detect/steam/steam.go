// Package steam implements the "steam" detection strategy (ADR §4.1).
package steam

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/logx"
)

// Strategy finds Steam games through Steam roots, library folders and app manifests.
type Strategy struct {
	// GOOS selects which game builds are considered (runtime.GOOS by default).
	GOOS string
	// Roots returns candidate Steam root directories; missing ones are ignored.
	Roots func() []string
	Log   *slog.Logger
}

// New returns the strategy for the current OS.
func New(log *slog.Logger) *Strategy {
	log = logx.OrDiscard(log)
	return &Strategy{GOOS: runtime.GOOS, Roots: defaultRoots, Log: log}
}

// Name implements detect.Strategy.
func (s *Strategy) Name() string { return "steam" }

// Detect implements detect.Strategy.
func (s *Strategy) Detect(game detect.GameDef) ([]detect.Result, error) {
	if game.Steam == nil || game.Steam.AppID <= 0 {
		return nil, fmt.Errorf("game %q has no steam.appid", game.ID)
	}
	appid := strconv.Itoa(game.Steam.AppID)
	libs := s.Libraries()
	var out []detect.Result
	for _, lib := range libs {
		acf := filepath.Join(lib, "steamapps", "appmanifest_"+appid+".acf")
		installDir, err := ReadInstallDir(acf)
		if err != nil {
			if !os.IsNotExist(err) {
				s.Log.Warn("skipping Steam library: cannot read app manifest", "file", acf, "err", err)
			}
			continue
		}
		root := filepath.Join(lib, "steamapps", "common", installDir)
		build, anchor, ok := detect.SelectBuild(game.Builds, s.GOOS, root)
		if !ok {
			s.Log.Info("game installed but no build anchor matched for this OS", "game", game.ID, "root", root, "goos", s.GOOS)
			continue
		}
		out = append(out, detect.Result{
			GameID:     game.ID,
			BuildID:    build.ID,
			AnchorPath: anchor,
			RootDir:    root,
			Extra: map[string]string{
				detect.ExtraSteamLibrary:   lib,
				detect.ExtraSteamAppID:     appid,
				detect.ExtraSteamLibraries: strings.Join(libs, string(os.PathListSeparator)),
			},
		})
	}
	return out, nil
}

// Libraries returns all Steam library directories: every existing Steam root
// plus the folders listed in its steamapps/libraryfolders.vdf. Paths are
// absolute, symlink-resolved and deduplicated.
func (s *Strategy) Libraries() []string {
	var libs []string
	seen := map[string]bool{}
	add := func(p string) {
		p, ok := existingDir(p)
		if !ok || seen[detect.PathKey(p)] {
			return
		}
		seen[detect.PathKey(p)] = true
		libs = append(libs, p)
	}
	var roots []string
	for _, r := range s.Roots() {
		if p, ok := existingDir(r); ok && !seen[detect.PathKey(p)] {
			roots = append(roots, p)
			add(p)
		}
	}
	for _, root := range roots {
		vdfPath := filepath.Join(root, "steamapps", "libraryfolders.vdf")
		paths, err := ReadLibraryFolders(vdfPath)
		if err != nil {
			if !os.IsNotExist(err) {
				s.Log.Warn("cannot read Steam library folders", "file", vdfPath, "err", err)
			}
			continue
		}
		for _, p := range paths {
			add(p)
		}
	}
	return libs
}

// existingDir returns p as a clean, absolute, symlink-resolved path if it is
// an existing directory.
func existingDir(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", false
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	return abs, true
}

// linuxRootCandidates lists the Linux Steam root candidates under home.
func linuxRootCandidates(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".local", "share", "Steam"),
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".steam", "root"),
		filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
	}
}

// ReadLibraryFolders parses libraryfolders.vdf and returns the library paths
// in key order. Both the new format (children are maps with "path") and the
// old format (children are plain path strings) are supported; non-numeric
// keys such as "TimeNextStatsReport" are ignored. Keys match case-insensitively.
func ReadLibraryFolders(file string) ([]string, error) {
	m, err := parseVDFFile(file)
	if err != nil {
		return nil, err
	}
	top, ok := lookupMap(m, "libraryfolders")
	if !ok {
		return nil, fmt.Errorf("%s: no libraryfolders key", file)
	}
	keys := make([]string, 0, len(top))
	for k := range top {
		if isDigits(k) {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	var paths []string
	for _, k := range keys {
		switch v := top[k].(type) {
		case string:
			paths = append(paths, v)
		case map[string]any:
			if p, ok := lookupString(v, "path"); ok {
				paths = append(paths, p)
			}
		}
	}
	return paths, nil
}

// ReadInstallDir returns AppState.installdir from an appmanifest_<appid>.acf.
func ReadInstallDir(file string) (string, error) {
	m, err := parseVDFFile(file)
	if err != nil {
		return "", err
	}
	state, ok := lookupMap(m, "AppState")
	if !ok {
		return "", fmt.Errorf("%s: no AppState key", file)
	}
	dir, ok := lookupString(state, "installdir")
	if !ok || dir == "" {
		return "", fmt.Errorf("%s: no AppState.installdir", file)
	}
	if dir == "." || dir == ".." || strings.ContainsAny(dir, `/\`) {
		return "", fmt.Errorf("%s: invalid installdir %q", file, dir)
	}
	return dir, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
