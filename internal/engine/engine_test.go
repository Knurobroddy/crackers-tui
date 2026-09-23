package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// ---- test remote -----------------------------------------------------------

type fakeRemote struct {
	t     *testing.T
	mu    sync.Mutex
	files map[string][]byte
	srv   *httptest.Server
}

func newFakeRemote(t *testing.T) *fakeRemote {
	r := &fakeRemote{t: t, files: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/")]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRemote) put(name string, b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[name] = b
}

func (r *fakeRemote) client() *remote.Client {
	c, err := remote.NewClient(r.srv.URL+"/", "0.1.0", nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

type zipEntry struct {
	name    string
	content string
	mode    os.FileMode
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
			if strings.HasSuffix(e.name, "/") {
				mode = fs.ModeDir | 0o755
			}
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(e.name, "/") {
			w.Write([]byte(e.content))
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func hexSHA(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func entry(url string, b []byte, kind, dest, zipRoot string) map[string]any {
	e := map[string]any{"url": url, "sha256": hexSHA(b), "size": len(b), "kind": kind, "dest": dest}
	if zipRoot != "" {
		e["zip_root"] = zipRoot
	}
	return e
}

func bepinexZip(t *testing.T) []byte {
	return makeZip(t,
		zipEntry{name: "icon.png", content: "outside zip_root"},
		zipEntry{name: "BepInExPack_Valheim/"},
		zipEntry{name: "BepInExPack_Valheim/winhttp.dll", content: "winhttp"},
		zipEntry{name: "BepInExPack_Valheim/doorstop_config.ini", content: "[General]\nenabled=true\n"},
		zipEntry{name: "BepInExPack_Valheim/BepInEx/"},
		zipEntry{name: "BepInExPack_Valheim/BepInEx/core/BepInEx.dll", content: "core"},
		zipEntry{name: "BepInExPack_Valheim/BepInEx/config/"},
		zipEntry{name: "BepInExPack_Valheim/start_game_bepinex.sh", content: "#!/bin/sh\n", mode: 0o755},
	)
}

const packID = "valheim-test"

// publish stores a manifest (and the index) on the fake remote.
func (r *fakeRemote) publish(t *testing.T, id string, manifest map[string]any) remote.PackRef {
	t.Helper()
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	r.put("packs/"+id+".json", b)
	return remote.PackRef{ID: id, GameID: "valheim", Name: id, Manifest: "packs/" + id + ".json"}
}

func defaultManifest(t *testing.T, r *fakeRemote) map[string]any {
	zipBytes := bepinexZip(t)
	mod := []byte("some mod")
	common := []byte("common file")
	r.put("files/BepInExPack_Valheim.zip", zipBytes)
	r.put("files/SomeMod.dll", mod)
	r.put("files/common.txt", common)
	return map[string]any{
		"schema_version": 1,
		"id":             packID,
		"game_id":        "valheim",
		"name":           "Test pack",
		"version":        "2026.09.22",
		"owned_dirs":     []string{"BepInEx"},
		"files": map[string]any{
			"common": []any{entry("files/common.txt", common, "file", "BepInEx/plugins/common.txt", "")},
			"windows": []any{
				entry("files/BepInExPack_Valheim.zip", zipBytes, "zip", "", "BepInExPack_Valheim/"),
				entry("files/SomeMod.dll", mod, "file", "BepInEx/plugins/SomeMod.dll", ""),
			},
		},
		"hooks": []any{
			map[string]any{"type": "proton_dll_override", "builds": []string{"linux_proton"}, "dll": "winhttp", "mode": "native,builtin"},
		},
		"loader": nil,
	}
}

// ---- fake game -------------------------------------------------------------

type fakeGame struct {
	lib  string
	root string
	reg  string // user.reg path (linux_proton only)
}

func newFakeGame(t *testing.T, withPrefix bool) fakeGame {
	t.Helper()
	lib := t.TempDir()
	root := filepath.Join(lib, "steamapps", "common", "Valheim")
	for _, f := range []string{"valheim.exe", "valheim_Data/Managed/assembly_valheim.dll", "UnityPlayer.dll"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("vanilla "+f), 0o644)
	}
	g := fakeGame{lib: lib, root: root}
	if withPrefix {
		g.reg = filepath.Join(lib, "steamapps", "compatdata", "892970", "pfx", "user.reg")
		os.MkdirAll(filepath.Dir(g.reg), 0o755)
		orig, err := os.ReadFile("../../testdata/wine/user.reg")
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(g.reg, orig, 0o644)
	}
	return g
}

func (g fakeGame) request(build string, pack remote.PackRef) InstallRequest {
	return InstallRequest{
		Game: detect.Result{
			GameID: "valheim", BuildID: build, RootDir: g.root,
			AnchorPath: filepath.Join(g.root, "valheim.exe"),
			Extra: map[string]string{
				detect.ExtraSteamAppID: "892970", detect.ExtraSteamLibrary: g.lib, detect.ExtraSteamLibraries: g.lib,
			},
		},
		FilesKey: "windows",
		Pack:     pack,
	}
}

// snapshot maps every path below dir to "dir" or the file's content hash.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			m[filepath.ToSlash(rel)] = "dir"
			return nil
		}
		b, _ := os.ReadFile(p)
		m[filepath.ToSlash(rel)] = hexSHA(b)
		return nil
	})
	return m
}

func assertSameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	if reflect.DeepEqual(before, after) {
		return
	}
	var diff []string
	for k, v := range after {
		if before[k] != v {
			diff = append(diff, "+ "+k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			diff = append(diff, "- "+k)
		}
	}
	sort.Strings(diff)
	t.Fatalf("tree differs from pre-install state:\n%s", strings.Join(diff, "\n"))
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- tests -----------------------------------------------------------------

func TestInstallAndRemoveRestoresExactTree(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, defaultManifest(t, r))
	g := newFakeGame(t, false)
	e := New(r.client(), "0.1.0", nil)
	ctx := context.Background()
	before := snapshot(t, g.lib)

	var events []Event
	if err := e.Install(ctx, g.request("windows", pack), func(ev Event) { events = append(events, ev) }); err != nil {
		t.Fatal(err)
	}

	for rel, want := range map[string]string{
		"winhttp.dll":                 "winhttp",
		"doorstop_config.ini":         "[General]\nenabled=true\n",
		"BepInEx/core/BepInEx.dll":    "core",
		"BepInEx/plugins/SomeMod.dll": "some mod",
		"BepInEx/plugins/common.txt":  "common file",
	} {
		if got := readFile(t, g.root, rel); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(g.root, "BepInEx", "config")); err != nil || !fi.IsDir() {
		t.Error("empty zip directory entry BepInEx/config not created")
	}
	if _, err := os.Stat(filepath.Join(g.root, "icon.png")); !os.IsNotExist(err) {
		t.Error("entry outside zip_root was extracted")
	}
	if runtime.GOOS != "windows" { // zip modes are kept on Linux
		for rel, want := range map[string]os.FileMode{"start_game_bepinex.sh": 0o755, "winhttp.dll": 0o644, "BepInEx/plugins/SomeMod.dll": 0o644} {
			if fi, err := os.Stat(filepath.Join(g.root, filepath.FromSlash(rel))); err != nil || fi.Mode().Perm() != want {
				t.Errorf("%s mode = %v, want %v", rel, fi.Mode().Perm(), want)
			}
		}
	}

	mk, err := ReadMarker(g.root)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"BepInEx/plugins/common.txt", "winhttp.dll", "doorstop_config.ini", "BepInEx/core/BepInEx.dll", "start_game_bepinex.sh", "BepInEx/plugins/SomeMod.dll"}
	if !reflect.DeepEqual(mk.Files, wantFiles) {
		t.Errorf("marker files = %q, want %q", mk.Files, wantFiles)
	}
	wantDirs := []string{"BepInEx", "BepInEx/config", "BepInEx/plugins", "BepInEx/core"}
	if !reflect.DeepEqual(mk.DirsCreated, wantDirs) {
		t.Errorf("marker dirs_created = %q, want %q", mk.DirsCreated, wantDirs)
	}
	manifestRaw, _ := json.Marshal(defaultManifest(t, r))
	if mk.PackID != packID || mk.PackName != "Test pack" || mk.PackVersion != "2026.09.22" || mk.BuildID != "windows" ||
		mk.ManifestSHA256 != hexSHA(manifestRaw) || !reflect.DeepEqual(mk.OwnedDirs, []string{"BepInEx"}) || len(mk.Undo) != 0 ||
		mk.AppVersion != "0.1.0" || mk.InstalledAt.IsZero() {
		t.Errorf("marker = %+v", mk)
	}
	raw, _ := os.ReadFile(MarkerPath(g.root))
	if !bytes.Contains(raw, []byte(`"undo": []`)) {
		t.Errorf("marker undo must be an empty array:\n%s", raw)
	}
	if len(events) == 0 {
		t.Error("no progress events")
	}

	ix := &remote.Index{Packs: []remote.PackRef{pack}}
	if st := e.Status(ctx, g.root, ix); st.State != Installed {
		t.Errorf("status = %v, want Installed", st)
	}

	// Files created by mods at runtime live in owned_dirs and must go too.
	os.WriteFile(filepath.Join(g.root, "BepInEx", "config", "mod.cfg"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(g.root, "BepInEx", "LogOutput.log"), []byte("x"), 0o644)

	if err := e.Remove(ctx, g.root, nil); err != nil {
		t.Fatal(err)
	}
	assertSameTree(t, before, snapshot(t, g.lib))
	if st := e.Status(ctx, g.root, ix); st.State != NotInstalled {
		t.Errorf("status after remove = %v", st)
	}
	if err := e.Remove(ctx, g.root, nil); !errors.Is(err, ErrNothingToRemove) {
		t.Errorf("second remove: err = %v", err)
	}
}

func TestInstallWithProtonHookAndRemove(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, defaultManifest(t, r))
	g := newFakeGame(t, true)
	e := New(r.client(), "0.1.0", nil)
	ctx := context.Background()
	regBefore, _ := os.ReadFile(g.reg)
	before := snapshot(t, g.root)

	if err := e.Install(ctx, g.request("linux_proton", pack), nil); err != nil {
		t.Fatal(err)
	}
	reg, _ := os.ReadFile(g.reg)
	if !bytes.Contains(reg, []byte(`"winhttp"="native,builtin"`)) {
		t.Fatalf("override missing from user.reg:\n%s", reg)
	}
	mk, _ := ReadMarker(g.root)
	if len(mk.Undo) != 1 || mk.Undo[0].Op != "wine_reg_restore" || mk.BuildID != "linux_proton" {
		t.Fatalf("marker undo = %+v", mk.Undo)
	}
	if !strings.Contains(string(mk.Undo[0].Raw), `"previous":null`) {
		t.Errorf("undo = %s", mk.Undo[0].Raw)
	}

	if err := e.Remove(ctx, g.root, nil); err != nil {
		t.Fatal(err)
	}
	assertSameTree(t, before, snapshot(t, g.root))
	regAfter, _ := os.ReadFile(g.reg)
	if !bytes.Equal(regAfter, regBefore) {
		t.Fatalf("user.reg not restored byte-identically:\n%s", regAfter)
	}
}

