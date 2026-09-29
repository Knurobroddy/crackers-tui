package functional_test

import (
	"context"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/detect/steam"
	"github.com/Knurobroddy/crackers-tui/internal/detect/userfolder"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/servers"
	"github.com/Knurobroddy/crackers-tui/internal/testsupport/fakeworld"
)

const (
	testAppVersion   = "0.1.0"
	testPackID       = "valheim-test"
	testServerPackID = "valheim-server-test"
	testGameID       = "valheim"
	testServerID     = "valheim-server"
)

// stack wires real components against a fake world. Scenario tests use only
// its methods, so its internals can move to the app layer without touching them.
type stack struct {
	t         *testing.T
	app       *app.App
	gameID    string // the detected game or server the stack works on
	packID    string
	library   *app.Library
	game      app.Game
	pack      remote.PackRef
	detection app.Detection
}

// newStack works on the Steam game of w.
func newStack(t *testing.T, w *fakeworld.World, goos string) *stack {
	t.Helper()
	s := &stack{t: t, app: newApp(t, w, goos), gameID: testGameID, packID: testPackID}
	s.detect()
	return s
}

// newServerStack saves w's server folder and works on that server.
func newServerStack(t *testing.T, w *fakeworld.World, goos string) *stack {
	t.Helper()
	s := &stack{t: t, app: newApp(t, w, goos), gameID: testServerID, packID: testServerPackID}
	s.loadLibrary()
	if _, err := s.app.AddServer(s.library, w.ServerRoot); err != nil {
		t.Fatal(err)
	}
	s.detect()
	return s
}

func newApp(t *testing.T, w *fakeworld.World, goos string) *app.App {
	t.Helper()
	client, err := remote.NewClient(w.LibraryURL, testAppVersion)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(client, testAppVersion, engine.OSFiles{})
	serverStore := servers.New(w.ServersFile)
	serverFolders := userfolder.New(serverStore, goos)
	return app.New(app.Deps{
		Catalog:       client,
		Detector:      detect.NewRegistry(steam.New([]string{w.SteamRoot}, goos), serverFolders),
		Installer:     eng,
		Status:        eng,
		Servers:       serverStore,
		ServerMatcher: serverFolders,
	})
}

func (s *stack) loadLibrary() {
	s.t.Helper()
	library, err := s.app.LoadLibrary(context.Background())
	if err != nil {
		s.t.Fatal(err)
	}
	s.library = library
}

func (s *stack) detect() {
	s.t.Helper()
	s.loadLibrary()
	s.detection = s.app.Detect(context.Background(), s.library)
	var matches []app.Game
	for _, game := range s.detection.Games {
		if game.Def.ID == s.gameID {
			matches = append(matches, game)
		}
	}
	if len(matches) != 1 {
		s.t.Fatalf("detected %d installs of %s, want 1: %+v", len(matches), s.gameID, s.detection.Games)
	}
	s.game = matches[0]
	s.pack, _ = s.library.Index.Pack(s.packID)
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
