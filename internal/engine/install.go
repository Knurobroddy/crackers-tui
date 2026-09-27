package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
	"github.com/Knurobroddy/crackers-tui/internal/hooks"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// writeProgressEvery is how many written files share one progress step.
const writeProgressEvery = 25

// installJournal records everything written so a failed install can be rolled
// back.
type installJournal struct {
	files  []string          // rel paths of files renamed into place
	hashes map[string]string // rel path -> SHA-256 of the pack's content
	dirs   []string          // rel paths of directories created, parents first
	undo   []hooks.UndoAction
	// oldHooksUndone is set once the previous pack's hook changes are undone;
	// a rollback after that cannot bring them back.
	oldHooksUndone bool
}

func newInstallJournal() *installJournal {
	return &installJournal{hashes: map[string]string{}}
}

// preparedPack is a pack after pre-flight, download and planning: everything
// is verified, nothing in the game dir has been touched.
type preparedPack struct {
	manifest     *remote.Manifest
	manifestHash string
	ownedDirs    []string
	preserve     []string
	plan         *plan
	activeHooks  []hooks.Hook
	hookCtx      hooks.HookCtx
	tempDir      string
}

func (p *preparedPack) close() {
	if p.plan != nil {
		p.plan.Close()
	}
	_ = os.RemoveAll(p.tempDir)
}

type downloadProgress struct {
	emit         ProgressFunc
	name         string
	index, count int
}

func (p downloadProgress) report(done, total int64) {
	p.emit(Event{Kind: EventDownload, File: p.name, Index: p.index, Count: p.count, Done: done, Total: total})
}

// Install installs a pack, replacing an installed one. Nothing in the game
// dir changes before every download is verified and every path validated; any
// later failure rolls back and restores the previous pack.
//
// Leftovers are files the pack would write, or folders it owns, that already
// exist and do not belong to the installed pack (e.g. from an earlier manual
// mod install). They fail the install with a *LeftoversError unless
// req.CleanLeftovers is set, in which case they are deleted first.
func (e *Engine) Install(ctx context.Context, req InstallRequest, progress ProgressFunc) error {
	emit := emitter(progress)
	root := req.Game.RootDir
	if err := e.recoverStaging(root); err != nil {
		return fmt.Errorf("recover interrupted install: %w", err)
	}
	prepared, err := e.prepare(ctx, req, emit)
	if err != nil {
		return err
	}
	defer prepared.close()
	stagedPack, err := e.stageInstalledPack(root, emit)
	if err != nil {
		return err
	}
	if err := e.handleLeftovers(req, prepared, emit); err != nil {
		return restoreAfter(stagedPack, err)
	}
	journal := newInstallJournal()
	if err := e.writePack(ctx, req, prepared, stagedPack, journal, emit); err != nil {
		return e.abortInstall(root, journal, stagedPack, err)
	}
	e.discardStaged(stagedPack)
	return nil
}

// prepare fetches and checks the manifest, validates the hooks that apply to
// the build, downloads and verifies every file and expands the plan (which
// validates every zip entry).
func (e *Engine) prepare(ctx context.Context, req InstallRequest, emit ProgressFunc) (*preparedPack, error) {
	prepared, err := e.fetchCheckedManifest(ctx, req, emit)
	if err != nil {
		return nil, err
	}
	prepared.hookCtx = hooks.HookCtx{GameID: req.Game.GameID, BuildID: req.Game.BuildID, RootDir: req.Game.RootDir, Extra: req.Game.Extra}
	if prepared.activeHooks, err = activeHooks(prepared.manifest, req, prepared.hookCtx); err != nil {
		return nil, err
	}
	if err := e.downloadPack(ctx, req, prepared, emit); err != nil {
		return nil, err
	}
	return prepared, nil
}

