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

// zipCreatorUnix is the zip "version made by" host of archives made on Unix,
// whose external attributes hold Unix permission bits.
const zipCreatorUnix = 3

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

func (p *plan) Close() {
	for _, closer := range p.closers {
		_ = closer.Close()
	}
}

// pathKey compares target paths the way the OS does (case-insensitive on
// Windows). rel is already clean.
func pathKey(rel string) string {
	return fsutil.FoldCase(rel)
}

// buildPlan expands the downloaded entries into a plan. It validates every
// target path, rejects zip symlinks and duplicate targets, but does not look
// at the game directory. downloaded[i] is the local copy of entries[i].
func buildPlan(root string, entries []remote.FileEntry, downloaded []string) (*plan, error) {
	p := &plan{}
	if err := p.addEntries(root, entries, downloaded); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.checkConflicts(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *plan) addEntries(root string, entries []remote.FileEntry, downloaded []string) error {
	for i, entry := range entries {
		switch entry.Kind {
		case remote.KindFile:
			if err := p.addFile(root, entry, downloaded[i]); err != nil {
				return err
			}
		case remote.KindZip:
			if err := p.addZip(root, entry, downloaded[i]); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown file kind %q for %s", entry.Kind, entry.URL)
		}
	}
	return nil
}

func (p *plan) addFile(root string, entry remote.FileEntry, local string) error {
	rel, err := pathsafe.Clean(entry.Dest)
	if err != nil {
		return err
	}
	abs, err := pathsafe.JoinNotRoot(root, rel)
	if err != nil {
		return err
	}
	p.Files = append(p.Files, planFile{Rel: rel, Abs: abs, Mode: filePerm, srcPath: local})
	return nil
}

func (p *plan) addZip(root string, entry remote.FileEntry, archive string) error {
	dest, err := pathsafe.Clean(entry.Dest)
	if err != nil {
		return err
	}
	if _, err := pathsafe.Join(root, dest); err != nil {
		return err
	}
	zipRoot := normalizeZipName(entry.ZipRoot)
	if zipRoot != "" && !strings.HasSuffix(zipRoot, "/") {
		zipRoot += "/"
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open archive %s: %w", entry.URL, err)
	}
	p.closers = append(p.closers, reader)

	hasMatch := false
	for _, file := range reader.File {
		name := normalizeZipName(file.Name)
		if file.Mode()&os.ModeSymlink != 0 {
			return &pathsafe.Error{Path: file.Name, Reason: "symlink entries are not allowed in archives (" + entry.URL + ")"}
		}
		if !strings.HasPrefix(name, zipRoot) {
			continue
		}
		hasMatch = true
		if err := p.addZipEntry(root, dest, entry.URL, strings.TrimPrefix(name, zipRoot), file); err != nil {
			return err
		}
	}
	if zipRoot != "" && !hasMatch {
		return fmt.Errorf("archive %s has no entries under zip_root %q", entry.URL, entry.ZipRoot)
	}
	return nil
}

// addZipEntry adds the archive entry file, named name below the zip root, to
// the plan at dest.
func (p *plan) addZipEntry(root, dest, archiveURL, name string, file *zip.File) error {
	if name == "" {
		return nil // the zip_root directory itself
	}
	isDir := strings.HasSuffix(name, "/") || file.FileInfo().IsDir()
	entryRel, err := pathsafe.Clean(name)
	if err != nil {
		return fmt.Errorf("check archive %s: %w", archiveURL, err)
	}
	if entryRel == "" {
		return nil
	}
	rel := path.Join(dest, entryRel)
	abs, err := pathsafe.JoinNotRoot(root, rel)
	if err != nil {
		return fmt.Errorf("check archive %s: %w", archiveURL, err)
	}
	if isDir {
		p.Dirs = append(p.Dirs, rel)
		return nil
	}
	p.Files = append(p.Files, planFile{Rel: rel, Abs: abs, Mode: zipFileMode(file), zipFile: file})
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
func zipFileMode(file *zip.File) os.FileMode {
	if runtime.GOOS == "windows" {
		return filePerm // a missing write bit would create a read-only file
	}
	perm := file.Mode().Perm()
	if file.CreatorVersion>>8 == zipCreatorUnix && perm&0o400 != 0 {
		return perm
	}
	if perm&0o111 != 0 {
		return execFilePerm
	}
	return filePerm
}

// checkConflicts rejects duplicate targets, a file that is also used as a
// directory, and a file named like the marker.
func (p *plan) checkConflicts() error {
	isFile := map[string]bool{}
	for _, file := range p.Files {
		key := pathKey(file.Rel)
		if isFile[key] {
			return fmt.Errorf("%s is written twice", file.Rel)
		}
		if strings.EqualFold(file.Rel, config.MarkerFileName) {
			return fmt.Errorf("pack must not contain %s", config.MarkerFileName)
		}
		if first, _, _ := strings.Cut(file.Rel, "/"); strings.EqualFold(first, config.StagingDirName) {
			return fmt.Errorf("pack must not contain %s", config.StagingDirName)
		}
		isFile[key] = true
	}
	for _, file := range p.Files {
		for dir := path.Dir(file.Rel); dir != "."; dir = path.Dir(dir) {
			if isFile[pathKey(dir)] {
				return fmt.Errorf("%s is both a file and a directory", dir)
			}
		}
	}
	for _, dir := range p.Dirs {
		if isFile[pathKey(dir)] {
			return fmt.Errorf("%s is both a file and a directory", dir)
		}
	}
	return nil
}
