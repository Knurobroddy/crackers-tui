package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestTestdataRemoteIsConsistent serves testdata/remote (used for manual smoke
// tests) and checks that every document parses and every hash matches.
func TestTestdataRemoteIsConsistent(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir("../../testdata/remote")))
	defer srv.Close()
	c, err := NewClient(srv.URL, "0.1.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	games, err := c.FetchGames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := c.FetchIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Packs) == 0 {
		t.Fatal("no packs")
	}
	for _, p := range ix.Packs {
		if _, ok := games.Game(p.GameID); !ok {
			t.Errorf("pack %s: unknown game %s", p.ID, p.GameID)
		}
		m, _, err := c.FetchManifest(ctx, p.Manifest)
		if err != nil {
			t.Fatalf("pack %s: %v", p.ID, err)
		}
		for list, entries := range m.Files {
			for i, fe := range entries {
				if err := c.Download(ctx, fe, filepath.Join(t.TempDir(), "f"), nil); err != nil {
					t.Errorf("pack %s files.%s[%d]: %v", p.ID, list, i, err)
				}
			}
		}
	}
}