// prepareFiles is prepare without hook validation, for leftover cleanup,
// which runs no hooks. Hook types are still checked.
func (e *Engine) prepareFiles(ctx context.Context, req InstallRequest, emit ProgressFunc) (*preparedPack, error) {
	prepared, err := e.fetchCheckedManifest(ctx, req, emit)
	if err != nil {
		return nil, err
	}
	if err := checkHookTypes(prepared.manifest); err != nil {
		return nil, err
	}
	if err := e.downloadPack(ctx, req, prepared, emit); err != nil {
		return nil, err
	}
	return prepared, nil
}

// fetchCheckedManifest fetches the manifest and validates everything in it
// that does not need the downloads: its pack and game, owned_dirs, preserve
// and every destination path.
func (e *Engine) fetchCheckedManifest(ctx context.Context, req InstallRequest, emit ProgressFunc) (*preparedPack, error) {
	root := req.Game.RootDir
	step(emit, "Fetching pack manifest…")
	manifest, manifestHash, err := e.source.FetchManifest(ctx, req.Pack.Manifest)
	if err != nil {
		return nil, err
	}
	if manifest.ID != req.Pack.ID || manifest.GameID != req.Game.GameID {
		return nil, fmt.Errorf("manifest mismatch: got pack %q game %q, want %q %q", manifest.ID, manifest.GameID, req.Pack.ID, req.Game.GameID)
	}
	step(emit, "Checking pack…")
	ownedDirs, err := cleanRelPaths("owned_dirs", root, manifest.OwnedDirs)
	if err != nil {
		return nil, err
	}
	preserve, err := cleanRelPaths("preserve", root, manifest.Preserve)
	if err != nil {
		return nil, err
	}
	if err := validateDestinations(root, manifest.EffectiveFiles(req.FilesKey)); err != nil {
		return nil, err
	}
	return &preparedPack{manifest: manifest, manifestHash: manifestHash, ownedDirs: ownedDirs, preserve: preserve}, nil
}

func validateDestinations(root string, entries []remote.FileEntry) error {
	for _, entry := range entries {
		if err := checkDest(root, entry); err != nil {
			return err
		}
	}
	return nil
}

// activeHooks creates every hook of the manifest and validates the ones that
// apply to the build.
func activeHooks(manifest *remote.Manifest, req InstallRequest, hookCtx hooks.HookCtx) ([]hooks.Hook, error) {
	var active []hooks.Hook
	for _, spec := range manifest.Hooks {
		hook, err := hooks.New(spec.Type, spec.Raw)
		if err != nil {
			return nil, err
		}
		if !spec.AppliesTo(req.Game.BuildID) {
			continue
		}
		if err := hook.Validate(hookCtx); err != nil {
			return nil, err
		}
		active = append(active, hook)
	}
	return active, nil
}

// checkHookTypes fails on a hook type this version does not know.
func checkHookTypes(manifest *remote.Manifest) error {
	for _, spec := range manifest.Hooks {
		if _, err := hooks.New(spec.Type, spec.Raw); err != nil {
			return err
		}
	}
	return nil
}

// downloadPack downloads every file into a new temp dir and builds the plan.
// On error the temp dir is deleted.
func (e *Engine) downloadPack(ctx context.Context, req InstallRequest, prepared *preparedPack, emit ProgressFunc) error {
	tempDir, err := os.MkdirTemp("", config.AppSlug+"-*")
	if err != nil {
		return err
	}
	prepared.tempDir = tempDir
	entries := prepared.manifest.EffectiveFiles(req.FilesKey)
	downloaded, err := e.downloadAll(ctx, entries, tempDir, emit)
	if err != nil {
		prepared.close()
		return err
	}
	step(emit, "Preparing files…")
	if prepared.plan, err = buildPlan(req.Game.RootDir, entries, downloaded); err != nil {
		prepared.close()
		return err
	}
	return nil
}

