package engine

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine/pathsafe"
	"github.com/Knurobroddy/crackers-tui/internal/fsutil"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

// planFile is one file to write.
type planFile struct {
	Rel  string // cleaned, slash-separated, relative to root
	Abs  string
	Mode os.FileMode

	srcPath string    // downloaded file (kind "file")
	zipFile *zip.File // archive entry (kind "zip")
}

func (f planFile) open() (io.ReadCloser, error) {
	if f.zipFile != nil {
		return f.zipFile.Open()
	}
	return os.Open(f.srcPath)
}

// plan is the full list of targets, in write order.
type plan struct {
	Dirs    []string // explicit directories from zip directory entries (rel)
	Files   []planFile
	closers []io.Closer
}

func (p *plan) Close() {
	for _, c := range p.closers {
		c.Close()
	}
}

// pathKey compares target paths the way the OS does (case-insensitive on
// Windows). rel is already clean.
func pathKey(rel string) string {
	return fsutil.FoldCase(rel)
}

// buildPlan expands the downloaded entries into a plan. It validates every
// target path (ADR §5.2), rejects zip symlinks and duplicate targets, but does
// not look at the game directory. downloaded[i] is the local copy of files[i].
func buildPlan(root string, files []remote.FileEntry, downloaded []string) (_ *plan, err error) {
	p := &plan{}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	for i, fe := range files {
		switch fe.Kind {
		case remote.KindFile:
			rel, err := pathsafe.Clean(fe.Dest)
			if err != nil {
				return nil, err
			}
			abs, err := pathsafe.JoinNotRoot(root, rel)
			if err != nil {
				return nil, err
			}
			p.Files = append(p.Files, planFile{Rel: rel, Abs: abs, Mode: 0o644, srcPath: downloaded[i]})
		case remote.KindZip:
			if err := p.addZip(root, fe, downloaded[i]); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown file kind %q for %s", fe.Kind, fe.URL)
		}
	}
	return p, p.checkConflicts()
}

func (p *plan) addZip(root string, fe remote.FileEntry, archive string) error {
	dest, err := pathsafe.Clean(fe.Dest)
	if err != nil {
		return err
	}
	if _, err := pathsafe.Join(root, dest); err != nil {
		return err
	}
	zipRoot := normalizeZipName(fe.ZipRoot)
	if zipRoot != "" && !strings.HasSuffix(zipRoot, "/") {
		zipRoot += "/"
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("cannot open archive %s: %w", fe.URL, err)
	}
	p.closers = append(p.closers, zr)

	matched := false
	for _, f := range zr.File {
		name := normalizeZipName(f.Name)
		if f.Mode()&os.ModeSymlink != 0 {
			return &pathsafe.Error{Path: f.Name, Reason: "symlink entries are not allowed in archives (" + fe.URL + ")"}
		}
		if !strings.HasPrefix(name, zipRoot) {
			continue
		}
		matched = true
		name = strings.TrimPrefix(name, zipRoot)
		if name == "" {
			continue // the zip_root directory itself
		}
		isDir := strings.HasSuffix(name, "/") || f.FileInfo().IsDir()
		entry, err := pathsafe.Clean(name)
		if err != nil {
			return fmt.Errorf("%s: %w", fe.URL, err)
		}
		if entry == "" {
			continue
		}
		rel := path.Join(dest, entry)
		abs, err := pathsafe.JoinNotRoot(root, rel)
		if err != nil {
			return fmt.Errorf("%s: %w", fe.URL, err)
		}
		if isDir {
			p.Dirs = append(p.Dirs, rel)
			continue
		}
		p.Files = append(p.Files, planFile{Rel: rel, Abs: abs, Mode: zipFileMode(f), zipFile: f})
	}
	if zipRoot != "" && !matched {
		return fmt.Errorf("archive %s has no entries under zip_root %q", fe.URL, fe.ZipRoot)
	}
	return nil
}

// normalizeZipName uses "/" as separator and drops a leading "./".
func normalizeZipName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	return name
}

// zipFileMode returns the mode for an extracted file. On Linux the archive's
// Unix permission bits are kept when present; otherwise 0644, or 0755 when the
// archive marks the entry executable. Windows ignores modes.
func zipFileMode(f *zip.File) os.FileMode {
	if runtime.GOOS == "windows" {
		return 0o644 // a missing write bit would create a read-only file
	}
	perm := f.Mode().Perm()
	const creatorUnix = 3
	if f.CreatorVersion>>8 == creatorUnix && perm&0o400 != 0 {
		return perm
	}
	if perm&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// checkConflicts rejects duplicate targets, a file that is also used as a
// directory, and a file named like the marker.
func (p *plan) checkConflicts() error {
	files := map[string]bool{}
	for _, f := range p.Files {
		k := pathKey(f.Rel)
		if files[k] {
			return fmt.Errorf("pack error: %s is written twice", f.Rel)
		}
		if strings.EqualFold(f.Rel, config.MarkerFileName) {
			return fmt.Errorf("pack error: the pack must not contain %s", config.MarkerFileName)
		}
		if first, _, _ := strings.Cut(f.Rel, "/"); strings.EqualFold(first, config.StagingDirName) {
			return fmt.Errorf("pack error: the pack must not contain %s", config.StagingDirName)
		}
		files[k] = true
	}
	isFile := func(dir string) bool { return files[pathKey(dir)] }
	for _, f := range p.Files {
		for d := path.Dir(f.Rel); d != "."; d = path.Dir(d) {
			if isFile(d) {
				return fmt.Errorf("pack error: %s is both a file and a directory", d)
			}
		}
	}
	for _, d := range p.Dirs {
		if isFile(d) {
			return fmt.Errorf("pack error: %s is both a file and a directory", d)
		}
	}
	return nil
}

// ValidateFiles checks that files would expand into a valid plan: every
// target path is safe, archives contain no unsafe or symlink entries, and no
// target is written twice. local[i] is a local copy of files[i]. No game
// directory is involved; pack authoring tools use this to catch pack errors
// before publishing.
func ValidateFiles(files []remote.FileEntry, local []string) error {
	root, err := filepath.Abs(filepath.Join(os.TempDir(), "validate-root"))
	if err != nil {
		return err
	}
	p, err := buildPlan(root, files, local)
	if err != nil {
		return err
	}
	p.Close()
	return nil
}
