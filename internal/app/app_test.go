package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

var testGames = &remote.Games{Games: []detect.GameDef{{
	ID: "valheim", Name: "Valheim",
	Builds: []detect.BuildDef{{ID: "linux_proton", OS: "linux", Anchor: "valheim.exe", Files: "windows"}},
}}}

var testIndex = &remote.Index{Packs: []remote.PackRef{{ID: "p", GameID: "valheim", Name: "P"}}}

// serverLibrary has a game, a server with a pack and a server without one.
var serverLibrary = &app.Library{
	Games: &remote.Games{Games: []detect.GameDef{
		testGames.Games[0],
		{ID: "valheim-server", Name: "Valheim Dedicated Server", Kind: detect.KindServer},
		{ID: "other-server", Name: "Other Server", Kind: detect.KindServer},
	}},
	Index: &remote.Index{Packs: []remote.PackRef{
		{ID: "p", GameID: "valheim", Name: "P"},
		{ID: "s", GameID: "valheim-server", Name: "S"},
	}},
}

func TestApp_LoadLibrary_success_returnsGamesAndIndex(t *testing.T) {
	a := app.New(app.Deps{Catalog: fakeCatalog{games: testGames, index: testIndex}})

	got, err := a.LoadLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := &app.Library{Games: testGames, Index: testIndex}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("library (-want +got):\n%s", diff)
	}
}

func TestApp_LoadLibrary_catalogError_wrapsLoadGames(t *testing.T) {
	a := app.New(app.Deps{Catalog: fakeCatalog{err: errors.New("boom")}})

	_, err := a.LoadLibrary(context.Background())

	if err == nil || !strings.Contains(err.Error(), "load games: boom") {
		t.Fatalf("err = %v, want it to contain %q", err, "load games: boom")
	}
}

func TestApp_Detect_oneInstall_returnsGameWithFilesKeyAndStatus(t *testing.T) {
	result := detect.Result{GameID: "valheim", BuildID: "linux_proton", RootDir: "/g"}
	a := app.New(app.Deps{Detector: fakeDetector{results: []detect.Result{result}}, Status: fakeStatus{state: engine.Installed}})

	got := a.Detect(context.Background(), &app.Library{Games: testGames, Index: testIndex})

	want := app.Detection{Games: []app.Game{{Install: result, Def: testGames.Games[0], FilesKey: "windows", Status: engine.Status{State: engine.Installed}}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("detection (-want +got):\n%s", diff)
	}
}

func TestApp_Detect_savedServerFolderEmpty_reportsItMissing(t *testing.T) {
	found := detect.Result{GameID: "valheim-server", BuildID: "windows", RootDir: "/srv/a"}
	a := app.New(app.Deps{
		Detector: fakeDetector{results: []detect.Result{found}},
		Status:   fakeStatus{state: engine.NotInstalled},
		Servers:  &fakeServers{folders: []string{"/srv/a", "/srv/gone"}},
	})

	got := a.Detect(context.Background(), serverLibrary)

	if diff := cmp.Diff([]string{"/srv/gone"}, got.MissingServers); diff != "" {
		t.Errorf("missing servers (-want +got):\n%s", diff)
	}
	if len(got.Games) != 1 || !got.Games[0].Def.IsServer() {
		t.Errorf("games = %+v, want the one server", got.Games)
	}
}

func TestApp_Detect_savedServersUnreadable_reportsErrorAndKeepsGames(t *testing.T) {
	a := app.New(app.Deps{
		Detector: fakeDetector{results: []detect.Result{{GameID: "valheim", RootDir: "/g"}}},
		Status:   fakeStatus{},
		Servers:  &fakeServers{err: errors.New("broken")},
	})

	got := a.Detect(context.Background(), serverLibrary)

	if got.ServersErr == nil || len(got.Games) != 1 {
		t.Fatalf("detection = %+v, want the game and a servers error", got)
	}
}

func TestApp_AddServer_enteredFolder_savesAbsolutePathOnlyWhenServerFound(t *testing.T) {
	serverDir, err := filepath.Abs("srv")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		entered string
		matches bool
		wantErr bool
	}{
		{"server found", "srv", true, false},
		{"quoted and padded", ` "` + serverDir + `" `, true, false},
		{"no server", "srv", false, true},
		{"nothing entered", "  ", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeServers{}
			a := app.New(app.Deps{Servers: store, ServerMatcher: fakeMatcher{dirs: map[string]bool{serverDir: tc.matches}}})

			saved, err := a.AddServer(serverLibrary, tc.entered)

			if tc.wantErr {
				if err == nil || len(store.added) != 0 {
					t.Fatalf("err = %v, added = %v, want an error and nothing saved", err, store.added)
				}
				return
			}
			if err != nil || saved != serverDir {
				t.Fatalf("saved = %q, err = %v, want %q", saved, err, serverDir)
			}
			if diff := cmp.Diff([]string{serverDir}, store.added); diff != "" {
				t.Errorf("added (-want +got):\n%s", diff)
			}
		})
	}
}

