package functional_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/testsupport/fakeworld"
)

const regBackupRel = "steamapps/compatdata/892970/pfx/user.reg.modinst-bak"

var packFiles = []string{
	"winhttp.dll",
	"doorstop_config.ini",
	"BepInEx/core/BepInEx.dll",
	"BepInEx/plugins/SomeMod.dll",
	"BepInEx/config/crackers-test.cfg",
	".crackers-modinst.json",
}

func TestValheim_InstallThenRemove_eachBuild_restoresVanilla(t *testing.T) {
	for _, tc := range []struct {
		goos, build string
		withPrefix  bool
	}{
		{"windows", "windows", false},
		{"linux", "linux_proton", true},
	} {
		t.Run(tc.build, func(t *testing.T) {
			w := fakeworld.New(t, fakeworld.Options{WithPrefix: tc.withPrefix})
			s := newStack(t, w, tc.goos)
			if s.game.Install.BuildID != tc.build {
				t.Fatalf("build = %q, want %q", s.game.Install.BuildID, tc.build)
			}
			vanilla := fakeworld.Tree(t, w.Library)

			if err := s.install(); err != nil {
				t.Fatal(err)
			}
			requireFiles(t, w.GameRoot, packFiles)
			if tc.withPrefix {
				requireContains(t, w.UserReg, `"winhttp"="native,builtin"`)
			}
			if got := s.state(); got != engine.Installed {
				t.Fatalf("state = %v, want Installed", got)
			}

			if err := s.remove(); err != nil {
				t.Fatal(err)
			}
			after := fakeworld.Tree(t, w.Library)
			delete(after, regBackupRel) // kept on purpose
			if diff := cmp.Diff(vanilla, after); diff != "" {
				t.Errorf("tree after remove (-vanilla +after):\n%s", diff)
			}
		})
	}
}

func TestValheim_Reinstall_preservedUserChangedConfig_keepsUserVersion(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	manifest, err := os.ReadFile(filepath.Join(w.LibraryDir, "packs", "valheim-test.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	m["preserve"] = []string{"BepInEx/config"}
	patched, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	w.WriteLibraryFile(t, "packs/valheim-test.json", patched)

	s := newStack(t, w, "windows")
	if err := s.install(); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(w.GameRoot, "BepInEx", "config", "crackers-test.cfg")
	os.WriteFile(cfg, []byte("user edit"), 0o644)

	if err := s.install(); err != nil {
		t.Fatal(err)
	}

	requireContent(t, cfg, "user edit")
}

func TestValheim_Status_manifestChangedRemotely_updateAvailable(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	s := newStack(t, w, "windows")
	if err := s.install(); err != nil {
		t.Fatal(err)
	}
	manifest, _ := os.ReadFile(filepath.Join(w.LibraryDir, "packs", "valheim-test.json"))
	w.WriteLibraryFile(t, "packs/valheim-test.json", []byte(strings.Replace(string(manifest), "2026.09.22", "2026.09.23", 1)))

	if got := s.state(); got != engine.UpdateAvailable {
		t.Fatalf("state = %v, want UpdateAvailable", got)
	}
}

func TestValheim_Install_manualModsPresent_reportsLeftoversThenCleansOnConfirm(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	s := newStack(t, w, "windows")
	oldMod := filepath.Join(w.GameRoot, "BepInEx", "plugins", "Old.dll")
	os.MkdirAll(filepath.Dir(oldMod), 0o755)
	os.WriteFile(oldMod, []byte("old"), 0o644)

	var leftovers *engine.LeftoversError
	if err := s.install(); !errors.As(err, &leftovers) {
		t.Fatalf("err = %v, want *engine.LeftoversError", err)
	}
	if diff := cmp.Diff([]string{"BepInEx/"}, leftovers.Paths); diff != "" {
		t.Errorf("leftover paths (-want +got):\n%s", diff)
	}
	if err := s.installCleaningLeftovers(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(oldMod); !os.IsNotExist(err) {
		t.Errorf("Old.dll still exists (err = %v)", err)
	}
	requireFiles(t, w.GameRoot, packFiles)
}

func TestValheim_CleanLeftovers_noPackInstalled_removesOnlyPackPaths(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	s := newStack(t, w, "windows")
	os.MkdirAll(filepath.Join(w.GameRoot, "BepInEx", "plugins"), 0o755)
	notes := filepath.Join(w.GameRoot, "notes.txt")
	os.WriteFile(notes, []byte("mine"), 0o644)

	removed, err := s.cleanLeftovers()

	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"BepInEx/"}, removed); diff != "" {
		t.Errorf("removed (-want +got):\n%s", diff)
	}
	requireContent(t, notes, "mine")
}

func TestValheim_Install_downloadFails_leavesGameUntouched(t *testing.T) {
	for _, tc := range []struct{ name, rel string }{
		{"missing file", "files/SomeMod.dll"},
		{"wrong hash", "files/crackers-test.cfg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := fakeworld.New(t, fakeworld.Options{})
			s := newStack(t, w, "windows")
			if tc.name == "missing file" {
				w.RemoveLibraryFile(t, tc.rel)
			} else {
				w.WriteLibraryFile(t, tc.rel, []byte("tampered"))
			}
			before := fakeworld.Tree(t, w.Library)

			if err := s.install(); err == nil {
				t.Fatal("install succeeded, want error")
			}

			if diff := cmp.Diff(before, fakeworld.Tree(t, w.Library)); diff != "" {
				t.Errorf("tree changed (-before +after):\n%s", diff)
			}
		})
	}
}

func TestValheim_Remove_markerFromV010_readsAndRemoves(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	s := newStack(t, w, "windows")
	if err := s.install(); err != nil {
		t.Fatal(err)
	}
	pinned, err := os.ReadFile(filepath.Join("testdata", "marker-v0.1.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(w.GameRoot, ".crackers-modinst.json"), pinned, 0o644)

	if got := s.state(); got != engine.Installed {
		t.Fatalf("state = %v, want Installed", got)
	}
	if err := s.remove(); err != nil {
		t.Fatal(err)
	}
	requireFiles(t, w.GameRoot, []string{"valheim.exe"})
}

func requireFiles(t *testing.T, root string, rels []string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func requireContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

func requireContains(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), want) {
		t.Errorf("%s does not contain %q", path, want)
	}
}
