package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

// otherPack publishes a second pack for the same game that owns BepInEx.
func otherPack(r *fakeRemote, extra ...map[string]any) map[string]any {
	other := []byte("other pack")
	r.put("files/other.dll", other)
	files := []any{entry("files/other.dll", other, "file", "BepInEx/plugins/other.dll", "")}
	for _, e := range extra {
		files = append(files, e)
	}
	return map[string]any{
		"schema_version": 1, "id": "valheim-other", "game_id": "valheim", "name": "Other", "version": "1",
		"owned_dirs": []string{"BepInEx"},
		"files":      map[string]any{"common": files},
	}
}

func assertNoStaging(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, config.StagingDirName)); !os.IsNotExist(err) {
		t.Errorf("staging folder left behind (err = %v)", err)
	}
}

func TestEngine_Install_reinstallOrSwitchFails_keepsOldPack(t *testing.T) {
	markerWrite := faultyFiles{op: "rename", pathMatch: config.MarkerFileName + config.TmpSuffix, nth: 1}
	for _, tc := range []struct {
		name       string
		switchPack bool
		files      faultyFiles
		hooksLost  bool // the old pack's game settings were already undone
	}{
		{"reinstall-file-1", false, faultyFiles{op: "rename", pathMatch: config.TmpSuffix, nth: 1}, false},
		{"reinstall-file-4", false, faultyFiles{op: "rename", pathMatch: config.TmpSuffix, nth: 4}, false},
		{"reinstall-marker", false, markerWrite, true},
		{"switch-file-1", true, faultyFiles{op: "rename", pathMatch: config.TmpSuffix, nth: 1}, false},
		{"switch-marker", true, markerWrite, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRemote(t)
			packA := r.publish(t, packID, defaultManifest(t, r))
			packB := r.publish(t, "valheim-other", otherPack(r))
			g := newFakeGame(t, true)
			e := New(r.client(), "0.1.0", OSFiles{})
			ctx := context.Background()
			if err := e.Install(ctx, g.request("linux_proton", packA), nil); err != nil {
				t.Fatal(err)
			}
			installed := snapshot(t, g.lib)
			regInstalled, _ := os.ReadFile(g.reg)

			next := packA
			if tc.switchPack {
				next = packB
			}
			err := New(r.client(), "0.1.0", &tc.files).Install(ctx, g.request("linux_proton", next), nil)
			if !errors.Is(err, errInjected) {
				t.Fatalf("err = %v, want injected failure", err)
			}
			if got := errors.Is(err, ErrPreviousHooksUndone); got != tc.hooksLost {
				t.Errorf("errors.Is(err, ErrPreviousHooksUndone) = %v, want %v: %v", got, tc.hooksLost, err)
			}
			after := snapshot(t, g.lib)
			if tc.hooksLost { // user.reg and its backup are compared by the hook tests
				for _, m := range []map[string]string{installed, after} {
					delete(m, "steamapps/compatdata/892970/pfx/user.reg")
					delete(m, "steamapps/compatdata/892970/pfx/user.reg.modinst-bak")
				}
			} else if regAfter, _ := os.ReadFile(g.reg); string(regAfter) != string(regInstalled) {
				t.Errorf("user.reg changed:\n%s", regAfter)
			}
			assertSameTree(t, installed, after)
			if mk, err := ReadMarker(g.root); err != nil || mk.PackID != packID {
				t.Errorf("marker = %+v, %v", mk, err)
			}
		})
	}
}

func TestEngine_Install_switchWithLeftovers_keepsOldPackUntilConfirmed(t *testing.T) {
	r := newFakeRemote(t)
	packA := r.publish(t, packID, defaultManifest(t, r))
	extra := []byte("pack B's extra")
	r.put("files/extra.dll", extra)
	packB := r.publish(t, "valheim-other", otherPack(r, entry("files/extra.dll", extra, "file", "extra.dll", "")))
	g := newFakeGame(t, false)
	e := New(r.client(), "0.1.0", OSFiles{})
	ctx := context.Background()
	vanilla := snapshot(t, g.lib)
	if err := e.Install(ctx, g.request("windows", packA), nil); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(g.root, "extra.dll"), "manual")
	installed := snapshot(t, g.lib)

	// Only the manual file is a leftover; pack A's files and folders are not.
	err := e.Install(ctx, g.request("windows", packB), nil)
	var lo *LeftoversError
	if !errors.As(err, &lo) || !reflect.DeepEqual(lo.Paths, []string{"extra.dll"}) {
		t.Fatalf("err = %v, want LeftoversError for extra.dll only", err)
	}
	assertSameTree(t, installed, snapshot(t, g.lib))

	req := g.request("windows", packB)
	req.CleanLeftovers = true
	if err := e.Install(ctx, req, nil); err != nil {
		t.Fatal(err)
	}
	assertNoStaging(t, g.root)
	if err := e.Remove(ctx, g.root, nil); err != nil {
		t.Fatal(err)
	}
	assertSameTree(t, vanilla, snapshot(t, g.lib))
}

// configManifest is a pack that ships two configs and preserves BepInEx/config.
func configManifest(r *fakeRemote, version, modCfg, otherCfg string) map[string]any {
	var files []any
	for name, content := range map[string]string{
		"BepInEx/plugins/Mod.dll": "mod " + version, "BepInEx/config/Mod.cfg": modCfg, "BepInEx/config/Other.cfg": otherCfg,
	} {
		url := "files/" + version + "/" + filepath.Base(name)
		r.put(url, []byte(content))
		files = append(files, entry(url, []byte(content), "file", name, ""))
	}
	return map[string]any{
		"schema_version": 1, "id": packID, "game_id": "valheim", "name": "Configs", "version": version,
		"owned_dirs": []string{"BepInEx"},
		"preserve":   []string{"BepInEx/config"},
		"files":      map[string]any{"common": files},
	}
}

