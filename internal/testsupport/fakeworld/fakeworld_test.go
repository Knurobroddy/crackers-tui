package fakeworld_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/testsupport/fakeworld"
)

func TestNew_withPrefix_buildsSteamLayoutAndServesLibrary(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{WithPrefix: true})

	for _, p := range []string{
		filepath.Join(w.SteamRoot, "steamapps", "libraryfolders.vdf"),
		filepath.Join(w.Library, "steamapps", "appmanifest_892970.acf"),
		filepath.Join(w.GameRoot, "valheim.exe"),
		w.UserReg,
		filepath.Join(w.LibraryDir, "games.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	if !strings.Contains(w.Library, " ") || !strings.Contains(w.Library, "ł") {
		t.Errorf("library path %q should contain a space and a non-ASCII rune", w.Library)
	}
	if !strings.HasPrefix(w.LibraryURL, "http://127.0.0.1:") || !strings.HasSuffix(w.LibraryURL, "/") {
		t.Errorf("LibraryURL = %q", w.LibraryURL)
	}
}

func TestTree_fileAndDir_mapsDirsAndHashes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := fakeworld.Tree(t, dir)

	if got["a"] != "dir" || len(got["a/f.txt"]) != 64 {
		t.Errorf("Tree = %v", got)
	}
}
