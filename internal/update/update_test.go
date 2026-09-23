package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creativeprojects/go-selfupdate"
)

// Fake GitHub release with the asset layout produced by .goreleaser.yaml.

type fakeAsset struct {
	id   int64
	name string
	data []byte
}

func (a fakeAsset) GetID() int64                  { return a.id }
func (a fakeAsset) GetName() string               { return a.name }
func (a fakeAsset) GetSize() int                  { return len(a.data) }
func (a fakeAsset) GetBrowserDownloadURL() string { return "https://example.invalid/" + a.name }

type fakeRelease struct {
	tag    string
	assets []fakeAsset
}

func (r fakeRelease) GetID() int64              { return 1 }
func (r fakeRelease) GetTagName() string        { return r.tag }
func (r fakeRelease) GetDraft() bool            { return false }
func (r fakeRelease) GetPrerelease() bool       { return false }
func (r fakeRelease) GetPublishedAt() time.Time { return time.Now() }
func (r fakeRelease) GetReleaseNotes() string   { return "" }
func (r fakeRelease) GetName() string           { return r.tag }
func (r fakeRelease) GetURL() string            { return "" }
func (r fakeRelease) GetAssets() []selfupdate.SourceAsset {
	out := make([]selfupdate.SourceAsset, len(r.assets))
	for i, a := range r.assets {
		out[i] = a
	}
	return out
}

type fakeSource struct{ rel fakeRelease }

func (s fakeSource) ListReleases(context.Context, selfupdate.Repository) ([]selfupdate.SourceRelease, error) {
	return []selfupdate.SourceRelease{s.rel}, nil
}

func (s fakeSource) DownloadReleaseAsset(_ context.Context, _ *selfupdate.Release, id int64) (io.ReadCloser, error) {
	for _, a := range s.rel.assets {
		if a.id == id {
			return io.NopCloser(bytes.NewReader(a.data)), nil
		}
	}
	return nil, fmt.Errorf("asset %d not found", id)
}

func zipWith(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarGzWith(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func release(t *testing.T, version string, tamper bool) fakeRelease {
	win := zipWith(t, map[string]string{"crackers-modinst.exe": "new windows binary", "README.md": "readme"})
	lin := tarGzWith(t, map[string]string{"crackers-modinst": "new linux binary", "README.md": "readme"})
	winName := "crackers-modinst_" + version + "_windows_amd64.zip"
	linName := "crackers-modinst_" + version + "_linux_amd64.tar.gz"
	var sums strings.Builder
	for _, a := range []struct {
		name string
		data []byte
	}{{linName, lin}, {winName, win}} {
		h := sha256.Sum256(a.data)
		if tamper {
			h[0] ^= 0xff
		}
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), a.name)
	}
	return fakeRelease{tag: "v" + version, assets: []fakeAsset{
		{1, "checksums.txt", []byte(sums.String())},
		{2, linName, lin},
		{3, winName, win},
	}}
}

func TestAssetNamesAndApplyPerOS(t *testing.T) {
	for _, tc := range []struct{ goos, exeName, want string }{
		{"windows", "crackers-modinst.exe", "new windows binary"},
		{"windows", "crackers-modinst (1).exe", "new windows binary"}, // renamed by a browser
		{"linux", "crackers-modinst", "new linux binary"},
	} {
		t.Run(tc.goos+"/"+tc.exeName, func(t *testing.T) {
			u, err := newUpdater("0.1.0", fakeSource{release(t, "0.2.0", false)}, tc.goos, "amd64", nil)
			if err != nil {
				t.Fatal(err)
			}
			r, err := u.Check(context.Background())
			if err != nil || r == nil {
				t.Fatalf("Check = %v, %v", r, err)
			}
			if r.Version != "0.2.0" || !strings.Contains(r.rel.AssetName, "_"+tc.goos+"_amd64") {
				t.Fatalf("picked %s %s", r.Version, r.rel.AssetName)
			}
			target := filepath.Join(t.TempDir(), tc.exeName)
			os.WriteFile(target, []byte("old binary"), 0o755)
			if err := u.applyTo(context.Background(), r, target); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(target); string(b) != tc.want {
				t.Errorf("target = %q, want %q", b, tc.want)
			}
		})
	}
}

func TestNoUpdateWhenCurrent(t *testing.T) {
	u, _ := newUpdater("0.2.0", fakeSource{release(t, "0.2.0", false)}, "linux", "amd64", nil)
	if r, err := u.Check(context.Background()); err != nil || r != nil {
		t.Errorf("Check = %v, %v; want nil", r, err)
	}
}

func TestChecksumMismatchRejected(t *testing.T) {
	u, _ := newUpdater("0.1.0", fakeSource{release(t, "0.2.0", true)}, "linux", "amd64", nil)
	r, err := u.Check(context.Background())
	if err != nil || r == nil {
		t.Fatalf("Check = %v, %v", r, err)
	}
	target := filepath.Join(t.TempDir(), "crackers-modinst")
	os.WriteFile(target, []byte("old binary"), 0o755)
	if err := u.applyTo(context.Background(), r, target); err == nil {
		t.Fatal("tampered release applied")
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Error("target modified despite failed validation")
	}
}
