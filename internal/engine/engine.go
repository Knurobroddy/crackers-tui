// Package engine installs and removes modpacks (ADR §5). The marker file in
// the game root is the only persisted state.
package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/logx"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// EventKind classifies progress events.
type EventKind int

const (
	// EventStep announces the current step (Message).
	EventStep EventKind = iota
	// EventDownload reports download progress of file Index of Count.
	EventDownload
)

// Event is a progress update sent to the UI.
type Event struct {
	Kind         EventKind
	Message      string
	File         string // display name of the file being downloaded
	Index, Count int    // 1-based file index and total number of files
	Done, Total  int64  // bytes; Total is 0 if unknown
}

// ProgressFunc receives progress events. It is called from the goroutine
// running the operation.
type ProgressFunc func(Event)

// ErrNothingToRemove is returned by Remove when there is no marker.
var ErrNothingToRemove = errors.New("nothing to remove: no pack is installed")

// Engine performs installs and removals.
type Engine struct {
	client     *remote.Client
	appVersion string
	log        *slog.Logger

	// failpoint, if set, is called before each write stage ("file" with the
	// 0-based file index, "hook", "marker", and "staging" after the marker was
	// written, before the previous pack's staged files are deleted); a
	// returned error is treated as a failure at that point. Tests use it to
	// exercise rollback and (by panicking) interrupted installs.
	failpoint func(stage string, n int) error
}

// New returns an engine.
func New(client *remote.Client, appVersion string, log *slog.Logger) *Engine {
	log = logx.OrDiscard(log)
	return &Engine{client: client, appVersion: appVersion, log: log}
}

// InstallRequest describes what to install where.
type InstallRequest struct {
	Game     detect.Result
	FilesKey string // the detected build's "files" value (e.g. "windows")
	Pack     remote.PackRef
	// CleanLeftovers deletes leftovers (see LeftoversError) before writing
	// instead of failing. The UI sets it only after the user confirmed.
	CleanLeftovers bool
}

func (e *Engine) fail(stage string, n int) error {
	if e.failpoint == nil {
		return nil
	}
	return e.failpoint(stage, n)
}

// PermissionError is returned when the OS refuses a write.
type PermissionError struct {
	Path string
	Err  error
}

func (p *PermissionError) Error() string {
	return fmt.Sprintf("Permission denied writing to %s. On Windows try running as administrator.", p.Path)
}

func (p *PermissionError) Unwrap() error { return p.Err }

// fsErr turns permission errors into a PermissionError naming path.
func fsErr(path string, err error) error {
	if err != nil && errors.Is(err, fs.ErrPermission) {
		return &PermissionError{Path: path, Err: err}
	}
	return err
}

func emitter(p ProgressFunc) ProgressFunc {
	if p == nil {
		return func(Event) {}
	}
	return p
}

func step(p ProgressFunc, format string, args ...any) {
	p(Event{Kind: EventStep, Message: fmt.Sprintf(format, args...)})
}
