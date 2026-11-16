package wizard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type wizardSetup struct {
	SchemaVersion int    `json:"schemaVersion"`
	Role          string `json:"role"`
}

func wizardSetupPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/grid-uxs-wizard/setup.json"
	case "windows":
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "grid-uxs-wizard", "setup.json")
	default:
		return "/var/lib/grid-uxs-wizard/setup.json"
	}
}

func readSetupRole(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", errors.New("invalid wizard setup record")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var setup wizardSetup
	if err := json.Unmarshal(data, &setup); err != nil {
		return "", fmt.Errorf("wizard setup record: %w", err)
	}
	if setup.SchemaVersion != 1 || (setup.Role != "gcs" && setup.Role != "uxs") {
		return "", errors.New("invalid wizard setup role")
	}
	return setup.Role, nil
}

func saveSetupRole(path, role string) error {
	if role != "gcs" && role != "uxs" {
		return errors.New("invalid wizard setup role")
	}
	existing, err := readSetupRole(path)
	if err != nil {
		return err
	}
	if existing != "" {
		if existing != role {
			return errors.New("wizard is already configured for a different role")
		}
		return nil
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("wizard setup directory must not be a symlink")
	}
	if err := os.Chmod(directory, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(wizardSetup{SchemaVersion: 1, Role: role})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
