package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/detect/steam"
	"github.com/Knurobroddy/crackers-tui/internal/detect/userfolder"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/servers"
	"github.com/Knurobroddy/crackers-tui/internal/testsupport/fakeworld"
)

const (
	flowWidth   = 120
	flowHeight  = 40
	flowTimeout = 10 * time.Second

	// wrapBoundary is the optional line break the panel's hard wrap may
	// have inserted between two runes of a long path, once box-drawing
	// decoration and per-line whitespace have already been stripped.
	wrapBoundary = `\n?`

	gameRootPlaceholder = "<GAME_ROOT>"
)

// boxDrawingRegexp matches the panel's box-drawing border (lipgloss's
// RoundedBorder) and the header banner's block-art glyphs — the Unicode Box
// Drawing and Block Elements ranges — so normalizePaths can reduce a screen
// to its text content.
var boxDrawingRegexp = regexp.MustCompile(`[\x{2500}-\x{259F}]`)

// Word-wrap breaks the panel may put right before or right after a replaced
// path; see joinPlaceholderWraps.
var (
	breakBeforePlaceholderRegexp = regexp.MustCompile(`\n` + gameRootPlaceholder)
	breakAfterPlaceholderRegexp  = regexp.MustCompile(gameRootPlaceholder + `\n\.`)
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii) // golden files without ANSI colors
	os.Exit(m.Run())
}

func newFlowModel(t *testing.T, w *fakeworld.World) *model {
	t.Helper()
	client, err := remote.NewClient(w.LibraryURL, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(client, "0.1.0", engine.OSFiles{})
	serverStore := servers.New(w.ServersFile)
	serverFolders := userfolder.New(serverStore, "windows")
	application := app.New(app.Deps{
		Catalog:       client,
		Detector:      detect.NewRegistry(steam.New([]string{w.SteamRoot}, "windows"), serverFolders),
		Installer:     eng,
		Status:        eng,
		Servers:       serverStore,
		ServerMatcher: serverFolders,
	})
	return newModel(Deps{AppVersion: "0.1.0", App: application, LogPath: "crackers-modinst.log"})
}

func waitFor(t *testing.T, tm *teatest.TestModel, text string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(text)) },
		teatest.WithDuration(flowTimeout))
}

func finalView(t *testing.T, tm *teatest.TestModel) []byte {
	t.Helper()
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	final := tm.FinalModel(t, teatest.WithFinalTimeout(flowTimeout))
	return []byte(final.View())
}

