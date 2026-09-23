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
	"github.com/Knurobroddy/crackers-tui/internal/logx"
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

// Extra keys set by the steam strategy.
const (
	ExtraSteamLibrary   = "steam_library"
	ExtraSteamAppID     = "steam_appid"
	ExtraSteamLibraries = "steam_libraries" // all known libraries, joined with os.PathListSeparator
)

// Strategy detects installs of one game.
type Strategy interface {
	Name() string
	Detect(game GameDef) ([]Result, error) // usually 0 or 1 result
}

// Registry maps strategy names to implementations.
type Registry struct {
	strategies map[string]Strategy
	log        *slog.Logger
}

// NewRegistry returns a registry containing the given strategies.
func NewRegistry(log *slog.Logger, strategies ...Strategy) *Registry {
	log = logx.OrDiscard(log)
	r := &Registry{strategies: map[string]Strategy{}, log: log}
	for _, s := range strategies {
		r.strategies[s.Name()] = s
	}
	return r
}

// Detect runs detection for every game that has at least one pack
// (hasPacks[game.ID]). Games with an unknown strategy are skipped and logged.
// Strategy errors are logged and never fatal. Results keep games.json order.
func (r *Registry) Detect(games []GameDef, hasPacks map[string]bool) []Result {
	var out []Result
	seen := map[string]bool{}
	for _, g := range games {
		if !hasPacks[g.ID] {
			r.log.Info("skipping game without packs", "game", g.ID)
			continue
		}
		s, ok := r.strategies[g.Strategy]
		if !ok {
			r.log.Warn("unknown detection strategy; app too old for this game", "game", g.ID, "strategy", g.Strategy)
			continue
		}
		results, err := s.Detect(g)
		if err != nil {
			r.log.Error("detection failed", "game", g.ID, "err", err)
		}
		for _, res := range results {
			key := g.ID + "\x00" + PathKey(res.RootDir)
			if seen[key] {
				continue
			}
			seen[key] = true
			r.log.Info("detected game", "game", res.GameID, "build", res.BuildID, "root", res.RootDir)
			out = append(out, res)
		}
	}
	return out
}

// SelectBuild evaluates builds for goos in array order and returns the first
// one whose anchor exists under rootDir, together with the absolute anchor path.
func SelectBuild(builds []BuildDef, goos, rootDir string) (BuildDef, string, bool) {
	for _, b := range builds {
		if b.OS != goos || b.Anchor == "" {
			continue
		}
		anchor := filepath.Join(rootDir, filepath.FromSlash(b.Anchor))
		if fi, err := os.Stat(anchor); err == nil && !fi.IsDir() {
			return b, anchor, true
		}
	}
	return BuildDef{}, "", false
}

// PathKey returns a comparison key for a path: cleaned, and lower-cased on
// Windows where the file system is case-insensitive.
func PathKey(p string) string {
	return fsutil.FoldCase(filepath.Clean(p))
}