func TestRollbackOnInjectedFailure(t *testing.T) {
	for _, tc := range []struct {
		stage string
		n     int
	}{
		{"file", 0}, {"file", 3}, {"hook", 0}, {"marker", 0},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.stage, tc.n), func(t *testing.T) {
			r := newFakeRemote(t)
			pack := r.publish(t, packID, defaultManifest(t, r))
			g := newFakeGame(t, true)
			e := New(r.client(), "0.1.0", nil)
			injected := errors.New("injected failure")
			e.failpoint = func(stage string, n int) error {
				if stage == tc.stage && n == tc.n {
					return injected
				}
				return nil
			}
			before := snapshot(t, g.lib)
			regBefore, _ := os.ReadFile(g.reg)

			err := e.Install(context.Background(), g.request("linux_proton", pack), nil)
			if !errors.Is(err, injected) {
				t.Fatalf("err = %v, want injected failure", err)
			}
			after := snapshot(t, g.lib)
			delete(after, "steamapps/compatdata/892970/pfx/user.reg.modinst-bak") // the backup is kept on purpose
			delete(before, "steamapps/compatdata/892970/pfx/user.reg.modinst-bak")
			// user.reg content is compared separately below.
			delete(after, "steamapps/compatdata/892970/pfx/user.reg")
			delete(before, "steamapps/compatdata/892970/pfx/user.reg")
			assertSameTree(t, before, after)
			if regAfter, _ := os.ReadFile(g.reg); !bytes.Equal(regAfter, regBefore) {
				t.Errorf("user.reg not restored:\n%s", regAfter)
			}
		})
	}
}

