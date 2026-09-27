package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/hooks"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// Marker is <RootDir>/.crackers-modinst.json. Paths in Files, DirsCreated and
// OwnedDirs are relative to the root with forward slashes; paths inside Undo
// are absolute.
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
	markerPath := MarkerPath(root)
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return nil, err
	}
	var marker Marker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return nil, fmt.Errorf("parse marker %s: %w", markerPath, err)
	}
	if err := remote.CheckSchema("marker "+markerPath, marker.SchemaVersion, config.MarkerSchemaVersion); err != nil {
		return nil, err
	}
	return &marker, nil
}

// writeMarker writes the marker via a temp file and rename.
func writeMarker(files FileWriter, root string, marker *Marker) error {
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	markerPath := MarkerPath(root)
	return fsErr(markerPath, writeAtomic(files, markerPath, append(raw, '\n'), filePerm))
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
