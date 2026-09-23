package detect

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelectBuildOrder(t *testing.T) {
	builds := []BuildDef{
		{ID: "win", OS: "windows", Anchor: "game.exe"},
		{ID: "proton", OS: "linux", Anchor: "game.exe"},
		{ID: "native", OS: "linux", Anchor: "bin/game.x86_64"},
	}
	root := t.TempDir()

	if _, _, ok := SelectBuild(builds, "linux", root); ok {
		t.Error("empty dir matched a build")
	}

	touch(t, root, "bin/game.x86_64")
	if b, anchor, ok := SelectBuild(builds, "linux", root); !ok || b.ID != "native" || anchor != filepath.Join(root, "bin", "game.x86_64") {
		t.Errorf("got %v %q %v, want native", b.ID, anchor, ok)
	}

	touch(t, root, "game.exe") // both linux anchors exist: array order wins
	if b, _, ok := SelectBuild(builds, "linux", root); !ok || b.ID != "proton" {
		t.Errorf("got %v, want proton (first in array order)", b.ID)
	}
	if b, _, ok := SelectBuild(builds, "windows", root); !ok || b.ID != "win" {
		t.Errorf("got %v, want win", b.ID)
	}
	if _, _, ok := SelectBuild(builds, "darwin", root); ok {
		t.Error("darwin matched")
	}
}

func TestSelectBuildIgnoresDirectoryAnchor(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "game.exe"), 0o755)
	if _, _, ok := SelectBuild([]BuildDef{{ID: "w", OS: "windows", Anchor: "game.exe"}}, "windows", root); ok {
		t.Error("directory accepted as anchor")
	}
}

type fakeStrategy struct {
	results []Result
	err     error
}

func (f fakeStrategy) Name() string                     { return "fake" }
func (f fakeStrategy) Detect(GameDef) ([]Result, error) { return f.results, f.err }

func TestRegistryDetect(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(nil, fakeStrategy{results: []Result{
		{GameID: "a", BuildID: "b", RootDir: root},
		{GameID: "a", BuildID: "b", RootDir: root + string(filepath.Separator)}, // duplicate
	}, err: errors.New("one library broken")})
	games := []GameDef{
		{ID: "a", Strategy: "fake"},
		{ID: "nopacks", Strategy: "fake"},
		{ID: "future", Strategy: "minecraft"},
	}
	got := reg.Detect(games, map[string]bool{"a": true, "future": true})
	if len(got) != 1 || got[0].GameID != "a" {
		t.Errorf("got %+v, want one result for game a", got)
	}
}
