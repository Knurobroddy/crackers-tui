// Package detect finds installed games using pluggable strategies.
//
// Strategies are code (e.g. "steam"); per-game rules come from the remote
// games.json and are data only.
package detect

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Knurobroddy/crackers-tui/internal/fsutil"
)

// Extra keys set by the steam strategy.
const (
	ExtraSteamLibrary   = "steam_library"
	ExtraSteamAppID     = "steam_appid"
	ExtraSteamLibraries = "steam_libraries" // all known libraries, joined with os.PathListSeparator
)

// BuildDef is one build variant of a game (games.json "builds").
type BuildDef struct {
	ID     string `json:"id"`
	OS     string `json:"os"`
	Anchor string `json:"anchor"`
	Files  string `json:"files"`
}

// SteamDef holds the Steam strategy settings of a game.
type SteamDef struct {
	AppID int `json:"appid"`
}

// GameDef is one entry of games.json.
type GameDef struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Strategy     string     `json:"strategy"`
	Steam        *SteamDef  `json:"steam,omitempty"`
	NotFoundHint string     `json:"not_found_hint,omitempty"`
	Builds       []BuildDef `json:"builds"`
}

// Result is one detected game install.
type Result struct {
	GameID     string            `json:"game_id"`
	BuildID    string            `json:"build_id"`
	AnchorPath string            `json:"anchor_path"` // absolute
	RootDir    string            `json:"root_dir"`    // absolute game install dir (install target)
	Extra      map[string]string `json:"extra,omitempty"`
}

// Strategy detects installs of one game.
type Strategy interface {
	Name() string
	Detect(game GameDef) ([]Result, error) // usually 0 or 1 result
}

// Registry maps strategy names to implementations.
type Registry struct {
	strategies map[string]Strategy
}

// NewRegistry returns a registry containing the given strategies.
func NewRegistry(strategies ...Strategy) *Registry {
	r := &Registry{strategies: map[string]Strategy{}}
	for _, s := range strategies {
		r.strategies[s.Name()] = s
	}
	return r
}

// FilesFor returns the manifest file list used by the given build, or "" if
// the build is unknown.
func (g GameDef) FilesFor(buildID string) string {
	for _, build := range g.Builds {
		if build.ID == buildID {
			return build.Files
		}
	}
	return ""
}

// Detect runs detection for every game that has at least one pack
// (hasPacks[game.ID]). Games with an unknown strategy are skipped and logged.
// Strategy errors are logged and never fatal. Results keep games.json order.
func (r *Registry) Detect(games []GameDef, hasPacks map[string]bool) []Result {
	var out []Result
	seen := map[string]bool{}
	for _, game := range games {
		if !hasPacks[game.ID] {
			slog.Debug("skip game without packs", "game_id", game.ID)
			continue
		}
		strategy, ok := r.strategies[game.Strategy]
		if !ok {
			slog.Warn("skip game with unknown strategy", "game_id", game.ID, "strategy", game.Strategy)
			continue
		}
		results, err := strategy.Detect(game)
		if err != nil {
			slog.Warn("detect game", "game_id", game.ID, "err", err)
		}
		for _, result := range results {
			key := game.ID + "\x00" + PathKey(result.RootDir)
			if seen[key] {
				continue
			}
			seen[key] = true
			slog.Debug("game detected", "game_id", result.GameID, "build_id", result.BuildID, "path", result.RootDir)
			out = append(out, result)
		}
	}
	return out
}

// SelectBuild evaluates builds for goos in array order and returns the first
// one whose anchor exists under rootDir, together with the absolute anchor path.
func SelectBuild(builds []BuildDef, goos, rootDir string) (BuildDef, string, bool) {
	for _, build := range builds {
		if build.OS != goos || build.Anchor == "" {
			continue
		}
		anchor := filepath.Join(rootDir, filepath.FromSlash(build.Anchor))
		if info, err := os.Stat(anchor); err == nil && !info.IsDir() {
			return build, anchor, true
		}
	}
	return BuildDef{}, "", false
}

// PathKey returns a comparison key for a path: cleaned, and lower-cased on
// Windows where the file system is case-insensitive.
func PathKey(p string) string {
	return fsutil.FoldCase(filepath.Clean(p))
}
