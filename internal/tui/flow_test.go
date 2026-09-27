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
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/testsupport/fakeworld"
)

const (
	flowWidth   = 120
	flowHeight  = 40
	flowTimeout = 10 * time.Second

	// borderRune is the panel's vertical border (lipgloss.RoundedBorder),
	// which a hard-wrapped path is split around.
	borderRune = "│"
)

// wrapBoundary matches the border and line-fill padding lipgloss inserts
// where it hard-wraps a token (a long temp path) that overflows one line of
// the panel.
var wrapBoundary = "(?:[ \t]*" + borderRune + "[ \t]*\r?\n[ \t]*" + borderRune + "[ \t]*)?"

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
	application := app.New(app.Deps{
		Catalog:   client,
		Detector:  detect.NewRegistry(steam.New([]string{w.SteamRoot}, "windows")),
		Installer: eng,
		Status:    eng,
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
	if err := os.MkdirAll(filepath.Join(w.GameRoot, "BepInEx", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
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

	golden.RequireEqual(t, normalizePaths(finalView(t, tm), w))
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

// installFirst installs the test pack directly, without the UI.
func installFirst(t *testing.T, w *fakeworld.World) {
	t.Helper()
	flowModel := newFlowModel(t, w)
	application := flowModel.deps.App
	library, err := application.LoadLibrary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	games := application.DetectGames(t.Context(), library)
	pack, _ := library.Index.Pack("valheim-test")
	if _, err := application.Run(t.Context(), app.Operation{Kind: app.OpInstall, Game: games[0], Pack: pack}, nil); err != nil {
		t.Fatal(err)
	}
}

// normalizePaths replaces the per-run temp paths so golden files are stable.
// w.GameRoot embeds a random, variable-length suffix from t.TempDir(), so on
// Windows it can be long enough to be hard-wrapped inside the 96-column
// panel; replaceWrappedPath tolerates that, and trimBorderPadding removes
// the resulting variable-length line-fill padding before the border.
func normalizePaths(view []byte, w *fakeworld.World) []byte {
	view = replaceWrappedPath(view, w.GameRoot)
	view = replaceWrappedPath(view, filepath.ToSlash(w.GameRoot))
	return trimBorderPadding(view)
}

// replaceWrappedPath replaces every occurrence of path in view with
// "<GAME_ROOT>", tolerating a wrapBoundary between any two of its runes.
func replaceWrappedPath(view []byte, path string) []byte {
	if path == "" {
		return view
	}
	var pattern strings.Builder
	for i, r := range []rune(path) {
		if i > 0 {
			pattern.WriteString(wrapBoundary)
		}
		pattern.WriteString(regexp.QuoteMeta(string(r)))
	}
	return regexp.MustCompile(pattern.String()).ReplaceAll(view, []byte("<GAME_ROOT>"))
}

// trimBorderPadding drops the line-fill spaces lipgloss adds before the
// panel's border; their count depends on how long the replaced path was.
func trimBorderPadding(view []byte) []byte {
	return regexp.MustCompile(`[ \t]+`+borderRune).ReplaceAll(view, []byte(borderRune))
}
