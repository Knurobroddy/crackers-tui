package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
)

func fakePrefix(t *testing.T, lib, appid string) string {
	t.Helper()
	p := filepath.Join(lib, "steamapps", "compatdata", appid, "pfx", "user.reg")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, fixture(t), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newHook(t *testing.T) Hook {
	t.Helper()
	h, err := New("proton_dll_override", json.RawMessage(`{"type":"proton_dll_override","builds":["linux_proton"],"dll":"winhttp","mode":"native,builtin"}`))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestProtonHookValidate(t *testing.T) {
	gameLib, otherLib := t.TempDir(), t.TempDir()
	ctx := HookCtx{Extra: map[string]string{
		detect.ExtraSteamAppID:     "892970",
		detect.ExtraSteamLibrary:   gameLib,
		detect.ExtraSteamLibraries: strings.Join([]string{gameLib, otherLib}, string(os.PathListSeparator)),
	}}
	h := newHook(t)
	err := h.Validate(ctx)
	if err == nil || !strings.Contains(err.Error(), "Launch the game once via Steam (Proton), close it, then retry.") {
		t.Fatalf("missing prefix: err = %v", err)
	}
	// The prefix may live in a different library than the game.
	want := fakePrefix(t, otherLib, "892970")
	if err := h.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := FindUserReg(ctx.Extra); got != want {
		t.Errorf("FindUserReg = %q, want %q", got, want)
	}
	if err := h.Validate(HookCtx{Extra: map[string]string{}}); err == nil {
		t.Error("non-Steam game accepted")
	}
}

func TestProtonHookApplyAndUndo(t *testing.T) {
	lib := t.TempDir()
	reg := fakePrefix(t, lib, "892970")
	orig, _ := os.ReadFile(reg)
	ctx := HookCtx{Extra: map[string]string{detect.ExtraSteamAppID: "892970", detect.ExtraSteamLibrary: lib}}
	h := newHook(t)
	if err := h.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	undo, err := h.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(reg)
	if !bytes.Contains(after, []byte(`"winhttp"="native,builtin"`)) {
		t.Fatalf("override not written:\n%s", after)
	}
	if bak, err := os.ReadFile(reg + ".modinst-bak"); err != nil || !bytes.Equal(bak, orig) {
		t.Errorf("backup missing or wrong: %v", err)
	}
	if _, err := os.Stat(reg + ".modinst-tmp"); !os.IsNotExist(err) {
		t.Error("tmp file left behind")
	}

	// The undo action survives a marker JSON round trip in the ADR format.
	b, err := json.Marshal(undo)
	if err != nil {
		t.Fatal(err)
	}
	var generic []map[string]any
	json.Unmarshal(b, &generic)
	if len(generic) != 1 || generic[0]["op"] != "wine_reg_restore" || generic[0]["file"] != reg ||
		generic[0]["section"] != `Software\\Wine\\DllOverrides` || generic[0]["name"] != "winhttp" {
		t.Fatalf("marker form = %s", b)
	}
	if v, ok := generic[0]["previous"]; !ok || v != nil {
		t.Fatalf(`"previous" must be present and null: %s`, b)
	}
	var decoded []UndoAction
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := RunUndo(decoded[0], nil); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(reg)
	if !bytes.Equal(restored, orig) {
		t.Fatalf("undo not byte-identical:\n%s", restored)
	}

	// Undo when the prefix is gone is a no-op, not an error.
	os.Remove(reg)
	if err := RunUndo(decoded[0], nil); err != nil {
		t.Errorf("undo with missing file: %v", err)
	}
}

func TestHookRegistry(t *testing.T) {
	if _, err := New("proton_dll_override", []byte(`{"dll":"winhttp","mode":"native,builtin"}`)); err != nil {
		t.Errorf("known hook: %v", err)
	}
	if _, err := New("run_script", nil); err == nil || !strings.Contains(err.Error(), "update") {
		t.Errorf("unknown hook: err = %v", err)
	}
	for _, raw := range []string{
		`{"dll":"win\"http","mode":"native"}`,
		`{"dll":"","mode":"native"}`,
		`{"dll":"winhttp","mode":"native\n[evil]"}`,
	} {
		if _, err := New("proton_dll_override", json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if err := RunUndo(UndoAction{Op: "nope", Raw: json.RawMessage(`{"op":"nope"}`)}, nil); err == nil {
		t.Error("unknown undo op accepted")
	}
}
