// Package packer builds pack manifests for the remote library.
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

const (
	// MetaFileName is the pack metadata file inside a pack folder.
	MetaFileName = "pack.modinst"

	dirPerm      fs.FileMode = 0o755
	filePerm     fs.FileMode = 0o644
	execFilePerm fs.FileMode = 0o755
)

var (
	// Lists are the pack folder's list subfolders, in manifest order.
	Lists = []string{"common", "windows", "linux"}

	idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

	// junk files that are never packed.
	junk = map[string]bool{"thumbs.db": true, "desktop.ini": true, ".ds_store": true}

	// zipTime is the fixed timestamp for zip entries so identical input gives
	// identical archives.
	zipTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
)

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
	// Out receives progress lines; io.Discard is used if nil.
	Out io.Writer
}

// packedUpload is a list's zip staged in the temp folder, pending upload to
// the library's files/ folder.
type packedUpload struct {
	src  string
	name string
}

// packedLists is what packLists produced: the manifest file entries and their
// local copies (for install validation), keyed by list, plus the zips pending
// upload.
type packedLists struct {
	files   map[string][]remote.FileEntry
	local   map[string][]string
	uploads []packedUpload
}

// zipEntry is a file or folder found while collecting one list's files, in
// walk order.
type zipEntry struct {
	rel   string // relative, slash-separated, no trailing slash
	isDir bool
}

// fileCollector accumulates the zipEntry values found while walking a list's
// source folder.
type fileCollector struct {
	root    string
	entries []zipEntry
}

// listBuilder packs the lists of one pack.modinst, holding what every list
// needs: the downloader, the pack metadata and the folders involved.
type listBuilder struct {
	*Builder
	meta    *Meta
	packDir string
	tempDir string
}

// ReadMeta reads and validates <packDir>/pack.modinst.
func ReadMeta(packDir string) (*Meta, error) {
	metaPath := filepath.Join(packDir, MetaFileName)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var meta Meta
	if err := dec.Decode(&meta); err != nil {
		return nil, fmt.Errorf("%s: %w", metaPath, err)
	}
	if err := meta.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", metaPath, err)
	}
	return &meta, nil
}

// Build packs packDir into libraryDir.
func (b *Builder) Build(ctx context.Context, packDir, libraryDir string) (*Result, error) {
	meta, ignored, err := b.readInputs(packDir, libraryDir)
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", "modinst-pack-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	packed, err := b.packLists(ctx, meta, packDir, tempDir)
	if err != nil {
		return nil, err
	}
	manifest := assembleManifest(meta, packed.files)
	if err := validateManifest(manifest, packed.local); err != nil {
		return nil, err
	}
	res := &Result{Manifest: manifest, Ignored: ignored}
	if err := writeOutputs(libraryDir, meta, packed.uploads, res); err != nil {
		return nil, err
	}
	return res, nil
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
	if err := validateRelPaths("owned_dirs", m.OwnedDirs); err != nil {
		return err
	}
	if err := validateRelPaths("preserve", m.Preserve); err != nil {
		return err
	}
	for i, hook := range m.Hooks {
		if _, err := hooks.New(hook.Type, hook.Raw); err != nil {
			return fmt.Errorf("hooks[%d]: %w", i, err)
		}
		if len(hook.Builds) == 0 {
			return fmt.Errorf("hooks[%d]: builds is empty, so the hook would never run", i)
		}
	}
	for list, entries := range m.External {
		if !isList(list) {
			return fmt.Errorf("external: unknown list %q (use common, windows or linux)", list)
		}
		for i, entry := range entries {
			if entry.URL == "" || (entry.Kind != remote.KindFile && entry.Kind != remote.KindZip) {
				return fmt.Errorf("external.%s[%d]: url and kind (file or zip) are required", list, i)
			}
			if entry.Kind == remote.KindFile && entry.Dest == "" {
				return fmt.Errorf("external.%s[%d]: kind file needs dest", list, i)
			}
		}
	}
	return nil
}

