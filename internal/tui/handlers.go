package tui

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

func (m *model) onWindowSize(msg tea.WindowSizeMsg) tea.Cmd {
	m.width, m.height = msg.Width, msg.Height
	if m.form != nil {
		m.form = m.form.WithWidth(m.innerWidth())
	}
	return m.onForm(msg)
}

func (m *model) onKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "q":
		return m.quit()
	case "esc":
		if cmd, ok := m.back(); ok {
			return cmd
		}
	}
	switch m.screen {
	case screenResult:
		if msg.String() == "enter" || msg.String() == " " {
			return m.startDetect()
		}
		return nil
	case screenUpdated:
		return tea.Quit
	}
	return m.onForm(msg)
}

// quit refuses to quit while an operation runs, so it is never cut off
// halfway.
func (m *model) quit() tea.Cmd {
	if m.busy {
		m.pleaseWait = true
		return nil
	}
	return tea.Quit
}

func (m *model) onSpinnerTick(msg spinner.TickMsg) tea.Cmd {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

func (m *model) onLibraryLoaded(msg remoteLoadedMsg) tea.Cmd {
	m.remoteDone = true
	m.library, m.remoteErr = msg.library, msg.err
	if msg.err != nil {
		slog.Error("load library", "err", msg.err)
		m.detectDone = true
		return m.proceed()
	}
	return m.startDetect()
}

func (m *model) onUpdateChecked(msg updateCheckedMsg) tea.Cmd {
	m.updateDone = true
	if msg.err != nil {
		slog.Warn("check for app update", "err", msg.err)
	}
	m.updateRelease = msg.release
	return m.proceed()
}

func (m *model) onGamesDetected(msg gamesDetectedMsg) tea.Cmd {
	m.detectDone = true
	m.games = msg.games
	return m.proceed()
}

func (m *model) onUpdateApplied(msg updateAppliedMsg) tea.Cmd {
	m.busy = false
	if msg.err == nil {
		m.screen = screenUpdated
		return nil
	}
	slog.Error("apply app update", "err", msg.err)
	text := fmt.Sprintf("The update failed:\n\n%s\n\nLog file: %s", app.UserMessage(msg.err), m.deps.LogPath)
	if remote.IsUpdateRequired(m.remoteErr) {
		return m.showError(text, retryUpdate)
	}
	m.updateDeclined = true
	m.resultOK = false
	m.resultText = text
	m.screen = screenResult
	return nil
}

func (m *model) onProgress(msg progressMsg) tea.Cmd {
	event := engine.Event(msg)
	switch event.Kind {
	case engine.EventStep:
		m.stepText = event.Message
	case engine.EventDownload:
		m.download = event
	}
	return m.listenCmd()
}

// onOperationDone offers to delete leftovers that blocked an install, once;
// every other outcome goes to the result screen.
func (m *model) onOperationDone(msg opDoneMsg) tea.Cmd {
	m.busy, m.pleaseWait = false, false
	var leftoversErr *engine.LeftoversError
	isInstall := m.op.Kind == app.OpInstall || m.op.Kind == app.OpReinstall
	if errors.As(msg.err, &leftoversErr) && !m.op.CleanLeftovers && isInstall {
		return m.openLeftoversConfirm(leftoversErr)
	}
	m.resultOK = msg.err == nil
	m.resultText = m.resultMessage(msg.err, msg.outcome.Removed)
	m.screen = screenResult
	return nil
}

func (m *model) onForm(msg tea.Msg) tea.Cmd {
	if m.form == nil {
		return nil
	}
	updated, cmd := m.form.Update(msg)
	if form, ok := updated.(*huh.Form); ok {
		m.form = form
	}
	if m.form.State == huh.StateCompleted {
		return tea.Batch(cmd, m.formDone())
	}
	return cmd
}

// formDone handles a completed form on the current screen.
func (m *model) formDone() tea.Cmd {
	switch m.screen {
	case screenError:
		return m.formDoneError()
	case screenUpdatePrompt:
		return m.formDoneUpdatePrompt()
	case screenMain:
		return m.formDoneMain()
	case screenGame:
		return m.formDoneGame()
	case screenPacks:
		return m.formDonePacks()
	case screenConfirm:
		return m.formDoneConfirm()
	}
	return nil
}

func (m *model) formDoneError() tea.Cmd {
	if m.form.GetString("choice") != "retry" {
		return tea.Quit
	}
	if m.retryAction == retryUpdate {
		return m.startUpdate()
	}
	return m.reload()
}

func (m *model) formDoneUpdatePrompt() tea.Cmd {
	if m.form.GetBool("confirm") {
		return m.startUpdate()
	}
	m.updateDeclined = true
	if remote.IsUpdateRequired(m.remoteErr) {
		return tea.Quit
	}
	return m.proceed()
}

func (m *model) formDoneMain() tea.Cmd {
	switch choice := m.form.GetString("choice"); choice {
	case "detect":
		return m.reload() // fresh library + detection
	case "quit":
		return tea.Quit
	default:
		i, err := strconv.Atoi(choice)
		if err != nil || i < 0 || i >= len(m.games) {
			return m.openMain()
		}
		m.selected = i
		return m.openGame()
	}
}

func (m *model) formDoneGame() tea.Cmd {
	game := m.games[m.selected]
	switch m.form.GetString("choice") {
	case "install":
		m.packsKind = app.OpInstall
		return m.openPacks()
	case "cleanup":
		if packs := m.library.Index.PacksFor(game.Def.ID); len(packs) == 1 {
			return m.openConfirm(app.OpCleanLeftovers, packs[0], screenGame)
		}
		m.packsKind = app.OpCleanLeftovers
		return m.openPacks()
	case "remove":
		return m.openConfirm(app.OpRemove, game.Status.Pack, screenGame)
	case "reinstall":
		return m.openConfirm(app.OpReinstall, game.Status.Pack, screenGame)
	}
	return m.openMain()
}

func (m *model) formDonePacks() tea.Cmd {
	pack, ok := m.library.Index.Pack(m.form.GetString("choice"))
	if !ok {
		return m.openGame()
	}
	if m.packsKind == app.OpCleanLeftovers {
		return m.openConfirm(app.OpCleanLeftovers, pack, screenPacks)
	}
	if marker := m.games[m.selected].Status.Marker; marker != nil && marker.PackID == pack.ID {
		return m.openConfirm(app.OpReinstall, pack, screenPacks)
	}
	return m.openConfirm(app.OpInstall, pack, screenPacks)
}

func (m *model) formDoneConfirm() tea.Cmd {
	if m.form.GetBool("confirm") {
		return m.startOp()
	}
	cmd, _ := m.back()
	return cmd
}
