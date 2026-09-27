package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
)

// CleanLeftovers deletes the leftovers of req.Pack from a game dir without
// installing anything and returns what it deleted. It refuses to run while a
// pack is installed there (use Remove for that). The pack is downloaded to
// learn which paths it writes; only those paths and its owned folders are
// considered.
func (e *Engine) CleanLeftovers(ctx context.Context, req InstallRequest, progress ProgressFunc) ([]string, error) {
	emit := emitter(progress)
	root := req.Game.RootDir
	if _, err := os.Lstat(MarkerPath(root)); err == nil {
		return nil, fmt.Errorf("clean leftovers in %s: a pack is installed", root)
	}
	prepared, err := e.prepareFiles(ctx, req, emit)
	if err != nil {
		return nil, err
	}
	defer prepared.close()
	leftovers, err := prepared.plan.leftovers(root, prepared.ownedDirs)
	if err != nil || len(leftovers) == 0 {
		return nil, err
	}
	step(emit, "Removing leftover mod files…")
	if err := e.removeLeftovers(root, leftovers); err != nil {
		return nil, err
	}
	return leftovers, nil
}

// leftovers lists the pack's owned folders that exist and the target files
// that exist outside them. It only looks at paths the pack itself would write,
// so files it does not know about are never reported (or deleted).
func (p *plan) leftovers(root string, ownedDirs []string) ([]string, error) {
	var leftovers, ownedPrefixes []string
	for _, dir := range ownedDirs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(dir))); err == nil {
			leftovers = append(leftovers, dir+"/")
			ownedPrefixes = append(ownedPrefixes, pathKey(dir)+"/")
		} else if !os.IsNotExist(err) {
			return nil, fsErr(dir, err)
		}
	}
	for _, file := range p.Files {
		if hasAnyPrefix(pathKey(file.Rel), ownedPrefixes) {
			continue
		}
		if _, err := os.Lstat(file.Abs); err == nil {
			leftovers = append(leftovers, file.Rel)
		} else if !os.IsNotExist(err) {
			return nil, fsErr(file.Abs, err)
		}
	}
	return leftovers, nil
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// removeLeftovers deletes the given leftovers (folders recursively).
func (e *Engine) removeLeftovers(root string, paths []string) error {
	for _, rel := range paths {
		abs, err := pathsafe.JoinNotRoot(root, strings.TrimSuffix(rel, "/"))
		if err != nil {
			return err
		}
		if err := e.files.RemoveAll(abs); err != nil {
			return fsErr(abs, err)
		}
		slog.Debug("delete leftover", "path", abs)
	}
	return nil
}
