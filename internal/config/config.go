// Package config holds compile-time constants shared by all packages.
package config

import (
	"os"
	"path/filepath"
)

const (
	// AppName is the display name used in the title bar, prompts and messages.
	AppName = "Crackers Modinst"
	// AppSlug is used for the binary, marker, log, env var, User-Agent and release assets.
	AppSlug = "crackers-modinst"

	// RemoteBaseURL is the default remote library: the main branch of
	// github.com/Knurobroddy/modinst-packs served by raw.githubusercontent.com.
	RemoteBaseURL = "https://raw.githubusercontent.com/Knurobroddy/modinst-packs/main/"
	// GitHubRepo is the "owner/repo" slug used for self-update.
	GitHubRepo = "Knurobroddy/crackers-tui"

	// RemoteEnvVar overrides RemoteBaseURL (testing).
	RemoteEnvVar = "CRACKERS_MODINST_REMOTE"

	// MarkerFileName is the marker written to the game root after a successful install.
	MarkerFileName = "." + AppSlug + ".json"
	// StagingDirName is the folder in the game root that holds the previous
	// pack's files while a pack is reinstalled or replaced.
	StagingDirName = "." + AppSlug + "-old"
	// LogFileName is created in os.TempDir() and truncated on every start.
	LogFileName = AppSlug + ".log"
	// TmpSuffix is appended to files while they are being written.
	TmpSuffix = ".modinst-tmp"
	// BakSuffix is appended to backups of files edited by hooks.
	BakSuffix = ".modinst-bak"

	// ChecksumsAsset is the GoReleaser checksum file validated by self-update.
	ChecksumsAsset = "checksums.txt"
)

// Highest schema_version of each remote/local document this build understands.
const (
	GamesSchemaVersion    = 1
	IndexSchemaVersion    = 1
	ManifestSchemaVersion = 1
	MarkerSchemaVersion   = 1
)

// FirstAppVersion is the first release; the pack tool writes it as
// min_app_version into a new index.json.
const FirstAppVersion = "0.1.0"

// DevVersion is the version of builds without -ldflags; it skips the self-update check.
const DevVersion = "dev"

// UserAgent returns the HTTP User-Agent for the given app version.
func UserAgent(version string) string {
	return AppSlug + "/" + version
}

// LogPath returns the absolute path of the log file.
func LogPath() string {
	return filepath.Join(os.TempDir(), LogFileName)
}
