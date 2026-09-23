package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
)

// Remove uninstalls the pack recorded in the marker (ADR §5.4). The marker is
// deleted last and only if every step succeeded, so a failed remove can be
// retried.
func (e *Engine) Remove(ctx context.Context, root string, progress ProgressFunc) (err error) {
	emit := emitter(progress)
	if err := e.recoverStaging(root); err != nil {
		return fmt.Errorf("could not clean up after an interrupted install: %w", err)
	}
	mk, err := ReadMarker(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNothingToRemove
		}
		return err
	}
	log := e.log.With("pack", mk.PackID, "root", root)
	log.Info("remove started")
	defer func() {
		if err != nil {
			log.Error("remove failed", "err", err)
		} else {
			log.Info("remove finished")
		}
	}()
	step(emit, "Removing %s…", mk.PackName)

	var errs []error
	for _, f := range mk.Files {
		p, err := pathsafe.JoinNotRoot(root, f)
		if err != nil {
			errs = append(errs, fmt.Errorf("marker: %w", err))
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fsErr(p, err))
			continue
		}
		log.Info("deleted file", "path", p)
	}
	for _, d := range mk.OwnedDirs {
		p, err := pathsafe.JoinNotRoot(root, d)
		if err != nil {
			errs = append(errs, fmt.Errorf("marker: %w", err))
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, fsErr(p, err))
			continue
		}
		log.Info("deleted owned directory", "path", p)
	}
	if err := runUndo(mk.Undo, log); err != nil {
		errs = append(errs, err)
	}
	dirs := append([]string(nil), mk.DirsCreated...)
	sort.SliceStable(dirs, func(a, b int) bool { return depth(dirs[a]) > depth(dirs[b]) })
	for _, d := range dirs {
		p, err := pathsafe.JoinNotRoot(root, d)
		if err != nil {
			errs = append(errs, fmt.Errorf("marker: %w", err))
			continue
		}
		if err := removeIfEmpty(p); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("remove incomplete; the marker was kept so you can retry:\n%w", errors.Join(errs...))
	}
	if err := os.Remove(MarkerPath(root)); err != nil && !os.IsNotExist(err) {
		return fsErr(MarkerPath(root), err)
	}
	return nil
}

func depth(rel string) int { return strings.Count(rel, "/") }