func TestEngine_Install_updateSamePack_keepsModifiedPreservedFiles(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, configManifest(r, "1", "mod v1", "other v1"))
	g := newFakeGame(t, false)
	e := New(r.client(), "0.1.0", OSFiles{})
	ctx := context.Background()
	vanilla := snapshot(t, g.lib)
	if err := e.Install(ctx, g.request("windows", pack), nil); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(g.root, "BepInEx", "config")
	writeTestFile(t, filepath.Join(cfg, "Mod.cfg"), "edited by user")
	writeTestFile(t, filepath.Join(cfg, "Generated.cfg"), "written by a mod")

	r.publish(t, packID, configManifest(r, "2", "mod v2", "other v2"))
	if err := e.Install(ctx, g.request("windows", pack), nil); err != nil {
		t.Fatal(err)
	}
	assertNoStaging(t, g.root)
	for rel, want := range map[string]string{
		"BepInEx/plugins/Mod.dll":      "mod 2",
		"BepInEx/config/Mod.cfg":       "edited by user",   // modified: the user's copy wins
		"BepInEx/config/Other.cfg":     "other v2",         // unmodified: updated
		"BepInEx/config/Generated.cfg": "written by a mod", // not shipped: kept
	} {
		if got := readFile(t, g.root, rel); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}

	// A second update still knows Other.cfg is unmodified and Mod.cfg is not.
	r.publish(t, packID, configManifest(r, "3", "mod v3", "other v3"))
	if err := e.Install(ctx, g.request("windows", pack), nil); err != nil {
		t.Fatal(err)
	}
	if a, b := readFile(t, g.root, "BepInEx/config/Mod.cfg"), readFile(t, g.root, "BepInEx/config/Other.cfg"); a != "edited by user" || b != "other v3" {
		t.Errorf("after second update: Mod.cfg = %q, Other.cfg = %q", a, b)
	}

	// Remove still deletes everything.
	if err := e.Remove(ctx, g.root, nil); err != nil {
		t.Fatal(err)
	}
	assertSameTree(t, vanilla, snapshot(t, g.lib))
}

func TestEngine_Install_switchPack_dropsPreservedFiles(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, configManifest(r, "1", "mod v1", "other v1"))
	m := otherPack(r)
	m["preserve"] = []string{"BepInEx/config"}
	packB := r.publish(t, "valheim-other", m)
	g := newFakeGame(t, false)
	e := New(r.client(), "0.1.0", OSFiles{})
	ctx := context.Background()
	if err := e.Install(ctx, g.request("windows", pack), nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Install(ctx, g.request("windows", packB), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(g.root, "BepInEx", "config", "Mod.cfg")); !os.IsNotExist(err) {
		t.Error("config of the previous pack carried over to a different pack")
	}
}

func TestEngine_Install_crashDuringReinstall_nextRemoveRecovers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files faultyFiles
	}{
		{"file", faultyFiles{op: "create", pathMatch: config.TmpSuffix, nth: 3, crash: true}},
		{"staging", faultyFiles{op: "removeall", pathMatch: config.StagingDirName, nth: 1, crash: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRemote(t)
			pack := r.publish(t, packID, defaultManifest(t, r))
			g := newFakeGame(t, false)
			e := New(r.client(), "0.1.0", OSFiles{})
			ctx := context.Background()
			vanilla := snapshot(t, g.lib)
			if err := e.Install(ctx, g.request("windows", pack), nil); err != nil {
				t.Fatal(err)
			}

			// Simulate a crash: the panic skips rollback and cleanup.
			func() {
				defer func() { _ = recover() }()
				_ = New(r.client(), "0.1.0", &tc.files).Install(ctx, g.request("windows", pack), nil)
				t.Fatal("no crash")
			}()
			if _, err := os.Lstat(filepath.Join(g.root, config.StagingDirName)); err != nil {
				t.Fatalf("expected a staging folder after the crash: %v", err)
			}

			// Remove first recovers (or discards) the staged pack, then removes it.
			if err := e.Remove(ctx, g.root, nil); err != nil {
				t.Fatal(err)
			}
			assertSameTree(t, vanilla, snapshot(t, g.lib))
		})
	}
}

func TestEngine_Install_stageFailsDuringReinstall_keepsOldPack(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, defaultManifest(t, r))
	g := newFakeGame(t, false)
	ctx := context.Background()
	if err := New(r.client(), "0.1.0", OSFiles{}).Install(ctx, g.request("windows", pack), nil); err != nil {
		t.Fatal(err)
	}
	installed := snapshot(t, g.lib)
	markerBefore, err := os.ReadFile(MarkerPath(g.root))
	if err != nil {
		t.Fatal(err)
	}
	// The first rename of a BepInEx path moves the owned BepInEx folder into
	// the staging folder.
	failing := New(r.client(), "0.1.0", &faultyFiles{op: "rename", pathMatch: "BepInEx", nth: 1})

	err = failing.Install(ctx, g.request("windows", pack), nil)

	if !errors.Is(err, errInjected) {
		t.Fatalf("err = %v, want injected failure", err)
	}
	assertSameTree(t, installed, snapshot(t, g.lib))
	if markerAfter, _ := os.ReadFile(MarkerPath(g.root)); string(markerAfter) != string(markerBefore) {
		t.Errorf("marker changed:\n%s\nwant:\n%s", markerAfter, markerBefore)
	}
	assertNoStaging(t, g.root)
}
