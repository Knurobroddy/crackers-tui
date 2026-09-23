// Package update implements self-update from GitHub Releases (ADR §7).
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
	"github.com/Knurobroddy/crackers-tui/internal/logx"
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
	log        *slog.Logger
}

// New returns an updater for the running version using GitHub Releases.
func New(current string, log *slog.Logger) (*Updater, error) {
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		return nil, err
	}
	return newUpdater(current, source, runtime.GOOS, runtime.GOARCH, log)
}

func newUpdater(current string, source selfupdate.Source, goos, arch string, log *slog.Logger) (*Updater, error) {
	log = logx.OrDiscard(log)
	selfupdate.SetLogger(slog.NewLogLogger(log.Handler(), slog.LevelDebug))
	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    source,
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: config.ChecksumsAsset},
		OS:        goos,
		Arch:      arch,
	})
	if err != nil {
		return nil, err
	}
	return &Updater{current: current, source: source, up: up, goos: goos, arch: arch, log: log}, nil
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
		return nil, fmt.Errorf("update check failed: %w", err)
	}
	if !found {
		u.log.Info("no release found for this platform", "repo", config.GitHubRepo)
		return nil, nil
	}
	if !rel.GreaterThan(u.current) {
		u.log.Info("up to date", "current", u.current, "latest", rel.Version())
		return nil, nil
	}
	u.log.Info("update available", "current", u.current, "latest", rel.Version(), "asset", rel.AssetName)
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

// applyTo downloads the release archive, validates it against checksums.txt
// and replaces target. The archive entry is looked up by the canonical binary
// name (not the running file's name), so a renamed executable, such as
// "crackers-modinst (1).exe" from a browser, still updates in place.
func (u *Updater) applyTo(ctx context.Context, r *Release, target string) error {
	data, err := u.download(ctx, r.rel, r.rel.AssetID)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", r.rel.AssetName, err)
	}
	sums, err := u.download(ctx, r.rel, r.rel.ValidationAssetID)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", config.ChecksumsAsset, err)
	}
	validator := &selfupdate.ChecksumValidator{UniqueFilename: config.ChecksumsAsset}
	if err := validator.Validate(r.rel.AssetName, data, sums); err != nil {
		return fmt.Errorf("update %s failed validation: %w", r.rel.AssetName, err)
	}
	bin, err := selfupdate.DecompressCommand(bytes.NewReader(data), r.rel.AssetName, config.AppSlug, u.goos, u.arch)
	if err != nil {
		return err
	}
	if err := update.Apply(bin, update.Options{TargetPath: target}); err != nil {
		return fmt.Errorf("could not replace %s: %w", target, err)
	}
	u.log.Info("updated", "exe", target, "version", r.Version)
	return nil
}

func (u *Updater) download(ctx context.Context, rel *selfupdate.Release, assetID int64) ([]byte, error) {
	rc, err := u.source.DownloadReleaseAsset(ctx, rel, assetID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// CleanupOld removes the hidden ".<exe>.old" file that a previous update on
// Windows could not delete while the old process was still running.
func CleanupOld() {
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return
	}
	os.Remove(filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".old"))
}
