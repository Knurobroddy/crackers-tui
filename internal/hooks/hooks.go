// Package hooks implements game-specific install steps declared in pack
// manifests. Each hook returns undo actions that are stored in the marker and
// executed on remove or rollback.
package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

var hookTypes = map[string]func(raw json.RawMessage) (Hook, error){
	protonDLLOverrideType: newProtonDLLOverride,
}

var undoOps = map[string]func(raw json.RawMessage) error{
	wineRegRestoreOp: runWineRegRestore,
}

// HookCtx is what a hook knows about the target install.
type HookCtx struct {
	GameID  string
	BuildID string
	RootDir string
	Extra   map[string]string // detection data, e.g. steam_library, steam_appid
}

// Hook is one manifest hook instance.
type Hook interface {
	// Validate checks preconditions without writing anything.
	Validate(ctx HookCtx) error
	// Apply performs the change and returns the actions that undo it.
	Apply(ctx HookCtx) ([]UndoAction, error)
}

// UndoAction is one entry of the marker's "undo" list. It serializes as a flat
// JSON object: {"op": "...", <op-specific fields>}.
type UndoAction struct {
	Op  string
	Raw json.RawMessage // the whole JSON object, including "op"
}

// undoHeader reads only the "op" field, to route UnmarshalJSON.
type undoHeader struct {
	Op string `json:"op"`
}

// MarshalJSON implements json.Marshaler.
func (u UndoAction) MarshalJSON() ([]byte, error) {
	if len(u.Raw) == 0 {
		return json.Marshal(map[string]string{"op": u.Op})
	}
	return u.Raw, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (u *UndoAction) UnmarshalJSON(b []byte) error {
	var header undoHeader
	if err := json.Unmarshal(b, &header); err != nil {
		return err
	}
	if header.Op == "" {
		return errors.New("undo action without op")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return err
	}
	u.Op = header.Op
	u.Raw = buf.Bytes()
	return nil
}

// New creates a hook from its manifest JSON object.
func New(hookType string, raw json.RawMessage) (Hook, error) {
	factory, ok := hookTypes[hookType]
	if !ok {
		return nil, &remote.UpdateRequiredError{Doc: "pack manifest", Reason: fmt.Sprintf("unknown hook type %q", hookType)}
	}
	return factory(raw)
}

// RunUndo executes one undo action.
func RunUndo(undo UndoAction) error {
	run, ok := undoOps[undo.Op]
	if !ok {
		return &remote.UpdateRequiredError{Doc: "marker", Reason: fmt.Sprintf("unknown undo action %q", undo.Op)}
	}
	slog.Debug("running undo action", "op", undo.Op)
	return run(undo.Raw)
}

func newUndo(v any, op string) (UndoAction, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return UndoAction{}, err
	}
	return UndoAction{Op: op, Raw: b}, nil
}
