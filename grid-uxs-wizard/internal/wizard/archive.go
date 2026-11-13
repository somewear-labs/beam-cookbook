package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

type archivedPath struct {
	Source string `json:"source"`
	Backup string `json:"backup"`
}
type archivedService struct {
	Name       string `json:"name"`
	User       string `json:"user,omitempty"`
	UnitPath   string `json:"unitPath"`
	Backup     string `json:"backup"`
	WasActive  bool   `json:"wasActive"`
	WasEnabled bool   `json:"wasEnabled"`
}
type archivedInstallation struct {
	SchemaVersion int               `json:"schemaVersion"`
	State         string            `json:"state"`
	Services      []archivedService `json:"services,omitempty"`
	Moved         []archivedPath    `json:"moved"`
}

func archiveManagedInstallation(ctx context.Context, state, bundle, canonical, unitDir, backupRoot string, account *user.User, run func(context.Context, string, ...string) error) (backup string, result error) {
	if account != nil {
		if err := secureUserServiceParents(account); err != nil {
			return "", err
		}
	}
	for _, path := range []string{state, bundle, unitDir, backupRoot, filepath.Dir(canonical)} {
		if err := checkInstallParents(path); err != nil {
			return "", err
		}
	}
	stateExists := false
	if _, err := os.Lstat(state); err == nil {
		stateExists = true
		owner, err := os.ReadFile(filepath.Join(state, "owner"))
		if err != nil || strings.TrimSpace(string(owner)) != "uxs-install-v1" {
			return "", errors.New("refusing to replace an unmanaged UxS state directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	manifest := archivedInstallation{SchemaVersion: 1, State: state}
	bodies := [][]byte{}
	addService := func(path, name string, owner *user.User, valid func(string) bool) error {
		if err := checkInstallParents(path); err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !valid(string(body)) {
			return errors.New("refusing to replace a service not owned by this wizard")
		}
		service := archivedService{Name: name, UnitPath: path}
		if owner != nil {
			service.User = owner.Username
		}
		manifest.Services = append(manifest.Services, service)
		bodies = append(bodies, body)
		return nil
	}
	if _, err := os.Lstat(filepath.Join(state, "install-plan.json")); err == nil {
		plan, err := readPlan(filepath.Join(state, "install-plan.json"))
		if err != nil {
			return "", err
		}
		if err := addService(filepath.Join(unitDir, plan.UnitName), plan.UnitName, nil, func(body string) bool {
			return body == serviceUnit(bundle, state) || body == legacyServiceUnit(bundle, state)
		}); err != nil {
			return "", err
		}
		if account != nil {
			path := filepath.Join(account.HomeDir, ".config/systemd/user", plan.UnitName)
			if err := addService(path, plan.UnitName, account, func(body string) bool { return body == userRuntimeService(state) }); err != nil {
				return "", err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	hasCanonical := false
	if info, err := os.Lstat(canonical); err == nil {
		hasCanonical = true
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(canonical)
			if err != nil || target != filepath.Join(state, "tvpn") {
				return "", errors.New("TVPN belongs to a different installation; replacement stopped")
			}
		} else {
			if !info.IsDir() || !ownedRootTVPN(state, canonical) {
				return "", errors.New("TVPN belongs to a different installation; replacement stopped")
			}
			if err := checkInstallParents(canonical); err != nil {
				return "", err
			}
			if err := addService(filepath.Join(unitDir, "somewear-tvpnd.service"), "somewear-tvpnd.service", nil, func(body string) bool {
				for _, line := range strings.Split(body, "\n") {
					if command, ok := strings.CutPrefix(line, "ExecStart="); ok {
						return safePath.MatchString(command) && filepath.Clean(command) == command && body == rootTVPNService(command)
					}
				}
				return false
			}); err != nil {
				return "", err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	command := func(service archivedService, args ...string) error {
		if service.User != "" {
			name, argv := userCommand(account, "systemctl", append([]string{"--user"}, args...)...)
			return run(ctx, name, argv...)
		}
		return run(ctx, "systemctl", args...)
	}
	if err := os.MkdirAll(backupRoot, 0700); err != nil {
		return "", err
	}
	var err error
	backup, err = os.MkdirTemp(backupRoot, "replaced-uxs-")
	if err != nil {
		return "", err
	}
	defer func() {
		if result != nil {
			result = fmt.Errorf("replacement stopped; previous installation backup is %s: %w", backup, result)
		}
	}()
	save := func() error {
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		return atomicPrivateWrite(filepath.Join(backup, "reset.json"), data)
	}
	for index := range manifest.Services {
		service := &manifest.Services[index]
		service.WasActive = command(*service, "is-active", "--quiet", service.Name) == nil
		service.WasEnabled = command(*service, "is-enabled", "--quiet", service.Name) == nil
		service.Backup = filepath.Join(backup, fmt.Sprintf("service-%d.service", index))
		if err := os.WriteFile(service.Backup, bodies[index], 0600); err != nil {
			return backup, err
		}
	}
	if err := save(); err != nil {
		return backup, err
	}
	for _, service := range manifest.Services {
		if err := command(service, "disable", "--now", service.Name); err != nil {
			return backup, err
		}
		if err := os.Remove(service.UnitPath); err != nil {
			return backup, err
		}
		if err := command(service, "daemon-reload"); err != nil {
			return backup, err
		}
	}
	moves := []archivedPath{}
	if _, err := os.Lstat(bundle); err == nil {
		moves = append(moves, archivedPath{bundle, filepath.Join(backup, "claim-bundle")})
	} else if !errors.Is(err, os.ErrNotExist) {
		return backup, err
	}
	if stateExists {
		moves = append(moves, archivedPath{state, filepath.Join(backup, "state")})
	}
	if hasCanonical {
		moves = append([]archivedPath{{canonical, filepath.Join(backup, "tvpn")}}, moves...)
	}
	for _, move := range moves {
		if err := os.Rename(move.Source, move.Backup); err != nil {
			return backup, err
		}
		manifest.Moved = append(manifest.Moved, move)
		if err := save(); err != nil {
			return backup, err
		}
	}
	return backup, nil
}
