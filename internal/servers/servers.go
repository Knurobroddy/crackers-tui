// Package servers keeps the server folders the player saved, so they need
// not be entered again on every run.
package servers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/fsutil"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// Store reads and writes the saved servers file.
type Store struct{ path string }

// New returns a store for the file at path. An empty path means there is no
// user config folder; every call then fails with ErrNoConfigDir.
func New(path string) *Store {
	return &Store{path: path}
}

// Folders returns the saved folders in the order they were added. A missing
// file is an empty list.
func (s *Store) Folders() ([]string, error) {
	file, err := s.load()
	if err != nil {
		return nil, err
	}
	return file.Servers, nil
}

// Add saves dir, which must be absolute. A file that could not be read is
// never overwritten.
func (s *Store) Add(dir string) error {
	file, err := s.load()
	if err != nil {
		return err
	}
	if file.index(dir) >= 0 {
		return ErrAlreadySaved
	}
	file.Servers = append(file.Servers, filepath.Clean(dir))
	return s.save(file)
}

// Forget removes dir from the list. The folder itself is never touched.
func (s *Store) Forget(dir string) error {
	file, err := s.load()
	if err != nil {
		return err
	}
	i := file.index(dir)
	if i < 0 {
		return ErrNotSaved
	}
	file.Servers = slices.Delete(file.Servers, i, i+1)
	return s.save(file)
}

type serversFile struct {
	SchemaVersion int      `json:"schema_version"`
	Servers       []string `json:"servers"`
}

// index returns the position of dir in the list, comparing paths the way the
// OS does, or -1.
func (f *serversFile) index(dir string) int {
	key := pathKey(dir)
	return slices.IndexFunc(f.Servers, func(saved string) bool { return pathKey(saved) == key })
}

func (s *Store) load() (*serversFile, error) {
	if s.path == "" {
		return nil, ErrNoConfigDir
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &serversFile{SchemaVersion: config.ServersSchemaVersion}, nil
	}
	if err != nil {
		return nil, &FileError{Path: s.path, Err: err}
	}
	var file serversFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, &FileError{Path: s.path, Err: fmt.Errorf("parse: %w", err)}
	}
	if err := remote.CheckSchema("saved servers", file.SchemaVersion, config.ServersSchemaVersion); err != nil {
		return nil, &FileError{Path: s.path, Err: err}
	}
	return &file, nil
}

func (s *Store) save(file *serversFile) error {
	if file.Servers == nil {
		file.Servers = []string{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), dirPerm); err != nil {
		return fmt.Errorf("create folder for %s: %w", s.path, err)
	}
	if err := fsutil.WriteAtomic(s.path, append(data, '\n'), filePerm); err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	return nil
}

func pathKey(dir string) string {
	return fsutil.FoldCase(filepath.Clean(dir))
}
