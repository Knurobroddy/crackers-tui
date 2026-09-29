// Package app implements the use cases the UI offers: loading the remote
// library, detecting installed games and saved servers, saving server
// folders, running install/remove operations, and checking for app updates.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

// statusTimeout bounds each game's status check during DetectGames.
const statusTimeout = time.Minute

// Catalog loads the remote library.
type Catalog interface {
	FetchGames(ctx context.Context) (*remote.Games, error)
	FetchIndex(ctx context.Context) (*remote.Index, error)
}

// Detector finds installed games.
type Detector interface {
	Detect(games []detect.GameDef, hasPacks map[string]bool) []detect.Result
}

// Installer changes a game directory.
type Installer interface {
	Install(ctx context.Context, req engine.InstallRequest, progress engine.ProgressFunc) error
	Remove(ctx context.Context, root string, progress engine.ProgressFunc) error
	CleanLeftovers(ctx context.Context, req engine.InstallRequest, progress engine.ProgressFunc) ([]string, error)
}

// StatusReader reports what is installed in a game directory.
type StatusReader interface {
	Status(ctx context.Context, root string, index *remote.Index) engine.Status
}

// Updater checks for and applies app updates.
type Updater interface {
	Check(ctx context.Context) (*update.Release, error)
	Apply(ctx context.Context, release *update.Release) error
}

// ServerStore keeps the server folders the player saved.
type ServerStore interface {
	Folders() ([]string, error)
	Add(dir string) error
	Forget(dir string) error
}

// ServerMatcher checks whether a folder holds a build of a server.
type ServerMatcher interface {
	Match(game detect.GameDef, dir string) (detect.Result, bool)
}

// Deps are the collaborators of App. Updater may be nil (updates disabled);
// Servers and ServerMatcher may be nil (servers cannot be saved).
type Deps struct {
	Catalog       Catalog
	Detector      Detector
	Installer     Installer
	Status        StatusReader
	Updater       Updater
	Servers       ServerStore
	ServerMatcher ServerMatcher
}

// App runs the use cases the UI offers.
type App struct{ deps Deps }

// New returns an App using deps.
func New(deps Deps) *App {
	return &App{deps: deps}
}

// Library is the loaded remote library.
type Library struct {
	Games *remote.Games
	Index *remote.Index
}

// Game is one detected install of a game or server with its status.
type Game struct {
	Install  detect.Result
	Def      detect.GameDef
	FilesKey string
	Status   engine.Status
}

// Detection is what Detect found.
type Detection struct {
	Games          []Game   // detected games and servers, in games.json order
	MissingServers []string // saved server folders that hold no supported server
	ServersErr     error    // the saved server folders could not be read
}

// LoadLibrary fetches the games list, then the pack index.
func (a *App) LoadLibrary(ctx context.Context) (*Library, error) {
	games, err := a.deps.Catalog.FetchGames(ctx)
	if err != nil {
		return nil, fmt.Errorf("load games: %w", err)
	}
	index, err := a.deps.Catalog.FetchIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}
	return &Library{Games: games, Index: index}, nil
}

// Detect finds installed games and saved servers from library, reads each
// one's status, and lists the saved server folders where nothing was found.
func (a *App) Detect(ctx context.Context, library *Library) Detection {
	results := a.deps.Detector.Detect(library.Games.Games, library.Index.GamesWithPacks())
	games := make([]Game, 0, len(results))
	for _, result := range results {
		games = append(games, a.gameFor(ctx, result, library))
	}
	missing, err := a.missingServers(games)
	if err != nil {
		slog.Warn("read saved servers", "err", err)
	}
	return Detection{Games: games, MissingServers: missing, ServersErr: err}
}

// UpdatesEnabled reports whether an Updater was configured.
func (a *App) UpdatesEnabled() bool {
	return a.deps.Updater != nil
}

// CheckUpdate reports the latest release, or nil, nil when disabled.
func (a *App) CheckUpdate(ctx context.Context) (*update.Release, error) {
	if a.deps.Updater == nil {
		return nil, nil
	}
	return a.deps.Updater.Check(ctx)
}

// ApplyUpdate installs release, replacing the running executable. It fails
// when updates are disabled, so the caller never reports a skipped update as
// done.
func (a *App) ApplyUpdate(ctx context.Context, release *update.Release) error {
	if a.deps.Updater == nil {
		return errors.New("updates are disabled")
	}
	return a.deps.Updater.Apply(ctx, release)
}

// gameFor detects the GameDef and reads the status for one detected result.
func (a *App) gameFor(ctx context.Context, result detect.Result, library *Library) Game {
	def, _ := library.Games.Game(result.GameID)
	statusCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	return Game{
		Install:  result,
		Def:      def,
		FilesKey: def.FilesFor(result.BuildID),
		Status:   a.deps.Status.Status(statusCtx, result.RootDir, library.Index),
	}
}
