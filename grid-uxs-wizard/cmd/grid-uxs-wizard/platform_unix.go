//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
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
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()))
}
