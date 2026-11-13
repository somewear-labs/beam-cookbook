//go:build linux || darwin

package wizard

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func requireElevated() error {
	if os.Geteuid() != 0 {
		return errors.New("run setup with sudo to install Beam and TVPN")
	}
	return nil
}
func setupContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func safeInstallOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()) || (selectedInstallUID != 0 && stat.Uid == selectedInstallUID))
}

func requireRootExecutable(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("root TVPN executable and parents must be root-owned and not writable by other users")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}
