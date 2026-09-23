// Package tui is the Bubble Tea front end (ADR §6). It talks to detect,
// engine and update only through plain functions and progress callbacks.
package tui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/logx"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

// Deps are the collaborators of the UI.
type Deps struct {
	AppVersion string
	Client     *remote.Client
	Engine     *engine.Engine
	Detector   *detect.Registry
	Updater    *update.Updater // nil disables the update check
	LogPath    string
	Log        *slog.Logger
}

// Run starts the TUI and blocks until the user quits.
func Run(d Deps) error {
	_, err := tea.NewProgram(newModel(d), tea.WithAltScreen()).Run()
	return err
}

type screen int

const (
	scrLoading screen = iota
	scrError
	scrUpdatePrompt
	scrUpdating
	scrUpdated
	scrMain
	scrGame
	scrPacks
	scrConfirm
	scrProgress
	scrResult
)

type action int

const (
	actInstall action = iota
	actReinstall
	actRemove
	actCleanup // remove leftover mod files without installing
)

// actionTexts holds the fixed texts of each action.
var actionTexts = map[action]struct{ verb, failed string }{
	actInstall:   {"Install", "Installing the pack failed"},
	actReinstall: {"Reinstall", "Reinstalling the pack failed"},
	actRemove:    {"Remove", "Removing the pack failed"},
	actCleanup:   {"Clean up", "Removing leftover mod files failed"},
}

func (a action) verb() string { return actionTexts[a].verb }

// gameRow is one detected game install with its status.
type gameRow struct {
	Result detect.Result
	Game   detect.GameDef
	Files  string // the build's "files" key
	Status engine.Status
}

type pendingOp struct {
	kind  action
	row   gameRow
	pack  remote.PackRef
	clean bool // install: delete leftovers first (user confirmed)
}

// Messages.
type (
	remoteLoadedMsg struct {
		games *remote.Games
		index *remote.Index
		err   error
	}
	updateCheckedMsg struct {
		rel *update.Release
		err error
	}
	updateAppliedMsg struct{ err error }
	detectedMsg      struct{ rows []gameRow }
	progressMsg      engine.Event
	opDoneMsg        struct {
		err     error
		removed []string // actCleanup: what was deleted
	}
)

type errKind int

const (
	errRemote errKind = iota // Retry reloads the library
	errUpdate                // Retry re-applies the forced update
)

type model struct {
	d             Deps
	width, height int
	screen        screen
	spinner       spinner.Model
	bar           progress.Model
	form          *huh.Form
	loadingText   string

	// startup state
	remoteDone, detectDone, updateDone bool
	games                              *remote.Games
	index                              *remote.Index
	remoteErr                          error
	updateRel                          *update.Release
	updateDeclined                     bool

	errText string
	errKind errKind

	rows        []gameRow
	cur         int
	op          pendingOp
	confirmBack screen
	packsAction action // what choosing on the pack list does (install or cleanup)

	// operation in progress
	busy       bool
	pleaseWait bool
	stepText   string
	dl         engine.Event
	events     chan tea.Msg

	resultOK   bool
	resultText string
}

