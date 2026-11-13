package wizard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func executeElevated(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	if args[0] == "gcs" || args[0] == "uxs" {
		args = append([]string{"setup", "--role", args[0]}, args[1:]...)
	}
	fmt.Fprintln(os.Stderr, "Administrator permission is required for TVPN and system services.")
	command := exec.Command("sudo", append([]string{"--", executable}, args...)...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}
