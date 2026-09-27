// Package tui is the Bubble Tea front end. It runs the use cases of package
// app and shows their progress.
package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

const (
	screenLoading screen = iota
	screenError
	screenUpdatePrompt
	screenUpdating
	screenUpdated
	screenMain
	screenGame
	screenPacks
	screenConfirm
	screenProgress
	screenResult
)

const (
	retryReload retryAction = iota // Retry reloads the library
	retryUpdate                    // Retry re-applies the forced update
)

const (
	defaultWidth       = 80
	defaultHeight      = 24
	defaultBarWidth    = 50
	maxRemovedShown    = 10
	maxLeftoversShown  = 8
	loadingLibraryText = "Loading the modpack library…"
)

var actionTexts = map[app.OpKind]actionText{
	app.OpInstall:        {failed: "Installing the pack failed"},
	app.OpReinstall:      {failed: "Reinstalling the pack failed"},
	app.OpRemove:         {failed: "Removing the pack failed"},
	app.OpCleanLeftovers: {failed: "Removing leftover mod files failed"},
}

// Deps are the collaborators of the UI.
type Deps struct {
	AppVersion string
	App        *app.App
	LogPath    string
}

type screen int

type retryAction int

type actionText struct{ failed string }

type (
	remoteLoadedMsg struct {
		library *app.Library
		err     error
	}
	updateCheckedMsg struct {
		release *update.Release
		err     error
	}
	updateAppliedMsg struct{ err error }
	gamesDetectedMsg struct{ games []app.Game }
	progressMsg      engine.Event
	opDoneMsg        struct {
		outcome app.Outcome
		err     error
	}
)

type model struct {
	deps          Deps
	width, height int
	screen        screen
	spinner       spinner.Model
	bar           progress.Model
	form          *huh.Form
	loadingText   string

	// startup state
	remoteDone, detectDone, updateDone bool
	library                            *app.Library
	remoteErr                          error
	updateRelease                      *update.Release
	updateDeclined                     bool

	errText     string
	retryAction retryAction

	games       []app.Game
	selected    int
	op          app.Operation
	confirmBack screen
	packsKind   app.OpKind // what choosing on the pack list does (install or clean leftovers)

	// operation in progress
	busy       bool
	pleaseWait bool
	stepText   string
	download   engine.Event
	events     chan tea.Msg

	resultOK   bool
	resultText string
}

func newModel(deps Deps) *model {
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = accentStyle
	return &model{
		deps:        deps,
		screen:      screenLoading,
		spinner:     spin,
		bar:         progress.New(progress.WithDefaultGradient(), progress.WithWidth(defaultBarWidth)),
		loadingText: loadingLibraryText,
		updateDone:  !deps.App.UpdatesEnabled(),
		width:       defaultWidth,
		height:      defaultHeight,
	}
}

// Run starts the TUI and blocks until the user quits.
func Run(deps Deps) error {
	_, err := tea.NewProgram(newModel(deps), tea.WithAltScreen()).Run()
	return err
}

// Init starts loading the library and, if enabled, the update check.
func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, m.loadLibraryCmd()}
	if m.deps.App.UpdatesEnabled() {
		cmds = append(cmds, m.checkUpdateCmd())
	}
	return tea.Batch(cmds...)
}

// Update dispatches msg to its handler.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, m.onWindowSize(msg)
	case tea.KeyMsg:
		return m, m.onKey(msg)
	case spinner.TickMsg:
		return m, m.onSpinnerTick(msg)
	case remoteLoadedMsg:
		return m, m.onLibraryLoaded(msg)
	case updateCheckedMsg:
		return m, m.onUpdateChecked(msg)
	case gamesDetectedMsg:
		return m, m.onGamesDetected(msg)
	case updateAppliedMsg:
		return m, m.onUpdateApplied(msg)
	case progressMsg:
		return m, m.onProgress(msg)
	case opDoneMsg:
		return m, m.onOperationDone(msg)
	}
	return m, m.onForm(msg)
}

// back handles esc.
func (m *model) back() (tea.Cmd, bool) {
	switch m.screen {
	case screenGame:
		return m.openMain(), true
	case screenPacks:
		return m.openGame(), true
	case screenConfirm:
		if m.confirmBack == screenPacks {
			return m.openPacks(), true
		}
		return m.openGame(), true
	case screenResult:
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
	case m.updateRelease != nil && !m.updateDeclined:
		return m.openUpdatePrompt(forced)
	case forced:
		return m.showError(app.UserMessage(m.remoteErr)+"\n\nNo newer version could be found automatically. "+
			"Please download the latest release manually.", retryReload)
	case m.remoteErr != nil:
		return m.showError(fmt.Sprintf("Could not load the modpack library:\n\n%s\n\nLog file: %s",
			app.UserMessage(m.remoteErr), m.deps.LogPath), retryReload)
	}
	return m.openMain()
}

func (m *model) showError(text string, retry retryAction) tea.Cmd {
	m.errText, m.retryAction = text, retry
	m.screen = screenError
	return m.setForm(selectForm("What now?", "",
		huh.NewOption("Retry", "retry"),
		huh.NewOption("Quit", "quit"),
	))
}

func (m *model) reload() tea.Cmd {
	m.screen = screenLoading
	m.form = nil
	m.loadingText = loadingLibraryText
	m.remoteDone, m.detectDone = false, false
	m.remoteErr = nil
	return m.loadLibraryCmd()
}

func (m *model) startDetect() tea.Cmd {
	if m.library == nil {
		// The library never loaded (e.g. a failed update after a network error).
		return m.reload()
	}
	m.screen = screenLoading
	m.form = nil
	m.loadingText = "Detecting games…"
	m.detectDone = false
	return m.detectGamesCmd()
}

func (m *model) startUpdate() tea.Cmd {
	m.screen = screenUpdating
	m.form = nil
	m.busy = true
	return m.applyUpdateCmd()
}

// startOp runs the pending operation in a goroutine. Progress events and the
// final result arrive through m.events.
func (m *model) startOp() tea.Cmd {
	m.screen = screenProgress
	m.form = nil
	m.busy, m.pleaseWait = true, false
	m.stepText = "Starting…"
	m.download = engine.Event{}
	events := make(chan tea.Msg, eventBufferSize)
	m.events = events
	go runOperation(m.deps.App, m.op, events)
	return m.listenCmd()
}

func (m *model) resultMessage(err error, removed []string) string {
	// Removing when nothing is installed is not a failure worth a log path.
	if m.op.Kind == app.OpRemove && errors.Is(err, engine.ErrNothingToRemove) {
		return app.UserMessage(err)
	}
	if err != nil {
		return fmt.Sprintf("%s:\n\n%s\n\nLog file: %s", actionTexts[m.op.Kind].failed, app.UserMessage(err), m.deps.LogPath)
	}
	op := m.op
	root := op.Game.Install.RootDir
	switch op.Kind {
	case app.OpRemove:
		return fmt.Sprintf("Removed the pack from %s.\n%s is back to vanilla.", root, op.Game.Def.Name)
	case app.OpReinstall:
		return fmt.Sprintf("Reinstalled %s into %s.", op.Pack.Name, root)
	case app.OpCleanLeftovers:
		if len(removed) == 0 {
			return fmt.Sprintf("No leftover files of %s were found in %s.", op.Pack.Name, root)
		}
		return fmt.Sprintf("Removed %d leftover item(s) from %s:\n%s", len(removed), root, app.ListPaths(removed, maxRemovedShown))
	}
	return fmt.Sprintf("Installed %s into %s.", op.Pack.Name, root)
}