func TestApp_AddServer_noServer_namesSupportedServers(t *testing.T) {
	a := app.New(app.Deps{Servers: &fakeServers{}, ServerMatcher: fakeMatcher{}})

	_, err := a.AddServer(serverLibrary, "/nothing")

	var notServerErr *app.NotServerFolderError
	if !errors.As(err, &notServerErr) {
		t.Fatalf("err = %v, want *NotServerFolderError", err)
	}
	if diff := cmp.Diff([]string{"Valheim Dedicated Server"}, notServerErr.Supported); diff != "" {
		t.Errorf("supported (-want +got):\n%s", diff)
	}
}

func TestApp_Run_installWithConfirmedLeftovers_passesCleanFlagAndFilesKey(t *testing.T) {
	installer := &fakeInstaller{}
	a := app.New(app.Deps{Installer: installer})
	game := app.Game{Install: detect.Result{GameID: "valheim", RootDir: "/g"}, FilesKey: "windows"}

	_, err := a.Run(context.Background(), app.Operation{Kind: app.OpInstall, Game: game, Pack: testIndex.Packs[0], CleanLeftovers: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.InstallRequest{Game: game.Install, FilesKey: "windows", Pack: testIndex.Packs[0], CleanLeftovers: true}
	if diff := cmp.Diff(want, installer.gotRequest); diff != "" {
		t.Errorf("request (-want +got):\n%s", diff)
	}
}

func TestApp_Run_remove_callsRemoveWithRoot(t *testing.T) {
	installer := &fakeInstaller{}
	a := app.New(app.Deps{Installer: installer})

	_, err := a.Run(context.Background(), app.Operation{Kind: app.OpRemove, Game: app.Game{Install: detect.Result{RootDir: "/g"}}}, nil)

	if err != nil || installer.gotRoot != "/g" {
		t.Fatalf("err = %v, root = %q", err, installer.gotRoot)
	}
}

func TestApp_Run_cleanLeftovers_returnsRemovedPaths(t *testing.T) {
	a := app.New(app.Deps{Installer: &fakeInstaller{removed: []string{"BepInEx/"}}})

	outcome, err := a.Run(context.Background(), app.Operation{Kind: app.OpCleanLeftovers}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"BepInEx/"}, outcome.Removed); diff != "" {
		t.Errorf("removed (-want +got):\n%s", diff)
	}
}

func TestApp_ApplyUpdate_noUpdater_returnsError(t *testing.T) {
	a := app.New(app.Deps{})

	if err := a.ApplyUpdate(context.Background(), &update.Release{Version: "9.0.0"}); err == nil {
		t.Fatal("ApplyUpdate succeeded without an updater, want error")
	}
}

func TestApp_CheckUpdate_noUpdater_returnsNil(t *testing.T) {
	a := app.New(app.Deps{})

	release, err := a.CheckUpdate(context.Background())

	if release != nil || err != nil || a.UpdatesEnabled() {
		t.Fatalf("release = %v, err = %v, enabled = %v", release, err, a.UpdatesEnabled())
	}
}
