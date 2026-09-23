// Command crackers-modinst is the Crackers Modinst TUI: it detects supported
// games and installs or removes one modpack per game from a remote library.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/detect/steam"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/tui"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

// version is set with -ldflags "-X main.version=<semver>".
var version = config.DevVersion

func main() {
	os.Exit(run())
}

func run() (code int) {
	// Hidden flags (not shown in the UI).
	fs := flag.NewFlagSet(config.AppSlug, flag.ContinueOnError)
	remoteURL := fs.String("remote", "", "remote library base URL (testing)")
	noUpdate := fs.Bool("no-update", false, "skip the self-update check")
	detectOnly := fs.Bool("detect-only", false, "print detection results as JSON and exit")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println(config.AppSlug, version)
		return 0
	}
	// --detect-only must never wait for input.
	interactive := !*detectOnly

	log, closeLog := openLog()
	defer closeLog()
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic", "panic", r, "stack", string(debug.Stack()))
			fmt.Fprintf(os.Stderr, "%s hit an internal error: %v\nLog file: %s\n", config.AppName, r, config.LogPath())
			pauseOnWindows(interactive)
			code = 1
		}
	}()

	base := config.RemoteBaseURL
	if env := os.Getenv(config.RemoteEnvVar); env != "" {
		base = env
	}
	if *remoteURL != "" {
		base = *remoteURL
	}
	log.Info("starting", "app", config.AppName, "version", version, "os", runtime.GOOS, "arch", runtime.GOARCH, "remote", base)

	client, err := remote.NewClient(base, version, log)
	if err != nil {
		return fatal(log, err, interactive)
	}
	detector := detect.NewRegistry(log, steam.New(log))

	if *detectOnly {
		return runDetectOnly(client, detector, log)
	}

	update.CleanupOld()
	var updater *update.Updater
	if !*noUpdate && update.Enabled(version) {
		if updater, err = update.New(version, log); err != nil {
			log.Warn("self-update disabled", "err", err)
			updater = nil
		}
	}

	err = tui.Run(tui.Deps{
		AppVersion: version,
		Client:     client,
		Engine:     engine.New(client, version, log),
		Detector:   detector,
		Updater:    updater,
		LogPath:    config.LogPath(),
		Log:        log,
	})
	if err != nil {
		return fatal(log, err, interactive)
	}
	return 0
}

// runDetectOnly prints the detection results for games that have packs.
func runDetectOnly(client *remote.Client, detector *detect.Registry, log *slog.Logger) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	games, err := client.FetchGames(ctx)
	if err != nil {
		return fatal(log, err, false)
	}
	index, err := client.FetchIndex(ctx)
	if err != nil {
		return fatal(log, err, false)
	}
	results := detector.Detect(games.Games, index.GamesWithPacks())
	if results == nil {
		results = []detect.Result{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fatal(log, err, false)
	}
	return 0
}

func fatal(log *slog.Logger, err error, interactive bool) int {
	log.Error("fatal", "err", err)
	fmt.Fprintf(os.Stderr, "%s: %v\nLog file: %s\n", config.AppName, err, config.LogPath())
	pauseOnWindows(interactive)
	return 1
}

// pauseOnWindows keeps a double-clicked console window open so the message
// can be read. It does nothing when not interactive.
func pauseOnWindows(interactive bool) {
	if runtime.GOOS != "windows" || !interactive {
		return
	}
	fmt.Fprint(os.Stderr, "Press Enter to exit.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// openLog truncates and opens the log file; logging is disabled if that fails.
func openLog() (*slog.Logger, func()) {
	f, err := os.Create(config.LogPath())
	if err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})), func() { f.Close() }
}
