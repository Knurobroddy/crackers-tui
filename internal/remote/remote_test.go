package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func serve(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "crackers-modinst/1.2.3" {
			t.Errorf("User-Agent = %q", got)
		}
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/lib/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := NewClient(base, "1.2.3", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolve(t *testing.T) {
	c := newClient(t, "https://example.com/lib") // no trailing slash
	cases := map[string]string{
		"games.json":                    "https://example.com/lib/games.json",
		"packs/a.json":                  "https://example.com/lib/packs/a.json",
		"https://cdn.example.org/x.zip": "https://cdn.example.org/x.zip",
		"/root.json":                    "https://example.com/root.json",
		"files/with%20space.dll":        "https://example.com/lib/files/with%20space.dll",
	}
	for ref, want := range cases {
		got, err := c.Resolve(ref)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	if _, err := c.Resolve("file:///etc/passwd"); err == nil {
		t.Error("file:// URL accepted")
	}
	if _, err := NewClient("ftp://example.com/", "1", nil); err == nil {
		t.Error("ftp base accepted")
	}
	d := NewDownloader("1", nil)
	if got, err := d.Resolve("https://example.com/a.zip"); err != nil || got != "https://example.com/a.zip" {
		t.Errorf("downloader Resolve = %q, %v", got, err)
	}
	if _, err := d.Resolve("packs/x.json"); err == nil {
		t.Error("downloader accepted a relative URL")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"v0.2.0", "0.1.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0+build", "1.0.0", 0},
		{"1.2", "1.2.0", 0},
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
	if _, err := CompareVersions("banana", "1.0.0"); err == nil {
		t.Error("invalid version accepted")
	}
}

func TestSchemaAndMinVersion(t *testing.T) {
	srv := serve(t, map[string]string{
		"games.json": `{"schema_version":2,"games":[]}`,
		"index.json": `{"schema_version":1,"min_app_version":"9.0.0","packs":[]}`,
	})
	c := newClient(t, srv.URL+"/lib/")
	if _, err := c.FetchGames(context.Background()); !IsUpdateRequired(err) {
		t.Errorf("games schema 2: err = %v, want UpdateRequiredError", err)
	}
	if _, err := c.FetchIndex(context.Background()); !IsUpdateRequired(err) {
		t.Errorf("index min_app_version: err = %v, want UpdateRequiredError", err)
	}
	if err := CheckMinAppVersion("x", "9.0.0", "dev"); err != nil {
		t.Errorf("dev version must skip min_app_version: %v", err)
	}
	if err := CheckMinAppVersion("x", "0.1.0", "0.1.0"); err != nil {
		t.Errorf("equal version: %v", err)
	}
}

func TestFetchGamesAndIndex(t *testing.T) {
	srv := serve(t, map[string]string{
		"games.json": `{"schema_version":1,"min_app_version":"0.1.0","unknown_field":true,"games":[
			{"id":"valheim","name":"Valheim","strategy":"steam","steam":{"appid":892970},"not_found_hint":"hint",
			 "builds":[{"id":"windows","os":"windows","anchor":"valheim.exe","files":"windows"}]}]}`,
		"index.json": `{"schema_version":1,"min_app_version":"0.1.0","packs":[
			{"id":"p1","game_id":"valheim","name":"P1","description":"d","manifest":"packs/p1.json"}]}`,
	})
	c := newClient(t, srv.URL+"/lib/")
	g, err := c.FetchGames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Games) != 1 || g.Games[0].Steam.AppID != 892970 || g.Games[0].Builds[0].Anchor != "valheim.exe" {
		t.Errorf("games = %+v", g.Games)
	}
	ix, err := c.FetchIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ix.GamesWithPacks()["valheim"] || len(ix.PacksFor("valheim")) != 1 {
		t.Errorf("index = %+v", ix)
	}
}

const manifestTmpl = `{"schema_version":1,"id":"p1","game_id":"valheim","name":"P1","version":"1",
	"owned_dirs":["BepInEx"],
	"files":{"common":[{"url":"files/a.dll","sha256":"%s","size":3,"kind":"file","dest":"BepInEx/a.dll"}],
	         "windows":[{"url":"files/b.zip","sha256":"%s","size":0,"kind":"zip","dest":"","zip_root":"x/"}]},
	"hooks":[{"type":"proton_dll_override","builds":["linux_proton"],"dll":"winhttp","mode":"native,builtin"}]
	%s}`

func manifestJSON(loader string) string {
	h := strings.Repeat("ab", 32)
	return strings.Replace(strings.Replace(strings.Replace(manifestTmpl, "%s", h, 1), "%s", h, 1), "%s", loader, 1)
}

func TestFetchManifest(t *testing.T) {
	raw := manifestJSON(`,"loader":null`)
	srv := serve(t, map[string]string{
		"packs/p1.json":     raw,
		"packs/noload.json": manifestJSON(""),
		"packs/loader.json": manifestJSON(`,"loader":{"type":"fabric"}`),
		"packs/schema.json": `{"schema_version":2,"id":"x"}`,
		"packs/kind.json":   `{"schema_version":1,"id":"x","files":{"common":[{"url":"u","sha256":"` + strings.Repeat("00", 32) + `","kind":"tarball"}]}}`,
	})
	c := newClient(t, srv.URL+"/lib/")
	ctx := context.Background()

	m, hash, err := c.FetchManifest(ctx, "packs/p1.json")
	if err != nil {
		t.Fatal(err)
	}
	if hash != sha([]byte(raw)) {
		t.Errorf("manifest hash = %s, want hash of raw bytes", hash)
	}
	if got := m.EffectiveFiles("windows"); len(got) != 2 || got[0].Dest != "BepInEx/a.dll" || got[1].Kind != KindZip {
		t.Errorf("EffectiveFiles(windows) = %+v", got)
	}
	if got := m.EffectiveFiles("linux"); len(got) != 1 {
		t.Errorf("EffectiveFiles(linux) = %+v (missing list must be empty)", got)
	}
	if len(m.Hooks) != 1 || m.Hooks[0].Type != "proton_dll_override" || !m.Hooks[0].AppliesTo("linux_proton") || m.Hooks[0].AppliesTo("windows") {
		t.Errorf("hooks = %+v", m.Hooks)
	}
	var hookFields struct{ DLL, Mode string }
	if err := json.Unmarshal(m.Hooks[0].Raw, &hookFields); err != nil || hookFields.DLL != "winhttp" || hookFields.Mode != "native,builtin" {
		t.Errorf("hook raw = %s", m.Hooks[0].Raw)
	}
	if h2, err := c.ManifestHash(ctx, "packs/p1.json"); err != nil || h2 != hash {
		t.Errorf("ManifestHash = %s, %v", h2, err)
	}

	if _, _, err := c.FetchManifest(ctx, "packs/noload.json"); err != nil {
		t.Errorf("absent loader rejected: %v", err)
	}
	if _, _, err := c.FetchManifest(ctx, "packs/loader.json"); err == nil || !strings.Contains(err.Error(), "not supported in this version") {
		t.Errorf("non-null loader: err = %v", err)
	}
	if _, _, err := c.FetchManifest(ctx, "packs/schema.json"); !IsUpdateRequired(err) {
		t.Errorf("schema 2 manifest: err = %v", err)
	}
	if _, _, err := c.FetchManifest(ctx, "packs/kind.json"); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, _, err := c.FetchManifest(ctx, "packs/missing.json"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing manifest: err = %v", err)
	}
}

func TestDownload(t *testing.T) {
	content := "hello world"
	srv := serve(t, map[string]string{"files/f.bin": content})
	c := newClient(t, srv.URL+"/lib/")
	dir := t.TempDir()
	ctx := context.Background()

	var lastDone, lastTotal int64
	dst := filepath.Join(dir, "ok")
	err := c.Download(ctx, FileEntry{URL: "files/f.bin", SHA256: strings.ToUpper(sha([]byte(content))), Size: int64(len(content))}, dst,
		func(d, t int64) { lastDone, lastTotal = d, t })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != content {
		t.Errorf("content = %q", b)
	}
	if lastDone != int64(len(content)) || lastTotal != int64(len(content)) {
		t.Errorf("progress = %d/%d", lastDone, lastTotal)
	}

	// size 0 means "unknown": only the hash is checked.
	if err := c.Download(ctx, FileEntry{URL: "files/f.bin", SHA256: sha([]byte(content))}, filepath.Join(dir, "nosize"), nil); err != nil {
		t.Errorf("size 0: %v", err)
	}

	bad := filepath.Join(dir, "bad")
	err = c.Download(ctx, FileEntry{URL: "files/f.bin", SHA256: sha([]byte("other")), Size: int64(len(content))}, bad, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || !strings.Contains(err.Error(), "files/f.bin") {
		t.Errorf("hash mismatch: err = %v", err)
	}
	if _, statErr := os.Stat(bad); !os.IsNotExist(statErr) {
		t.Error("file left behind after hash mismatch")
	}

	for _, size := range []int64{5, 100} {
		err = c.Download(ctx, FileEntry{URL: "files/f.bin", SHA256: sha([]byte(content)), Size: size}, filepath.Join(dir, "size"), nil)
		if err == nil || !strings.Contains(err.Error(), "size mismatch") {
			t.Errorf("size %d: err = %v", size, err)
		}
	}

	if err := c.Download(ctx, FileEntry{URL: "files/nope", SHA256: sha(nil)}, filepath.Join(dir, "404"), nil); err == nil {
		t.Error("404 accepted")
	}
}

func TestTransientErrorsRetried(t *testing.T) {
	content := []byte("payload")
	var calls, missCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flaky":
			calls++
			if calls <= 2 {
				http.Error(w, "busy", http.StatusInternalServerError)
				return
			}
			w.Write(content)
		default:
			missCalls++
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "1.2.3", nil)
	c.retryDelay = time.Millisecond
	dst := filepath.Join(t.TempDir(), "f")
	if err := c.Download(context.Background(), FileEntry{URL: "flaky", SHA256: sha(content), Size: int64(len(content))}, dst, nil); err != nil {
		t.Fatalf("download after two 500s: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
	if err := c.Download(context.Background(), FileEntry{URL: "missing", SHA256: sha(nil)}, dst, nil); err == nil {
		t.Error("404 accepted")
	}
	if missCalls != 1 {
		t.Errorf("404 was retried: %d calls", missCalls)
	}
}
