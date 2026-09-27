package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
)

// stagedFilesDirName is the folder inside the staging folder that holds the
// staged files at their relative paths.
const stagedFilesDirName = "files"

// staging is the previously installed pack, moved aside while another install
// runs so it can be put back if that install fails. Its files are renamed into
// <root>/<StagingDirName>/files keeping their relative paths; a copy of its
// marker lies next to them so an interrupted install can be recovered.
type staging struct {
	root  string
	dir   string
	old   *Marker
	files FileWriter
}

// stagedRestore moves the files below filesDir back to root, collecting
// errors instead of stopping at the first.
type stagedRestore struct {
	files    FileWriter
	filesDir string
	root     string
	errs     []error
}

// userFileKeeper copies the preserved files of one staged folder back into
// the game dir (see keepUserFiles).
type userFileKeeper struct {
	engine     *Engine
	stagedPack *staging
	journal    *installJournal
	written    map[string]bool // pathKey of every file the new pack wrote
	base       string          // the staged folder being walked
}

func stagingDir(root string) string {
	return filepath.Join(root, config.StagingDirName)
}

// path returns where rel is kept in the staging folder.
func (s *staging) path(rel string) string {
	return filepath.Join(s.filesDir(), filepath.FromSlash(rel))
}

func (s *staging) filesDir() string {
	return filepath.Join(s.dir, stagedFilesDirName)
}

// stage moves the installed pack's owned folders, files and (then empty)
// created folders into the staging folder. Its marker stays in place until
// the new one replaces it. On error everything is moved back.
func (e *Engine) stage(root string) (*staging, error) {
	markerPath := MarkerPath(root)
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return nil, fsErr(markerPath, err)
	}
	old, err := ReadMarker(root)
	if err != nil {
		return nil, err
	}
	stagedPack := &staging{root: root, dir: stagingDir(root), old: old, files: e.files}
	if err := e.files.MkdirAll(stagedPack.filesDir(), dirPerm); err != nil {
		return nil, fsErr(stagedPack.dir, err)
	}
	if err := writeFile(e.files, filepath.Join(stagedPack.dir, config.MarkerFileName), raw, filePerm); err != nil {
		_ = e.files.RemoveAll(stagedPack.dir)
		return nil, fsErr(stagedPack.dir, err)
	}
	if err := stagedPack.moveAll(); err != nil {
		return nil, restoreAfter(stagedPack, err)
	}
	slog.Debug("stage installed pack", "pack_id", old.PackID, "path", stagedPack.dir)
	return stagedPack, nil
}

// moveAll moves owned folders and files first, then the created folders that
// are empty by then, deepest first.
func (s *staging) moveAll() error {
	for _, rel := range append(append([]string(nil), s.old.OwnedDirs...), s.old.Files...) {
		if err := s.move(rel); err != nil {
			return err
		}
	}
	for _, dir := range deepestFirst(s.old.DirsCreated) {
		if err := s.moveEmptyDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// move renames rel (a file or folder) into the staging folder; a missing rel
// (e.g. inside an owned folder moved before) is skipped.
func (s *staging) move(rel string) error {
	src, err := pathsafe.JoinNotRoot(s.root, rel)
	if err != nil {
		return fmt.Errorf("check marker path: %w", err)
	}
	if _, err := os.Lstat(src); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fsErr(src, err)
	}
	dst := s.path(rel)
	if err := s.files.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fsErr(dst, err)
	}
	if err := s.files.Rename(src, dst); err != nil {
		return fsErr(src, err)
	}
	return nil
}

// moveEmptyDir removes the created folder rel if it is empty, recording it in
// the staging folder so a restore recreates it.
func (s *staging) moveEmptyDir(rel string) error {
	src, err := pathsafe.JoinNotRoot(s.root, rel)
	if err != nil {
		return fmt.Errorf("check marker path: %w", err)
	}
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) || err == nil && len(entries) > 0 {
		return nil
	}
	if err != nil {
		return fsErr(src, err)
	}
	if err := s.files.MkdirAll(s.path(rel), dirPerm); err != nil {
		return fsErr(s.dir, err)
	}
	return fsErr(src, s.files.Remove(src))
}

// restore moves everything back into the game dir and deletes the staging
// folder.
func (s *staging) restore() error {
	return restoreStaged(s.files, s.dir, s.root)
}

// discard deletes the staging folder after a successful install.
func (s *staging) discard() error {
	return fsErr(s.dir, s.files.RemoveAll(s.dir))
}

