package userfolder_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/detect/userfolder"
)

var serverDef = detect.GameDef{
	ID: "valheim-server", Kind: detect.KindServer, Strategy: userfolder.Name,
	Builds: []detect.BuildDef{
		{ID: "windows", OS: "windows", Anchor: "valheim_server.exe", Files: "windows"},
		{ID: "linux", OS: "linux", Anchor: "valheim_server.x86_64", Files: "linux"},
	},
}

type fakeFolders struct {
	folders []string
	err     error
}

func (f fakeFolders) Folders() ([]string, error) { return f.folders, f.err }

func TestStrategy_Detect_savedFolders_returnsOnlyFoldersWithAnchorForOS(t *testing.T) {
	windowsServer := serverFolder(t, "valheim_server.exe")
	linuxServer := serverFolder(t, "valheim_server.x86_64")
	empty := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	for _, tc := range []struct {
		goos string
		want []detect.Result
	}{
		{"windows", []detect.Result{{
			GameID: "valheim-server", BuildID: "windows", RootDir: windowsServer,
			AnchorPath: filepath.Join(windowsServer, "valheim_server.exe"),
		}}},
		{"linux", []detect.Result{{
			GameID: "valheim-server", BuildID: "linux", RootDir: linuxServer,
			AnchorPath: filepath.Join(linuxServer, "valheim_server.x86_64"),
		}}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			strategy := userfolder.New(fakeFolders{folders: []string{windowsServer, linuxServer, empty, missing}}, tc.goos)

			got, err := strategy.Detect(serverDef)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("results (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStrategy_Detect_listFails_returnsError(t *testing.T) {
	strategy := userfolder.New(fakeFolders{err: errors.New("broken")}, "windows")

	if _, err := strategy.Detect(serverDef); err == nil {
		t.Fatal("Detect succeeded, want error")
	}
}

func serverFolder(t *testing.T, anchor string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, anchor), []byte("server"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