func TestHashMismatchAbortsBeforeWriting(t *testing.T) {
	r := newFakeRemote(t)
	m := defaultManifest(t, r)
	r.put("files/SomeMod.dll", []byte("tampered")) // served bytes no longer match the manifest
	pack := r.publish(t, packID, m)
	g := newFakeGame(t, false)
	before := snapshot(t, g.lib)

	err := New(r.client(), "0.1.0", nil).Install(context.Background(), g.request("windows", pack), nil)
	if err == nil || !strings.Contains(err.Error(), "SomeMod.dll") {
		t.Fatalf("err = %v, want mismatch naming SomeMod.dll", err)
	}
	assertSameTree(t, before, snapshot(t, g.lib))
}

func TestUnsafeArchivesRejectedBeforeWriting(t *testing.T) {
	cases := map[string][]byte{
		"zip-slip":      makeZip(t, zipEntry{name: "ok.txt", content: "x"}, zipEntry{name: "../evil.txt", content: "x"}),
		"nested-slip":   makeZip(t, zipEntry{name: "a/../../evil.txt", content: "x"}),
		"backslash":     makeZip(t, zipEntry{name: `..\evil.txt`, content: "x"}),
		"absolute":      makeZip(t, zipEntry{name: "/tmp/evil.txt", content: "x"}),
		"volume":        makeZip(t, zipEntry{name: "C:/evil.txt", content: "x"}),
		"symlink-entry": makeZip(t, zipEntry{name: "ok.txt", content: "x"}, zipEntry{name: "link", content: "/etc/passwd", mode: fs.ModeSymlink | 0o777}),
	}
	for name, zipBytes := range cases {
		t.Run(name, func(t *testing.T) {
			r := newFakeRemote(t)
			r.put("files/bad.zip", zipBytes)
			pack := r.publish(t, packID, map[string]any{
				"schema_version": 1, "id": packID, "game_id": "valheim", "name": "bad", "version": "1",
				"files": map[string]any{"common": []any{entry("files/bad.zip", zipBytes, "zip", "BepInEx", "")}},
			})
			g := newFakeGame(t, false)
			before := snapshot(t, g.lib)
			err := New(r.client(), "0.1.0", nil).Install(context.Background(), g.request("windows", pack), nil)
			if err == nil {
				t.Fatal("unsafe archive accepted")
			}
			t.Logf("rejected: %v", err)
			assertSameTree(t, before, snapshot(t, g.lib))
		})
	}
}

