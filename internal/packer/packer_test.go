package packer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	zw.Close()
	return buf.Bytes()
}

func write(t *testing.T, p, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// env serves a fake Thunderstore package and the library folder.
type env struct {
	srv  *httptest.Server
	lib  string
	pack string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{lib: t.TempDir(), pack: filepath.Join(t.TempDir(), "friends-pack")}
	games, err := os.ReadFile("../../testdata/remote/games.json")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.lib, "games.json"), string(games))
	bep := zipBytes(t, map[string]string{
		"manifest.json":                                  "{}",
		"BepInExPack_Valheim/winhttp.dll":                "winhttp",
		"BepInExPack_Valheim/doorstop_config.ini":        "[General]",
		"BepInExPack_Valheim/BepInEx/core/BepInEx.dll":   "core",
		"BepInExPack_Valheim/BepInEx/config/BepInEx.cfg": "cfg",
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/ts/bep.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(bep)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html>sign in</html>"))
	})
	mux.Handle("/lib/", http.StripPrefix("/lib/", http.FileServer(http.Dir(e.lib))))
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) meta(t *testing.T, extra string) {
	t.Helper()
	write(t, filepath.Join(e.pack, MetaFileName), `{
  "id": "friends-pack", "game_id": "valheim", "name": "Friends pack",
  "description": "Test", "version": "2026.09.22",
  "owned_dirs": ["BepInEx"],
  "external": {"windows": [{"url": "`+e.srv.URL+`/ts/bep.zip", "kind": "zip", "dest": "", "zip_root": "BepInExPack_Valheim/"}]},
  "hooks": [{"type": "proton_dll_override", "builds": ["linux_proton"], "dll": "winhttp", "mode": "native,builtin"}]
  `+extra+`
}`)
}

func (e *env) build(t *testing.T) (*Result, error) {
	t.Helper()
	return (&Builder{}).Build(context.Background(), e.pack, e.lib)
}

func TestBuildThenInstallAndRemove(t *testing.T) {
	e := newEnv(t)
	e.meta(t, `, "preserve": ["BepInEx/config"]`)
	write(t, filepath.Join(e.pack, "common", "BepInEx", "plugins", "Mod.dll"), "mod")
	write(t, filepath.Join(e.pack, "common", "BepInEx", "config", "mod.cfg"), "setting=1")
	write(t, filepath.Join(e.pack, "windows", "extra", "note.txt"), "windows only")
	write(t, filepath.Join(e.pack, "common", "Thumbs.db"), "junk")
	write(t, filepath.Join(e.pack, "README.md"), "notes for the pack author")

	res, err := e.build(t)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Ignored, []string{"README.md"}) {
		t.Errorf("ignored = %q", res.Ignored)
	}
	m := res.Manifest
	if !reflect.DeepEqual(m.Preserve, []string{"BepInEx/config"}) {
		t.Errorf("preserve = %q", m.Preserve)
	}
	if len(m.Files["common"]) != 1 || len(m.Files["windows"]) != 2 || m.Files["windows"][0].Kind != remote.KindZip ||
		m.Files["windows"][0].SHA256 == "" || m.Files["windows"][0].Size == 0 || len(m.Files["linux"]) != 0 {
		t.Fatalf("manifest files = %+v", m.Files)
	}
	if len(res.Uploads) != 2 {
		t.Errorf("uploads = %q, want the common and windows zips", res.Uploads)
	}
	raw1, _ := os.ReadFile(res.ManifestPath)
	if !bytes.Contains(raw1, []byte(`"loader": null`)) || !bytes.Contains(raw1, []byte(`"proton_dll_override"`)) {
		t.Errorf("manifest:\n%s", raw1)
	}

	// Rebuilding unchanged input is byte-identical, so clients see no update.
	res2, err := e.build(t)
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(res2.ManifestPath)
	if !bytes.Equal(raw1, raw2) || len(res2.Uploads) != 0 {
		t.Errorf("rebuild changed the manifest or re-uploaded files: %q", res2.Uploads)
	}

	// Install the built pack with the real engine from the served library.
	c, err := remote.NewClient(e.srv.URL+"/lib/", "0.1.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ix, err := c.FetchIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := ix.Pack("friends-pack")
	if !ok || ref.Name != "Friends pack" || ref.Description != "Test" {
		t.Fatalf("index = %+v", ix)
	}
	root := filepath.Join(t.TempDir(), "Valheim")
	write(t, filepath.Join(root, "valheim.exe"), "vanilla")
	eng := engine.New(c, "0.1.0", nil)
	req := engine.InstallRequest{Game: detect.Result{GameID: "valheim", BuildID: "windows", RootDir: root}, FilesKey: "windows", Pack: ref}
	if err := eng.Install(ctx, req, nil); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"winhttp.dll":                "winhttp",
		"BepInEx/plugins/Mod.dll":    "mod",
		"BepInEx/config/mod.cfg":     "setting=1",
		"BepInEx/config/BepInEx.cfg": "cfg",
		"extra/note.txt":             "windows only",
	} {
		if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", rel, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Thumbs.db")); !os.IsNotExist(err) {
		t.Error("junk file was packed")
	}
	if err := eng.Remove(ctx, root, nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "valheim.exe" {
		t.Errorf("after remove: %v", entries)
	}
}