// validateRelPaths checks that every path in a Meta field (owned_dirs or
// preserve) is a clean path inside the game directory.
func validateRelPaths(field string, paths []string) error {
	for _, rawPath := range paths {
		clean, err := pathsafe.Clean(rawPath)
		if err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		if clean == "" {
			return fmt.Errorf("%s: %q is the game directory itself", field, rawPath)
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

func (b *Builder) out() io.Writer {
	if b.Out == nil {
		return io.Discard
	}
	return b.Out
}

func (b *Builder) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(b.out(), format+"\n", args...)
}

// readInputs reads pack.modinst, lists the pack folder's ignored entries and
// checks game_id against the library's games.json.
func (b *Builder) readInputs(packDir, libraryDir string) (*Meta, []string, error) {
	meta, err := ReadMeta(packDir)
	if err != nil {
		return nil, nil, err
	}
	ignored, err := ignoredEntries(packDir)
	if err != nil {
		return nil, nil, err
	}
	if err := b.checkGame(libraryDir, meta.GameID); err != nil {
		return nil, nil, err
	}
	return meta, ignored, nil
}

// checkGame verifies gameID against <library>/games.json when it exists.
func (b *Builder) checkGame(libraryDir, gameID string) error {
	gamesPath := filepath.Join(libraryDir, "games.json")
	raw, err := os.ReadFile(gamesPath)
	if errors.Is(err, fs.ErrNotExist) {
		b.logf("warning: %s has no games.json; game_id %q is not checked", libraryDir, gameID)
		return nil
	}
	if err != nil {
		return err
	}
	var games remote.Games
	if err := json.Unmarshal(raw, &games); err != nil {
		return fmt.Errorf("games.json: %w", err)
	}
	if _, ok := games.Game(gameID); !ok {
		return fmt.Errorf("game_id %q is not in %s", gameID, gamesPath)
	}
	return nil
}

// packLists resolves each list's external entries and zips its folder, in
// Lists order.
func (b *Builder) packLists(ctx context.Context, meta *Meta, packDir, tempDir string) (*packedLists, error) {
	lb := &listBuilder{Builder: b, meta: meta, packDir: packDir, tempDir: tempDir}
	packed := &packedLists{files: map[string][]remote.FileEntry{}, local: map[string][]string{}}
	for _, list := range Lists {
		if err := lb.resolveExternal(ctx, list, packed); err != nil {
			return nil, err
		}
		if err := lb.packList(list, packed); err != nil {
			return nil, err
		}
	}
	if len(packed.files) == 0 {
		return nil, fmt.Errorf("%s: nothing to pack: add files under common/, windows/ or linux/, or external entries", packDir)
	}
	return packed, nil
}

// resolveExternal downloads list's external entries, verifies their declared
// sha256 and size (if given) and records the verified entries in packed.
func (lb *listBuilder) resolveExternal(ctx context.Context, list string, packed *packedLists) error {
	for i, entry := range lb.meta.External[list] {
		dst := filepath.Join(lb.tempDir, fmt.Sprintf("ext-%s-%d", list, i))
		lb.logf("downloading %s", entry.URL)
		sum, size, err := lb.fetch(ctx, entry.URL, dst)
		if err != nil {
			return fmt.Errorf("external.%s[%d]: %w", list, i, err)
		}
		if entry.SHA256 != "" && !strings.EqualFold(entry.SHA256, sum) {
			return fmt.Errorf("external.%s[%d]: %s has sha256 %s, pack.modinst says %s", list, i, entry.URL, sum, entry.SHA256)
		}
		if entry.Size > 0 && entry.Size != size {
			return fmt.Errorf("external.%s[%d]: %s has %d bytes, pack.modinst says %d", list, i, entry.URL, size, entry.Size)
		}
		entry.SHA256, entry.Size = sum, size
		packed.files[list] = append(packed.files[list], entry)
		packed.local[list] = append(packed.local[list], dst)
	}
	return nil
}

// packList zips lb.packDir/list into a deterministic archive and records it
// in packed, unless the list has no files to pack.
func (lb *listBuilder) packList(list string, packed *packedLists) error {
	dir := filepath.Join(lb.packDir, list)
	paths, err := collectFiles(dir)
	if err != nil {
		return err
	}
	fileCount := 0
	for _, rel := range paths {
		if !isZipDirEntry(rel) {
			fileCount++
		}
	}
	if fileCount == 0 {
		return nil
	}
	zipPath := filepath.Join(lb.tempDir, list+".zip")
	if err := writeZip(zipPath, dir, paths); err != nil {
		return err
	}
	sum, size, err := hashFile(zipPath)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s-%s.zip", lb.meta.ID, list, sum[:12])
	packed.files[list] = append(packed.files[list], remote.FileEntry{
		URL: "files/" + name, SHA256: sum, Size: size, Kind: remote.KindZip, Dest: "",
	})
	packed.local[list] = append(packed.local[list], zipPath)
	packed.uploads = append(packed.uploads, packedUpload{zipPath, name})
	lb.logf("packed %s/ (%d files) -> files/%s", list, fileCount, name)
	return nil
}

// collectFiles walks dir and returns the relative, slash-separated entries to
// zip, sorted for determinism; folders are suffixed with "/". A missing dir
// returns nil.
func collectFiles(dir string) ([]string, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	collector := &fileCollector{root: dir}
	if err := filepath.WalkDir(dir, collector.visit); err != nil {
		return nil, err
	}
	sort.Slice(collector.entries, func(i, j int) bool { return collector.entries[i].rel < collector.entries[j].rel })

	paths := make([]string, len(collector.entries))
	for i, entry := range collector.entries {
		paths[i] = entry.rel
		if entry.isDir {
			paths[i] += "/"
		}
	}
	return paths, nil
}

func (c *fileCollector) visit(p string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if p == c.root {
		return nil
	}
	rel, err := filepath.Rel(c.root, p)
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
	c.entries = append(c.entries, zipEntry{rel: rel, isDir: d.IsDir()})
	return nil
}

// isZipDirEntry reports whether a path returned by collectFiles is a folder
// (folders are suffixed with "/").
func isZipDirEntry(rel string) bool {
	return strings.HasSuffix(rel, "/")
}

// writeZip writes a deterministic zip of paths (as returned by collectFiles)
// resolved against dir: sorted entries, fixed timestamps, normalized modes.
func writeZip(zipPath, dir string, paths []string) error {
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	zw := zip.NewWriter(out)
	for _, rel := range paths {
		if err := writeZipEntry(zw, dir, rel); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

// writeZipEntry writes one collectFiles entry (a file or, for a folder, an
// empty directory entry) to zw.
func writeZipEntry(zw *zip.Writer, dir, rel string) error {
	isDir := isZipDirEntry(rel)
	abs := filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(rel, "/")))
	mode := filePerm
	switch {
	case isDir:
		mode = fs.ModeDir | dirPerm
	default:
		if fi, err := os.Stat(abs); err == nil && fi.Mode().Perm()&0o111 != 0 {
			mode = execFilePerm
		}
	}
	h := &zip.FileHeader{Name: rel, Method: zip.Deflate, Modified: zipTime}
	if isDir {
		h.Method = zip.Store
	}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	if isDir {
		return nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

// assembleManifest builds the remote manifest from meta and the file entries
// packLists produced.
func assembleManifest(meta *Meta, files map[string][]remote.FileEntry) *remote.Manifest {
	return &remote.Manifest{
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
}

// validateManifest checks every combination a build can select (common alone
// and common + each list) against the engine, then the manifest's own rules.
func validateManifest(manifest *remote.Manifest, local map[string][]string) error {
	for _, list := range Lists {
		entries := append(append([]remote.FileEntry(nil), manifest.Files["common"]...), extraList(manifest.Files, list)...)
		copies := append(append([]string(nil), local["common"]...), extraList(local, list)...)
		if err := engine.ValidateFiles(entries, copies); err != nil {
			return fmt.Errorf("pack would not install (files list %q): %w", list, err)
		}
	}
	return manifest.Validate()
}

// extraList returns the list m[list] that a build adds on top of "common"
// (nil for "common" itself).
func extraList[T any](m map[string][]T, list string) []T {
	if list == "common" {
		return nil
	}
	return m[list]
}

// writeOutputs writes the manifest JSON, uploads new or changed zips and
// upserts the library's index.json, filling res.Uploads and res.ManifestPath.
func writeOutputs(libraryDir string, meta *Meta, uploads []packedUpload, res *Result) error {
	raw, err := json.MarshalIndent(res.Manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	for _, u := range uploads {
		dst := filepath.Join(libraryDir, "files", u.name)
		if _, err := os.Stat(dst); err == nil {
			continue // content-addressed name: already there
		}
		if err := copyFile(u.src, dst); err != nil {
			return err
		}
		res.Uploads = append(res.Uploads, dst)
	}
	res.ManifestPath = filepath.Join(libraryDir, "packs", meta.ID+".json")
	if err := writeFile(res.ManifestPath, raw); err != nil {
		return err
	}
	return upsertIndex(filepath.Join(libraryDir, "index.json"), meta)
}

// ignoredEntries lists top-level entries of the pack folder that are not packed.
func ignoredEntries(packDir string) ([]string, error) {
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.Name() == MetaFileName || (entry.IsDir() && isList(entry.Name())) {
			continue
		}
		out = append(out, entry.Name())
	}
	return out, nil
}

// fetch downloads rawURL to dst and returns its sha256 and size.
func (b *Builder) fetch(ctx context.Context, rawURL, dst string) (string, int64, error) {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return "", 0, fmt.Errorf("external url %q must be an absolute http(s) URL", rawURL)
	}
	if b.Client == nil {
		b.Client = remote.NewDownloader("modinst-pack")
	}
	return b.Client.Fetch(ctx, rawURL, dst)
}

func hashFile(filePath string) (string, int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFile(dst, data)
}

// writeFile creates filePath's folder and writes filePath atomically.
func writeFile(filePath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filePath), dirPerm); err != nil {
		return err
	}
	return fsutil.WriteAtomic(filePath, data, filePerm)
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
	if existing, ok := doc["packs"].([]any); ok {
		packs = existing
	}
	entry := map[string]any{
		"id":          meta.ID,
		"game_id":     meta.GameID,
		"name":        meta.Name,
		"description": meta.Description,
		"manifest":    path.Join("packs", meta.ID+".json"),
	}
	replaced := false
	for i, existing := range packs {
		if existingMap, ok := existing.(map[string]any); ok && existingMap["id"] == meta.ID {
			for k, v := range entry {
				existingMap[k] = v // keep unknown fields of the existing entry
			}
			packs[i] = existingMap
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
