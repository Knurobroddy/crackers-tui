package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
)

// Remove uninstalls the pack recorded in the marker. The marker is deleted
// last and only if every step succeeded, so a failed remove can be retried.
func (e *Engine) Remove(_ context.Context, root string, progress ProgressFunc) error {
	emit := emitter(progress)
	if err := e.recoverStaging(root); err != nil {
		return fmt.Errorf("recover interrupted install: %w", err)
	}
	marker, err := ReadMarker(root)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNothingToRemove
	}
	if err != nil {
		return err
	}
	step(emit, "Removing %s…", marker.PackName)
	errs := e.removeFiles(root, marker.Files)
	errs = append(errs, e.removeOwnedDirs(root, marker.OwnedDirs)...)
	if err := runUndo(marker.Undo); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, e.removeCreatedDirs(root, marker.DirsCreated)...)
	if len(errs) > 0 {
		return errors.Join(ErrRemoveIncomplete, errors.Join(errs...))
	}
	return e.deleteMarker(root)
}

func (e *Engine) removeFiles(root string, files []string) []error {
	var errs []error
	for _, rel := range files {
		file, err := pathsafe.JoinNotRoot(root, rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("check marker path: %w", err))
			continue
		}
		if err := e.files.Remove(file); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fsErr(file, err))
			continue
		}
		slog.Debug("delete file", "path", file)
	}
	return errs
}

func (e *Engine) removeOwnedDirs(root string, ownedDirs []string) []error {
	var errs []error
	for _, rel := range ownedDirs {
		dir, err := pathsafe.JoinNotRoot(root, rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("check marker path: %w", err))
			continue
		}
		if err := e.files.RemoveAll(dir); err != nil {
			errs = append(errs, fsErr(dir, err))
			continue
		}
		slog.Debug("delete owned directory", "path", dir)
	}
	return errs
}

// removeCreatedDirs removes the created folders that are empty, deepest
// first.
func (e *Engine) removeCreatedDirs(root string, dirsCreated []string) []error {
	var errs []error
	for _, rel := range deepestFirst(dirsCreated) {
		dir, err := pathsafe.JoinNotRoot(root, rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("check marker path: %w", err))
			continue
		}
		if err := removeIfEmpty(e.files, dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (e *Engine) deleteMarker(root string) error {
	markerPath := MarkerPath(root)
	if err := e.files.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		return fsErr(markerPath, err)
	}
	return nil
}

// deepestFirst returns a copy of dirs sorted by depth, deepest first; dirs of
// equal depth keep their order.
func deepestFirst(dirs []string) []string {
	sorted := append([]string(nil), dirs...)
	sort.SliceStable(sorted, func(a, b int) bool { return depth(sorted[a]) > depth(sorted[b]) })
	return sorted
}

func depth(rel string) int { return strings.Count(rel, "/") }
