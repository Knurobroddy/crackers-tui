package app_test

import (
	"context"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/detect/userfolder"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/servers"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

// Compile-time assertions that the real collaborators satisfy the
// interfaces app declares.
var (
	_ app.Catalog       = (*remote.Client)(nil)
	_ app.Detector      = (*detect.Registry)(nil)
	_ app.Installer     = (*engine.Engine)(nil)
	_ app.StatusReader  = (*engine.Engine)(nil)
	_ app.Updater       = (*update.Updater)(nil)
	_ app.ServerStore   = (*servers.Store)(nil)
	_ app.ServerMatcher = (*userfolder.Strategy)(nil)
)

type fakeServers struct {
	folders []string
	err     error
	added   []string
}

func (f *fakeServers) Folders() ([]string, error) { return f.folders, f.err }

func (f *fakeServers) Add(dir string) error {
	f.added = append(f.added, dir)
	return f.err
}

func (f *fakeServers) Forget(string) error { return f.err }

// fakeMatcher matches the folders in dirs, for every game.
type fakeMatcher struct{ dirs map[string]bool }

func (f fakeMatcher) Match(game detect.GameDef, dir string) (detect.Result, bool) {
	return detect.Result{GameID: game.ID, RootDir: dir}, f.dirs[dir]
}

type fakeCatalog struct {
	games *remote.Games
	index *remote.Index
	err   error
}

func (f fakeCatalog) FetchGames(context.Context) (*remote.Games, error) { return f.games, f.err }
func (f fakeCatalog) FetchIndex(context.Context) (*remote.Index, error) { return f.index, f.err }

type fakeDetector struct{ results []detect.Result }

func (f fakeDetector) Detect([]detect.GameDef, map[string]bool) []detect.Result { return f.results }

type fakeInstaller struct {
	gotRequest engine.InstallRequest
	gotRoot    string
	removed    []string
	err        error
}

func (f *fakeInstaller) Install(_ context.Context, req engine.InstallRequest, _ engine.ProgressFunc) error {
	f.gotRequest = req
	return f.err
}

func (f *fakeInstaller) Remove(_ context.Context, root string, _ engine.ProgressFunc) error {
	f.gotRoot = root
	return f.err
}

func (f *fakeInstaller) CleanLeftovers(_ context.Context, req engine.InstallRequest, _ engine.ProgressFunc) ([]string, error) {
	f.gotRequest = req
	return f.removed, f.err
}

type fakeStatus struct{ state engine.State }

func (f fakeStatus) Status(context.Context, string, *remote.Index) engine.Status {
	return engine.Status{State: f.state}
}