func TestUnsafeManifestRejected(t *testing.T) {
	good := []byte("x")
	cases := map[string]map[string]any{
		"dest-slip":    {"files": map[string]any{"common": []any{entry("files/x", good, "file", "../outside.dll", "")}}},
		"dest-abs":     {"files": map[string]any{"common": []any{entry("files/x", good, "file", "/etc/x", "")}}},
		"owned-root":   {"owned_dirs": []string{"."}},
		"owned-slip":   {"owned_dirs": []string{"../.."}},
		"unknown-hook": {"hooks": []any{map[string]any{"type": "run_script", "builds": []string{"other"}}}},
		"loader":       {"loader": map[string]any{"type": "fabric"}},
		"duplicate": {"files": map[string]any{"common": []any{
			entry("files/x", good, "file", "a/x.dll", ""), entry("files/x", good, "file", "a/x.dll", ""),
		}}},
		"wrong-game": {"game_id": "minecraft"},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			r := newFakeRemote(t)
			r.put("files/x", good)
			m := map[string]any{"schema_version": 1, "id": packID, "game_id": "valheim", "name": "p", "version": "1"}
			for k, v := range override {
				m[k] = v
			}
			pack := r.publish(t, packID, m)
			g := newFakeGame(t, false)
			before := snapshot(t, g.lib)
			err := New(r.client(), "0.1.0", nil).Install(context.Background(), g.request("windows", pack), nil)
			if err == nil {
				t.Fatal("unsafe manifest accepted")
			}
			t.Logf("rejected: %v", err)
			assertSameTree(t, before, snapshot(t, g.lib))
		})
	}
}

func TestExistingFileAborts(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, defaultManifest(t, r))
	g := newFakeGame(t, false)
	os.WriteFile(filepath.Join(g.root, "winhttp.dll"), []byte("someone else's"), 0o644)
	before := snapshot(t, g.lib)
	err := New(r.client(), "0.1.0", nil).Install(context.Background(), g.request("windows", pack), nil)
	var lo *LeftoversError
	if !errors.As(err, &lo) || !reflect.DeepEqual(lo.Paths, []string{"winhttp.dll"}) {
		t.Fatalf("err = %v, want LeftoversError for winhttp.dll", err)
	}
	assertSameTree(t, before, snapshot(t, g.lib))
}

