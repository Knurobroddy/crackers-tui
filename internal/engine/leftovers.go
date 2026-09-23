package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
)

// LeftoversError means files the pack would write, or folders it owns,
// already exist in the game dir and do not belong to an installed pack, e.g.
// from an earlier manual mod install. Nothing was written.
type LeftoversError struct {
	Root  string
	Paths []string // relative, forward slashes; folders end with "/"
}

func (e *LeftoversError) Error() string {
	const show = 5
	list := strings.Join(e.Paths[:min(show, len(e.Paths))], ", ")
	if n := len(e.Paths) - show; n > 0 {
		list += fmt.Sprintf(" and %d more", n)
	}
	return fmt.Sprintf("leftover mod files already exist in %s: %s", e.Root, list)
}

// leftovers lists the pack's owned folders that exist and the target files
// that exist outside them. It only looks at paths the pack itself would write,
// so files it does not know about are never reported (or deleted).
func (p *plan) leftovers(root string, ownedDirs []string) ([]string, error) {
	var out, owned []string
	for _, d := range ownedDirs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(d))); err == nil {
			out = append(out, d+"/")
			owned = append(owned, pathKey(d)+"/")
		} else if !os.IsNotExist(err) {
			return nil, fsErr(d, err)
		}
	}
	for _, f := range p.Files {
		k := pathKey(f.Rel)
		inOwned := false
		for _, o := range owned {
			if strings.HasPrefix(k, o) {
				inOwned = true
				break
			}
		}
		if inOwned {
			continue
		}
		if _, err := os.Lstat(f.Abs); err == nil {
			out = append(out, f.Rel)
		} else if !os.IsNotExist(err) {
			return nil, fsErr(f.Abs, err)
		}
	}
	return out, nil
}

// removeLeftovers deletes the given leftovers (folders recursively).
func (e *Engine) removeLeftovers(root string, paths []string) error {
	for _, rel := range paths {
		abs, err := pathsafe.JoinNotRoot(root, strings.TrimSuffix(rel, "/"))
		if err != nil {
			return err
		}
		if err := os.RemoveAll(abs); err != nil {
			return fsErr(abs, err)
		}
		e.log.Info("deleted leftover", "path", abs)
	}
	return nil
}

// CleanLeftovers deletes the leftovers of req.Pack from a game dir without
// installing anything and returns what it deleted. It refuses to run while a
// pack is installed there (use Remove for that). The pack is downloaded to
// learn which paths it writes; only those paths and its owned folders are
// considered.
func (e *Engine) CleanLeftovers(ctx context.Context, req InstallRequest, progress ProgressFunc) (removed []string, err error) {
	emit := emitter(progress)
	root := req.Game.RootDir
	log := e.log.With("pack", req.Pack.ID, "root", root)
	log.Info("leftover cleanup started")
	defer func() {
		if err != nil {
			log.Error("leftover cleanup failed", "err", err)
		} else {
			log.Info("leftover cleanup finished", "removed", len(removed))
		}
	}()
	if _, err := os.Lstat(MarkerPath(root)); err == nil {
		return nil, fmt.Errorf("a pack is installed in %s; use Remove pack instead", root)
	}
	pr, err := e.prepare(ctx, req, emit, log, false)
	if err != nil {
		return nil, err
	}
	defer pr.close()
	left, err := pr.plan.leftovers(root, pr.ownedDirs)
	if err != nil || len(left) == 0 {
		return nil, err
	}
	step(emit, "Removing leftover mod files…")
	if err := e.removeLeftovers(root, left); err != nil {
		return nil, err
	}
	return left, nil
}