func newModel(d Deps) *model {
	d.Log = logx.OrDiscard(d.Log)
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accentStyle
	return &model{
		d:           d,
		screen:      scrLoading,
		spinner:     sp,
		bar:         progress.New(progress.WithDefaultGradient(), progress.WithWidth(50)),
		loadingText: "Loading the modpack library…",
		updateDone:  d.Updater == nil,
		width:       80,
		height:      24,
	}
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, m.loadRemoteCmd()}
	if m.d.Updater != nil {
		cmds = append(cmds, m.checkUpdateCmd())
	}
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.form != nil {
			m.form = m.form.WithWidth(m.innerWidth())
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.busy {
				m.pleaseWait = true
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if cmd, ok := m.back(); ok {
				return m, cmd
			}
		}
		switch m.screen {
		case scrResult:
			if msg.String() == "enter" || msg.String() == " " {
				return m, m.startDetect()
			}
			return m, nil
		case scrUpdated:
			return m, tea.Quit
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case remoteLoadedMsg:
		m.remoteDone = true
		m.games, m.index, m.remoteErr = msg.games, msg.index, msg.err
		if msg.err != nil {
			m.d.Log.Error("loading the library failed", "err", msg.err)
			m.detectDone = true
			return m, m.proceed()
		}
		return m, m.startDetect()

	case updateCheckedMsg:
		m.updateDone = true
		if msg.err != nil {
			m.d.Log.Warn("self-update check failed", "err", msg.err)
		}
		m.updateRel = msg.rel
		return m, m.proceed()

	case detectedMsg:
		m.detectDone = true
		m.rows = msg.rows
		return m, m.proceed()

	case updateAppliedMsg:
		m.busy = false
		if msg.err != nil {
			m.d.Log.Error("self-update failed", "err", msg.err)
			if remote.IsUpdateRequired(m.remoteErr) {
				return m, m.showError(fmt.Sprintf("The update failed:\n\n%v\n\nLog file: %s", msg.err, m.d.LogPath), errUpdate)
			}
			m.updateDeclined = true
			m.resultOK = false
			m.resultText = fmt.Sprintf("The update failed:\n\n%v\n\nLog file: %s", msg.err, m.d.LogPath)
			m.screen = scrResult
			return m, nil
		}
		m.screen = scrUpdated
		return m, nil

	case progressMsg:
		ev := engine.Event(msg)
		switch ev.Kind {
		case engine.EventStep:
			m.stepText = ev.Message
		case engine.EventDownload:
			m.dl = ev
		}
		return m, listen(m.events)

	case opDoneMsg:
		m.busy, m.pleaseWait = false, false
		var lo *engine.LeftoversError
		if errors.As(msg.err, &lo) && !m.op.clean && (m.op.kind == actInstall || m.op.kind == actReinstall) {
			return m, m.openLeftoversConfirm(lo)
		}
		m.resultOK = msg.err == nil
		m.resultText = m.resultMessage(msg.err, msg.removed)
		m.screen = scrResult
		return m, nil
	}

	if m.form != nil {
		f, cmd := m.form.Update(msg)
		if ff, ok := f.(*huh.Form); ok {
			m.form = ff
		}
		if m.form.State == huh.StateCompleted {
			return m, tea.Batch(cmd, m.formDone())
		}
		return m, cmd
	}
	return m, nil
}

// back handles esc.
func (m *model) back() (tea.Cmd, bool) {
	switch m.screen {
	case scrGame:
		return m.openMain(), true
	case scrPacks:
		return m.openGame(), true
	case scrConfirm:
		if m.confirmBack == scrPacks {
			return m.openPacks(), true
		}
		return m.openGame(), true
	case scrResult:
		return m.startDetect(), true
	}
	return nil, false
}

// proceed leaves the startup phase once the library, detection and the
// update check are all done.
func (m *model) proceed() tea.Cmd {
	if !m.remoteDone || !m.detectDone || !m.updateDone {
		if !m.updateDone && m.remoteDone && m.detectDone {
			m.loadingText = "Checking for updates…"
		}
		return nil
	}
	forced := remote.IsUpdateRequired(m.remoteErr)
	switch {
	case m.updateRel != nil && !m.updateDeclined:
		return m.openUpdatePrompt(forced)
	case forced:
		return m.showError(m.remoteErr.Error()+"\n\nNo newer version could be found automatically. "+
			"Please download the latest release manually.", errRemote)
	case m.remoteErr != nil:
		return m.showError(fmt.Sprintf("Could not load the modpack library:\n\n%v\n\nLog file: %s", m.remoteErr, m.d.LogPath), errRemote)
	}
	return m.openMain()
}

func (m *model) showError(text string, kind errKind) tea.Cmd {
	m.errText, m.errKind = text, kind
	m.screen = scrError
	return m.setForm(selectForm("What now?", "",
		huh.NewOption("Retry", "retry"),
		huh.NewOption("Quit", "quit"),
	))
}

// ---- commands --------------------------------------------------------------

func (m *model) loadRemoteCmd() tea.Cmd {
	c := m.d.Client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		games, err := c.FetchGames(ctx)
		if err != nil {
			return remoteLoadedMsg{err: err}
		}
		index, err := c.FetchIndex(ctx)
		if err != nil {
			return remoteLoadedMsg{err: err}
		}
		return remoteLoadedMsg{games: games, index: index}
	}
}

func (m *model) checkUpdateCmd() tea.Cmd {
	u := m.d.Updater
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rel, err := u.Check(ctx)
		return updateCheckedMsg{rel: rel, err: err}
	}
}

