package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// OpKind is the kind of operation Run performs.
type OpKind int

// The kinds of operations Run performs.
const (
	OpInstall OpKind = iota
	OpReinstall
	OpRemove
	OpCleanLeftovers
)

// String returns the lowercase name shown to the player.
func (k OpKind) String() string {
	switch k {
	case OpInstall:
		return "install"
	case OpReinstall:
		return "reinstall"
	case OpRemove:
		return "remove"
	case OpCleanLeftovers:
		return "clean leftovers"
	default:
		return "unknown"
	}
}

// Operation is one install/remove use case to run.
type Operation struct {
	Kind           OpKind
	Game           Game
	Pack           remote.PackRef
	CleanLeftovers bool // install: delete leftovers first (user confirmed)
}

// Outcome is the result of a completed Operation.
type Outcome struct {
	Removed []string // OpCleanLeftovers: what was deleted
}

// Run performs op and logs its start and result.
func (a *App) Run(ctx context.Context, op Operation, progress engine.ProgressFunc) (Outcome, error) {
	log := slog.With("op", op.Kind.String(), "game_id", op.Game.Install.GameID, "path", op.Game.Install.RootDir, "pack_id", op.Pack.ID)
	log.Info("operation started")
	outcome, err := a.run(ctx, op, progress)
	if err != nil {
		log.Error("operation failed", "err", err)
		return outcome, err
	}
	log.Info("operation finished")
	return outcome, nil
}

func (a *App) run(ctx context.Context, op Operation, progress engine.ProgressFunc) (Outcome, error) {
	switch op.Kind {
	case OpInstall, OpReinstall:
		return Outcome{}, a.deps.Installer.Install(ctx, op.request(), progress)
	case OpRemove:
		return Outcome{}, a.deps.Installer.Remove(ctx, op.Game.Install.RootDir, progress)
	case OpCleanLeftovers:
		removed, err := a.deps.Installer.CleanLeftovers(ctx, op.request(), progress)
		return Outcome{Removed: removed}, err
	}
	return Outcome{}, fmt.Errorf("unknown operation %d", op.Kind)
}

func (op Operation) request() engine.InstallRequest {
	return engine.InstallRequest{Game: op.Game.Install, FilesKey: op.Game.FilesKey, Pack: op.Pack, CleanLeftovers: op.CleanLeftovers}
}
