package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/fsutil"
	"github.com/Knurobroddy/crackers-tui/internal/hooks"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// Marker is <RootDir>/.crackers-modinst.json (ADR §5.1). Paths in Files,
// DirsCreated and OwnedDirs are relative to the root with forward slashes;
// paths inside Undo are absolute.
type Marker struct {
	SchemaVersion  int                `json:"schema_version"`
	AppVersion     string             `json:"app_version"`
	PackID         string             `json:"pack_id"`
	PackName       string             `json:"pack_name"`
	PackVersion    string             `json:"pack_version"`
	ManifestSHA256 string             `json:"manifest_sha256"`
	BuildID        string             `json:"build_id"`
	InstalledAt    time.Time          `json:"installed_at"`
	Files          []string           `json:"files"`
	DirsCreated    []string           `json:"dirs_created"`
	OwnedDirs      []string           `json:"owned_dirs"`
	FileSHA256     map[string]string  `json:"file_sha256,omitempty"` // Files entry -> SHA-256 as shipped
	Undo           []hooks.UndoAction `json:"undo"`
}

// MarkerPath returns the marker path for a game root.
func MarkerPath(root string) string {
	return filepath.Join(root, config.MarkerFileName)
}

// ReadMarker reads the marker. A missing marker returns an error for which
// os.IsNotExist / errors.Is(err, fs.ErrNotExist) is true.
func ReadMarker(root string) (*Marker, error) {
	p := MarkerPath(root)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("marker %s is unreadable: %w", p, err)
	}
	if err := remote.CheckSchema("marker "+p, m.SchemaVersion, config.MarkerSchemaVersion); err != nil {
		return nil, err
	}
	return &m, nil
}

// writeMarker writes the marker via a tmp file and rename.
func writeMarker(root string, m *Marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := MarkerPath(root)
	return fsErr(p, fsutil.WriteAtomic(p, append(b, '\n'), 0o644))
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
