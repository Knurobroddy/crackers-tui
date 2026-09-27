package functional_test

import (
	"context"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/detect/steam"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/testsupport/fakeworld"
)

const (
	testAppVersion = "0.1.0"
	testPackID     = "valheim-test"
)

// stack wires real components against a fake world. Scenario tests use only
// its methods, so its internals can move to the app layer without touching them.
type stack struct {
	t       *testing.T
	app     *app.App
	library *app.Library
	game    app.Game
	pack    remote.PackRef
}

func newStack(t *testing.T, w *fakeworld.World, goos string) *stack {
	t.Helper()
	client, err := remote.NewClient(w.LibraryURL, testAppVersion)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(client, testAppVersion, engine.OSFiles{})
	application := app.New(app.Deps{
		Catalog:   client,
		Detector:  detect.NewRegistry(steam.New([]string{w.SteamRoot}, goos)),
		Installer: eng,
		Status:    eng,
	})
	s := &stack{t: t, app: application}
	s.detect()
	return s
}

func (s *stack) detect() {
	s.t.Helper()
	ctx := context.Background()
	library, err := s.app.LoadLibrary(ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	games := s.app.DetectGames(ctx, library)
	if len(games) != 1 {
		s.t.Fatalf("detected %d installs, want 1: %+v", len(games), games)
	}
	s.library, s.game = library, games[0]
	s.pack, _ = library.Index.Pack(testPackID)
}

func (s *stack) operation(kind app.OpKind) app.Operation {
	return app.Operation{Kind: kind, Game: s.game, Pack: s.pack}
}

func (s *stack) run(op app.Operation) (app.Outcome, error) {
	return s.app.Run(context.Background(), op, nil)
}

func (s *stack) install() error {
	_, err := s.run(s.operation(app.OpInstall))
	return err
}

func (s *stack) installCleaningLeftovers() error {
	op := s.operation(app.OpInstall)
	op.CleanLeftovers = true
	_, err := s.run(op)
	return err
}

func (s *stack) remove() error {
	_, err := s.run(s.operation(app.OpRemove))
	return err
}

func (s *stack) cleanLeftovers() ([]string, error) {
	outcome, err := s.run(s.operation(app.OpCleanLeftovers))
	return outcome.Removed, err
}

func (s *stack) state() engine.State {
	s.detect()
	return s.game.Status.State
}
