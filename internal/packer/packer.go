// Package packer builds pack manifests for the remote library (ADR §13).
//
// Input is a pack folder:
//
//	<pack>/pack.modinst   metadata (JSON, see Meta)
//	<pack>/common/        files mirroring the game root, for every build
//	<pack>/windows/       files only for builds whose "files" list is "windows"
//	<pack>/linux/         files only for builds whose "files" list is "linux"
//
// Each non-empty list folder becomes one deterministic zip in <library>/files/.
// Entries in Meta.External (e.g. Thunderstore packages) are downloaded only to
// compute their sha256 and size; they are referenced by URL, not re-hosted.
// The result is <library>/packs/<id>.json plus an upserted index.json entry.
package packer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
	"github.com/Knurobroddy/crackers-tui/internal/fsutil"
	"github.com/Knurobroddy/crackers-tui/internal/hooks"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// MetaFileName is the pack metadata file inside a pack folder.
const MetaFileName = "pack.modinst"

// Lists are the pack folder's list subfolders, in manifest order.
var Lists = []string{"common", "windows", "linux"}

// Meta is the content of pack.modinst.
type Meta struct {
	ID          string   `json:"id"`
	GameID      string   `json:"game_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	OwnedDirs   []string `json:"owned_dirs"`
	// Preserve lists files or folders whose user-changed files survive a
	// reinstall or update of the same pack (e.g. "BepInEx/config").
	Preserve []string          `json:"preserve,omitempty"`
	Hooks    []remote.HookSpec `json:"hooks"`
	// External files per list ("common", "windows", "linux"). url, kind, dest
	// and zip_root are required as in the manifest; sha256 and size are
	// computed (or, if given, verified).
	External map[string][]remote.FileEntry `json:"external,omitempty"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// junk files that are never packed.
var junk = map[string]bool{"thumbs.db": true, "desktop.ini": true, ".ds_store": true}

// fixed timestamp for zip entries so identical input gives identical archives.
var zipTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Result describes what Build wrote.
type Result struct {
	Manifest     *remote.Manifest
	ManifestPath string   // <library>/packs/<id>.json
	Uploads      []string // files written to <library>/files (new or changed)
	Ignored      []string // top-level entries of the pack folder that were not packed
}

// Builder builds packs.
type Builder struct {
	// Client downloads External files; remote.NewDownloader is used if nil.
	Client *remote.Client
	Logf   func(format string, args ...any)
}

func (b *Builder) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
	}
}

// ReadMeta reads and validates <packDir>/pack.modinst.
func ReadMeta(packDir string) (*Meta, error) {
	p := filepath.Join(packDir, MetaFileName)
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Meta
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &m, nil
}

func (m *Meta) validate() error {
	switch {
	case !idRe.MatchString(m.ID):
		return fmt.Errorf("id %q must be lower case letters, digits, '.', '_' or '-'", m.ID)
	case m.GameID == "":
		return errors.New("game_id is required")
	case m.Name == "":
		return errors.New("name is required")
	case m.Version == "":
		return errors.New("version is required")
	}
	for field, paths := range map[string][]string{"owned_dirs": m.OwnedDirs, "preserve": m.Preserve} {
		for _, d := range paths {
			c, err := pathsafe.Clean(d)
			if err != nil {
				return fmt.Errorf("%s: %w", field, err)
			}
			if c == "" {
				return fmt.Errorf("%s: %q is the game directory itself", field, d)
			}
		}
	}
	for i, h := range m.Hooks {
		if _, err := hooks.New(h.Type, h.Raw); err != nil {
			return fmt.Errorf("hooks[%d]: %w", i, err)
		}
		if len(h.Builds) == 0 {
			return fmt.Errorf("hooks[%d]: builds is empty, so the hook would never run", i)
		}
	}
	for list, entries := range m.External {
		if !isList(list) {
			return fmt.Errorf("external: unknown list %q (use common, windows or linux)", list)
		}
		for i, fe := range entries {
			if fe.URL == "" || (fe.Kind != remote.KindFile && fe.Kind != remote.KindZip) {
				return fmt.Errorf("external.%s[%d]: url and kind (file or zip) are required", list, i)
			}
			if fe.Kind == remote.KindFile && fe.Dest == "" {
				return fmt.Errorf("external.%s[%d]: kind file needs dest", list, i)
			}
		}
	}
	return nil
}

