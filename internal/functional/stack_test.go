package functional_test

import (
	"context"
	"testing"

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
	t      *testing.T
	world  *fakeworld.World
	client *remote.Client
	reg    *detect.Registry
	engine *engine.Engine
	game   detect.Result
	pack   remote.PackRef
	index  *remote.Index
}

func newStack(t *testing.T, w *fakeworld.World, goos string) *stack {
	t.Helper()
	client, err := remote.NewClient(w.LibraryURL, testAppVersion)
	if err != nil {
		t.Fatal(err)
	}
	strategy := steam.New([]string{w.SteamRoot}, goos)
	s := &stack{t: t, world: w, client: client, reg: detect.NewRegistry(strategy), engine: engine.New(client, testAppVersion, engine.OSFiles{})}
	s.detect()
	return s
}

// detect loads the library and requires exactly one detected install.
func (s *stack) detect() {
	s.t.Helper()
	ctx := context.Background()
	games, err := s.client.FetchGames(ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	if s.index, err = s.client.FetchIndex(ctx); err != nil {
		s.t.Fatal(err)
	}
	results := s.reg.Detect(games.Games, s.index.GamesWithPacks())
	if len(results) != 1 {
		s.t.Fatalf("detected %d installs, want 1: %+v", len(results), results)
	}
	s.game = results[0]
	s.pack, _ = s.index.Pack(testPackID)
}

func (s *stack) request(cleanLeftovers bool) engine.InstallRequest {
	return engine.InstallRequest{Game: s.game, FilesKey: "windows", Pack: s.pack, CleanLeftovers: cleanLeftovers}
}

func (s *stack) install() error {
	return s.engine.Install(context.Background(), s.request(false), nil)
}

func (s *stack) installCleaningLeftovers() error {
	return s.engine.Install(context.Background(), s.request(true), nil)
}

func (s *stack) remove() error {
	return s.engine.Remove(context.Background(), s.game.RootDir, nil)
}

func (s *stack) cleanLeftovers() ([]string, error) {
	return s.engine.CleanLeftovers(context.Background(), s.request(false), nil)
}

func (s *stack) state() engine.State {
	return s.engine.Status(context.Background(), s.game.RootDir, s.index).State
}
