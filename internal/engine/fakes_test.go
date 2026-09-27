package engine

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
)

var errInjected = errors.New("injected failure")

// faultyFiles is OSFiles that fails (or panics, to simulate a crash) on the
// nth call of op whose path contains pathMatch. For Rename the old path is
// matched, so ".modinst-tmp" selects file writes and "-old" selects staging.
type faultyFiles struct {
	OSFiles
	op        string // "mkdir", "create", "rename", "remove", "removeall", ...
	pathMatch string
	nth       int // 1-based
	crash     bool
	calls     int
}

func (f *faultyFiles) check(op, path string) error {
	if op != f.op || !strings.Contains(filepath.ToSlash(path), f.pathMatch) {
		return nil
	}
	f.calls++
	if f.calls != f.nth {
		return nil
	}
	if f.crash {
		panic("injected crash")
	}
	return errInjected
}

func (f *faultyFiles) Mkdir(path string, perm fs.FileMode) error {
	if err := f.check("mkdir", path); err != nil {
		return err
	}
	return f.OSFiles.Mkdir(path, perm)
}

func (f *faultyFiles) MkdirAll(path string, perm fs.FileMode) error {
	if err := f.check("mkdirall", path); err != nil {
		return err
	}
	return f.OSFiles.MkdirAll(path, perm)
}

func (f *faultyFiles) Create(path string, perm fs.FileMode) (io.WriteCloser, error) {
	if err := f.check("create", path); err != nil {
		return nil, err
	}
	return f.OSFiles.Create(path, perm)
}

func (f *faultyFiles) Chmod(path string, perm fs.FileMode) error {
	if err := f.check("chmod", path); err != nil {
		return err
	}
	return f.OSFiles.Chmod(path, perm)
}

func (f *faultyFiles) Rename(oldPath, newPath string) error {
	if err := f.check("rename", oldPath); err != nil {
		return err
	}
	return f.OSFiles.Rename(oldPath, newPath)
}

func (f *faultyFiles) Remove(path string) error {
	if err := f.check("remove", path); err != nil {
		return err
	}
	return f.OSFiles.Remove(path)
}

func (f *faultyFiles) RemoveAll(path string) error {
	if err := f.check("removeall", path); err != nil {
		return err
	}
	return f.OSFiles.RemoveAll(path)
}
