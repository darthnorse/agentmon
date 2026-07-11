// Package fsatomic replaces files via a same-directory temp file + rename:
// readers never observe truncated content, a crash mid-write leaves the old
// file intact, and a symlink squatting at the destination is replaced, never
// followed.
package fsatomic

import (
	"os"
	"path/filepath"
)

// WriteFile atomically replaces dst with b at the given mode.
func WriteFile(dst string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
