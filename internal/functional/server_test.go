package functional_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/servers"
	"github.com/Knurobroddy/crackers-tui/internal/testsupport/fakeworld"
)

func TestServer_InstallThenRemove_eachBuild_restoresVanilla(t *testing.T) {
	for _, tc := range []struct {
		goos, build  string
		platformFile string
	}{
		{"windows", "windows", "winhttp.dll"},
		{"linux", "linux", "start_server_bepinex.sh"},
	} {
		t.Run(tc.build, func(t *testing.T) {
			w := fakeworld.New(t, fakeworld.Options{WithServer: true})
			s := newServerStack(t, w, tc.goos)
			if s.game.Install.BuildID != tc.build {
				t.Fatalf("build = %q, want %q", s.game.Install.BuildID, tc.build)
			}
			vanilla := fakeworld.Tree(t, w.ServerRoot)
			game := fakeworld.Tree(t, w.GameRoot)

			if err := s.install(); err != nil {
				t.Fatal(err)
			}
			requireFiles(t, w.ServerRoot, []string{tc.platformFile, "BepInEx/plugins/SomeMod.dll", ".crackers-modinst.json"})
			if got := s.state(); got != engine.Installed {
				t.Fatalf("state = %v, want Installed", got)
			}
			if err := s.remove(); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(vanilla, fakeworld.Tree(t, w.ServerRoot)); diff != "" {
				t.Errorf("server after remove (-vanilla +after):\n%s", diff)
			}
			if diff := cmp.Diff(game, fakeworld.Tree(t, w.GameRoot)); diff != "" {
				t.Errorf("game folder changed (-before +after):\n%s", diff)
			}
		})
	}
}

func TestServer_Reinstall_userEditedLaunchScript_keepsUserVersion(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{WithServer: true})
	s := newServerStack(t, w, "linux")
	if err := s.install(); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(w.ServerRoot, "start_server_bepinex.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n# my world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.install(); err != nil {
		t.Fatal(err)
	}

	requireContent(t, script, "#!/bin/sh\n# my world\n")
}

func TestServer_Detect_folderEmptied_reportsMissingThenForgetLeavesFolder(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{WithServer: true})
	s := newServerStack(t, w, "windows")
	if err := os.Remove(filepath.Join(w.ServerRoot, "valheim_server.exe")); err != nil {
		t.Fatal(err)
	}
	before := fakeworld.Tree(t, w.ServerRoot)

	detection := s.app.Detect(context.Background(), s.library)
	if diff := cmp.Diff([]string{w.ServerRoot}, detection.MissingServers); diff != "" {
		t.Fatalf("missing servers (-want +got):\n%s", diff)
	}
	if err := s.app.ForgetServer(w.ServerRoot); err != nil {
		t.Fatal(err)
	}

	if got := s.app.Detect(context.Background(), s.library).MissingServers; len(got) != 0 {
		t.Errorf("missing servers after forget = %v, want none", got)
	}
	if diff := cmp.Diff(before, fakeworld.Tree(t, w.ServerRoot)); diff != "" {
		t.Errorf("forget changed the folder (-before +after):\n%s", diff)
	}
}

func TestServer_AddServer_gameFolderOrTwice_refuses(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{WithServer: true})
	s := newServerStack(t, w, "windows")

	var notServerErr *app.NotServerFolderError
	if _, err := s.app.AddServer(s.library, w.GameRoot); !errors.As(err, &notServerErr) {
		t.Errorf("adding the game folder: err = %v, want *app.NotServerFolderError", err)
	}
	if _, err := s.app.AddServer(s.library, w.ServerRoot); !errors.Is(err, servers.ErrAlreadySaved) {
		t.Errorf("adding the server twice: err = %v, want ErrAlreadySaved", err)
	}
}

func TestServer_savedServersUnreadable_gamesStillDetectedAndFileKept(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{WithServer: true})
	broken := []byte("{broken")
	if err := os.WriteFile(w.ServersFile, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	s := newStack(t, w, "windows")

	if s.detection.ServersErr == nil {
		t.Error("no servers error reported")
	}
	if _, err := s.app.AddServer(s.library, w.ServerRoot); err == nil {
		t.Error("AddServer succeeded with an unreadable file")
	}
	requireContent(t, w.ServersFile, string(broken))
}
