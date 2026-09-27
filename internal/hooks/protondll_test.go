package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/detect"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

func fakePrefix(t *testing.T, library, appID string) string {
	t.Helper()
	path := filepath.Join(library, "steamapps", "compatdata", appID, "pfx", "user.reg")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture(t), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newHook(t *testing.T) Hook {
	t.Helper()
	h, err := New("proton_dll_override", json.RawMessage(`{"type":"proton_dll_override","builds":["linux_proton"],"dll":"winhttp","mode":"native,builtin"}`))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestFindUserReg_noPrefixInAnyLibrary_returnsErrPrefixNotFound(t *testing.T) {
	extra := map[string]string{detect.ExtraSteamAppID: "892970", detect.ExtraSteamLibrary: t.TempDir()}

	_, err := FindUserReg(extra)

	if !errors.Is(err, ErrPrefixNotFound) {
		t.Fatalf("err = %v, want ErrPrefixNotFound", err)
	}
}

func TestFindUserReg_prefixInAnotherLibrary_returnsItsPath(t *testing.T) {
	gameLib, otherLib := t.TempDir(), t.TempDir()
	extra := map[string]string{
		detect.ExtraSteamAppID:     "892970",
		detect.ExtraSteamLibrary:   gameLib,
		detect.ExtraSteamLibraries: strings.Join([]string{gameLib, otherLib}, string(os.PathListSeparator)),
	}
	// The prefix may live in a different library than the game.
	want := fakePrefix(t, otherLib, "892970")

	got, err := FindUserReg(extra)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("FindUserReg = %q, want %q", got, want)
	}
}

func TestProtonDLLOverride_Validate_nonSteamGame_returnsError(t *testing.T) {
	h := newHook(t)

	if err := h.Validate(HookCtx{Extra: map[string]string{}}); err == nil {
		t.Error("non-Steam game accepted")
	}
}

func TestProtonDLLOverride_ApplyAndUndo_setsAndRestoresValue(t *testing.T) {
	library := t.TempDir()
	reg := fakePrefix(t, library, "892970")
	orig, _ := os.ReadFile(reg)
	ctx := HookCtx{Extra: map[string]string{detect.ExtraSteamAppID: "892970", detect.ExtraSteamLibrary: library}}
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

	// The undo action survives a marker JSON round trip.
	marshaled, err := json.Marshal(undo)
	if err != nil {
		t.Fatal(err)
	}
	var generic []map[string]any
	if err := json.Unmarshal(marshaled, &generic); err != nil {
		t.Fatal(err)
	}
	if len(generic) != 1 || generic[0]["op"] != "wine_reg_restore" || generic[0]["file"] != reg ||
		generic[0]["section"] != `Software\\Wine\\DllOverrides` || generic[0]["name"] != "winhttp" {
		t.Fatalf("marker form = %s", marshaled)
	}
	if v, ok := generic[0]["previous"]; !ok || v != nil {
		t.Fatalf(`"previous" must be present and null: %s`, marshaled)
	}
	var decoded []UndoAction
	if err := json.Unmarshal(marshaled, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := RunUndo(decoded[0]); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(reg)
	if !bytes.Equal(restored, orig) {
		t.Fatalf("undo not byte-identical:\n%s", restored)
	}

	// Undo when the prefix is gone is a no-op, not an error.
	if err := os.Remove(reg); err != nil {
		t.Fatal(err)
	}
	if err := RunUndo(decoded[0]); err != nil {
		t.Errorf("undo with missing file: %v", err)
	}
}

func TestNew_knownType_returnsHook(t *testing.T) {
	if _, err := New("proton_dll_override", []byte(`{"dll":"winhttp","mode":"native,builtin"}`)); err != nil {
		t.Errorf("known hook: %v", err)
	}
}

func TestNew_unknownType_returnsUpdateRequired(t *testing.T) {
	_, err := New("nope", nil)

	if !remote.IsUpdateRequired(err) {
		t.Fatalf("err = %v, want *remote.UpdateRequiredError", err)
	}
}

func TestNew_invalidDLLOrMode_returnsError(t *testing.T) {
	for _, raw := range []string{
		`{"dll":"win\"http","mode":"native"}`,
		`{"dll":"","mode":"native"}`,
		`{"dll":"winhttp","mode":"native\n[evil]"}`,
	} {
		if _, err := New("proton_dll_override", json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestRunUndo_unknownOp_returnsUpdateRequired(t *testing.T) {
	err := RunUndo(UndoAction{Op: "nope", Raw: json.RawMessage(`{"op":"nope"}`)})

	if !remote.IsUpdateRequired(err) {
		t.Fatalf("err = %v, want *remote.UpdateRequiredError", err)
	}
}
