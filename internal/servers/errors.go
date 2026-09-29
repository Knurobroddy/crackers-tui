package servers

import (
	"errors"
	"fmt"
)

var (
	// ErrAlreadySaved means the folder is already in the list.
	ErrAlreadySaved = errors.New("server folder already saved")
	// ErrNotSaved means the folder is not in the list.
	ErrNotSaved = errors.New("server folder not saved")
	// ErrNoConfigDir means the OS has no user config folder to keep the list in.
	ErrNoConfigDir = errors.New("no user config folder for saved servers")
)

// FileError means the saved servers file exists but could not be used. The
// file is left as it is.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string {
	return fmt.Sprintf("read saved servers %s: %v", e.Path, e.Err)
}

func (e *FileError) Unwrap() error { return e.Err }
