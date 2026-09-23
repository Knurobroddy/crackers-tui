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

// journal records everything written so a failed install can be rolled back.
type journal struct {
	files  []string          // rel paths of files renamed into place
	hashes map[string]string // rel path -> SHA-256 of the pack's content
	dirs   []string          // rel paths of directories created, parents first
	undo   []hooks.UndoAction
	tmp    string // abs path of a tmp file currently being written
}

// prepared is a pack after pre-flight, download and planning: everything is
// verified, nothing in the game dir has been touched.
type prepared struct {
	m            *remote.Manifest
	manifestHash string
	ownedDirs    []string
	preserve     []string
	plan         *plan
	active       []hooks.Hook
	hctx         hooks.HookCtx
	tmpDir       string
}

func (p *prepared) close() {
	if p.plan != nil {
		p.plan.Close()
	}
	os.RemoveAll(p.tmpDir)
}

// prepare runs pre-flight, downloads and verifies every file and expands the
// plan (which validates every zip entry). Hooks are validated only when
// withHooks is set (installs; leftover cleanup does not run hooks).
func (e *Engine) prepare(ctx context.Context, req InstallRequest, emit ProgressFunc, log *slog.Logger, withHooks bool) (_ *prepared, err error) {
	root := req.Game.RootDir
	step(emit, "Fetching pack manifest…")
	m, manifestHash, err := e.client.FetchManifest(ctx, req.Pack.Manifest)
	if err != nil {
		return nil, err
	}
	if m.ID != req.Pack.ID || m.GameID != req.Game.GameID {
		return nil, fmt.Errorf("pack error: manifest is for pack %q / game %q, expected %q / %q", m.ID, m.GameID, req.Pack.ID, req.Game.GameID)
	}
	step(emit, "Checking pack…")
	ownedDirs, err := cleanRelPaths("owned_dirs", root, m.OwnedDirs)
	if err != nil {
		return nil, err
	}
	preserve, err := cleanRelPaths("preserve", root, m.Preserve)
	if err != nil {
		return nil, err
	}
	files := m.EffectiveFiles(req.FilesKey)
	for _, fe := range files {
		if err := checkDest(root, fe); err != nil {
			return nil, err
		}
	}
	hctx := hooks.HookCtx{GameID: req.Game.GameID, BuildID: req.Game.BuildID, RootDir: root, Extra: req.Game.Extra, Log: log}
	var active []hooks.Hook
	for _, spec := range m.Hooks {
		h, err := hooks.New(spec.Type, spec.Raw)
		if err != nil {
			return nil, err
		}
		if !withHooks || !spec.AppliesTo(req.Game.BuildID) {
			continue
		}
		if err := h.Validate(hctx); err != nil {
			return nil, err
		}
		active = append(active, h)
	}

	tmpDir, err := os.MkdirTemp("", config.AppSlug+"-*")
	if err != nil {
		return nil, err
	}
	pr := &prepared{m: m, manifestHash: manifestHash, ownedDirs: ownedDirs, preserve: preserve, active: active, hctx: hctx, tmpDir: tmpDir}
	defer func() {
		if err != nil {
			pr.close()
		}
	}()
	downloaded := make([]string, len(files))
	for i, fe := range files {
		name := displayName(fe.URL)
		step(emit, "Downloading %s (%d/%d)…", name, i+1, len(files))
		downloaded[i] = filepath.Join(tmpDir, fmt.Sprintf("%04d", i))
		err := e.client.Download(ctx, fe, downloaded[i], func(done, total int64) {
			emit(Event{Kind: EventDownload, File: name, Index: i + 1, Count: len(files), Done: done, Total: total})
		})
		if err != nil {
			return nil, err
		}
	}
	step(emit, "Preparing files…")
	if pr.plan, err = buildPlan(root, files, downloaded); err != nil {
		return nil, err
	}
	return pr, nil
}

