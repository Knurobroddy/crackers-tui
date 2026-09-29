package app

import (
	"errors"
	"fmt"
)

var (
	// ErrNoFolder means the player entered no folder.
	ErrNoFolder = errors.New("no folder entered")

	errServersUnavailable = errors.New("saving servers is not available")
)

// NotServerFolderError means a folder holds none of the supported servers.
type NotServerFolderError struct {
	Dir       string
	Supported []string // names of the supported servers
}

func (e *NotServerFolderError) Error() string {
	return fmt.Sprintf("no supported server in %s", e.Dir)
}
