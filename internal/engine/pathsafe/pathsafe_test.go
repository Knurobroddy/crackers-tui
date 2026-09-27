package pathsafe

import (
	"path/filepath"
	"testing"
)

func TestClean_rootOrEmpty_returnsEmpty(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"dot", "."},
		{"dot_slash", "./"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Clean(tc.input)
			if err != nil || got != "" {
				t.Errorf("Clean(%q) = %q, %v; want \"\", nil", tc.input, got, err)
			}
		})
	}
}

func TestClean_relativePath_normalized(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain_file", "winhttp.dll", "winhttp.dll"},
		{"nested_dirs", "BepInEx/plugins/Mod.dll", "BepInEx/plugins/Mod.dll"},
		{"trailing_slash", "BepInEx/plugins/", "BepInEx/plugins"},
		{"double_slash_and_dot_segments", "./BepInEx//core/./x.dll", "BepInEx/core/x.dll"},
		{"backslash_treated_as_separator", `BepInEx\plugins\Mod.dll`, "BepInEx/plugins/Mod.dll"},
		{"spaces_preserved", "dir with spaces/f i l e", "dir with spaces/f i l e"},
		{"dots_in_filename_preserved", "..foo/bar..", "..foo/bar.."},
		{"unicode_preserved", "unicodé/ファイル.txt", "unicodé/ファイル.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Clean(tc.input)
			if err != nil || got != tc.want {
				t.Errorf("Clean(%q) = %q, %v; want %q, nil", tc.input, got, err, tc.want)
			}
		})
	}
}

func TestClean_embeddedDotDot_resolvedWithinRoot(t *testing.T) {
	got, err := Clean("a/b/../c")
	if err != nil || got != "a/c" {
		t.Errorf(`Clean("a/b/../c") = %q, %v; want "a/c", nil`, got, err)
	}
}

func TestClean_absolutePath_rejected(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"unix", "/etc/passwd"},
		{"root", "/"},
		{"windows_backslash_rooted", `\Windows\evil.dll`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Clean(tc.input); err == nil {
				t.Errorf("Clean(%q) = %q, want error", tc.input, got)
			}
		})
	}
}

func TestClean_volumeName_rejected(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"drive_and_absolute", "C:/Windows/evil.dll"},
		{"drive_relative", "C:evil.dll"},
		{"drive_backslash", `c:\evil.dll`},
		{"unc", "//server/share/x"},
		{"unc_backslash", `\\server\share\x`},
		{"device_path", `\\?\C:\x`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Clean(tc.input); err == nil {
				t.Errorf("Clean(%q) = %q, want error", tc.input, got)
			}
		})
	}
}

func TestClean_dotDot_rejected(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"parent", ".."},
		{"leading", "../evil"},
		{"after_cleaning", "a/../../evil"},
		{"deep", "BepInEx/../../../evil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Clean(tc.input); err == nil {
				t.Errorf("Clean(%q) = %q, want error", tc.input, got)
			}
		})
	}
}

func TestClean_backslashDotDot_rejected(t *testing.T) {
	if got, err := Clean(`..\..\evil.dll`); err == nil {
		t.Errorf(`Clean("..\\..\\evil.dll") = %q, want error`, got)
	}
}

func TestClean_ntfsStream_rejected(t *testing.T) {
	if got, err := Clean("file.txt:stream"); err == nil {
		t.Errorf("Clean(%q) = %q, want error", "file.txt:stream", got)
	}
}

func TestClean_nulByte_rejected(t *testing.T) {
	if got, err := Clean("nul\x00byte"); err == nil {
		t.Errorf("Clean(%q) = %q, want error", "nul\x00byte", got)
	}
}

func TestJoin_relativePath_returnsAbsoluteJoinedPath(t *testing.T) {
	root := t.TempDir()
	got, err := Join(root, "BepInEx/plugins/Mod.dll")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "BepInEx", "plugins", "Mod.dll"); got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}
}

func TestJoin_emptyPath_returnsRoot(t *testing.T) {
	root := t.TempDir()
	if got, err := Join(root, ""); err != nil || got != filepath.Clean(root) {
		t.Errorf("Join(root, \"\") = %q, %v", got, err)
	}
}

func TestJoin_unsafePath_rejected(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name  string
		input string
	}{
		{"dot_dot", "../x"},
		{"absolute", "/x"},
		{"volume", "C:/x"},
		{"escapes_after_cleaning", "a/../../x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Join(root, tc.input); err == nil {
				t.Errorf("Join(%q) = %q, want error", tc.input, got)
			}
		})
	}
}

func TestJoinNotRoot_root_rejected(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"dot", "."},
		{"dot_slash", "./"},
		{"dot_dot_resolves_to_root", "a/.."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := JoinNotRoot(root, tc.input); err == nil {
				t.Errorf("JoinNotRoot(%q) = %q, want error", tc.input, got)
			}
		})
	}
}

func TestJoinNotRoot_nonRootPath_accepted(t *testing.T) {
	root := t.TempDir()
	if _, err := JoinNotRoot(root, "BepInEx"); err != nil {
		t.Errorf("JoinNotRoot(BepInEx): %v", err)
	}
}
