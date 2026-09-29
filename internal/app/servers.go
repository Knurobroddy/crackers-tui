package app

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
)

// pathQuotes are stripped from an entered folder: terminals add them when a
// folder is dragged in, and Windows Explorer adds them with "Copy as path".
const pathQuotes = `"'`

// ServerDefs returns the servers of the library that have at least one pack,
// in games.json order.
func (l *Library) ServerDefs() []detect.GameDef {
	hasPacks := l.Index.GamesWithPacks()
	var defs []detect.GameDef
	for _, def := range l.Games.Games {
		if def.IsServer() && hasPacks[def.ID] {
			defs = append(defs, def)
		}
	}
	return defs
}

// AddServer saves the folder the player entered, once it holds a supported
// server of library for this OS, and returns it as saved (absolute).
func (a *App) AddServer(library *Library, entered string) (string, error) {
	if a.deps.Servers == nil || a.deps.ServerMatcher == nil {
		return "", errServersUnavailable
	}
	dir, err := absFolder(entered)
	if err != nil {
		return "", err
	}
	defs := library.ServerDefs()
	if !a.holdsServer(defs, dir) {
		return "", &NotServerFolderError{Dir: dir, Supported: defNames(defs)}
	}
	if err := a.deps.Servers.Add(dir); err != nil {
		return "", err
	}
	slog.Info("server folder saved", "path", dir)
	return dir, nil
}

// ForgetServer stops remembering dir. The folder and its pack stay as they are.
func (a *App) ForgetServer(dir string) error {
	if a.deps.Servers == nil {
		return errServersUnavailable
	}
	if err := a.deps.Servers.Forget(dir); err != nil {
		return err
	}
	slog.Info("server folder forgotten", "path", dir)
	return nil
}

// missingServers returns the saved folders that no detected server lives in.
func (a *App) missingServers(games []Game) ([]string, error) {
	if a.deps.Servers == nil {
		return nil, nil
	}
	folders, err := a.deps.Servers.Folders()
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for _, game := range games {
		if game.Def.IsServer() {
			found[detect.PathKey(game.Install.RootDir)] = true
		}
	}
	var missing []string
	for _, dir := range folders {
		if !found[detect.PathKey(dir)] {
			missing = append(missing, dir)
		}
	}
	return missing, nil
}

func (a *App) holdsServer(defs []detect.GameDef, dir string) bool {
	for _, def := range defs {
		if _, ok := a.deps.ServerMatcher.Match(def, dir); ok {
			return true
		}
	}
	return false
}

func absFolder(entered string) (string, error) {
	folder := strings.Trim(strings.TrimSpace(entered), pathQuotes)
	if folder == "" {
		return "", ErrNoFolder
	}
	dir, err := filepath.Abs(folder)
	if err != nil {
		return "", fmt.Errorf("resolve folder %s: %w", folder, err)
	}
	return dir, nil
}

func defNames(defs []detect.GameDef) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}
