//go:build linux

package steam

import "testing"

// TestDefaultRootsFromHOME runs the real Linux root discovery against a temp HOME.
func TestDefaultRootsFromHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakeLinuxSteam(t, home, "valheim.exe")
	res, err := New(nil).Detect(valheim)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].BuildID != "linux_proton" {
		t.Errorf("results = %+v, want one linux_proton result", res)
	}

	native := t.TempDir()
	t.Setenv("HOME", native)
	fakeLinuxSteam(t, native, "valheim.x86_64")
	if res, _ := New(nil).Detect(valheim); len(res) != 0 {
		t.Errorf("native install detected: %+v", res)
	}
}