// Install installs a pack (ADR §5.3). If a pack is already installed it is
// replaced; the UI must have confirmed that.
//
// Order: pre-flight, download + verify every file, expand the plan (which
// validates every zip entry), then move an installed pack aside (see
// staging), check for leftovers, write files, keep the user's preserved files
// (same pack only), undo the old pack's hooks, run the new hooks and write the
// marker last. Nothing in the game dir is touched before all downloads are
// verified and all paths are validated. Any failure rolls back and moves the
// old pack back; only its hook changes are lost if the failure comes after
// they were undone.
//
// Leftovers are files the pack would write, or folders it owns, that already
// exist and do not belong to the installed pack (e.g. from an earlier manual
// mod install). They fail the install with a *LeftoversError unless
// req.CleanLeftovers is set, in which case they are deleted first.
func (e *Engine) Install(ctx context.Context, req InstallRequest, progress ProgressFunc) (err error) {
	emit := emitter(progress)
	root := req.Game.RootDir
	log := e.log.With("pack", req.Pack.ID, "root", root)
	log.Info("install started", "build", req.Game.BuildID, "clean_leftovers", req.CleanLeftovers)
	defer func() {
		if err != nil {
			log.Error("install failed", "err", err)
		} else {
			log.Info("install finished")
		}
	}()

	if err := e.recoverStaging(root); err != nil {
		return fmt.Errorf("could not clean up after an interrupted install: %w", err)
	}
	pr, err := e.prepare(ctx, req, emit, log, true)
	if err != nil {
		return err
	}
	defer pr.close()

	// Move the installed pack, if any, aside.
	var st *staging
	if _, err := os.Lstat(MarkerPath(root)); err == nil {
		step(emit, "Moving the installed pack aside…")
		if st, err = e.stage(root); err != nil {
			return fmt.Errorf("could not move the currently installed pack aside: %w", err)
		}
	}
	left, err := pr.plan.leftovers(root, pr.ownedDirs)
	if err == nil && len(left) > 0 {
		if !req.CleanLeftovers {
			err = &LeftoversError{Root: root, Paths: left}
		} else {
			step(emit, "Removing leftover mod files…")
			err = e.removeLeftovers(root, left)
		}
	}
	if err != nil {
		if st != nil {
			if rErr := st.restore(); rErr != nil {
				return fmt.Errorf("%w\n\nMoving the installed pack back also had errors:\n%v", err, rErr)
			}
		}
		return err
	}

	// Write files, run hooks, write the marker last; roll back on any error.
	j := &journal{hashes: map[string]string{}}
	oldUndone := false
	err = e.applyFiles(ctx, root, pr.plan, j, emit)
	if err == nil && st != nil && st.old.PackID == pr.m.ID {
		err = e.keepUserFiles(st, pr.preserve, j)
	}
	if err == nil && st != nil && len(st.old.Undo) > 0 {
		oldUndone = true
		err = runUndo(st.old.Undo, log)
	}
	if err == nil {
		err = e.applyHooks(pr.active, pr.hctx, j, emit)
	}
	if err == nil {
		step(emit, "Finishing…")
		err = e.commit(root, pr.m, pr.manifestHash, req.Game.BuildID, pr.ownedDirs, j)
	}
	if err != nil {
		var rbErrs []error
		if rbErr := e.rollback(root, j); rbErr != nil {
			rbErrs = append(rbErrs, rbErr)
		}
		if st != nil {
			if rErr := st.restore(); rErr != nil {
				rbErrs = append(rbErrs, rErr)
			}
		}
		if oldUndone {
			err = fmt.Errorf("%w\n\nThe previous pack's files were put back, but its game settings were already undone; reinstall it to fix that.", err)
		}
		if len(rbErrs) > 0 {
			return fmt.Errorf("%w\n\nRollback also had errors:\n%v", err, errors.Join(rbErrs...))
		}
		return err
	}
	if st != nil {
		if err := e.fail("staging", 0); err != nil {
			return err
		}
		if err := st.discard(); err != nil {
			log.Warn("could not delete the previous pack's files", "err", err)
		}
	}
	return nil
}

// commit writes the marker (step 10).
func (e *Engine) commit(root string, m *remote.Manifest, manifestHash, buildID string, ownedDirs []string, j *journal) error {
	if err := e.fail("marker", 0); err != nil {
		return err
	}
	mk := &Marker{
		SchemaVersion:  config.MarkerSchemaVersion,
		AppVersion:     e.appVersion,
		PackID:         m.ID,
		PackName:       m.Name,
		PackVersion:    m.Version,
		ManifestSHA256: manifestHash,
		BuildID:        buildID,
		InstalledAt:    time.Now().UTC().Truncate(time.Second),
		Files:          nonNil(j.files),
		FileSHA256:     j.hashes,
		DirsCreated:    nonNil(j.dirs),
		OwnedDirs:      nonNil(ownedDirs),
		Undo:           j.undo,
	}
	if mk.Undo == nil {
		mk.Undo = []hooks.UndoAction{}
	}
	if err := writeMarker(root, mk); err != nil {
		return err
	}
	e.log.Info("marker written", "path", MarkerPath(root), "files", len(mk.Files), "dirs_created", len(mk.DirsCreated), "undo", len(mk.Undo))
	return nil
}

// applyFiles performs step 6: directories and files in plan order.
func (e *Engine) applyFiles(ctx context.Context, root string, p *plan, j *journal, emit ProgressFunc) error {
	for _, d := range p.Dirs {
		if err := e.ensureDir(root, d, j); err != nil {
			return err
		}
	}
	for i, f := range p.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i%25 == 0 || i == len(p.Files)-1 {
			step(emit, "Writing files (%d/%d)…", i+1, len(p.Files))
		}
		if err := e.fail("file", i); err != nil {
			return err
		}
		if dir := path.Dir(f.Rel); dir != "." {
			if err := e.ensureDir(root, dir, j); err != nil {
				return err
			}
		}
		if err := e.writeFile(f, j); err != nil {
			return err
		}
	}
	return nil
}