func (m *model) applyUpdateCmd() tea.Cmd {
	u, rel := m.d.Updater, m.updateRel
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return updateAppliedMsg{err: u.Apply(ctx, rel)}
	}
}

func (m *model) startDetect() tea.Cmd {
	if m.games == nil || m.index == nil {
		// The library never loaded (e.g. a failed update after a network error).
		return m.reload()
	}
	m.screen = scrLoading
	m.form = nil
	m.loadingText = "Detecting games…"
	m.detectDone = false
	games, index, d := m.games, m.index, m.d
	return func() tea.Msg {
		results := d.Detector.Detect(games.Games, index.GamesWithPacks())
		rows := make([]gameRow, 0, len(results))
		for _, r := range results {
			g, _ := games.Game(r.GameID)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			st := d.Engine.Status(ctx, r.RootDir, index)
			cancel()
			rows = append(rows, gameRow{Result: r, Game: g, Files: filesKey(g, r.BuildID), Status: st})
		}
		return detectedMsg{rows: rows}
	}
}

func filesKey(g detect.GameDef, buildID string) string {
	for _, b := range g.Builds {
		if b.ID == buildID {
			return b.Files
		}
	}
	return ""
}

// startOp runs the pending install/remove in a goroutine. Progress events and
// the final result arrive through m.events.
func (m *model) startOp() tea.Cmd {
	m.screen = scrProgress
	m.form = nil
	m.busy, m.pleaseWait = true, false
	m.stepText = "Starting…"
	m.dl = engine.Event{}
	ch := make(chan tea.Msg, 64)
	m.events = ch
	op, eng, log := m.op, m.d.Engine, m.d.Log
	log.Info("operation started", "action", op.kind.verb(), "game", op.row.Result.GameID, "root", op.row.Result.RootDir, "pack", op.pack.ID)
	go func() {
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic during operation", "panic", r, "stack", string(debug.Stack()))
				ch <- opDoneMsg{err: fmt.Errorf("internal error: %v", r)}
			}
		}()
		progress := func(ev engine.Event) { ch <- progressMsg(ev) }
		ctx := context.Background()
		req := engine.InstallRequest{Game: op.row.Result, FilesKey: op.row.Files, Pack: op.pack, CleanLeftovers: op.clean}
		var err error
		var removed []string
		switch op.kind {
		case actInstall, actReinstall:
			err = eng.Install(ctx, req, progress)
		case actRemove:
			err = eng.Remove(ctx, op.row.Result.RootDir, progress)
		case actCleanup:
			removed, err = eng.CleanLeftovers(ctx, req, progress)
		}
		ch <- opDoneMsg{err: err, removed: removed}
	}()
	return listen(ch)
}

func listen(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *model) resultMessage(err error, removed []string) string {
	op := m.op
	if err != nil {
		if op.kind == actRemove && errors.Is(err, engine.ErrNothingToRemove) {
			return "Nothing to remove: no pack is installed for this game."
		}
		return fmt.Sprintf("%s:\n\n%v\n\nLog file: %s", actionTexts[op.kind].failed, err, m.d.LogPath)
	}
	switch op.kind {
	case actRemove:
		return fmt.Sprintf("Removed the pack from %s.\n%s is back to vanilla.", op.row.Result.RootDir, op.row.Game.Name)
	case actReinstall:
		return fmt.Sprintf("Reinstalled %s into %s.", op.pack.Name, op.row.Result.RootDir)
	case actCleanup:
		if len(removed) == 0 {
			return fmt.Sprintf("No leftover files of %s were found in %s.", op.pack.Name, op.row.Result.RootDir)
		}
		return fmt.Sprintf("Removed %d leftover item(s) from %s:\n%s", len(removed), op.row.Result.RootDir, listPaths(removed, 10))
	}
	return fmt.Sprintf("Installed %s into %s.", op.pack.Name, op.row.Result.RootDir)
}

// listPaths formats up to limit paths, one per line; folders are marked.
func listPaths(paths []string, limit int) string {
	var b strings.Builder
	for i, p := range paths {
		if i == limit {
			fmt.Fprintf(&b, "  … and %d more", len(paths)-limit)
			break
		}
		if strings.HasSuffix(p, "/") {
			p += " (folder)"
		}
		fmt.Fprintf(&b, "  %s\n", p)
	}
	return strings.TrimRight(b.String(), "\n")
}