// downloadAll downloads entries[i] to tempDir and returns the local paths.
func (e *Engine) downloadAll(ctx context.Context, entries []remote.FileEntry, tempDir string, emit ProgressFunc) ([]string, error) {
	downloaded := make([]string, len(entries))
	for i, entry := range entries {
		name := displayName(entry.URL)
		step(emit, "Downloading %s (%d/%d)…", name, i+1, len(entries))
		downloaded[i] = filepath.Join(tempDir, fmt.Sprintf("%04d", i))
		progress := downloadProgress{emit: emit, name: name, index: i + 1, count: len(entries)}
		if err := e.source.Download(ctx, entry, downloaded[i], progress.report); err != nil {
			return nil, err
		}
	}
	return downloaded, nil
}

// stageInstalledPack moves the installed pack, if any, aside. It returns nil
// when no pack is installed.
func (e *Engine) stageInstalledPack(root string, emit ProgressFunc) (*staging, error) {
	if _, err := os.Lstat(MarkerPath(root)); err != nil {
		return nil, nil
	}
	step(emit, "Moving the installed pack aside…")
	stagedPack, err := e.stage(root)
	if err != nil {
		return nil, fmt.Errorf("stage installed pack: %w", err)
	}
	return stagedPack, nil
}

// handleLeftovers fails with a *LeftoversError, or deletes the leftovers when
// the request allows it.
func (e *Engine) handleLeftovers(req InstallRequest, prepared *preparedPack, emit ProgressFunc) error {
	root := req.Game.RootDir
	leftovers, err := prepared.plan.leftovers(root, prepared.ownedDirs)
	if err != nil {
		return err
	}
	if len(leftovers) == 0 {
		return nil
	}
	if !req.CleanLeftovers {
		return &LeftoversError{Root: root, Paths: leftovers}
	}
	step(emit, "Removing leftover mod files…")
	return e.removeLeftovers(root, leftovers)
}

// restoreAfter moves a staged pack back after err and returns err, joined
// with the restore failure if there is one.
func restoreAfter(stagedPack *staging, err error) error {
	if stagedPack == nil {
		return err
	}
	if restoreErr := stagedPack.restore(); restoreErr != nil {
		return errors.Join(err, ErrRollbackIncomplete, restoreErr)
	}
	return err
}

// writePack writes the files, keeps the user's preserved files (same pack
// only), undoes the old pack's hooks, runs the new hooks and writes the
// marker last.
func (e *Engine) writePack(ctx context.Context, req InstallRequest, prepared *preparedPack, stagedPack *staging, journal *installJournal, emit ProgressFunc) error {
	root := req.Game.RootDir
	if err := e.applyFiles(ctx, root, prepared.plan, journal, emit); err != nil {
		return err
	}
	if stagedPack != nil && stagedPack.old.PackID == prepared.manifest.ID {
		if err := e.keepUserFiles(stagedPack, prepared.preserve, journal); err != nil {
			return err
		}
	}
	if err := undoOldHooks(stagedPack, journal); err != nil {
		return err
	}
	if err := e.applyHooks(prepared.activeHooks, prepared.hookCtx, journal, emit); err != nil {
		return err
	}
	step(emit, "Finishing…")
	return e.commit(root, prepared, req.Game.BuildID, journal)
}

func undoOldHooks(stagedPack *staging, journal *installJournal) error {
	if stagedPack == nil || len(stagedPack.old.Undo) == 0 {
		return nil
	}
	journal.oldHooksUndone = true
	return runUndo(stagedPack.old.Undo)
}

// abortInstall rolls back the journal, moves the staged pack back and returns
// err joined with what could not be undone.
func (e *Engine) abortInstall(root string, journal *installJournal, stagedPack *staging, err error) error {
	var rollbackErrs []error
	if rollbackErr := e.rollback(root, journal); rollbackErr != nil {
		rollbackErrs = append(rollbackErrs, rollbackErr)
	}
	if stagedPack != nil {
		if restoreErr := stagedPack.restore(); restoreErr != nil {
			rollbackErrs = append(rollbackErrs, restoreErr)
		}
	}
	errs := []error{err}
	if journal.oldHooksUndone {
		errs = append(errs, ErrPreviousHooksUndone)
	}
	if len(rollbackErrs) > 0 {
		errs = append(append(errs, ErrRollbackIncomplete), rollbackErrs...)
	}
	if len(errs) == 1 {
		return err
	}
	return errors.Join(errs...)
}

