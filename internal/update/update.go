// Package update implements self-update from GitHub Releases.
package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/creativeprojects/go-selfupdate/update"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

// Release is a newer release that can be applied.
type Release struct {
	Version string
	rel     *selfupdate.Release
}

// Updater checks for and applies releases of config.GitHubRepo.
type Updater struct {
	current    string
	source     selfupdate.Source
	up         *selfupdate.Updater
	goos, arch string
}

// New returns an updater for the running version using GitHub Releases.
func New(current string) (*Updater, error) {
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return nil, err
	}
	return newUpdater(current, source, runtime.GOOS, runtime.GOARCH)
}

// Enabled reports whether update checks make sense for this build: not a dev
// build.
func Enabled(current string) bool {
	return current != config.DevVersion
}

// Check returns the latest release if it is newer than the running version,
// or nil if there is none.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	rel, found, err := u.up.DetectLatest(ctx, selfupdate.ParseSlug(config.GitHubRepo))
	if err != nil {
		return nil, fmt.Errorf("detect latest release: %w", err)
	}
	if !found {
		slog.Debug("no release found for this platform", "repo", config.GitHubRepo)
		return nil, nil
	}
	if !rel.GreaterThan(u.current) {
		slog.Debug("up to date", "current", u.current, "latest", rel.Version())
		return nil, nil
	}
	slog.Debug("update available", "current", u.current, "latest", rel.Version(), "asset", rel.AssetName)
	return &Release{Version: rel.Version(), rel: rel}, nil
}

// Apply replaces the running executable with the release.
func (u *Updater) Apply(ctx context.Context, r *Release) error {
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return err
	}
	return u.applyTo(ctx, r, exe)
}

// CleanupOld removes the hidden ".<exe>.old" file that a previous update on
// Windows could not delete while the old process was still running.
func CleanupOld() {
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".old"))
}

func newUpdater(current string, source selfupdate.Source, goos, arch string) (*Updater, error) {
	selfupdate.SetLogger(slog.NewLogLogger(slog.Default().Handler(), slog.LevelDebug))
	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    source,
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: config.ChecksumsAsset},
		OS:        goos,
		Arch:      arch,
	})
	if err != nil {
		return nil, err
	}
	return &Updater{current: current, source: source, up: up, goos: goos, arch: arch}, nil
}

// applyTo downloads the release archive, validates it against checksums.txt
// and replaces target. The archive entry is looked up by the canonical binary
// name (not the running file's name), so a renamed executable, such as
// "crackers-modinst (1).exe" from a browser, still updates in place.
func (u *Updater) applyTo(ctx context.Context, r *Release, target string) error {
	data, err := u.download(ctx, r.rel, r.rel.AssetID)
	if err != nil {
		return fmt.Errorf("download %s: %w", r.rel.AssetName, err)
	}
	sums, err := u.download(ctx, r.rel, r.rel.ValidationAssetID)
	if err != nil {
		return fmt.Errorf("download %s: %w", config.ChecksumsAsset, err)
	}
	validator := &selfupdate.ChecksumValidator{UniqueFilename: config.ChecksumsAsset}
	if err := validator.Validate(r.rel.AssetName, data, sums); err != nil {
		return fmt.Errorf("validate %s: %w", r.rel.AssetName, err)
	}
	bin, err := selfupdate.DecompressCommand(bytes.NewReader(data), r.rel.AssetName, config.AppSlug, u.goos, u.arch)
	if err != nil {
		return err
	}
	if err := update.Apply(bin, update.Options{TargetPath: target}); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	slog.Debug("updated", "exe", target, "version", r.Version)
	return nil
}

func (u *Updater) download(ctx context.Context, rel *selfupdate.Release, assetID int64) ([]byte, error) {
	rc, err := u.source.DownloadReleaseAsset(ctx, rel, assetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}
