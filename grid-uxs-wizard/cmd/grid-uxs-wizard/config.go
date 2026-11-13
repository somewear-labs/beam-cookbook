package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type wizardConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	FileServer    string `json:"fileServer"`
}

func loadWizardConfig(path string, explicit bool) (wizardConfig, error) {
	var config wizardConfig
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 65536))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("wizard config: %w", err)
	}
	if config.SchemaVersion != 1 {
		return config, errors.New("wizard config requires schemaVersion 1")
	}
	if _, err := claimReleaseURL(config.FileServer); err != nil {
		return config, err
	}
	return config, nil
}

func claimReleaseURL(server string) (string, error) {
	base, err := releaseURL(server)
	if err != nil {
		return "", err
	}
	if base.RawQuery != "" {
		return "", errors.New("fileServer must be a base URL without query parameters")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/"
	return base.ResolveReference(&url.URL{Path: "claim-release.json"}).String(), nil
}

func configuredClaimRelease(configPath, server, release string) (string, error) {
	if configPath == "" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return "", err
		}
		configPath = filepath.Join(filepath.Dir(executable), "grid-uxs-wizard.json")
		config, err := loadWizardConfig(configPath, false)
		if err != nil {
			return "", err
		}
		if server == "" {
			server = config.FileServer
		}
	} else {
		config, err := loadWizardConfig(configPath, true)
		if err != nil {
			return "", err
		}
		if server == "" {
			server = config.FileServer
		}
	}
	if release != "" {
		return release, nil
	}
	if server != "" {
		return claimReleaseURL(server)
	}
	return defaultClaimReleaseURL, nil
}