func TestIndexUpsertKeepsOtherPacks(t *testing.T) {
	e := newEnv(t)
	e.meta(t, "")
	write(t, filepath.Join(e.lib, "index.json"), `{"schema_version":1,"min_app_version":"0.2.0","packs":[
		{"id":"other","game_id":"valheim","name":"Other","description":"","manifest":"packs/other.json"},
		{"id":"friends-pack","game_id":"valheim","name":"Old name","manifest":"packs/friends-pack.json","note":"kept"}]}`)
	if _, err := e.build(t); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(e.lib, "index.json"))
	var doc struct {
		MinAppVersion string           `json:"min_app_version"`
		Packs         []map[string]any `json:"packs"`
	}
	json.Unmarshal(raw, &doc)
	if doc.MinAppVersion != "0.2.0" || len(doc.Packs) != 2 || doc.Packs[0]["id"] != "other" ||
		doc.Packs[1]["name"] != "Friends pack" || doc.Packs[1]["note"] != "kept" {
		t.Errorf("index.json:\n%s", raw)
	}
}

func TestBuildRejects(t *testing.T) {
	cases := map[string]struct {
		extra string
		setup func(e *env)
		want  string
	}{
		"duplicate target": {setup: func(e *env) {
			os.WriteFile(filepath.Join(e.pack, "common", "winhttp.dll"), []byte("mine"), 0o644)
		}, want: "written twice"},
		"hash mismatch": {extra: `, "external": {"windows": [{"url": "URL/ts/bep.zip", "kind": "zip", "dest": "", "sha256": "` + strings.Repeat("0", 64) + `"}]}`,
			want: "pack.modinst says"},
		"html page":     {extra: `, "external": {"common": [{"url": "URL/login", "kind": "file", "dest": "x.dll"}]}`, want: "HTML page"},
		"unknown field": {extra: `, "files": {}`, want: "unknown field"},
		"unknown game":  {extra: `, "game_id": "minecraft"`, want: "not in"},
		"unknown hook":  {extra: `, "hooks": [{"type": "run_script", "builds": ["windows"]}]`, want: "unknown hook type"},
		"owned root":    {extra: `, "owned_dirs": ["."]`, want: "game directory itself"},
		"preserve root": {extra: `, "preserve": [""]`, want: "preserve"},
		"preserve up":   {extra: `, "preserve": ["../x"]`, want: "preserve"},
		"unknown list":  {extra: `, "external": {"macos": []}`, want: "unknown list"},
		"marker in pack": {setup: func(e *env) {
			os.WriteFile(filepath.Join(e.pack, "common", ".crackers-modinst.json"), []byte("{}"), 0o644)
		}, want: "must not contain"},
		"staging folder in pack": {setup: func(e *env) {
			os.MkdirAll(filepath.Join(e.pack, "common", ".crackers-modinst-old"), 0o755)
			os.WriteFile(filepath.Join(e.pack, "common", ".crackers-modinst-old", "x.dll"), []byte("x"), 0o644)
		}, want: "must not contain"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			// Later duplicate keys override earlier ones in encoding/json, so
			// "extra" replaces fields of the base metadata.
			e.meta(t, strings.ReplaceAll(tc.extra, "URL", e.srv.URL))
			os.MkdirAll(filepath.Join(e.pack, "common"), 0o755)
			write(t, filepath.Join(e.pack, "common", "BepInEx", "plugins", "Mod.dll"), "mod")
			if tc.setup != nil {
				tc.setup(e)
			}
			_, err := e.build(t)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(e.lib, "packs")); !os.IsNotExist(statErr) {
				t.Error("output written despite the error")
			}
		})
	}
}

func TestInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Friends Pack")
	p, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("template does not validate: %v", err)
	}
	if m.ID != "my-friends-pack" || m.Name != "My Friends Pack" || m.GameID != "valheim" || len(m.External["windows"]) != 1 ||
		!reflect.DeepEqual(m.Preserve, []string{"BepInEx/config"}) {
		t.Errorf("meta = %+v", m)
	}
	if fi, err := os.Stat(filepath.Join(dir, "common", "BepInEx", "plugins")); err != nil || !fi.IsDir() {
		t.Error("plugins folder not created")
	}
	if _, err := Init(dir); err == nil {
		t.Errorf("second init overwrote %s", p)
	}
}
