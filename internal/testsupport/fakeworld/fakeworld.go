// Package fakeworld builds a fake Steam install with Valheim and serves a copy
// of the example library, for functional tests.
package fakeworld

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const (
	valheimAppID = "892970"

	dirPerm  = 0o755
	filePerm = 0o644
)

// Options selects optional parts of the world.
type Options struct {
	WithPrefix bool // create steamapps/compatdata/<appid>/pfx/user.reg (Proton)
	// WithServer creates a Valheim dedicated server folder outside Steam and
	// adds the server and its test pack (testdata/remote-server) to the library.
	WithServer bool
}

// World is one fake Steam install plus a served library copy.
type World struct {
	SteamRoot   string // Steam root with steamapps/libraryfolders.vdf
	Library     string // the Steam library holding the game (same as SteamRoot)
	GameRoot    string // steamapps/common/Valheim
	UserReg     string // Wine prefix user.reg; "" without WithPrefix
	ServerRoot  string // dedicated server folder; "" without WithServer
	ServersFile string // where the saved servers file may be written (not created)
	LibraryDir  string // local copy of testdata/remote, served at LibraryURL
	LibraryURL  string // base URL ending in "/"
}

// New builds the world below t.TempDir(). The Steam root path contains a space
// and a non-ASCII rune on purpose.
func New(t *testing.T, opts Options) *World {
	t.Helper()
	steamRoot := filepath.Join(resolvedTempDir(t), "Gry Steam ł")
	w := &World{
		SteamRoot:   steamRoot,
		Library:     steamRoot,
		GameRoot:    filepath.Join(steamRoot, "steamapps", "common", "Valheim"),
		LibraryDir:  filepath.Join(t.TempDir(), "library"),
		ServersFile: filepath.Join(t.TempDir(), "servers.modinst"),
	}
	w.writeSteamFiles(t)
	w.writeGameFiles(t)
	if opts.WithPrefix {
		w.writePrefix(t)
	}
	copyDir(t, RepoPath("testdata", "remote"), w.LibraryDir)
	if opts.WithServer {
		w.writeServer(t)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(w.LibraryDir)))
	t.Cleanup(server.Close)
	w.LibraryURL = server.URL + "/"
	return w
}

// WriteLibraryFile replaces a file in the served library.
func (w *World) WriteLibraryFile(t *testing.T, rel string, data []byte) {
	t.Helper()
	writeFile(t, filepath.Join(w.LibraryDir, filepath.FromSlash(rel)), data)
}

// RemoveLibraryFile deletes a file from the served library (the server then answers 404).
func (w *World) RemoveLibraryFile(t *testing.T, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(w.LibraryDir, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

// Tree maps every path below dir (slash-separated, relative) to "dir" or the
// SHA-256 of the file's content.
func Tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		return addTreeEntry(tree, dir, p, d, err)
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// RepoPath returns an absolute path below the repository root.
func RepoPath(parts ...string) string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	return filepath.Join(append([]string{root}, parts...)...)
}

// resolvedTempDir returns t.TempDir() in the form detection reports: on
// Windows runners TEMP can be an 8.3 short path (RUNNER~1) that
// filepath.EvalSymlinks expands, so World paths must be resolved too.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func (w *World) writeSteamFiles(t *testing.T) {
	vdf := fmt.Sprintf("\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t%q\n\t}\n}\n", w.Library)
	writeFile(t, filepath.Join(w.SteamRoot, "steamapps", "libraryfolders.vdf"), []byte(vdf))
	acf := "\"AppState\"\n{\n\t\"appid\"\t\t\"" + valheimAppID + "\"\n\t\"installdir\"\t\t\"Valheim\"\n}\n"
	writeFile(t, filepath.Join(w.Library, "steamapps", "appmanifest_"+valheimAppID+".acf"), []byte(acf))
}

func (w *World) writeGameFiles(t *testing.T) {
	for _, rel := range []string{"valheim.exe", "UnityPlayer.dll", "valheim_Data/Managed/assembly_valheim.dll"} {
		writeFile(t, filepath.Join(w.GameRoot, filepath.FromSlash(rel)), []byte("vanilla "+rel))
	}
}

func (w *World) writePrefix(t *testing.T) {
	w.UserReg = filepath.Join(w.Library, "steamapps", "compatdata", valheimAppID, "pfx", "user.reg")
	data, err := os.ReadFile(RepoPath("testdata", "wine", "user.reg"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, w.UserReg, data)
}

// writeServer creates a server folder holding both the Windows and the Linux
// server program, so either build is detected, and overlays the library.
func (w *World) writeServer(t *testing.T) {
	t.Helper()
	w.ServerRoot = filepath.Join(resolvedTempDir(t), "Valheim Server ł")
	for _, rel := range []string{"valheim_server.exe", "valheim_server.x86_64", "start_headless_server.bat"} {
		writeFile(t, filepath.Join(w.ServerRoot, rel), []byte("vanilla "+rel))
	}
	copyDir(t, RepoPath("testdata", "remote-server"), w.LibraryDir)
}

func addTreeEntry(tree map[string]string, dir, p string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." {
		return err
	}
	if d.IsDir() {
		tree[filepath.ToSlash(rel)] = "dir"
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	tree[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
	return nil
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		return copyEntry(src, dst, p, d, err)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyEntry(src, dst, p string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	rel, err := filepath.Rel(src, p)
	if err != nil {
		return err
	}
	target := filepath.Join(dst, rel)
	if d.IsDir() {
		return os.MkdirAll(target, dirPerm)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, filePerm)
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, filePerm); err != nil {
		t.Fatal(err)
	}
}
