package steam

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
)

const fixtures = "../../../testdata/steam"

func TestReadLibraryFoldersNewFormatEscapedPaths(t *testing.T) {
	got, err := ReadLibraryFolders(filepath.Join(fixtures, "libraryfolders_new.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	// The parser must turn "\\" into "\" (the fixture stores escaped Windows paths),
	// and "Path" must match case-insensitively.
	want := []string{`C:\Program Files (x86)\Steam`, `D:\SteamLibrary`, `\\nas\games\Steam`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReadLibraryFoldersOldFormat(t *testing.T) {
	got, err := ReadLibraryFolders(filepath.Join(fixtures, "libraryfolders_old.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`D:\SteamLibrary`, "/mnt/games/SteamLibrary"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReadInstallDir(t *testing.T) {
	for file, want := range map[string]string{
		"appmanifest_892970.acf": "Valheim",
		"appmanifest_casing.acf": "Valheim Beta", // "appstate" / "InstallDir"
	} {
		got, err := ReadInstallDir(filepath.Join(fixtures, file))
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", file, got, err, want)
		}
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.acf")
	os.WriteFile(bad, []byte("\"AppState\"\n{\n\t\"installdir\"\t\t\"..\"\n}\n"), 0o644)
	if _, err := ReadInstallDir(bad); err == nil {
		t.Error(`installdir ".." accepted`)
	}
	if _, err := ReadInstallDir(filepath.Join(dir, "missing.acf")); !os.IsNotExist(err) {
		t.Errorf("missing file: err = %v", err)
	}
}

var valheim = detect.GameDef{
	ID:       "valheim",
	Name:     "Valheim",
	Strategy: "steam",
	Steam:    &detect.SteamDef{AppID: 892970},
	Builds: []detect.BuildDef{
		{ID: "windows", OS: "windows", Anchor: "valheim.exe", Files: "windows"},
		{ID: "linux_proton", OS: "linux", Anchor: "valheim.exe", Files: "windows"},
	},
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// vdfPath escapes a path the way Steam writes it into VDF files.
func vdfPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }

// fakeLinuxSteam builds ~/.local/share/Steam with a second library under
// ~/games/lib2 and installs Valheim into lib2 with the given files.
func fakeLinuxSteam(t *testing.T, home string, gameFiles ...string) (steamRoot, lib2 string) {
	t.Helper()
	steamRoot = filepath.Join(home, ".local", "share", "Steam")
	lib2 = filepath.Join(home, "games", "lib2")
	writeFile(t, filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf"),
		"\"libraryfolders\"\n{\n"+
			"\t\"0\"\n\t{\n\t\t\"path\"\t\t\""+vdfPath(steamRoot)+"\"\n\t}\n"+
			"\t\"1\"\n\t{\n\t\t\"path\"\t\t\""+vdfPath(lib2)+"\"\n\t}\n}\n")
	writeFile(t, filepath.Join(lib2, "steamapps", "appmanifest_892970.acf"),
		"\"AppState\"\n{\n\t\"appid\"\t\t\"892970\"\n\t\"installdir\"\t\t\"Valheim\"\n}\n")
	for _, f := range gameFiles {
		writeFile(t, filepath.Join(lib2, "steamapps", "common", "Valheim", f), "x")
	}
	return steamRoot, lib2
}

func linuxStrategy(home string) *Strategy {
	s := New(nil)
	s.GOOS = "linux"
	s.Roots = func() []string { return linuxRootCandidates(home) }
	return s
}

func TestLinuxProtonDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	steamRoot, lib2 := fakeLinuxSteam(t, home, "valheim.exe", "valheim_Data/x")
	// ~/.steam/steam is normally a symlink to ~/.local/share/Steam; it must be deduplicated.
	os.MkdirAll(filepath.Join(home, ".steam"), 0o755)
	symlinked := os.Symlink(steamRoot, filepath.Join(home, ".steam", "steam")) == nil

	s := linuxStrategy(home)
	libs := s.Libraries()
	if len(libs) != 2 {
		t.Fatalf("libraries = %q (symlink created: %v), want root + lib2", libs, symlinked)
	}
	res, err := s.Detect(valheim)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("results = %+v, want 1", res)
	}
	r := res[0]
	wantRoot, _ := filepath.EvalSymlinks(filepath.Join(lib2, "steamapps", "common", "Valheim"))
	gotRoot, _ := filepath.EvalSymlinks(r.RootDir)
	if r.BuildID != "linux_proton" || gotRoot != wantRoot || filepath.Base(r.AnchorPath) != "valheim.exe" {
		t.Errorf("result = %+v", r)
	}
	if r.Extra[detect.ExtraSteamAppID] != "892970" || r.Extra[detect.ExtraSteamLibrary] == "" ||
		len(filepath.SplitList(r.Extra[detect.ExtraSteamLibraries])) != 2 {
		t.Errorf("extra = %+v", r.Extra)
	}
}

func TestLinuxNativeNotDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakeLinuxSteam(t, home, "valheim.x86_64", "valheim_Data/x") // native build: no valheim.exe
	res, err := linuxStrategy(home).Detect(valheim)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("native Linux install detected: %+v", res)
	}
}

func TestBrokenLibrarySkipped(t *testing.T) {
	home := t.TempDir()
	_, lib2 := fakeLinuxSteam(t, home, "valheim.exe")
	// A second root with a corrupt libraryfolders.vdf must not break detection.
	flatpak := filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam")
	writeFile(t, filepath.Join(flatpak, "steamapps", "libraryfolders.vdf"), "\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\"unterminated")
	writeFile(t, filepath.Join(flatpak, "steamapps", "appmanifest_892970.acf"), "garbage {{{")
	res, err := linuxStrategy(home).Detect(valheim)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve like detection does (e.g. 8.3 short names such as RUNNER~1 in TEMP on Windows).
	wantPrefix, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(lib2)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !strings.HasPrefix(res[0].Extra[detect.ExtraSteamLibrary], wantPrefix) {
		t.Errorf("results = %+v", res)
	}
}

func TestMissingAppIDIsError(t *testing.T) {
	g := valheim
	g.Steam = nil
	if _, err := linuxStrategy(t.TempDir()).Detect(g); err == nil {
		t.Error("missing appid accepted")
	}
}