func (e *Engine) discardStaged(stagedPack *staging) {
	if stagedPack == nil {
		return
	}
	if err := stagedPack.discard(); err != nil {
		slog.Warn("delete staged pack", "path", stagedPack.dir, "err", err)
	}
}

func (e *Engine) commit(root string, prepared *preparedPack, buildID string, journal *installJournal) error {
	manifest := prepared.manifest
	marker := &Marker{
		SchemaVersion:  config.MarkerSchemaVersion,
		AppVersion:     e.appVersion,
		PackID:         manifest.ID,
		PackName:       manifest.Name,
		PackVersion:    manifest.Version,
		ManifestSHA256: prepared.manifestHash,
		BuildID:        buildID,
		InstalledAt:    time.Now().UTC().Truncate(time.Second),
		Files:          nonNil(journal.files),
		FileSHA256:     journal.hashes,
		DirsCreated:    nonNil(journal.dirs),
		OwnedDirs:      nonNil(prepared.ownedDirs),
		Undo:           journal.undo,
	}
	if marker.Undo == nil {
		marker.Undo = []hooks.UndoAction{}
	}
	if err := writeMarker(e.files, root, marker); err != nil {
		return err
	}
	slog.Debug("write marker", "path", MarkerPath(root))
	return nil
}

// applyFiles creates the plan's directories, then writes its files in order.
func (e *Engine) applyFiles(ctx context.Context, root string, p *plan, journal *installJournal, emit ProgressFunc) error {
	for _, dir := range p.Dirs {
		if err := e.ensureDir(root, dir, journal); err != nil {
			return err
		}
	}
	for i, file := range p.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i%writeProgressEvery == 0 || i == len(p.Files)-1 {
			step(emit, "Writing files (%d/%d)…", i+1, len(p.Files))
		}
		if dir := path.Dir(file.Rel); dir != "." {
			if err := e.ensureDir(root, dir, journal); err != nil {
				return err
			}
		}
		if err := e.writePlanFile(file, journal); err != nil {
			return err
		}
	}
	return nil
}

// applyHooks runs the hooks, journaling their undo actions.
func (e *Engine) applyHooks(active []hooks.Hook, hookCtx hooks.HookCtx, journal *installJournal, emit ProgressFunc) error {
	if len(active) > 0 {
		step(emit, "Applying game settings…")
	}
	for _, hook := range active {
		undo, err := hook.Apply(hookCtx)
		if err != nil {
			return err
		}
		journal.undo = append(journal.undo, undo...)
	}
	return nil
}

// ensureDir creates rel (and its parents) below root, journaling each
// directory it creates. Existing non-directories (including symlinks) fail.
func (e *Engine) ensureDir(root, rel string, journal *installJournal) error {
	current, currentRel := root, ""
	for _, part := range strings.Split(rel, "/") {
		currentRel = path.Join(currentRel, part)
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("create directory %s: path exists and is not a directory", current)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fsErr(current, err)
		}
		if err := e.files.Mkdir(current, dirPerm); err != nil {
			return fsErr(current, err)
		}
		journal.dirs = append(journal.dirs, currentRel)
		slog.Debug("create directory", "path", current)
	}
	return nil
}

// writePlanFile writes file to <target>.modinst-tmp and renames it into
// place, so the target is never partly written.
func (e *Engine) writePlanFile(file planFile, journal *installJournal) error {
	tempPath := file.Abs + config.TmpSuffix
	sum, err := e.writePlanFileTemp(file, tempPath)
	if err != nil {
		_ = e.files.Remove(tempPath)
		return err
	}
	if err := e.files.Rename(tempPath, file.Abs); err != nil {
		_ = e.files.Remove(tempPath)
		return fsErr(file.Abs, err)
	}
	journal.files = append(journal.files, file.Rel)
	journal.hashes[file.Rel] = sum
	slog.Debug("write file", "path", file.Abs)
	return nil
}

