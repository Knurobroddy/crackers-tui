//go:build linux

package steam

import "testing"

// TestDefaultRoots_realHOMEEnv_discoversSteamLibraries runs the real Linux
// root discovery against a temp HOME.
func TestDefaultRoots_realHOMEEnv_discoversSteamLibraries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakeLinuxSteam(t, home, "valheim.exe")
	res, err := New(DefaultRoots(), "linux").Detect(valheim)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].BuildID != "linux_proton" {
		t.Errorf("results = %+v, want one linux_proton result", res)
	}

	native := t.TempDir()
	t.Setenv("HOME", native)
	fakeLinuxSteam(t, native, "valheim.x86_64")
	if res, _ := New(DefaultRoots(), "linux").Detect(valheim); len(res) != 0 {
		t.Errorf("native install detected: %+v", res)
	}
}

// TestStrategy_Detect_nilRoots_readsDefaultRootsOnEveryCall proves "Detect
// again" sees a Steam root that appeared after the strategy was built.
func TestStrategy_Detect_nilRoots_readsDefaultRootsOnEveryCall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	strategy := New(nil, "linux")
	if res, _ := strategy.Detect(valheim); len(res) != 0 {
		t.Fatalf("results before Steam exists = %+v, want none", res)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	fakeLinuxSteam(t, home, "valheim.exe")
	res, err := strategy.Detect(valheim)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("results = %+v, want one", res)
	}
}
