// Package userfolder implements the "user_folder" detection strategy: the
// player names the folder, and the strategy only checks it.
package userfolder

import (
	"path/filepath"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
)

// Name is the strategy name used in games.json.
const Name = "user_folder"

// FolderLister returns the folders the player saved.
type FolderLister interface {
	Folders() ([]string, error)
}

// Strategy finds installs in the folders the player saved.
type Strategy struct {
	folders FolderLister
	goos    string // selects which builds are considered
}

// New returns a strategy that checks the folders listed by folders on every
// Detect, so folders added or forgotten while the app runs are seen.
func New(folders FolderLister, goos string) *Strategy {
	return &Strategy{folders: folders, goos: goos}
}

// Name implements detect.Strategy.
func (s *Strategy) Name() string { return Name }

// Detect implements detect.Strategy: one result per saved folder that holds a
// build of game.
func (s *Strategy) Detect(game detect.GameDef) ([]detect.Result, error) {
	folders, err := s.folders.Folders()
	if err != nil {
		return nil, err
	}
	var out []detect.Result
	for _, dir := range folders {
		if result, ok := s.Match(game, dir); ok {
			out = append(out, result)
		}
	}
	return out, nil
}

// Match reports whether dir holds a build of game for this OS.
func (s *Strategy) Match(game detect.GameDef, dir string) (detect.Result, bool) {
	root := filepath.Clean(dir)
	build, anchor, ok := detect.SelectBuild(game.Builds, s.goos, root)
	if !ok {
		return detect.Result{}, false
	}
	return detect.Result{GameID: game.ID, BuildID: build.ID, AnchorPath: anchor, RootDir: root}, true
}
