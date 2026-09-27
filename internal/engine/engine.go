// Package engine installs and removes modpacks. The marker file in the game
// root is the only persisted state.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
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

// Source is where the engine gets pack manifests and files from.
type Source interface {
	FetchManifest(ctx context.Context, ref string) (*remote.Manifest, string, error)
	ManifestHash(ctx context.Context, ref string) (string, error)
	Download(ctx context.Context, entry remote.FileEntry, dst string, progress remote.ProgressFunc) error
}

// Engine performs installs and removals.
type Engine struct {
	source     Source
	appVersion string
	files      FileWriter
}

// New returns an engine that installs from source and writes through files.
func New(source Source, appVersion string, files FileWriter) *Engine {
	return &Engine{source: source, appVersion: appVersion, files: files}
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

// fsErr turns permission errors into a PermissionError naming path.
func fsErr(path string, err error) error {
	if err != nil && errors.Is(err, fs.ErrPermission) {
		return &PermissionError{Path: path, Err: err}
	}
	return err
}

func emitter(progress ProgressFunc) ProgressFunc {
	if progress == nil {
		return discardEvent
	}
	return progress
}

func discardEvent(Event) {}

func step(emit ProgressFunc, format string, args ...any) {
	emit(Event{Kind: EventStep, Message: fmt.Sprintf(format, args...)})
}
