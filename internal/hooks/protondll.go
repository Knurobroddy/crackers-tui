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
	"github.com/Knurobroddy/crackers-tui/internal/logx"
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

// FindUserReg returns <library>/steamapps/compatdata/<appid>/pfx/user.reg,
// looking in the game's own library first and then in all Steam libraries.
func FindUserReg(extra map[string]string) (string, error) {
	appid := extra[detect.ExtraSteamAppID]
	if appid == "" {
		return "", fmt.Errorf("%s needs a Steam game (no steam_appid in detection data)", protonDLLOverrideType)
	}
	libs := []string{}
	if lib := extra[detect.ExtraSteamLibrary]; lib != "" {
		libs = append(libs, lib)
	}
	libs = append(libs, filepath.SplitList(extra[detect.ExtraSteamLibraries])...)
	for _, lib := range libs {
		p := filepath.Join(lib, "steamapps", "compatdata", appid, "pfx", "user.reg")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("The Proton prefix for this game was not found (steamapps/compatdata/%s/pfx/user.reg). "+
		"Launch the game once via Steam (Proton), close it, then retry.", appid)
}

func (h *protonDLLOverride) Validate(ctx HookCtx) error {
	p, err := FindUserReg(ctx.Extra)
	if err != nil {
		return err
	}
	h.regPath = p
	return nil
}

func (h *protonDLLOverride) Apply(ctx HookCtx) ([]UndoAction, error) {
	if h.regPath == "" {
		if err := h.Validate(ctx); err != nil {
			return nil, err
		}
	}
	log := logx.OrDiscard(ctx.Log)
	fi, err := os.Stat(h.regPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(h.regPath)
	if err != nil {
		return nil, err
	}
	backup := h.regPath + config.BakSuffix
	if err := os.WriteFile(backup, data, fi.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("could not back up %s: %w", h.regPath, err)
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
	if err := fsutil.WriteAtomic(h.regPath, out, fi.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("could not write %s: %w", h.regPath, err)
	}
	log.Info("set Wine DLL override", "file", h.regPath, "dll", h.DLL, "mode", h.Mode, "previous", prev, "backup", backup)
	return []UndoAction{undo}, nil
}

// wineRegRestore is the marker form of the wine_reg_restore undo action.
type wineRegRestore struct {
	Op       string  `json:"op"`
	File     string  `json:"file"`
	Section  string  `json:"section"`
	Name     string  `json:"name"`
	Previous *string `json:"previous"` // null: the value did not exist
}

func runWineRegRestore(raw json.RawMessage, log *slog.Logger) error {
	var u wineRegRestore
	if err := json.Unmarshal(raw, &u); err != nil {
		return fmt.Errorf("invalid %s action: %w", wineRegRestoreOp, err)
	}
	if u.File == "" || u.Section == "" || u.Name == "" {
		return fmt.Errorf("invalid %s action: missing file, section or name", wineRegRestoreOp)
	}
	fi, err := os.Stat(u.File)
	if os.IsNotExist(err) {
		// The prefix is gone (e.g. the game was uninstalled): nothing to restore.
		log.Warn("registry file to restore no longer exists; skipping", "file", u.File)
		return nil
	}
	if err != nil {
		return err
	}
	data, err := os.ReadFile(u.File)
	if err != nil {
		return err
	}
	out, err := RestoreRegValue(data, u.Section, u.Name, u.Previous)
	if err != nil {
		return err
	}
	if string(out) == string(data) {
		return nil
	}
	if err := fsutil.WriteAtomic(u.File, out, fi.Mode().Perm()); err != nil {
		return fmt.Errorf("could not write %s: %w", u.File, err)
	}
	log.Info("restored Wine registry value", "file", u.File, "name", u.Name, "previous", u.Previous)
	return nil
}
