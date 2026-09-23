// Package fsutil holds small file helpers shared by several packages.
package fsutil

import (
	"os"
	"runtime"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

// WriteAtomic writes data to path via path+TmpSuffix and a rename, so readers
// never see a partial file. The tmp file is removed on error.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + config.TmpSuffix
	if err := os.WriteFile(tmp, data, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// FoldCase returns a key for comparing paths the way the OS does:
// lower-cased on Windows, where the file system is case-insensitive.
func FoldCase(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}