func TestFlow_InstallFromMainMenu_vanillaGame_showsInstalledResult(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	tm := teatest.NewTestModel(t, newFlowModel(t, w), teatest.WithInitialTermSize(flowWidth, flowHeight))

	waitFor(t, tm, "Not installed")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // choose Valheim
	waitFor(t, tm, "Install pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Install pack
	waitFor(t, tm, "Test pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // choose the pack
	waitFor(t, tm, "Install Test pack?")
	tm.Send(tea.KeyMsg{Type: tea.KeyLeft}) // move to "Yes"
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Installed Test pack into")

	view := finalView(t, tm)
	if _, err := os.Stat(filepath.Join(w.GameRoot, ".crackers-modinst.json")); err != nil {
		t.Fatalf("marker missing after UI install: %v", err)
	}
	golden.RequireEqual(t, normalizePaths(view, w))
}

func TestFlow_Install_manualModsPresent_offersCleanupThenInstalls(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	leftover := filepath.Join(w.GameRoot, "BepInEx", "plugins", "Old.dll")
	writeLeftoverFile(t, leftover)
	tm := teatest.NewTestModel(t, newFlowModel(t, w), teatest.WithInitialTermSize(flowWidth, flowHeight))

	waitFor(t, tm, "Not installed")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Install pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Test pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Install Test pack?")
	tm.Send(tea.KeyMsg{Type: tea.KeyLeft})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Leftover mod files found")
	tm.Send(tea.KeyMsg{Type: tea.KeyLeft})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Installed Test pack into")

	view := finalView(t, tm)
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("leftover file still present after cleanup+install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.GameRoot, ".crackers-modinst.json")); err != nil {
		t.Fatalf("marker missing after UI install: %v", err)
	}
	golden.RequireEqual(t, normalizePaths(view, w))
}

// writeLeftoverFile creates a manually-installed mod file the leftovers
// screen should offer to delete.
func writeLeftoverFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("an old manually-installed mod"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFlow_RemoveInstalledPack_showsVanillaResult(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{})
	installFirst(t, w)
	tm := teatest.NewTestModel(t, newFlowModel(t, w), teatest.WithInitialTermSize(flowWidth, flowHeight))

	waitFor(t, tm, "Installed: Test pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Remove pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Remove Test pack?")
	tm.Send(tea.KeyMsg{Type: tea.KeyLeft})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "is back to vanilla")

	golden.RequireEqual(t, normalizePaths(finalView(t, tm), w))
}

func TestFlow_AddServerThenInstall_writesPackIntoServerFolder(t *testing.T) {
	w := fakeworld.New(t, fakeworld.Options{WithServer: true})
	tm := teatest.NewTestModel(t, newFlowModel(t, w), teatest.WithInitialTermSize(flowWidth, flowHeight))

	waitFor(t, tm, "+ Add server")
	// Rows: ── Games ──, Valheim (cursor), ── Servers ──, + Add server.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Add a server")
	// One message, as a paste: tm.Type splits "ł" into bytes.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(w.ServerRoot)})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Saved the server folder")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // continue: detect again
	waitFor(t, tm, "Valheim Dedicated Server — Not installed")
	// Rows: ── Games ──, Valheim (cursor), ── Servers ──, the server.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Install pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Test server pack")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Stop the server first.")
	tm.Send(tea.KeyMsg{Type: tea.KeyLeft})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Installed Test server pack into")

	finalView(t, tm)
	if _, err := os.Stat(filepath.Join(w.ServerRoot, ".crackers-modinst.json")); err != nil {
		t.Fatalf("marker missing in the server folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.GameRoot, ".crackers-modinst.json")); !os.IsNotExist(err) {
		t.Fatalf("game folder changed (marker err = %v)", err)
	}
}

// installFirst installs the test pack directly, without the UI.
func installFirst(t *testing.T, w *fakeworld.World) {
	t.Helper()
	flowModel := newFlowModel(t, w)
	application := flowModel.deps.App
	library, err := application.LoadLibrary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	games := application.Detect(t.Context(), library).Games
	pack, _ := library.Index.Pack("valheim-test")
	if _, err := application.Run(t.Context(), app.Operation{Kind: app.OpInstall, Game: games[0], Pack: pack}, nil); err != nil {
		t.Fatal(err)
	}
}

// normalizePaths reduces view to its text content — stripping box-drawing
// decoration, trimming each line and dropping blank ones — then replaces
// the run-specific temp paths with a fixed placeholder, tolerating a wrap
// between any two of a path's runes, right before the path or right before
// the full stop after it. Normalizing to text content, rather
// than matching the raw bordered layout, keeps the golden files independent
// of the panel's row count: whether w.GameRoot is short enough to fit one
// line (e.g. a CI runner's shorter temp dir) or long enough to hard-wrap (a
// Windows dev machine's temp dir), the normalized output is identical; see
// TestNormalizePaths_shortAndWrappedPath_sameOutput.
func normalizePaths(view []byte, w *fakeworld.World) []byte {
	text := stripDecoration(string(view))
	text = replaceWrappedPath(text, w.GameRoot)
	text = replaceWrappedPath(text, filepath.ToSlash(w.GameRoot))
	return []byte(joinPlaceholderWraps(dropBlankLines(text)))
}

// stripDecoration removes box-drawing characters from each line, then trims
// the line's remaining whitespace.
func stripDecoration(view string) string {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(boxDrawingRegexp.ReplaceAllString(line, ""))
	}
	return strings.Join(lines, "\n")
}

// replaceWrappedPath replaces every occurrence of path in text with
// gameRootPlaceholder, tolerating wrapBoundary between any two of its runes and a
// space rune of path itself being consumed by a word-wrap break (the panel
// wraps on whitespace first, and a path can contain one, e.g. "Gry Steam ł").
func replaceWrappedPath(text, path string) string {
	if path == "" {
		return text
	}
	var pattern strings.Builder
	for i, r := range []rune(path) {
		if i > 0 {
			pattern.WriteString(wrapBoundary)
		}
		pattern.WriteString(runePattern(r))
	}
	return regexp.MustCompile(pattern.String()).ReplaceAllString(text, gameRootPlaceholder)
}

// joinPlaceholderWraps undoes a word-wrap break the panel put right before
// the replaced path (in place of the space after "into" or "from") or right
// before the full stop after it; whether such a break exists depends only on
// the temp dir's length. In every screen the flows compare, the path follows
// text on the same line, so a line break before the placeholder is a wrap.
func joinPlaceholderWraps(text string) string {
	text = breakBeforePlaceholderRegexp.ReplaceAllString(text, " "+gameRootPlaceholder)
	return breakAfterPlaceholderRegexp.ReplaceAllString(text, gameRootPlaceholder+".")
}

// runePattern matches r literally, except a space, which the panel's
// word-wrap may replace with a line break instead of leaving it in the text.
func runePattern(r rune) string {
	if r == ' ' {
		return `[ \n]`
	}
	return regexp.QuoteMeta(string(r))
}

// dropBlankLines removes lines left empty by stripDecoration.
func dropBlankLines(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// TestNormalizePaths_shortAndWrappedPath_sameOutput proves normalizePaths is
// independent of whether, and where, the panel had to hard-wrap the path: a
// short path that fits on one line (as on a CI runner with a short temp
// dir), a long path split mid-token across two lines (as on a Windows dev
// machine's deep temp dir), and a long path split exactly on a space the
// path itself contains (lipgloss word-wraps on whitespace first, and "Gry
// Steam ł" has one), or a path moved whole to the next line, or followed by
// a full stop that wrapped, must all normalize to the exact same text.
func TestNormalizePaths_shortAndWrappedPath_sameOutput(t *testing.T) {
	short := &fakeworld.World{GameRoot: `C:\g\Valheim`}
	long := &fakeworld.World{
		GameRoot: `C:\Users\test\AppData\Local\Temp\ALongTestNameThatOverflowsPanel1234567890\001\Gry Steam ł\steamapps\common\Valheim`,
	}
	spaceWrap := &fakeworld.World{GameRoot: `C:\st\TestFlowShort0123456789\001\Gry Steam ł\steamapps\common\Valheim`}
	want := []byte("Installed Test pack into <GAME_ROOT>.")

	runes := []rune(long.GameRoot)
	split := len(runes) / 2
	spaceIdx := strings.IndexRune(spaceWrap.GameRoot, ' ')
	for _, tc := range []struct {
		name string
		w    *fakeworld.World
		view string
	}{
		{"unwrapped", short, "│  Installed Test pack into " + short.GameRoot + ".  │\n"},
		{"wrapped mid-token", long, "│  Installed Test pack into " + string(runes[:split]) + "  │\n" +
			"│  " + string(runes[split:]) + ".  │\n"},
		{"wrapped on the path's own space", spaceWrap, "│  Installed Test pack into " + spaceWrap.GameRoot[:spaceIdx] + "│\n" +
			"│  " + spaceWrap.GameRoot[spaceIdx+1:] + ".  │\n"},
		{"wrapped before path", short, "│  Installed Test pack into  │\n" +
			"│  " + short.GameRoot + ".  │\n"},
		{"wrapped before full stop", short, "│  Installed Test pack into " + short.GameRoot + "│\n" +
			"│  .  │\n"},
	} {
		if got := normalizePaths([]byte(tc.view), tc.w); !bytes.Equal(got, want) {
			t.Errorf("%s: normalizePaths() = %q, want %q", tc.name, got, want)
		}
	}
}
