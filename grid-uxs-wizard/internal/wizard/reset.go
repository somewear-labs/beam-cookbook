package wizard

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func executeReset(args []string) error {
	flags := flag.NewFlagSet("reset", flag.ContinueOnError)
	state := flags.String("state", "", "Advanced: managed UxS state directory")
	username := flags.String("user", "", "Normal account owning the Beam service; defaults to the invoking sudo user")
	backupRequested := flags.Bool("backup", false, "Keep a private backup of the installation before resetting")
	yes := flags.Bool("yes", false, "Confirm reset without prompting")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected reset arguments")
	}
	if runtime.GOOS != "linux" {
		return errors.New("reset currently supports Linux UxS mission computers")
	}
	role, err := readSetupRole(wizardSetupPath())
	if err != nil {
		return err
	}
	if role == "gcs" {
		return errors.New("reset is for UxS mission computers, not the GCS")
	}
	if os.Geteuid() != 0 {
		return errors.New("run reset with sudo to stop TVPN and clear its private state")
	}
	account, err := resolveInstallUser(*username)
	if err != nil {
		return err
	}
	if *state == "" {
		*state, err = selectUxsState("/var/lib/somewear/tvpn", filepath.Dir(defaultUxsState))
		if err != nil {
			return err
		}
	}
	if !safePath.MatchString(*state) || filepath.Clean(*state) != *state {
		return errors.New("use a clean absolute UxS state path")
	}
	unlock, err := lockUxS(filepath.Dir(*state))
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Lstat(*state); errors.Is(err, os.ErrNotExist) {
		fmt.Println("No UxS installation to reset.")
		return nil
	} else if err != nil {
		return err
	}
	action := "Reset without a backup"
	if *backupRequested {
		action = "Back up and reset"
		fmt.Println("A private backup of the local UxS installation and TVPN identity will be retained.")
	} else {
		fmt.Println("The local UxS installation and TVPN identity will be removed without retaining a backup.")
	}
	fmt.Println("The next setup starts fresh. The GCS setup and backend identity are unchanged.")
	if !*yes {
		choice, err := promptChoice("Reset this mission computer?", []menuOption{{"Keep the current installation", "cancel"}, {action, "reset"}})
		if err != nil {
			return err
		}
		if choice != "reset" {
			return nil
		}
	}
	ctx, stop := setupContext()
	defer stop()
	progressOutput()("resetting-installation")
	backup, err := archiveManagedInstallation(ctx, *state, *state+"-bundle", "/var/lib/somewear/tvpn", "/etc/systemd/system", "/var/lib/somewear/uxs-backups", account, runCommand)
	if err != nil {
		return err
	}
	if *backupRequested {
		fmt.Println("Backup saved:", backup)
	} else if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("installation reset, but private staging cleanup failed at %s: %w", backup, err)
	}
	fmt.Println("UxS reset. Downloaded software and the wizard are retained.")
	fmt.Println("Start a setup on the GCS, then run on this mission computer:")
	fmt.Println("\n  grid-uxs-wizard setup --role uxs")
	return nil
}