// leftoverGame is a vanilla game plus files from an earlier manual BepInEx
// install: an old plugin in the owned BepInEx/ folder and root files the pack
// also ships. unrelated.txt is not part of the pack and must survive.
func leftoverGame(t *testing.T) (fakeGame, map[string]string) {
	g := newFakeGame(t, false)
	vanilla := snapshot(t, g.lib)
	for rel, content := range map[string]string{
		"winhttp.dll":                  "old doorstop",
		"doorstop_config.ini":          "old",
		"BepInEx/plugins/OldMod.dll":   "old plugin",
		"BepInEx/config/OldMod.cfg":    "x",
		"unrelated.txt":                "keep me",
		"valheim_Data/Managed/old.txt": "game subfolder, not the pack's",
	} {
		p := filepath.Join(g.root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	return g, vanilla
}

func TestLeftoversCleanedOnConfirmedInstall(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, defaultManifest(t, r))
	g, _ := leftoverGame(t)
	e := New(r.client(), "0.1.0", nil)
	ctx := context.Background()

	err := e.Install(ctx, g.request("windows", pack), nil)
	var lo *LeftoversError
	if !errors.As(err, &lo) {
		t.Fatalf("err = %v, want LeftoversError", err)
	}
	if want := []string{"BepInEx/", "winhttp.dll", "doorstop_config.ini"}; !reflect.DeepEqual(lo.Paths, want) {
		t.Fatalf("leftovers = %q, want %q", lo.Paths, want)
	}
	withLeftovers := snapshot(t, g.lib)

	req := g.request("windows", pack)
	req.CleanLeftovers = true
	if err := e.Install(ctx, req, nil); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, g.root, "winhttp.dll"); got != "winhttp" {
		t.Errorf("winhttp.dll = %q, want the pack's", got)
	}
	if _, err := os.Stat(filepath.Join(g.root, "BepInEx", "plugins", "OldMod.dll")); !os.IsNotExist(err) {
		t.Error("old plugin in the owned folder survived")
	}
	if readFile(t, g.root, "unrelated.txt") != "keep me" {
		t.Error("file unknown to the pack was touched")
	}
	if err := e.Remove(ctx, g.root, nil); err != nil {
		t.Fatal(err)
	}
	// After remove: the pre-install tree minus the deleted leftovers.
	for _, rel := range []string{"winhttp.dll", "doorstop_config.ini", "BepInEx", "BepInEx/plugins", "BepInEx/plugins/OldMod.dll", "BepInEx/config", "BepInEx/config/OldMod.cfg"} {
		delete(withLeftovers, "steamapps/common/Valheim/"+rel)
	}
	assertSameTree(t, withLeftovers, snapshot(t, g.lib))
}

func TestCleanLeftoversStandalone(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, defaultManifest(t, r))
	g, vanilla := leftoverGame(t)
	e := New(r.client(), "0.1.0", nil)
	ctx := context.Background()

	removed, err := e.CleanLeftovers(ctx, g.request("windows", pack), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"BepInEx/", "winhttp.dll", "doorstop_config.ini"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed = %q, want %q", removed, want)
	}
	vanilla["steamapps/common/Valheim/unrelated.txt"] = hexSHA([]byte("keep me"))
	vanilla["steamapps/common/Valheim/valheim_Data/Managed/old.txt"] = hexSHA([]byte("game subfolder, not the pack's"))
	assertSameTree(t, vanilla, snapshot(t, g.lib))

	// Nothing left: a second run removes nothing.
	if removed, err := e.CleanLeftovers(ctx, g.request("windows", pack), nil); err != nil || len(removed) != 0 {
		t.Errorf("second cleanup = %q, %v", removed, err)
	}
	// Refused while a pack is installed.
	if err := e.Install(ctx, g.request("windows", pack), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CleanLeftovers(ctx, g.request("windows", pack), nil); err == nil || !strings.Contains(err.Error(), "Remove pack") {
		t.Errorf("cleanup with installed pack: err = %v", err)
	}
}

func TestMissingPrefixAborts(t *testing.T) {
	r := newFakeRemote(t)
	pack := r.publish(t, packID, defaultManifest(t, r))
	g := newFakeGame(t, false) // no compatdata
	before := snapshot(t, g.lib)
	err := New(r.client(), "0.1.0", nil).Install(context.Background(), g.request("linux_proton", pack), nil)
	if err == nil || !strings.Contains(err.Error(), "Launch the game once via Steam (Proton)") {
		t.Fatalf("err = %v", err)
	}
	assertSameTree(t, before, snapshot(t, g.lib))
}

