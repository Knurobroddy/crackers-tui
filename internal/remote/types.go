// Package remote fetches and validates the remote library: games.json,
// index.json, pack manifests and the files they reference. The remote is data
// only; nothing downloaded is ever executed.
package remote

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
)

// Games is games.json (detection rules).
type Games struct {
	SchemaVersion int              `json:"schema_version"`
	MinAppVersion string           `json:"min_app_version"`
	Games         []detect.GameDef `json:"games"`
}

// Game returns the game with the given id.
func (g *Games) Game(id string) (detect.GameDef, bool) {
	for _, gd := range g.Games {
		if gd.ID == id {
			return gd, true
		}
	}
	return detect.GameDef{}, false
}

// Index is index.json (pack catalog).
type Index struct {
	SchemaVersion int       `json:"schema_version"`
	MinAppVersion string    `json:"min_app_version"`
	Packs         []PackRef `json:"packs"`
}

// PackRef is one catalog entry of index.json.
type PackRef struct {
	ID          string `json:"id"`
	GameID      string `json:"game_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Manifest    string `json:"manifest"`
}

// PacksFor returns the packs of a game in index order.
func (ix *Index) PacksFor(gameID string) []PackRef {
	var out []PackRef
	for _, p := range ix.Packs {
		if p.GameID == gameID {
			out = append(out, p)
		}
	}
	return out
}

// Pack returns the pack with the given id.
func (ix *Index) Pack(id string) (PackRef, bool) {
	for _, p := range ix.Packs {
		if p.ID == id {
			return p, true
		}
	}
	return PackRef{}, false
}

// GamesWithPacks returns the set of game ids that have at least one pack.
func (ix *Index) GamesWithPacks() map[string]bool {
	m := map[string]bool{}
	for _, p := range ix.Packs {
		m[p.GameID] = true
	}
	return m
}

// File entry kinds.
const (
	KindFile = "file"
	KindZip  = "zip"
)

// FileEntry is one downloadable object of a manifest.
type FileEntry struct {
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Kind    string `json:"kind"`
	Dest    string `json:"dest"`
	ZipRoot string `json:"zip_root,omitempty"`
}

// HookSpec is one manifest hook. Raw keeps the whole JSON object so each hook
// type can decode its own fields.
type HookSpec struct {
	Type   string
	Builds []string
	Raw    json.RawMessage
}

// UnmarshalJSON decodes the common fields and keeps the raw object.
func (h *HookSpec) UnmarshalJSON(b []byte) error {
	var common struct {
		Type   string   `json:"type"`
		Builds []string `json:"builds"`
	}
	if err := json.Unmarshal(b, &common); err != nil {
		return err
	}
	h.Type = common.Type
	h.Builds = common.Builds
	h.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON returns the raw object.
func (h HookSpec) MarshalJSON() ([]byte, error) {
	if len(h.Raw) > 0 {
		return h.Raw, nil
	}
	return json.Marshal(struct {
		Type   string   `json:"type"`
		Builds []string `json:"builds"`
	}{h.Type, h.Builds})
}

// AppliesTo reports whether the hook runs for the given build id.
func (h HookSpec) AppliesTo(buildID string) bool {
	for _, b := range h.Builds {
		if b == buildID {
			return true
		}
	}
	return false
}

// Manifest is a pack manifest (ADR §3.3).
type Manifest struct {
	SchemaVersion int                    `json:"schema_version"`
	ID            string                 `json:"id"`
	GameID        string                 `json:"game_id"`
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	OwnedDirs     []string               `json:"owned_dirs"`
	Preserve      []string               `json:"preserve,omitempty"`
	Files         map[string][]FileEntry `json:"files"`
	Hooks         []HookSpec             `json:"hooks"`
	Loader        json.RawMessage        `json:"loader"`
}

// EffectiveFiles returns "common" followed by the list named filesKey.
// Missing lists are treated as empty.
func (m *Manifest) EffectiveFiles(filesKey string) []FileEntry {
	out := append([]FileEntry(nil), m.Files["common"]...)
	if filesKey != "common" {
		out = append(out, m.Files[filesKey]...)
	}
	return out
}

// HasLoader reports whether loader is present and non-null.
func (m *Manifest) HasLoader() bool {
	t := bytes.TrimSpace(m.Loader)
	return len(t) > 0 && !bytes.Equal(t, []byte("null"))
}

// Validate checks the manifest parts that do not depend on the game root:
// schema version, loader, and file entry fields. Paths and hooks are checked
// by the engine.
func (m *Manifest) Validate() error {
	if err := CheckSchema("pack manifest", m.SchemaVersion, config.ManifestSchemaVersion); err != nil {
		return err
	}
	if m.HasLoader() {
		return fmt.Errorf("pack %q uses a mod loader, which is not supported in this version of %s", m.ID, config.AppName)
	}
	for list, entries := range m.Files {
		for i, fe := range entries {
			if err := fe.validate(); err != nil {
				return fmt.Errorf("pack %q: files.%s[%d]: %w", m.ID, list, i, err)
			}
		}
	}
	for i, h := range m.Hooks {
		if h.Type == "" {
			return fmt.Errorf("pack %q: hooks[%d] has no type", m.ID, i)
		}
	}
	return nil
}

func (fe FileEntry) validate() error {
	if fe.URL == "" {
		return fmt.Errorf("missing url")
	}
	if b, err := hex.DecodeString(fe.SHA256); err != nil || len(b) != 32 {
		return fmt.Errorf("invalid sha256 for %s", fe.URL)
	}
	if fe.Size < 0 {
		return fmt.Errorf("invalid size for %s", fe.URL)
	}
	switch fe.Kind {
	case KindFile:
		if fe.Dest == "" {
			return fmt.Errorf("file %s has no dest", fe.URL)
		}
	case KindZip:
	default:
		return fmt.Errorf("unknown kind %q for %s (update %s)", fe.Kind, fe.URL, config.AppName)
	}
	return nil
}