// applyHooks performs step 9: runs the hooks, journaling their undo actions.
func (e *Engine) applyHooks(active []hooks.Hook, hctx hooks.HookCtx, j *journal, emit ProgressFunc) error {
	if len(active) > 0 {
		step(emit, "Applying game settings…")
	}
	for i, h := range active {
		if err := e.fail("hook", i); err != nil {
			return err
		}
		undo, err := h.Apply(hctx)
		if err != nil {
			return err
		}
		j.undo = append(j.undo, undo...)
	}
	return nil
}

// ensureDir creates rel (and its parents) below root, journaling each
// directory it creates. Existing non-directories (including symlinks) fail.
func (e *Engine) ensureDir(root, rel string, j *journal) error {
	cur, acc := root, ""
	for _, part := range strings.Split(rel, "/") {
		acc = path.Join(acc, part)
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("cannot create directory %s: a file or link with that name exists", cur)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fsErr(cur, err)
		}
		if err := os.Mkdir(cur, 0o755); err != nil {
			return fsErr(cur, err)
		}
		j.dirs = append(j.dirs, acc)
		e.log.Info("created directory", "path", cur)
	}
	return nil
}

// writeFile writes f to <target>.modinst-tmp and renames it into place.
func (e *Engine) writeFile(f planFile, j *journal) (err error) {
	tmp := f.Abs + config.TmpSuffix
	j.tmp = tmp
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
		j.tmp = ""
	}()
	src, err := f.open()
	if err != nil {
		return fmt.Errorf("cannot read %s from the pack: %w", f.Rel, err)
	}
	defer src.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode)
	if err != nil {
		return fsErr(f.Abs, err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), src); err != nil {
		out.Close()
		return fmt.Errorf("cannot write %s: %w", f.Abs, fsErr(f.Abs, err))
	}
	if err := out.Close(); err != nil {
		return fsErr(f.Abs, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp, f.Mode); err != nil { // not subject to umask
			return fsErr(f.Abs, err)
		}
	}
	if err := os.Rename(tmp, f.Abs); err != nil {
		return fsErr(f.Abs, err)
	}
	j.files = append(j.files, f.Rel)
	j.hashes[f.Rel] = hex.EncodeToString(h.Sum(nil))
	e.log.Info("wrote file", "path", f.Abs)
	return nil
}

// rollback undoes a journal: delete written files, run undo actions in
// reverse, then remove created directories that are empty, deepest first.
func (e *Engine) rollback(root string, j *journal) error {
	e.log.Warn("rolling back", "files", len(j.files), "dirs", len(j.dirs), "undo", len(j.undo))
	var errs []error
	if j.tmp != "" {
		os.Remove(j.tmp)
	}
	for i := len(j.files) - 1; i >= 0; i-- {
		p := filepath.Join(root, filepath.FromSlash(j.files[i]))
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fsErr(p, err))
		}
	}
	if err := runUndo(j.undo, e.log); err != nil {
		errs = append(errs, err)
	}
	for i := len(j.dirs) - 1; i >= 0; i-- {
		if err := removeIfEmpty(filepath.Join(root, filepath.FromSlash(j.dirs[i]))); err != nil {
			errs = append(errs, err)
		}
	}
	err := errors.Join(errs...)
	if err != nil {
		e.log.Error("rollback errors", "err", err)
	}
	return err
}

// removeIfEmpty removes dir if it exists and is empty.
func removeIfEmpty(dir string) error {
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
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return fsErr(dir, err)
	}
	return nil
}

// runUndo runs undo actions in reverse order and collects their errors.
func runUndo(undo []hooks.UndoAction, log *slog.Logger) error {
	var errs []error
	for i := len(undo) - 1; i >= 0; i-- {
		if err := hooks.RunUndo(undo[i], log); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cleanRelPaths validates a manifest list of paths below the game root
// (owned_dirs, preserve); none may be the game root itself.
func cleanRelPaths(field, root string, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, d := range paths {
		c, err := pathsafe.Clean(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		if _, err := pathsafe.JoinNotRoot(root, c); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// checkDest validates a manifest dest before anything is downloaded.
func checkDest(root string, fe remote.FileEntry) error {
	if fe.Kind == remote.KindFile {
		_, err := pathsafe.JoinNotRoot(root, fe.Dest)
		return err
	}
	_, err := pathsafe.Join(root, fe.Dest)
	return err
}

// displayName is a short name for a download: the URL's last path segment,
// or the last two when the last one is a bare version or has no extension
// (e.g. ".../BepInExPack_Valheim/5.4.2202/"), or host/segment as a fallback
// (e.g. "drive.google.com/uc").
func displayName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	var segs []string
	for _, s := range strings.Split(u.Path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	switch n := len(segs); {
	case n == 0:
		return u.Host
	case !isVersion(segs[n-1]) && strings.Contains(segs[n-1], "."):
		return segs[n-1]
	case n >= 2:
		return segs[n-2] + " " + segs[n-1]
	default:
		return u.Host + "/" + segs[0]
	}
}

func isVersion(s string) bool {
	return strings.Trim(s, "0123456789.") == ""
}
