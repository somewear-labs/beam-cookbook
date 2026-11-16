package wizard

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

type wizardRunner struct {
	statePath  string
	execute    func([]string) error
	chooseRole func() (string, error)
	chooseTask func(string, []menuOption) (string, error)
}

func Run(args []string) error {
	runner := wizardRunner{wizardSetupPath(), execute, promptRole, promptChoice}
	return runner.run(args)
}

func (r wizardRunner) run(args []string) error {
	args = presentationArgs(args)
	if len(args) > 0 && args[0] == "__update-tvpn" {
		return r.execute(args)
	}
	if len(args) > 0 && args[0] == "reset" {
		return r.execute(args)
	}
	if len(args) > 0 && args[0] == "update" {
		return r.execute(args)
	}
	if len(args) > 0 && args[0] == "run" {
		return r.execute(args)
	}
	role, err := readSetupRole(r.statePath)
	if err != nil {
		return err
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		usage := wizardUsage
		if role != "" {
			usage = roleUsage(role)
		}
		fmt.Fprint(os.Stdout, usage)
		return flag.ErrHelp
	}
	if len(args) == 0 && role == "" {
		return r.setup("", nil)
	}
	if len(args) > 0 && args[0] == "setup" {
		return r.setup(role, args[1:])
	}
	if role == "" {
		return errors.New("run grid-uxs-wizard setup first to configure this computer")
	}
	if len(args) == 0 {
		action, err := r.chooseTask("Grid Networked UxS", roleTasks(role))
		if err != nil {
			return err
		}
		args = []string{action}
	}
	if args[0] == "update" || args[0] == "reset" {
		return r.execute(args)
	}
	if args[0] == "setup" {
		return r.setup(role, args[1:])
	}
	routed, err := roleCommand(role, args)
	if err != nil {
		return err
	}
	return r.execute(routed)
}

func (r wizardRunner) setup(savedRole string, args []string) error {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			fmt.Fprint(os.Stdout, setupUsage)
			return flag.ErrHelp
		}
	}
	role := savedRole
	remaining := args
	explicit := false
	for _, arg := range args {
		if arg == "--role" || strings.HasPrefix(arg, "--role=") {
			explicit = true
		}
	}
	if explicit {
		var err error
		role, remaining, err = setupRole(args)
		if err != nil {
			return err
		}
	}
	if role == "" {
		var err error
		role, err = r.chooseRole()
		if err != nil {
			return err
		}
	}
	if savedRole != "" && savedRole != role {
		return errors.New("this computer's role was selected during initial setup and cannot be changed here")
	}
	if role != "gcs" && role != "uxs" {
		return errors.New("choose gcs or uxs")
	}
	if err := r.execute(append([]string{role}, remaining...)); err != nil {
		return err
	}
	for _, arg := range remaining {
		if arg == "--dry-run" || arg == "--dry-run=true" {
			return nil
		}
	}
	if err := saveSetupRole(r.statePath, role); err != nil {
		return fmt.Errorf("software setup completed but saving the wizard role failed: %w", err)
	}
	return nil
}

func roleTasks(role string) []menuOption {
	if role == "gcs" {
		return []menuOption{
			{"Provision a UxS", "provision-uxs"},
			{"Continue UxS provisioning", "resume-claim"},
			{"Check for updates", "update"},
			{"Set up this GCS", "setup"},
		}
	}
	return []menuOption{{"Check for updates", "update"}, {"Set up this mission computer", "setup"}, {"Reset this mission computer", "reset"}}
}

func roleCommand(role string, args []string) ([]string, error) {
	if role != "gcs" && role != "uxs" {
		return nil, errors.New("unknown wizard role")
	}
	if len(args) == 0 {
		return nil, errors.New("missing command")
	}
	switch args[0] {
	case "provision-uxs", "resume-claim":
		if role == "gcs" {
			return args, nil
		}
	}
	return nil, fmt.Errorf("command %q is unavailable for this computer's %s role; run grid-uxs-wizard --help", args[0], role)
}

func roleUsage(role string) string {
	if role == "gcs" {
		return gcsWizardUsage
	}
	return uxsWizardUsage
}