func isList(s string) bool {
	for _, l := range Lists {
		if l == s {
			return true
		}
	}
	return false
}

// Build packs packDir into libraryDir.
func (b *Builder) Build(ctx context.Context, packDir, libraryDir string) (*Result, error) {
	meta, err := ReadMeta(packDir)
	if err != nil {
		return nil, err
	}
	if err := checkGame(libraryDir, meta.GameID, b); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "modinst-pack-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	res := &Result{}
	if res.Ignored, err = ignoredEntries(packDir); err != nil {
		return nil, err
	}

	files := map[string][]remote.FileEntry{}
	local := map[string][]string{} // local copies, parallel to files
	type upload struct{ src, name string }
	var uploads []upload

	for _, list := range Lists {
		for i, fe := range meta.External[list] {
			dst := filepath.Join(tmp, fmt.Sprintf("ext-%s-%d", list, i))
			b.logf("downloading %s", fe.URL)
			sum, size, err := b.fetch(ctx, fe.URL, dst)
			if err != nil {
				return nil, fmt.Errorf("external.%s[%d]: %w", list, i, err)
			}
			if fe.SHA256 != "" && !strings.EqualFold(fe.SHA256, sum) {
				return nil, fmt.Errorf("external.%s[%d]: %s has sha256 %s, pack.modinst says %s", list, i, fe.URL, sum, fe.SHA256)
			}
			if fe.Size > 0 && fe.Size != size {
				return nil, fmt.Errorf("external.%s[%d]: %s has %d bytes, pack.modinst says %d", list, i, fe.URL, size, fe.Size)
			}
			fe.SHA256, fe.Size = sum, size
			files[list] = append(files[list], fe)
			local[list] = append(local[list], dst)
		}

		dir := filepath.Join(packDir, list)
		zipPath := filepath.Join(tmp, list+".zip")
		n, err := zipDir(dir, zipPath)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		sum, size, err := hashFile(zipPath)
		if err != nil {
			return nil, err
		}
		name := fmt.Sprintf("%s-%s-%s.zip", meta.ID, list, sum[:12])
		files[list] = append(files[list], remote.FileEntry{
			URL: "files/" + name, SHA256: sum, Size: size, Kind: remote.KindZip, Dest: "",
		})
		local[list] = append(local[list], zipPath)
		uploads = append(uploads, upload{zipPath, name})
		b.logf("packed %s/ (%d files) -> files/%s", list, n, name)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: nothing to pack: add files under common/, windows/ or linux/, or external entries", packDir)
	}

	// Validate every combination a build can select: common alone and common + each list.
	for _, list := range Lists {
		entries := append(append([]remote.FileEntry(nil), files["common"]...), extraList(files, list)...)
		copies := append(append([]string(nil), local["common"]...), extraList(local, list)...)
		if err := engine.ValidateFiles(entries, copies); err != nil {
			return nil, fmt.Errorf("pack would not install (files list %q): %w", list, err)
		}
	}

	m := &remote.Manifest{
		SchemaVersion: config.ManifestSchemaVersion,
		ID:            meta.ID,
		GameID:        meta.GameID,
		Name:          meta.Name,
		Version:       meta.Version,
		OwnedDirs:     append([]string{}, meta.OwnedDirs...),
		Preserve:      meta.Preserve,
		Files:         files,
		Hooks:         append([]remote.HookSpec{}, meta.Hooks...),
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')

	for _, u := range uploads {
		dst := filepath.Join(libraryDir, "files", u.name)
		if _, err := os.Stat(dst); err == nil {
			continue // content-addressed name: already there
		}
		if err := copyFile(u.src, dst); err != nil {
			return nil, err
		}
		res.Uploads = append(res.Uploads, dst)
	}
	res.ManifestPath = filepath.Join(libraryDir, "packs", meta.ID+".json")
	if err := writeFile(res.ManifestPath, raw); err != nil {
		return nil, err
	}
	if err := upsertIndex(filepath.Join(libraryDir, "index.json"), meta); err != nil {
		return nil, err
	}
	res.Manifest = m
	return res, nil
}

// extraList returns the list m[list] that a build adds on top of "common"
// (nil for "common" itself).
func extraList[T any](m map[string][]T, list string) []T {
	if list == "common" {
		return nil
	}
	return m[list]
}

// ignoredEntries lists top-level entries of the pack folder that are not packed.
func ignoredEntries(packDir string) ([]string, error) {
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Name() == MetaFileName || (e.IsDir() && isList(e.Name())) {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// checkGame verifies game_id against <library>/games.json when it exists.
func checkGame(libraryDir, gameID string, b *Builder) error {
	raw, err := os.ReadFile(filepath.Join(libraryDir, "games.json"))
	if errors.Is(err, fs.ErrNotExist) {
		b.logf("warning: %s has no games.json; game_id %q is not checked", libraryDir, gameID)
		return nil
	}
	if err != nil {
		return err
	}
	var g remote.Games
	if err := json.Unmarshal(raw, &g); err != nil {
		return fmt.Errorf("games.json: %w", err)
	}
	if _, ok := g.Game(gameID); !ok {
		return fmt.Errorf("game_id %q is not in %s", gameID, filepath.Join(libraryDir, "games.json"))
	}
	return nil
}

// zipDir writes a deterministic zip of dir (sorted entries, fixed times,
// normalized modes) and returns the number of files. A missing dir packs nothing.
func zipDir(dir, zipPath string) (int, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	type item struct {
		rel  string
		abs  string
		dir  bool
		mode os.FileMode
	}
	var items []item
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if junk[strings.ToLower(d.Name())] {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || (!d.IsDir() && !d.Type().IsRegular()) {
			return fmt.Errorf("%s: only regular files and folders can be packed", p)
		}
		clean, err := pathsafe.Clean(rel)
		if err != nil || clean != rel {
			return fmt.Errorf("%s: unsupported file name", p)
		}
		if strings.EqualFold(rel, config.MarkerFileName) {
			return fmt.Errorf("%s: the pack must not contain %s", p, config.MarkerFileName)
		}
		mode := os.FileMode(0o644)
		if d.IsDir() {
			mode = fs.ModeDir | 0o755
		} else if fi, err := d.Info(); err == nil && fi.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		items = append(items, item{rel: rel, abs: p, dir: d.IsDir(), mode: mode})
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].rel < items[j].rel })

	out, err := os.Create(zipPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	n := 0
	for _, it := range items {
		name := it.rel
		if it.dir {
			name += "/"
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime}
		if it.dir {
			h.Method = zip.Store
		}
		h.SetMode(it.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return 0, err
		}
		if it.dir {
			continue
		}
		f, err := os.Open(it.abs)
		if err != nil {
			return 0, err
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			return 0, err
		}
		n++
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return n, out.Close()
}

// fetch downloads rawURL to dst and returns its sha256 and size.
func (b *Builder) fetch(ctx context.Context, rawURL, dst string) (string, int64, error) {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return "", 0, fmt.Errorf("external url %q must be an absolute http(s) URL", rawURL)
	}
	if b.Client == nil {
		b.Client = remote.NewDownloader("modinst-pack", nil)
	}
	return b.Client.Fetch(ctx, rawURL, dst)
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFile(dst, b)
}

// writeFile creates p's folder and writes p atomically.
func writeFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return fsutil.WriteAtomic(p, b, 0o644)
}

// upsertIndex adds or replaces the pack's entry in index.json, keeping all
// other entries and fields. A missing index.json is created.
func upsertIndex(indexPath string, meta *Meta) error {
	doc := map[string]any{}
	raw, err := os.ReadFile(indexPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc["schema_version"] = config.IndexSchemaVersion
		doc["min_app_version"] = config.FirstAppVersion
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", indexPath, err)
		}
	}
	var packs []any
	if p, ok := doc["packs"].([]any); ok {
		packs = p
	}
	entry := map[string]any{
		"id":          meta.ID,
		"game_id":     meta.GameID,
		"name":        meta.Name,
		"description": meta.Description,
		"manifest":    path.Join("packs", meta.ID+".json"),
	}
	replaced := false
	for i, p := range packs {
		if pm, ok := p.(map[string]any); ok && pm["id"] == meta.ID {
			for k, v := range entry {
				pm[k] = v // keep unknown fields of the existing entry
			}
			packs[i] = pm
			replaced = true
		}
	}
	if !replaced {
		packs = append(packs, entry)
	}
	doc["packs"] = packs
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(indexPath, append(out, '\n'))
}
