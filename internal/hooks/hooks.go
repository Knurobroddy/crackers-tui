// Package hooks implements game-specific install steps declared in pack
// manifests. Each hook returns undo actions that are stored in the marker and
// executed on remove or rollback.
package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/logx"
)

// HookCtx is what a hook knows about the target install.
type HookCtx struct {
	GameID  string
	BuildID string
	RootDir string
	Extra   map[string]string // detection data, e.g. steam_library, steam_appid
	Log     *slog.Logger
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

// MarshalJSON implements json.Marshaler.
func (u UndoAction) MarshalJSON() ([]byte, error) {
	if len(u.Raw) == 0 {
		return json.Marshal(map[string]string{"op": u.Op})
	}
	return u.Raw, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (u *UndoAction) UnmarshalJSON(b []byte) error {
	var h struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(b, &h); err != nil {
		return err
	}
	if h.Op == "" {
		return fmt.Errorf("undo action without op")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return err
	}
	u.Op = h.Op
	u.Raw = buf.Bytes()
	return nil
}

func newUndo(v any, op string) (UndoAction, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return UndoAction{}, err
	}
	return UndoAction{Op: op, Raw: b}, nil
}

var hookTypes = map[string]func(raw json.RawMessage) (Hook, error){
	protonDLLOverrideType: newProtonDLLOverride,
}

var undoOps = map[string]func(raw json.RawMessage, log *slog.Logger) error{
	wineRegRestoreOp: runWineRegRestore,
}

// New creates a hook from its manifest JSON object.
func New(hookType string, raw json.RawMessage) (Hook, error) {
	f, ok := hookTypes[hookType]
	if !ok {
		return nil, fmt.Errorf("unknown hook type %q: please update %s", hookType, config.AppName)
	}
	return f(raw)
}

// RunUndo executes one undo action.
func RunUndo(u UndoAction, log *slog.Logger) error {
	log = logx.OrDiscard(log)
	f, ok := undoOps[u.Op]
	if !ok {
		return fmt.Errorf("unknown undo action %q: please update %s", u.Op, config.AppName)
	}
	log.Info("running undo action", "op", u.Op, "action", string(u.Raw))
	return f(u.Raw, log)
}
