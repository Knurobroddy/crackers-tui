package engine

import (
	"errors"
	"fmt"
	"strings"
)

const maxLeftoversShown = 5

var (
	// ErrNothingToRemove is returned by Remove when no pack is installed.
	ErrNothingToRemove = errors.New("nothing to remove: no pack is installed")
	// ErrPreviousHooksUndone is joined into an install error when the previous
	// pack's files were restored but its hook changes were already undone.
	ErrPreviousHooksUndone = errors.New("previous pack's hook changes were undone")
	// ErrRemoveIncomplete is joined into a remove error; the marker was kept.
	ErrRemoveIncomplete = errors.New("remove incomplete; marker kept")
	// ErrRollbackIncomplete is joined into an install error when rollback or
	// restoring the previous pack also failed.
	ErrRollbackIncomplete = errors.New("rollback incomplete")
	// ErrInvalidPack wraps an install error caused by a broken pack: a manifest
	// for another pack or game, or files that conflict with each other.
	ErrInvalidPack = errors.New("invalid pack")
)

// PermissionError is returned when the OS refuses a write.
type PermissionError struct {
	Path string
	Err  error
}

func (e *PermissionError) Error() string { return "permission denied: " + e.Path }
func (e *PermissionError) Unwrap() error { return e.Err }

// LeftoversError means files the pack would write, or folders it owns,
// already exist in the game dir and do not belong to an installed pack, e.g.
// from an earlier manual mod install. Nothing was written.
type LeftoversError struct {
	Root  string
	Paths []string // relative, forward slashes; folders end with "/"
}

func (e *LeftoversError) Error() string {
	list := strings.Join(e.Paths[:min(maxLeftoversShown, len(e.Paths))], ", ")
	if n := len(e.Paths) - maxLeftoversShown; n > 0 {
		list += fmt.Sprintf(" and %d more", n)
	}
	return fmt.Sprintf("leftover mod files in %s: %s", e.Root, list)
}