func TestSwitchPackAndStatus(t *testing.T) {
	r := newFakeRemote(t)
	packA := r.publish(t, packID, defaultManifest(t, r))
	other := []byte("other pack")
	r.put("files/other.dll", other)
	packB := r.publish(t, "valheim-other", map[string]any{
		"schema_version": 1, "id": "valheim-other", "game_id": "valheim", "name": "Other", "version": "1",
		"owned_dirs": []string{"BepInEx"},
		"files":      map[string]any{"common": []any{entry("files/other.dll", other, "file", "BepInEx/plugins/other.dll", "")}},
	})
	g := newFakeGame(t, false)
	e := New(r.client(), "0.1.0", nil)
	ctx := context.Background()
	before := snapshot(t, g.lib)

	if err := e.Install(ctx, g.request("windows", packA), nil); err != nil {
		t.Fatal(err)
	}
	ix := &remote.Index{Packs: []remote.PackRef{packA, packB}}

	// Reinstall of the same pack goes through remove + install.
	if err := e.Install(ctx, g.request("windows", packA), nil); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	// Changing the remote manifest shows "update available".
	m := defaultManifest(t, r)
	m["version"] = "2026.10.01"
	r.publish(t, packID, m)
	if st := e.Status(ctx, g.root, ix); st.State != UpdateAvailable {
		t.Errorf("status = %v, want UpdateAvailable", st)
	}
	// A pack missing from the index is "no longer offered".
	if st := e.Status(ctx, g.root, &remote.Index{Packs: []remote.PackRef{packB}}); st.State != NotOffered {
		t.Errorf("status = %v, want NotOffered", st)
	}

	// Switching packs removes the old one first.
	if err := e.Install(ctx, g.request("windows", packB), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(g.root, "winhttp.dll")); !os.IsNotExist(err) {
		t.Error("files of the previous pack left behind")
	}
	if got := readFile(t, g.root, "BepInEx/plugins/other.dll"); got != "other pack" {
		t.Errorf("other.dll = %q", got)
	}
	if mk, _ := ReadMarker(g.root); mk.PackID != "valheim-other" {
		t.Errorf("marker pack = %s", mk.PackID)
	}
	if err := e.Remove(ctx, g.root, nil); err != nil {
		t.Fatal(err)
	}
	assertSameTree(t, before, snapshot(t, g.lib))
}

func TestCorruptMarker(t *testing.T) {
	g := newFakeGame(t, false)
	os.WriteFile(MarkerPath(g.root), []byte("{not json"), 0o644)
	e := New(nil, "0.1.0", nil)
	if st := e.Status(context.Background(), g.root, &remote.Index{}); st.State != Unknown || st.Err == nil {
		t.Errorf("status = %+v", st)
	}
	if err := e.Remove(context.Background(), g.root, nil); err == nil || errors.Is(err, ErrNothingToRemove) {
		t.Errorf("remove with corrupt marker: err = %v", err)
	}
	if _, err := os.Stat(MarkerPath(g.root)); err != nil {
		t.Error("corrupt marker was deleted")
	}
}

func TestMarkerPathsValidatedOnRemove(t *testing.T) {
	g := newFakeGame(t, false)
	outside := filepath.Join(g.lib, "outside.txt")
	os.WriteFile(outside, []byte("keep"), 0o644)
	mk := &Marker{SchemaVersion: 1, PackID: "x", Files: []string{"../../outside.txt"}, OwnedDirs: []string{"../.."}}
	if err := writeMarker(g.root, mk); err != nil {
		t.Fatal(err)
	}
	if err := New(nil, "0.1.0", nil).Remove(context.Background(), g.root, nil); err == nil {
		t.Fatal("tampered marker accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("file outside the game dir was deleted")
	}
	if _, err := os.Stat(MarkerPath(g.root)); err != nil {
		t.Error("marker deleted despite errors")
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		"http://x/files/SomeMod.dll":          "SomeMod.dll",
		"http://x/files/with%20space.zip?x=1": "with space.zip",
		"https://thunderstore.io/package/download/denikson/BepInExPack_Valheim/5.4.2202/": "BepInExPack_Valheim 5.4.2202",
		"https://drive.google.com/uc?export=download&id=abc":                              "drive.google.com/uc",
		"https://drive.usercontent.google.com/download?id=abc&export=download&confirm=t":  "drive.usercontent.google.com/download",
	} {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}
