package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
	"github.com/Knurobroddy/crackers-tui/internal/fsutil"
)

// staging is the previously installed pack, moved aside while another install
// runs so it can be put back if that install fails. Its files are renamed into
// <root>/<StagingDirName>/files keeping their relative paths; a copy of its
// marker lies next to them so an interrupted install can be recovered.
type staging struct {
	root string
	dir  string
	old  *Marker
}

func stagingDir(root string) string {
	return filepath.Join(root, config.StagingDirName)
}

// path returns where rel is kept in the staging folder.
func (s *staging) path(rel string) string {
	return filepath.Join(s.dir, "files", filepath.FromSlash(rel))
}

// stage moves the installed pack's owned folders, files and (then empty)
// created folders into the staging folder. Its marker stays in place until
// the new one replaces it. On error everything is moved back.
func (e *Engine) stage(root string) (_ *staging, err error) {
	raw, err := os.ReadFile(MarkerPath(root))
	if err != nil {
		return nil, fsErr(MarkerPath(root), err)
	}
	old, err := ReadMarker(root)
	if err != nil {
		return nil, err
	}
	s := &staging{root: root, dir: stagingDir(root), old: old}
	if err := os.MkdirAll(filepath.Join(s.dir, "files"), 0o755); err != nil {
		return nil, fsErr(s.dir, err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, config.MarkerFileName), raw, 0o644); err != nil {
		os.RemoveAll(s.dir)
		return nil, fsErr(s.dir, err)
	}
	defer func() {
		if err != nil {
			if rErr := s.restore(); rErr != nil {
				err = fmt.Errorf("%w\n\nMoving the pack back also had errors:\n%v", err, rErr)
			}
		}
	}()
	for _, rel := range append(append([]string(nil), old.OwnedDirs...), old.Files...) {
		if err := s.move(rel); err != nil {
			return nil, err
		}
	}
	dirs := append([]string(nil), old.DirsCreated...)
	sort.SliceStable(dirs, func(a, b int) bool { return depth(dirs[a]) > depth(dirs[b]) })
	for _, d := range dirs {
		if err := s.moveEmptyDir(d); err != nil {
			return nil, err
		}
	}
	e.log.Info("moved the installed pack aside", "pack", old.PackID, "dir", s.dir)
	return s, nil
}

// move renames rel (a file or folder) into the staging folder; a missing rel
// (e.g. inside an owned folder moved before) is skipped.
func (s *staging) move(rel string) error {
	src, err := pathsafe.JoinNotRoot(s.root, rel)
	if err != nil {
		return fmt.Errorf("marker: %w", err)
	}
	if _, err := os.Lstat(src); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fsErr(src, err)
	}
	dst := s.path(rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fsErr(dst, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fsErr(src, err)
	}
	return nil
}

// moveEmptyDir removes the created folder rel if it is empty, recording it in
// the staging folder so a restore recreates it.
func (s *staging) moveEmptyDir(rel string) error {
	src, err := pathsafe.JoinNotRoot(s.root, rel)
	if err != nil {
		return fmt.Errorf("marker: %w", err)
	}
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) || err == nil && len(entries) > 0 {
		return nil
	}
	if err != nil {
		return fsErr(src, err)
	}
	if err := os.MkdirAll(s.path(rel), 0o755); err != nil {
		return fsErr(s.dir, err)
	}
	return fsErr(src, os.Remove(src))
}

// restore moves everything back into the game dir and deletes the staging
// folder.
func (s *staging) restore() error {
	return restoreStaged(s.dir, s.root)
}

// discard deletes the staging folder after a successful install.
func (s *staging) discard() error {
	return fsErr(s.dir, os.RemoveAll(s.dir))
}

// restoreStaged moves every staged file back to its place below root,
// replacing whatever is there, and recreates staged folders. The staging
// folder is deleted only if everything was moved back.
func restoreStaged(dir, root string) error {
	files := filepath.Join(dir, "files")
	var errs []error
	err := filepath.WalkDir(files, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == files && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(files, p)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				errs = append(errs, fsErr(dst, err))
			}
			return nil
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fsErr(dst, err))
			return nil
		}
		if err := os.Rename(p, dst); err != nil {
			errs = append(errs, fsErr(dst, err))
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("some files are still in %s:\n%w", dir, errors.Join(errs...))
	}
	return fsErr(dir, os.RemoveAll(dir))
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
		e.log.Warn("interrupted install: moving the previous pack back", "dir", dir)
		return restoreStaged(dir, root)
	}
	e.log.Warn("interrupted install: deleting the previous pack's staged files", "dir", dir)
	return fsErr(dir, os.RemoveAll(dir))
}

// keepUserFiles copies staged files of the same pack that lie below one of
// preserve back into the game dir: files the new pack does not ship, and
// files it ships that were changed since they were installed (their hash
// differs from the marker's). Unchanged shipped files keep the new version.
func (e *Engine) keepUserFiles(s *staging, preserve []string, j *journal) error {
	written := map[string]bool{}
	for _, f := range j.files {
		written[pathKey(f)] = true
	}
	files := filepath.Join(s.dir, "files")
	for _, p := range preserve {
		base := s.path(p)
		err := filepath.WalkDir(base, func(abs string, d fs.DirEntry, err error) error {
			if err != nil {
				if abs == base && os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			r, err := filepath.Rel(files, abs)
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(r)
			data, err := os.ReadFile(abs)
			if err != nil {
				return err
			}
			shipped := written[pathKey(rel)]
			if sum := sha256.Sum256(data); shipped && s.old.FileSHA256[rel] == hex.EncodeToString(sum[:]) {
				return nil // unchanged: the new pack's version wins
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			dst, err := pathsafe.JoinNotRoot(s.root, rel)
			if err != nil {
				return err
			}
			if dir := path.Dir(rel); dir != "." {
				if err := e.ensureDir(s.root, dir, j); err != nil {
					return err
				}
			}
			if err := fsutil.WriteAtomic(dst, data, fi.Mode().Perm()); err != nil {
				return fsErr(dst, err)
			}
			if !shipped {
				j.files = append(j.files, rel)
			}
			e.log.Info("kept user file", "path", dst, "replaced_pack_version", shipped)
			return nil
		})
		if err != nil {
			return fmt.Errorf("could not keep files below %s: %w", p, err)
		}
	}
	return nil
}
