package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

func requireElevated() error {
	if err := exec.Command("fltmc.exe").Run(); err != nil {
		return errors.New("run setup from an Administrator terminal to install Beam and TVPN")
	}
	return nil
}
func setupContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func safeInstallOwner(os.FileInfo) bool { return false }
