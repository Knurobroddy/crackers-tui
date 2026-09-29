package servers_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Knurobroddy/crackers-tui/internal/remote"
	"github.com/Knurobroddy/crackers-tui/internal/servers"
)

func TestStore_Folders_noFile_returnsEmpty(t *testing.T) {
	store := servers.New(filepath.Join(t.TempDir(), "servers.modinst"))

	got, err := store.Folders()

	if err != nil || len(got) != 0 {
		t.Fatalf("folders = %v, err = %v, want none", got, err)
	}
}

func TestStore_AddThenForget_writesListAndKeepsOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "servers.modinst")
	store := servers.New(path)
	first, second := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")

	for _, dir := range []string{first, second} {
		if err := store.Add(dir); err != nil {
			t.Fatal(err)
		}
	}
	requireFolders(t, servers.New(path), []string{first, second})

	if err := store.Forget(first); err != nil {
		t.Fatal(err)
	}
	requireFolders(t, servers.New(path), []string{second})
}

func TestStore_Add_sameFolderTwice_returnsErrAlreadySaved(t *testing.T) {
	store := servers.New(filepath.Join(t.TempDir(), "servers.modinst"))
	dir := t.TempDir()
	if err := store.Add(dir); err != nil {
		t.Fatal(err)
	}

	if err := store.Add(dir + string(filepath.Separator)); !errors.Is(err, servers.ErrAlreadySaved) {
		t.Fatalf("err = %v, want ErrAlreadySaved", err)
	}
}

func TestStore_Forget_unknownFolder_returnsErrNotSaved(t *testing.T) {
	store := servers.New(filepath.Join(t.TempDir(), "servers.modinst"))

	if err := store.Forget(t.TempDir()); !errors.Is(err, servers.ErrNotSaved) {
		t.Fatalf("err = %v, want ErrNotSaved", err)
	}
}

func TestStore_unusableFile_failsAndNeverOverwrites(t *testing.T) {
	for _, tc := range []struct {
		name          string
		content       string
		wantUpdateErr bool
	}{
		{"invalid json", "{not json", false},
		{"newer schema", `{"schema_version": 9, "servers": []}`, true},
		{"missing schema", `{"servers": []}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "servers.modinst")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			store := servers.New(path)

			_, loadErr := store.Folders()
			addErr := store.Add(t.TempDir())

			var fileErr *servers.FileError
			if !errors.As(loadErr, &fileErr) || !errors.As(addErr, &fileErr) {
				t.Fatalf("load err = %v, add err = %v, want *FileError", loadErr, addErr)
			}
			if got := remote.IsUpdateRequired(loadErr); got != tc.wantUpdateErr {
				t.Errorf("update required = %v, want %v", got, tc.wantUpdateErr)
			}
			if data, _ := os.ReadFile(path); string(data) != tc.content {
				t.Errorf("file changed to %q", data)
			}
		})
	}
}

func TestStore_noConfigDir_returnsErrNoConfigDir(t *testing.T) {
	store := servers.New("")

	if _, err := store.Folders(); !errors.Is(err, servers.ErrNoConfigDir) {
		t.Fatalf("err = %v, want ErrNoConfigDir", err)
	}
}

func requireFolders(t *testing.T, store *servers.Store, want []string) {
	t.Helper()
	got, err := store.Folders()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("folders (-want +got):\n%s", diff)
	}
}