// writePlanFileTemp copies file's content to tempPath and returns its hex
// SHA-256.
func (e *Engine) writePlanFileTemp(file planFile, tempPath string) (string, error) {
	src, err := file.open()
	if err != nil {
		return "", fmt.Errorf("open %s in pack: %w", file.Rel, err)
	}
	defer func() { _ = src.Close() }()
	out, err := e.files.Create(tempPath, file.Mode)
	if err != nil {
		return "", fsErr(file.Abs, err)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hash), src); err != nil {
		_ = out.Close()
		return "", fmt.Errorf("write %s: %w", file.Abs, fsErr(file.Abs, err))
	}
	if err := out.Close(); err != nil {
		return "", fsErr(file.Abs, err)
	}
	if runtime.GOOS != "windows" {
		// Create's mode is subject to the umask; Chmod's is not.
		if err := e.files.Chmod(tempPath, file.Mode); err != nil {
			return "", fsErr(file.Abs, err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// rollback undoes a journal: delete written files, run undo actions in
// reverse, then remove created directories that are empty, deepest first.
func (e *Engine) rollback(root string, journal *installJournal) error {
	slog.Warn("roll back install", "path", root)
	var errs []error
	for i := len(journal.files) - 1; i >= 0; i-- {
		file := filepath.Join(root, filepath.FromSlash(journal.files[i]))
		if err := e.files.Remove(file); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fsErr(file, err))
		}
	}
	if err := runUndo(journal.undo); err != nil {
		errs = append(errs, err)
	}
	for i := len(journal.dirs) - 1; i >= 0; i-- {
		if err := removeIfEmpty(e.files, filepath.Join(root, filepath.FromSlash(journal.dirs[i]))); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func removeIfEmpty(files FileWriter, dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fsErr(dir, err)
	}
	if len(entries) > 0 {
		return nil
	}
	if err := files.Remove(dir); err != nil && !os.IsNotExist(err) {
		return fsErr(dir, err)
	}
	return nil
}

// runUndo runs undo actions in reverse order and collects their errors.
func runUndo(undo []hooks.UndoAction) error {
	var errs []error
	for i := len(undo) - 1; i >= 0; i-- {
		if err := hooks.RunUndo(undo[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cleanRelPaths validates a manifest list of paths below the game root
// (owned_dirs, preserve); none may be the game root itself.
func cleanRelPaths(field, root string, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, rel := range paths {
		cleaned, err := pathsafe.Clean(rel)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", field, err)
		}
		if _, err := pathsafe.JoinNotRoot(root, cleaned); err != nil {
			return nil, fmt.Errorf("check %s: %w", field, err)
		}
		out = append(out, cleaned)
	}
	return out, nil
}

// checkDest validates a manifest dest before anything is downloaded.
func checkDest(root string, entry remote.FileEntry) error {
	if entry.Kind == remote.KindFile {
		_, err := pathsafe.JoinNotRoot(root, entry.Dest)
		return err
	}
	_, err := pathsafe.Join(root, entry.Dest)
	return err
}

// displayName is a short name for a download: the URL's last path segment,
// or the last two when the last one is a bare version or has no extension
// (e.g. ".../BepInExPack_Valheim/5.4.2202/"), or host/segment as a fallback
// (e.g. "drive.google.com/uc").
func displayName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	var segments []string
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	switch n := len(segments); {
	case n == 0:
		return parsed.Host
	case !isVersion(segments[n-1]) && strings.Contains(segments[n-1], "."):
		return segments[n-1]
	case n >= 2:
		return segments[n-2] + " " + segments[n-1]
	default:
		return parsed.Host + "/" + segments[0]
	}
}

func isVersion(segment string) bool {
	return strings.Trim(segment, "0123456789.") == ""
}
