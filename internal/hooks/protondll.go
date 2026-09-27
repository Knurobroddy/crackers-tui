package hooks

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/fsutil"
)

const (
	protonDLLOverrideType = "proton_dll_override"
	wineRegRestoreOp      = "wine_reg_restore"

	// dllOverridesSection is the section name as written in user.reg (backslashes doubled).
	dllOverridesSection = `Software\\Wine\\DllOverrides`
)

var (
	dllNameRe = regexp.MustCompile(`^[A-Za-z0-9_.*-]+$`)
	dllModeRe = regexp.MustCompile(`^[a-z,]*$`)
)

// protonDLLOverride sets a Wine DLL override in the Proton prefix's user.reg,
// e.g. winhttp=native,builtin so BepInEx's doorstop loads under Proton.
type protonDLLOverride struct {
	DLL  string `json:"dll"`
	Mode string `json:"mode"`

	regPath string // found by Validate
}

func newProtonDLLOverride(raw json.RawMessage) (Hook, error) {
	var h protonDLLOverride
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("invalid %s hook: %w", protonDLLOverrideType, err)
	}
	if !dllNameRe.MatchString(h.DLL) {
		return nil, fmt.Errorf("invalid %s hook: bad dll %q", protonDLLOverrideType, h.DLL)
	}
	if !dllModeRe.MatchString(h.Mode) {
		return nil, fmt.Errorf("invalid %s hook: bad mode %q", protonDLLOverrideType, h.Mode)
	}
	return &h, nil
}

// wineRegRestore is the marker form of the wine_reg_restore undo action.
type wineRegRestore struct {
	Op       string  `json:"op"`
	File     string  `json:"file"`
	Section  string  `json:"section"`
	Name     string  `json:"name"`
	Previous *string `json:"previous"` // null: the value did not exist
}

// FindUserReg returns <library>/steamapps/compatdata/<appid>/pfx/user.reg,
// looking in the game's own library first and then in all Steam libraries.
func FindUserReg(extra map[string]string) (string, error) {
	appID := extra[detect.ExtraSteamAppID]
	if appID == "" {
		return "", fmt.Errorf("find user.reg: missing steam app id in detection data")
	}
	var libraries []string
	if library := extra[detect.ExtraSteamLibrary]; library != "" {
		libraries = append(libraries, library)
	}
	libraries = append(libraries, filepath.SplitList(extra[detect.ExtraSteamLibraries])...)
	for _, library := range libraries {
		path := filepath.Join(library, "steamapps", "compatdata", appID, "pfx", "user.reg")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("find user.reg for app %s: %w", appID, ErrPrefixNotFound)
}

func (h *protonDLLOverride) Validate(ctx HookCtx) error {
	path, err := FindUserReg(ctx.Extra)
	if err != nil {
		return err
	}
	h.regPath = path
	return nil
}

func (h *protonDLLOverride) Apply(ctx HookCtx) ([]UndoAction, error) {
	if h.regPath == "" {
		if err := h.Validate(ctx); err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(h.regPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(h.regPath)
	if err != nil {
		return nil, err
	}
	backup := h.regPath + config.BackupSuffix
	if err := os.WriteFile(backup, data, info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("back up %s: %w", h.regPath, err)
	}
	out, prev, err := SetRegValue(data, dllOverridesSection, h.DLL, h.Mode, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	undo, err := newUndo(wineRegRestore{
		Op: wineRegRestoreOp, File: h.regPath, Section: dllOverridesSection, Name: h.DLL, Previous: prev,
	}, wineRegRestoreOp)
	if err != nil {
		return nil, err
	}
	if err := fsutil.WriteAtomic(h.regPath, out, info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("write %s: %w", h.regPath, err)
	}
	slog.Debug("set wine dll override", "path", h.regPath, "dll", h.DLL, "mode", h.Mode, "previous", prev, "backup", backup)
	return []UndoAction{undo}, nil
}

func runWineRegRestore(raw json.RawMessage) error {
	var undo wineRegRestore
	if err := json.Unmarshal(raw, &undo); err != nil {
		return fmt.Errorf("invalid %s action: %w", wineRegRestoreOp, err)
	}
	if undo.File == "" || undo.Section == "" || undo.Name == "" {
		return fmt.Errorf("invalid %s action: missing file, section or name", wineRegRestoreOp)
	}
	info, err := os.Stat(undo.File)
	if os.IsNotExist(err) {
		// The prefix is gone (e.g. the game was uninstalled): nothing to restore.
		slog.Warn("registry file to restore no longer exists; skipping", "path", undo.File)
		return nil
	}
	if err != nil {
		return err
	}
	data, err := os.ReadFile(undo.File)
	if err != nil {
		return err
	}
	out, err := RestoreRegValue(data, undo.Section, undo.Name, undo.Previous)
	if err != nil {
		return err
	}
	if string(out) == string(data) {
		return nil
	}
	if err := fsutil.WriteAtomic(undo.File, out, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write %s: %w", undo.File, err)
	}
	slog.Debug("restored wine registry value", "path", undo.File, "name", undo.Name, "previous", undo.Previous)
	return nil
}
