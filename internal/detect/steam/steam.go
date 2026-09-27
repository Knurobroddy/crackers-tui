// Package steam implements the "steam" detection strategy.
package steam

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
)

// Strategy finds Steam games through Steam roots, library folders and app manifests.
type Strategy struct {
	roots []string // candidate Steam root directories; missing ones are ignored; nil means DefaultRoots
	goos  string   // selects which game builds are considered
}

// New returns a strategy that searches the given Steam roots for builds of
// goos. With nil roots it calls DefaultRoots on every Detect, so a Steam
// install that moves while the app runs is still found.
func New(roots []string, goos string) *Strategy {
	return &Strategy{roots: roots, goos: goos}
}

// Name implements detect.Strategy.
func (s *Strategy) Name() string { return "steam" }

// Detect implements detect.Strategy.
func (s *Strategy) Detect(game detect.GameDef) ([]detect.Result, error) {
	if game.Steam == nil || game.Steam.AppID <= 0 {
		return nil, fmt.Errorf("game %q has no steam appid", game.ID)
	}
	appID := strconv.Itoa(game.Steam.AppID)
	libraries := s.Libraries()
	var out []detect.Result
	for _, library := range libraries {
		if result, ok := s.detectInLibrary(game, appID, library, libraries); ok {
			out = append(out, result)
		}
	}
	return out, nil
}

// Libraries returns all Steam library directories: every existing Steam root
// plus the folders listed in its steamapps/libraryfolders.vdf. Paths are
// absolute, symlink-resolved and deduplicated.
func (s *Strategy) Libraries() []string {
	set := newLibrarySet()
	var roots []string
	for _, root := range s.candidateRoots() {
		resolved, ok := existingDir(root)
		if ok && set.add(resolved) {
			roots = append(roots, resolved)
		}
	}
	for _, root := range roots {
		s.addLibraryFolders(set, root)
	}
	return set.paths
}

func (s *Strategy) candidateRoots() []string {
	if s.roots == nil {
		return DefaultRoots()
	}
	return s.roots
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

// detectInLibrary checks one Steam library for an installed build of game.
func (s *Strategy) detectInLibrary(game detect.GameDef, appID, library string, libraries []string) (detect.Result, bool) {
	acf := filepath.Join(library, "steamapps", "appmanifest_"+appID+".acf")
	installDir, err := ReadInstallDir(acf)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("skip steam library", "path", acf, "err", err)
		}
		return detect.Result{}, false
	}
	root := filepath.Join(library, "steamapps", "common", installDir)
	build, anchor, ok := detect.SelectBuild(game.Builds, s.goos, root)
	if !ok {
		slog.Debug("no build anchor matched", "game_id", game.ID, "path", root, "goos", s.goos)
		return detect.Result{}, false
	}
	return detect.Result{
		GameID:     game.ID,
		BuildID:    build.ID,
		AnchorPath: anchor,
		RootDir:    root,
		Extra: map[string]string{
			detect.ExtraSteamLibrary:   library,
			detect.ExtraSteamAppID:     appID,
			detect.ExtraSteamLibraries: strings.Join(libraries, string(os.PathListSeparator)),
		},
	}, true
}

// addLibraryFolders reads root's libraryfolders.vdf and adds each listed
// path to set.
func (s *Strategy) addLibraryFolders(set *librarySet, root string) {
	vdfPath := filepath.Join(root, "steamapps", "libraryfolders.vdf")
	paths, err := ReadLibraryFolders(vdfPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("skip steam library folders", "path", vdfPath, "err", err)
		}
		return
	}
	for _, path := range paths {
		set.add(path)
	}
}

// librarySet collects existing library directories without duplicates.
type librarySet struct {
	paths []string
	seen  map[string]bool
}

func newLibrarySet() *librarySet {
	return &librarySet{seen: map[string]bool{}}
}

// add records dir if it exists and is new; it reports whether it was added.
func (s *librarySet) add(dir string) bool {
	resolved, ok := existingDir(dir)
	if !ok || s.seen[detect.PathKey(resolved)] {
		return false
	}
	s.seen[detect.PathKey(resolved)] = true
	s.paths = append(s.paths, resolved)
	return true
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
