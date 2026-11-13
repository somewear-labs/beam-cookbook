//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockUxS(root string) (func(), error) {
	if err := checkInstallParents(root); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return lockFile(filepath.Join(root, ".uxs-installer.lock"), "UxS setup")
}

func lockFile(path, label string) (func(), error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("%s is in use: %w", label, err)
	}
	return func() { file.Close() }, nil
}

func lockRuntime(state string) (func(), error) {
	return lockFile(filepath.Join(state, "runner.lock"), "UxS runtime")
}
