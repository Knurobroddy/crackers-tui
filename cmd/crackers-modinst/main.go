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

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/detect/steam"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/tui"
	"github.com/Knurobroddy/crackers-tui/internal/update"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// detectTimeout bounds the network calls --detect-only makes.
const detectTimeout = 2 * time.Minute

// version is set with -ldflags "-X main.version=<semver>".
var version = config.DevVersion

// cliFlags are the hidden flags (not shown in the UI).
type cliFlags struct {
	remoteURL   string
	noUpdate    bool
	detectOnly  bool
	showVersion bool
}

// interactive reports whether the run may wait for user input.
func (f cliFlags) interactive() bool { return !f.detectOnly }

// baseURL resolves the remote library base URL: flag, then env, then default.
func (f cliFlags) baseURL() string {
	if f.remoteURL != "" {
		return f.remoteURL
	}
	if env := os.Getenv(config.RemoteEnvVar); env != "" {
		return env
	}
	return config.RemoteBaseURL
}

func main() {
	os.Exit(run())
}

func run() int {
	flags, err := parseFlags(os.Args[1:])
	if err != nil {
		return exitUsage
	}
	if flags.showVersion {
		fmt.Println(config.AppSlug, version)
		return exitOK
	}
	closeLog := setupLogging()
	defer closeLog()
	defer recoverPanic(flags.interactive())
	application, err := buildApp(flags)
	if err != nil {
		return fatal(err, flags.interactive())
	}
	if flags.detectOnly {
		return printDetection(application)
	}
	update.CleanupOld()
	if err := tui.Run(tui.Deps{AppVersion: version, App: application, LogPath: config.LogPath()}); err != nil {
		return fatal(err, true)
	}
	return exitOK
}

// parseFlags parses the hidden CLI flags.
func parseFlags(args []string) (cliFlags, error) {
	var flags cliFlags
	fs := flag.NewFlagSet(config.AppSlug, flag.ContinueOnError)
	fs.StringVar(&flags.remoteURL, "remote", "", "remote library base URL (testing)")
	fs.BoolVar(&flags.noUpdate, "no-update", false, "skip the self-update check")
	fs.BoolVar(&flags.detectOnly, "detect-only", false, "print detection results as JSON and exit")
	fs.BoolVar(&flags.showVersion, "version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return cliFlags{}, err
	}
	return flags, nil
}

// setupLogging truncates and opens the log file and makes it the default
// slog output for every package, so all packages' log lines land in it.
// Logging is disabled if opening the file fails.
func setupLogging() func() {
	file, err := os.Create(config.LogPath())
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		return func() {}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return func() { _ = file.Close() }
}

// buildApp is the composition root: the only place that constructs concrete
// implementations of app's collaborator interfaces.
func buildApp(flags cliFlags) (*app.App, error) {
	base := flags.baseURL()
	slog.Info("starting", "app", config.AppName, "version", version, "os", runtime.GOOS, "arch", runtime.GOARCH, "remote", base)
	client, err := remote.NewClient(base, version)
	if err != nil {
		return nil, err
	}
	detector := detect.NewRegistry(steam.New(nil, runtime.GOOS))
	packEngine := engine.New(client, version, engine.OSFiles{})
	deps := app.Deps{
		Catalog:   client,
		Detector:  detector,
		Installer: packEngine,
		Status:    packEngine,
		Updater:   newUpdater(flags),
	}
	return app.New(deps), nil
}

// newUpdater returns an Updater for the running version, or nil when
// disabled by flag, by --detect-only, or by build (see update.Enabled). It
// returns a nil app.Updater interface, never a nil *update.Updater wrapped in
// one, so app.App.UpdatesEnabled keeps working.
func newUpdater(flags cliFlags) app.Updater {
	if flags.noUpdate || flags.detectOnly || !update.Enabled(version) {
		return nil
	}
	updater, err := update.New(version)
	if err != nil {
		slog.Warn("self-update disabled", "err", err)
		return nil
	}
	return updater
}

// printDetection prints the detection results for games that have packs, in
// the same JSON shape --detect-only has always produced.
func printDetection(application *app.App) int {
	ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
	defer cancel()
	library, err := application.LoadLibrary(ctx)
	if err != nil {
		return fatal(err, false)
	}
	games := application.DetectGames(ctx, library)
	results := make([]detect.Result, 0, len(games))
	for _, game := range games {
		results = append(results, game.Install)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fatal(err, false)
	}
	return exitOK
}

// fatal logs err, prints it to stderr with the log file path, and returns
// the process exit code.
func fatal(err error, interactive bool) int {
	slog.Error("fatal", "err", err)
	fmt.Fprintf(os.Stderr, "%s: %v\nLog file: %s\n", config.AppName, err, config.LogPath())
	pauseOnWindows(interactive)
	return exitError
}

// recoverPanic logs and reports a panic recovered from run, then exits the
// process. It is deferred directly so recover() can see the panic.
func recoverPanic(interactive bool) {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("panic", "panic", r, "stack", string(debug.Stack()))
	fmt.Fprintf(os.Stderr, "%s hit an internal error: %v\nLog file: %s\n", config.AppName, r, config.LogPath())
	pauseOnWindows(interactive)
	// os.Exit skips the deferred closeLog; safe because the log file is unbuffered.
	os.Exit(exitError)
}

// pauseOnWindows keeps a double-clicked console window open so the message
// can be read. It does nothing when not interactive.
func pauseOnWindows(interactive bool) {
	if runtime.GOOS != "windows" || !interactive {
		return
	}
	fmt.Fprint(os.Stderr, "Press Enter to exit.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
