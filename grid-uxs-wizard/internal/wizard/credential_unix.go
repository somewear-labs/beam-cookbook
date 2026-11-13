//go:build linux || darwin

package wizard

import (
	"errors"
	"os"
)

func secureBootstrapPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !safeInstallOwner(info) {
		return errors.New("bootstrap credentials require a private owned path")
	}
	return nil
}
