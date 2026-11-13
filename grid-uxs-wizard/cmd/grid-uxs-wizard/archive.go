package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type archivedPath struct {
	Source string `json:"source"`
	Backup string `json:"backup"`
}
type archivedInstallation struct {
	SchemaVersion int            `json:"schemaVersion"`
	State         string         `json:"state"`
	Unit          string         `json:"unit,omitempty"`
	WasActive     bool           `json:"wasActive"`
	WasEnabled    bool           `json:"wasEnabled"`
	Moved         []archivedPath `json:"moved"`
}

func archiveManagedInstallation(ctx context.Context, state, bundle, canonical, unitDir, backupRoot string, run func(context.Context, string, ...string) error) (backup string, result error) {
	for _, path := range []string{state, bundle, unitDir, backupRoot} {
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
	if err := checkInstallParents(filepath.Dir(canonical)); err != nil {
		return "", err
	}
	manifest := archivedInstallation{SchemaVersion: 1, State: state}
	unitPath := ""
	var unitBody []byte
	if _, err := os.Lstat(filepath.Join(state, "install-plan.json")); err == nil {
		plan, err := readPlan(filepath.Join(state, "install-plan.json"))
		if err != nil {
			return "", err
		}
		unitPath = filepath.Join(unitDir, plan.UnitName)
		body, err := os.ReadFile(unitPath)
		if err == nil {
			if string(body) != serviceUnit(bundle, state) && string(body) != legacyServiceUnit(bundle, state) {
				return "", errors.New("refusing to replace a service not owned by this wizard")
			}
			manifest.Unit = plan.UnitName
			unitBody = body
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	hasCanonical := false
	if _, err := os.Lstat(canonical); err == nil {
		target, err := os.Readlink(canonical)
		if err != nil || target != filepath.Join(state, "tvpn") {
			return "", errors.New("TVPN belongs to a different installation; replacement stopped")
		}
		hasCanonical = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
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
	if manifest.Unit != "" {
		manifest.WasActive = run(ctx, "systemctl", "is-active", "--quiet", manifest.Unit) == nil
		manifest.WasEnabled = run(ctx, "systemctl", "is-enabled", "--quiet", manifest.Unit) == nil
		if err := os.WriteFile(filepath.Join(backup, "unit.service"), unitBody, 0600); err != nil {
			return backup, err
		}
	}
	if err := save(); err != nil {
		return backup, err
	}
	if manifest.Unit != "" {
		if err := run(ctx, "systemctl", "disable", "--now", manifest.Unit); err != nil {
			return backup, err
		}
		if err := os.Remove(unitPath); err != nil {
			return backup, err
		}
		if err := run(ctx, "systemctl", "daemon-reload"); err != nil {
			return backup, err
		}
	}
	moves := []archivedPath{{bundle, filepath.Join(backup, "claim-bundle")}}
	if stateExists {
		moves = append(moves, archivedPath{state, filepath.Join(backup, "state")})
	}
	if hasCanonical {
		moves = append([]archivedPath{{canonical, filepath.Join(backup, "tvpn-link")}}, moves...)
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
