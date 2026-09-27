package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

var testGames = &remote.Games{Games: []detect.GameDef{{
	ID: "valheim", Name: "Valheim",
	Builds: []detect.BuildDef{{ID: "linux_proton", OS: "linux", Anchor: "valheim.exe", Files: "windows"}},
}}}

var testIndex = &remote.Index{Packs: []remote.PackRef{{ID: "p", GameID: "valheim", Name: "P"}}}

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

func TestApp_DetectGames_oneInstall_returnsGameWithFilesKeyAndStatus(t *testing.T) {
	result := detect.Result{GameID: "valheim", BuildID: "linux_proton", RootDir: "/g"}
	a := app.New(app.Deps{Detector: fakeDetector{results: []detect.Result{result}}, Status: fakeStatus{state: engine.Installed}})

	got := a.DetectGames(context.Background(), &app.Library{Games: testGames, Index: testIndex})

	want := []app.Game{{Install: result, Def: testGames.Games[0], FilesKey: "windows", Status: engine.Status{State: engine.Installed}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("games (-want +got):\n%s", diff)
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

func TestApp_CheckUpdate_noUpdater_returnsNil(t *testing.T) {
	a := app.New(app.Deps{})

	release, err := a.CheckUpdate(context.Background())

	if release != nil || err != nil || a.UpdatesEnabled() {
		t.Fatalf("release = %v, err = %v, enabled = %v", release, err, a.UpdatesEnabled())
	}
}
