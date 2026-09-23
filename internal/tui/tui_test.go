package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

var (
	testGames = &remote.Games{Games: []detect.GameDef{{ID: "valheim", Name: "Valheim", NotFoundHint: "Use Proton."}}}
	testIndex = &remote.Index{Packs: []remote.PackRef{{ID: "p", GameID: "valheim", Name: "P"}}}
)

// startup feeds the startup messages in order and returns the model.
func startup(t *testing.T, withUpdater bool, msgs ...tea.Msg) *model {
	t.Helper()
	m := newModel(Deps{AppVersion: "0.1.0", LogPath: "/tmp/x.log"})
	m.updateDone = !withUpdater
	for _, msg := range msgs {
		m.Update(msg)
	}
	return m
}

func TestStartupWaitsForUpdateCheck(t *testing.T) {
	m := startup(t, true, remoteLoadedMsg{err: &remote.UpdateRequiredError{Doc: "index.json", Reason: "x"}})
	if m.screen != scrLoading {
		t.Fatalf("screen = %v, want loading while the update check runs", m.screen)
	}
	m.Update(updateCheckedMsg{rel: &update.Release{Version: "9.0.0"}})
	if m.screen != scrUpdatePrompt {
		t.Fatalf("screen = %v, want update prompt", m.screen)
	}
	if !strings.Contains(m.View(), "Update Crackers Modinst to v9.0.0?") || !strings.Contains(m.View(), "Choosing No quits") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestForcedUpdateWithoutRelease(t *testing.T) {
	m := startup(t, true,
		remoteLoadedMsg{err: &remote.UpdateRequiredError{Doc: "games.json", Reason: "requires version 9.0.0 or newer"}},
		updateCheckedMsg{})
	if m.screen != scrError || !strings.Contains(m.View(), "Please update Crackers Modinst") {
		t.Fatalf("screen = %v, view:\n%s", m.screen, m.View())
	}
}

func TestRemoteErrorShowsRetry(t *testing.T) {
	m := startup(t, false, remoteLoadedMsg{err: errTest("connection refused")})
	v := m.View()
	if m.screen != scrError || !strings.Contains(v, "connection refused") || !strings.Contains(v, "Retry") || !strings.Contains(v, "/tmp/x.log") {
		t.Fatalf("screen = %v, view:\n%s", m.screen, v)
	}
}

func TestOptionalUpdatePromptThenMenu(t *testing.T) {
	m := startup(t, true, updateCheckedMsg{rel: &update.Release{Version: "0.2.0"}})
	m.games, m.index, m.remoteDone = testGames, testIndex, true
	m.Update(detectedMsg{})
	if m.screen != scrUpdatePrompt || strings.Contains(m.View(), "Choosing No quits") {
		t.Fatalf("screen = %v, view:\n%s", m.screen, m.View())
	}
	m.updateDeclined = true
	m.proceed()
	if m.screen != scrMain {
		t.Fatalf("screen = %v, want main", m.screen)
	}
}

func TestNoGamesScreenShowsHints(t *testing.T) {
	m := startup(t, false)
	m.games, m.index, m.remoteDone = testGames, testIndex, true
	m.Update(detectedMsg{})
	v := m.View()
	for _, want := range []string{"No supported games were detected", "Valheim", "Use Proton.", "Detect again", "Quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestQuitBlockedWhileBusy(t *testing.T) {
	m := startup(t, false)
	m.screen, m.busy = scrProgress, true
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd != nil {
		t.Fatal("quit command returned while busy")
	}
	if !m.pleaseWait || !strings.Contains(m.View(), "Please wait") {
		t.Errorf("no please-wait notice:\n%s", m.View())
	}
	m.Update(opDoneMsg{err: errTest("boom")})
	if m.busy || m.screen != scrResult || !strings.Contains(m.View(), "boom") || !strings.Contains(m.View(), "/tmp/x.log") {
		t.Errorf("result view:\n%s", m.View())
	}
}

func TestMainMenuRows(t *testing.T) {
	m := startup(t, false)
	m.games, m.index, m.remoteDone = testGames, testIndex, true
	m.Update(detectedMsg{rows: []gameRow{{
		Result: detect.Result{GameID: "valheim", RootDir: "/g"},
		Game:   testGames.Games[0],
		Status: engine.Status{State: engine.UpdateAvailable, Marker: &engine.Marker{PackName: "P", PackVersion: "1"}},
	}}})
	if v := m.View(); m.screen != scrMain || !strings.Contains(v, "Valheim — Update available: P 1") {
		t.Fatalf("view:\n%s", v)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestHeaderFitsTerminal(t *testing.T) {
	crackers, _ := logoParts()
	for _, tc := range []struct {
		w, h      int
		wantWide  bool
		wantStack bool
	}{
		{140, 40, true, false},
		{140, 7, true, false},  // 6 art lines + version
		{120, 30, false, true}, // 125-column art does not fit: CRACKERS over MODINST
		{60, 30, false, false},
		{140, 6, false, false},
	} {
		h := header("v0.1.0", tc.w, tc.h)
		wide, stacked := false, false
		for _, line := range strings.Split(h, "\n") {
			if strings.Contains(line, crackers[0]) {
				wide = strings.Contains(line, "███╗   ███╗")
				stacked = !wide
			}
		}
		if wide != tc.wantWide || stacked != tc.wantStack {
			t.Errorf("%dx%d: wide=%v stacked=%v\n%s", tc.w, tc.h, wide, stacked, h)
		}
		if !tc.wantWide && !tc.wantStack && !strings.Contains(h, "Crackers Modinst v0.1.0") {
			t.Errorf("%dx%d: no text title:\n%s", tc.w, tc.h, h)
		}
	}
}

func TestViewSpansTerminal(t *testing.T) {
	for _, size := range [][2]int{{140, 45}, {120, 30}, {80, 24}} {
		m := startup(t, false)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.games, m.index, m.remoteDone = testGames, testIndex, true
		m.Update(detectedMsg{})
		v := m.View()
		lines := strings.Split(v, "\n")
		if len(lines) != size[1] {
			t.Errorf("%v: %d lines, want %d", size, len(lines), size[1])
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > size[0] {
				t.Errorf("%v: line %d is %d wide", size, i, w)
			}
		}
		if !strings.Contains(lines[len(lines)-1], "q quit") {
			t.Errorf("%v: help not on the last line: %q", size, lines[len(lines)-1])
		}
	}
}

func TestLeftoversOfferCleanupThenResult(t *testing.T) {
	m := startup(t, false)
	m.games, m.index, m.remoteDone = testGames, testIndex, true
	row := gameRow{Result: detect.Result{GameID: "valheim", RootDir: "/g"}, Game: testGames.Games[0], Status: engine.Status{State: engine.NotInstalled}}
	m.Update(detectedMsg{rows: []gameRow{row}})
	m.op = pendingOp{kind: actInstall, row: row, pack: testIndex.Packs[0]}
	m.screen, m.busy = scrProgress, true

	m.Update(opDoneMsg{err: &engine.LeftoversError{Root: "/g", Paths: []string{"BepInEx/", "winhttp.dll"}}})
	v := m.View()
	if m.screen != scrConfirm || !m.op.clean || !strings.Contains(v, "Leftover mod files found") ||
		!strings.Contains(v, "BepInEx/ (folder)") || !strings.Contains(v, "winhttp.dll") {
		t.Fatalf("screen = %v clean = %v\n%s", m.screen, m.op.clean, v)
	}

	// A second leftovers error after the user agreed is shown as a failure, not offered again.
	m.screen = scrProgress
	m.Update(opDoneMsg{err: &engine.LeftoversError{Root: "/g", Paths: []string{"x"}}})
	if m.screen != scrResult || m.resultOK {
		t.Errorf("screen = %v", m.screen)
	}

	m.op = pendingOp{kind: actCleanup, row: row, pack: testIndex.Packs[0]}
	m.Update(opDoneMsg{removed: []string{"BepInEx/", "winhttp.dll"}})
	if v := m.View(); !strings.Contains(v, "Removed 2 leftover item(s)") || !strings.Contains(v, "BepInEx/ (folder)") {
		t.Errorf("cleanup result:\n%s", v)
	}
	m.Update(opDoneMsg{})
	if v := m.View(); !strings.Contains(v, "No leftover files of P were found") {
		t.Errorf("empty cleanup result:\n%s", v)
	}
}

func TestFailedUpdateAfterLibraryErrorReloads(t *testing.T) {
	m := startup(t, true, remoteLoadedMsg{err: errTest("connection refused")}, updateCheckedMsg{rel: &update.Release{Version: "9.0.0"}})
	m.startUpdate()
	m.Update(updateAppliedMsg{err: errTest("download failed")})
	if m.screen != scrResult {
		t.Fatalf("screen = %v, want result", m.screen)
	}
	// Continuing must reload the library, not detect games against a nil index.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("no command after continuing")
	}
	if m.screen != scrLoading || m.remoteDone {
		t.Fatalf("screen = %v, remoteDone = %v, want library reload", m.screen, m.remoteDone)
	}
}
