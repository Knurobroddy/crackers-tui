// Package pathsafe validates relative paths from manifests and zip archives
// before anything is written.
//
// The rules are the same on every OS: a backslash is treated as a separator,
// and absolute paths, volume names (including drive-relative "C:x" and UNC
// paths), NTFS stream names (":"), NUL bytes and any ".." that survives
// cleaning are rejected. Finally the joined path must stay inside the root.
package pathsafe

import (
	"path"
	"path/filepath"
	"strings"
)

// Clean validates a relative, slash-separated path and returns it cleaned.
// The root itself ("", ".") is returned as "".
func Clean(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return reject(p, "contains a NUL byte")
	}
	s := strings.ReplaceAll(p, `\`, "/")
	switch {
	case strings.HasPrefix(s, "/"), filepath.IsAbs(p):
		return reject(p, "absolute path")
	case filepath.VolumeName(p) != "", hasDriveLetter(s):
		return reject(p, "contains a volume name")
	case strings.Contains(s, ":"):
		return reject(p, `contains ":"`)
	}
	c := path.Clean(s)
	if c == "." {
		return "", nil
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == ".." {
			return reject(p, `contains ".."`)
		}
	}
	return c, nil
}

// Join validates p and returns the absolute path filepath.Join(root, p),
// verified to be inside root (or root itself).
func Join(root, p string) (string, error) {
	c, err := Clean(p)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	joined := filepath.Join(root, filepath.FromSlash(c))
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", &Error{Path: p, Reason: "resolves outside the game directory"}
	}
	return joined, nil
}

// JoinNotRoot is Join but also rejects paths that resolve to root itself.
// Use it for file targets and for directories that will be deleted.
func JoinNotRoot(root, p string) (string, error) {
	joined, err := Join(root, p)
	if err != nil {
		return "", err
	}
	if joined == filepath.Clean(root) {
		return "", &Error{Path: p, Reason: "refers to the game directory itself"}
	}
	return joined, nil
}

func reject(path, reason string) (string, error) {
	return "", &Error{Path: path, Reason: reason}
}

func hasDriveLetter(s string) bool {
	return len(s) >= 2 && s[1] == ':' && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z')
}