// restoreStaged moves every staged file back to its place below root,
// replacing whatever is there, and recreates staged folders. The staging
// folder is deleted only if everything was moved back.
func restoreStaged(files FileWriter, dir, root string) error {
	restore := &stagedRestore{files: files, filesDir: filepath.Join(dir, stagedFilesDirName), root: root}
	if err := filepath.WalkDir(restore.filesDir, restore.visit); err != nil {
		restore.errs = append(restore.errs, err)
	}
	if len(restore.errs) > 0 {
		return fmt.Errorf("restore staged files from %s: %w", dir, errors.Join(restore.errs...))
	}
	return fsErr(dir, files.RemoveAll(dir))
}

func (r *stagedRestore) visit(stagedPath string, entry fs.DirEntry, err error) error {
	if err != nil {
		if stagedPath == r.filesDir && os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rel, err := filepath.Rel(r.filesDir, stagedPath)
	if err != nil || rel == "." {
		return err
	}
	dst := filepath.Join(r.root, rel)
	if entry.IsDir() {
		r.recreateDir(dst)
		return nil
	}
	r.moveBack(stagedPath, dst)
	return nil
}

func (r *stagedRestore) recreateDir(dst string) {
	if err := r.files.MkdirAll(dst, dirPerm); err != nil {
		r.errs = append(r.errs, fsErr(dst, err))
	}
}

func (r *stagedRestore) moveBack(stagedPath, dst string) {
	if err := r.files.Remove(dst); err != nil && !os.IsNotExist(err) {
		r.errs = append(r.errs, fsErr(dst, err))
		return
	}
	if err := r.files.Rename(stagedPath, dst); err != nil {
		r.errs = append(r.errs, fsErr(dst, err))
	}
}

// recoverStaging cleans up after an install that was interrupted (e.g. the
// app was killed) while a previous pack was staged. If the marker is still the
// staged one, the new pack was never committed and the staged pack is moved
// back; otherwise the new pack was committed and the staging folder is
// deleted. (Identical markers mean an identical install, so moving the staged
// files back is harmless.)
func (e *Engine) recoverStaging(root string) error {
	dir := stagingDir(root)
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fsErr(dir, err)
	}
	staged, _ := os.ReadFile(filepath.Join(dir, config.MarkerFileName))
	current, _ := os.ReadFile(MarkerPath(root))
	if staged != nil && bytes.Equal(staged, current) {
		slog.Warn("restore staged pack after interrupted install", "path", dir)
		return restoreStaged(e.files, dir, root)
	}
	slog.Warn("delete staged pack after interrupted install", "path", dir)
	return fsErr(dir, e.files.RemoveAll(dir))
}

// keepUserFiles copies staged files of the same pack that lie below one of
// preserve back into the game dir: files the new pack does not ship, and
// files it ships that were changed since they were installed (their hash
// differs from the marker's). Unchanged shipped files keep the new version.
func (e *Engine) keepUserFiles(stagedPack *staging, preserve []string, journal *installJournal) error {
	written := map[string]bool{}
	for _, file := range journal.files {
		written[pathKey(file)] = true
	}
	for _, rel := range preserve {
		keeper := &userFileKeeper{engine: e, stagedPack: stagedPack, journal: journal, written: written, base: stagedPack.path(rel)}
		if err := filepath.WalkDir(keeper.base, keeper.visit); err != nil {
			return fmt.Errorf("keep user files below %s: %w", rel, err)
		}
	}
	return nil
}

func (k *userFileKeeper) visit(stagedPath string, entry fs.DirEntry, err error) error {
	if err != nil {
		if stagedPath == k.base && os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !entry.Type().IsRegular() {
		return nil
	}
	return k.keep(stagedPath, entry)
}

// keep copies one staged file back unless the new pack ships it and the user
// did not change it.
func (k *userFileKeeper) keep(stagedPath string, entry fs.DirEntry) error {
	relNative, err := filepath.Rel(k.stagedPack.filesDir(), stagedPath)
	if err != nil {
		return err
	}
	rel := filepath.ToSlash(relNative)
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		return err
	}
	isShipped := k.written[pathKey(rel)]
	if sum := sha256.Sum256(data); isShipped && k.stagedPack.old.FileSHA256[rel] == hex.EncodeToString(sum[:]) {
		return nil // unchanged: the new pack's version wins
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	dst, err := pathsafe.JoinNotRoot(k.stagedPack.root, rel)
	if err != nil {
		return err
	}
	if dir := path.Dir(rel); dir != "." {
		if err := k.engine.ensureDir(k.stagedPack.root, dir, k.journal); err != nil {
			return err
		}
	}
	if err := writeAtomic(k.engine.files, dst, data, info.Mode().Perm()); err != nil {
		return fsErr(dst, err)
	}
	if !isShipped {
		k.journal.files = append(k.journal.files, rel)
	}
	slog.Debug("keep user file", "path", dst, "replaced_pack_version", isShipped)
	return nil
}
