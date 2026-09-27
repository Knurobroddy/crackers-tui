package tui

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

const (
	libraryTimeout     = 2 * time.Minute
	updateCheckTimeout = 5 * time.Second
	updateApplyTimeout = 10 * time.Minute
	eventBufferSize    = 64
)

type loadLibrary struct{ app *app.App }

type checkUpdate struct{ app *app.App }

type applyUpdate struct {
	app     *app.App
	release *update.Release
}

type detectGames struct {
	app     *app.App
	library *app.Library
}

type listener struct{ events <-chan tea.Msg }

type progressSender struct{ events chan<- tea.Msg }

// loadLibraryCmd loads the remote library.
func (m *model) loadLibraryCmd() tea.Cmd {
	return loadLibrary{app: m.deps.App}.run
}

func (m *model) checkUpdateCmd() tea.Cmd {
	return checkUpdate{app: m.deps.App}.run
}

func (m *model) applyUpdateCmd() tea.Cmd {
	return applyUpdate{app: m.deps.App, release: m.updateRelease}.run
}

func (m *model) detectGamesCmd() tea.Cmd {
	return detectGames{app: m.deps.App, library: m.library}.run
}

func (m *model) listenCmd() tea.Cmd {
	return listener{events: m.events}.next
}

func (c loadLibrary) run() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), libraryTimeout)
	defer cancel()
	library, err := c.app.LoadLibrary(ctx)
	return remoteLoadedMsg{library: library, err: err}
}

func (c checkUpdate) run() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	release, err := c.app.CheckUpdate(ctx)
	return updateCheckedMsg{release: release, err: err}
}

func (c applyUpdate) run() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), updateApplyTimeout)
	defer cancel()
	return updateAppliedMsg{err: c.app.ApplyUpdate(ctx, c.release)}
}

func (c detectGames) run() tea.Msg {
	return gamesDetectedMsg{games: c.app.DetectGames(context.Background(), c.library)}
}

func (l listener) next() tea.Msg {
	msg, ok := <-l.events
	if !ok {
		return nil
	}
	return msg
}

func runOperation(application *app.App, op app.Operation, events chan<- tea.Msg) {
	defer close(events)
	defer recoverOperation(events)
	outcome, err := application.Run(context.Background(), op, progressSender{events: events}.send)
	events <- opDoneMsg{outcome: outcome, err: err}
}

func recoverOperation(events chan<- tea.Msg) {
	if r := recover(); r != nil {
		slog.Error("operation panicked", "panic", r, "stack", string(debug.Stack()))
		events <- opDoneMsg{err: fmt.Errorf("internal error: %v", r)}
	}
}

func (p progressSender) send(event engine.Event) { p.events <- progressMsg(event) }
