package engine

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// State is the install state of a game (ADR §5.1).
type State int

const (
	NotInstalled State = iota
	Installed
	UpdateAvailable
	NotOffered // installed pack is no longer in the index
	Unknown    // the marker could not be read
)

// Status is the derived status of a game root.
type Status struct {
	State  State
	Marker *Marker
	Pack   remote.PackRef // the installed pack's index entry, if offered
	Err    error          // marker read error (Unknown)
	// CheckErr is set when the remote manifest could not be fetched to check
	// for updates; State is then Installed.
	CheckErr error
}

// String is the short status shown in menus.
func (s Status) String() string {
	switch s.State {
	case NotInstalled:
		return "Not installed"
	case Installed:
		if s.CheckErr != nil {
			return fmt.Sprintf("Installed: %s %s (could not check for updates)", s.Marker.PackName, s.Marker.PackVersion)
		}
		return fmt.Sprintf("Installed: %s %s", s.Marker.PackName, s.Marker.PackVersion)
	case UpdateAvailable:
		return fmt.Sprintf("Update available: %s %s", s.Marker.PackName, s.Marker.PackVersion)
	case NotOffered:
		return fmt.Sprintf("Installed (pack no longer offered): %s %s", s.Marker.PackName, s.Marker.PackVersion)
	default:
		return "Unknown (marker unreadable)"
	}
}

// Status reads the marker in root and compares it with the remote.
func (e *Engine) Status(ctx context.Context, root string, ix *remote.Index) Status {
	mk, err := ReadMarker(root)
	if errors.Is(err, os.ErrNotExist) {
		return Status{State: NotInstalled}
	}
	if err != nil {
		return Status{State: Unknown, Err: err}
	}
	ref, ok := ix.Pack(mk.PackID)
	if !ok {
		return Status{State: NotOffered, Marker: mk}
	}
	hash, err := e.client.ManifestHash(ctx, ref.Manifest)
	if err != nil {
		e.log.Warn("could not check for pack update", "pack", mk.PackID, "err", err)
		return Status{State: Installed, Marker: mk, Pack: ref, CheckErr: err}
	}
	if hash != mk.ManifestSHA256 {
		return Status{State: UpdateAvailable, Marker: mk, Pack: ref}
	}
	return Status{State: Installed, Marker: mk, Pack: ref}
}
