package pathsafe

import (
	"path/filepath"
	"testing"
)

func TestCleanAccepts(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		".":                       "",
		"./":                      "",
		"winhttp.dll":             "winhttp.dll",
		"BepInEx/plugins/Mod.dll": "BepInEx/plugins/Mod.dll",
		"BepInEx/plugins/":        "BepInEx/plugins",
		"./BepInEx//core/./x.dll": "BepInEx/core/x.dll",
		"a/b/../c":                "a/c",
		`BepInEx\plugins\Mod.dll`: "BepInEx/plugins/Mod.dll", // backslash treated as separator
		"dir with spaces/f i l e": "dir with spaces/f i l e",
		"..foo/bar..":             "..foo/bar..",
		"unicodé/ファイル.txt":        "unicodé/ファイル.txt",
	}
	for in, want := range cases {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestCleanRejects(t *testing.T) {
	bad := []string{
		"/etc/passwd",           // absolute (unix)
		"/",                     // root
		`\Windows\evil.dll`,     // rooted (windows)
		"C:/Windows/evil.dll",   // volume + absolute
		"C:evil.dll",            // drive-relative volume
		`c:\evil.dll`,           // volume, backslashes
		"//server/share/x",      // UNC
		`\\server\share\x`,      // UNC, backslashes
		`\\?\C:\x`,              // device path
		"..",                    // parent
		"../evil",               // zip-slip
		"a/../../evil",          // zip-slip after cleaning
		`..\..\evil.dll`,        // zip-slip with backslashes
		"BepInEx/../../../evil", // deeper zip-slip
		"file.txt:stream",       // NTFS alternate data stream
		"nul\x00byte",           // NUL byte
	}
	for _, p := range bad {
		if got, err := Clean(p); err == nil {
			t.Errorf("Clean(%q) = %q, want error", p, got)
		}
	}
}

func TestJoin(t *testing.T) {
	root := t.TempDir()
	got, err := Join(root, "BepInEx/plugins/Mod.dll")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "BepInEx", "plugins", "Mod.dll"); got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}
	if got, err := Join(root, ""); err != nil || got != filepath.Clean(root) {
		t.Errorf("Join(root, \"\") = %q, %v", got, err)
	}
	for _, p := range []string{"../x", "/x", "C:/x", "a/../../x"} {
		if _, err := Join(root, p); err == nil {
			t.Errorf("Join(%q) accepted", p)
		}
	}
}

func TestJoinNotRoot(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"", ".", "./", "a/.."} {
		if _, err := JoinNotRoot(root, p); err == nil {
			t.Errorf("JoinNotRoot(%q) accepted the root itself", p)
		}
	}
	if _, err := JoinNotRoot(root, "BepInEx"); err != nil {
		t.Errorf("JoinNotRoot(BepInEx): %v", err)
	}
}
