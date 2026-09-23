package hooks

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

const section = `Software\\Wine\\DllOverrides`

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/wine/user.reg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("\r\n")) {
		t.Fatal("fixture must use LF line endings (check git autocrlf / .gitattributes)")
	}
	return b
}

func TestSetExistingSectionNewValueRoundTrip(t *testing.T) {
	orig := fixture(t)
	out, prev, err := SetRegValue(orig, section, "winhttp", "native,builtin", 42)
	if err != nil {
		t.Fatal(err)
	}
	if prev != nil {
		t.Fatalf("previous = %q, want nil", *prev)
	}
	want := strings.Replace(string(orig),
		"[Software\\\\Wine\\\\DllOverrides] 1726999999\n#time=1db0c3fa0f5e2a4\n",
		"[Software\\\\Wine\\\\DllOverrides] 1726999999\n#time=1db0c3fa0f5e2a4\n\"winhttp\"=\"native,builtin\"\n", 1)
	if string(out) != want {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if !strings.Contains(string(out), `"winhttp"="must-not-be-touched"`) {
		t.Error("value in another section was modified")
	}
	back, err := RestoreRegValue(out, section, "winhttp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, orig) {
		t.Fatalf("round trip not byte-identical:\n%s", back)
	}
}

func TestSetExistingValueRoundTrip(t *testing.T) {
	orig := fixture(t)
	out, prev, err := SetRegValue(orig, section, "D3D11", "builtin", 42) // name matches case-insensitively
	if err != nil {
		t.Fatal(err)
	}
	if prev == nil || *prev != `"native"` {
		t.Fatalf("previous = %v, want raw \"native\" with quotes", prev)
	}
	if !strings.Contains(string(out), "\n\"d3d11\"=\"builtin\"\n") {
		t.Fatalf("value not replaced (original name casing kept):\n%s", out)
	}
	if len(out) != len(orig)+len(`builtin`)-len(`native`) {
		t.Errorf("unexpected size change")
	}
	back, err := RestoreRegValue(out, section, "d3d11", prev)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, orig) {
		t.Fatalf("round trip not byte-identical:\n%s", back)
	}
}

func TestSetMissingSection(t *testing.T) {
	orig := []byte("WINE REGISTRY Version 2\n;; All keys relative to \\\\User\\\\S-1-5-21-0-0-0-1000\n\n#arch=win64\n")
	out, prev, err := SetRegValue(orig, section, "winhttp", "native,builtin", 1727000000)
	if err != nil || prev != nil {
		t.Fatal(err, prev)
	}
	want := string(orig) + "\n[Software\\\\Wine\\\\DllOverrides] 1727000000\n\"winhttp\"=\"native,builtin\"\n"
	if string(out) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
	// Undo removes the value but leaves the (harmless) section header.
	back, err := RestoreRegValue(out, section, "winhttp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(orig)+"\n[Software\\\\Wine\\\\DllOverrides] 1727000000\n" {
		t.Fatalf("undo result:\n%q", back)
	}
}

func TestHeaderMatchCaseInsensitive(t *testing.T) {
	orig := []byte("WINE REGISTRY Version 2\n\n[software\\\\wine\\\\dlloverrides] 1\n\"x\"=\"y\"\n")
	out, _, err := SetRegValue(orig, section, "winhttp", "native", 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "WINE REGISTRY Version 2\n\n[software\\\\wine\\\\dlloverrides] 1\n\"winhttp\"=\"native\"\n\"x\"=\"y\"\n" {
		t.Fatalf("got %q", out)
	}
}

func TestCRLFPreserved(t *testing.T) {
	orig := bytes.ReplaceAll(fixture(t), []byte("\n"), []byte("\r\n"))
	out, _, err := SetRegValue(orig, section, "winhttp", "native,builtin", 42)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("#time=1db0c3fa0f5e2a4\r\n\"winhttp\"=\"native,builtin\"\r\n")) {
		t.Fatalf("inserted line does not use CRLF:\n%q", out)
	}
	if bytes.Count(out, []byte("\n")) != bytes.Count(out, []byte("\r\n")) {
		t.Error("mixed line endings")
	}
	back, _ := RestoreRegValue(out, section, "winhttp", nil)
	if !bytes.Equal(back, orig) {
		t.Fatal("CRLF round trip not byte-identical")
	}
}

func TestNoTrailingNewline(t *testing.T) {
	orig := []byte("WINE REGISTRY Version 2\n\n[Software\\\\Wine\\\\DllOverrides] 1")
	out, _, err := SetRegValue(orig, section, "winhttp", "native", 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "WINE REGISTRY Version 2\n\n[Software\\\\Wine\\\\DllOverrides] 1\n\"winhttp\"=\"native\"\n" {
		t.Fatalf("got %q", out)
	}
}

func TestValueNotInOtherSections(t *testing.T) {
	// "winhttp" exists only in another section: it must be inserted, not replaced.
	orig := fixture(t)
	out, prev, _ := SetRegValue(orig, section, "winhttp", "native,builtin", 1)
	if prev != nil {
		t.Fatalf("matched a value from another section: %q", *prev)
	}
	if strings.Count(string(out), `"winhttp"=`) != 2 {
		t.Fatalf("expected the value in both sections:\n%s", out)
	}
}

func TestRestoreWhenValueDeletedMeanwhile(t *testing.T) {
	orig := []byte("[Software\\\\Wine\\\\DllOverrides] 1\n#time=abc\n")
	prev := `"builtin"`
	out, err := RestoreRegValue(orig, section, "winhttp", &prev)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[Software\\\\Wine\\\\DllOverrides] 1\n#time=abc\n\"winhttp\"=\"builtin\"\n" {
		t.Fatalf("got %q", out)
	}
	same, _ := RestoreRegValue(orig, section, "winhttp", nil)
	if !bytes.Equal(same, orig) {
		t.Error("restore of absent value changed the file")
	}
}

func TestParseValueLineEscapes(t *testing.T) {
	name, rest, ok := parseValueLine(`"a\"b\\c"="v"`)
	if !ok || name != `a\"b\\c` || rest != `"v"` {
		t.Errorf("got %q %q %v", name, rest, ok)
	}
	if _, _, ok := parseValueLine(`@="default"`); ok {
		t.Error("default value parsed as named value")
	}
}
