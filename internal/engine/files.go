package engine

import (
	"io"
	"io/fs"
	"os"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

const (
	dirPerm      fs.FileMode = 0o755
	filePerm     fs.FileMode = 0o644
	execFilePerm fs.FileMode = 0o755
)

// FileWriter makes every change the engine makes on disk. Tests inject a
// FileWriter that fails or crashes on a chosen call. It is larger than the
// usual 1–3 methods because it mirrors the os functions it replaces.
type FileWriter interface {
	Mkdir(path string, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Create(path string, perm fs.FileMode) (io.WriteCloser, error)
	Chmod(path string, perm fs.FileMode) error
	Rename(oldPath, newPath string) error
	Remove(path string) error
	RemoveAll(path string) error
}

// OSFiles is the FileWriter backed by the os package.
type OSFiles struct{}

// Mkdir implements FileWriter.
func (OSFiles) Mkdir(path string, perm fs.FileMode) error { return os.Mkdir(path, perm) }

// MkdirAll implements FileWriter.
func (OSFiles) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

// Create implements FileWriter; it truncates an existing file.
func (OSFiles) Create(path string, perm fs.FileMode) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
}

// Chmod implements FileWriter.
func (OSFiles) Chmod(path string, perm fs.FileMode) error { return os.Chmod(path, perm) }

// Rename implements FileWriter.
func (OSFiles) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

// Remove implements FileWriter.
func (OSFiles) Remove(path string) error { return os.Remove(path) }

// RemoveAll implements FileWriter.
func (OSFiles) RemoveAll(path string) error { return os.RemoveAll(path) }

// writeFile writes data to path through files.
func writeFile(files FileWriter, path string, data []byte, perm fs.FileMode) error {
	out, err := files.Create(path, perm)
	if err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// writeAtomic writes data to path via path+TmpSuffix and a rename, so readers
// never see a partial file.
func writeAtomic(files FileWriter, path string, data []byte, perm fs.FileMode) error {
	tempPath := path + config.TmpSuffix
	if err := writeFile(files, tempPath, data, perm); err != nil {
		_ = files.Remove(tempPath)
		return err
	}
	if err := files.Rename(tempPath, path); err != nil {
		_ = files.Remove(tempPath)
		return err
	}
	return nil
}
