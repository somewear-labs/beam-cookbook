package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type runtimeConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	State         string `json:"state"`
	Bundle        string `json:"bundle"`
	ClaimID       string `json:"claimId"`
	RequestID     string `json:"requestId"`
	Beam          string `json:"beam"`
	BeamFormat    string `json:"beamFormat"`
	BeamHome      string `json:"beamHome,omitempty"`
	BeamSHA256    string `json:"beamSha256"`
	TVPN          string `json:"tvpn"`
	TVPNSHA256    string `json:"tvpnSha256"`
}

type claimBootstrap struct {
	Name          string `json:"name"`
	Backend       string `json:"backend"`
	WorkspaceID   string `json:"workspaceId"`
	GCSIdentityID string `json:"gcsIdentityId"`
	IdentityID    string `json:"identityId"`
	APIKey        string `json:"apiKey"`
	BeamSHA256    string `json:"beamSha256"`
	Pairing       struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	} `json:"pairing"`
}

type clientPairing struct {
	ID             string `json:"id"`
	Token          string `json:"token"`
	GCSBaseURL     string `json:"gcsOrigin"`
	TLSFingerprint string `json:"fingerprint"`
	WorkspaceID    string `json:"workspaceId"`
	IdentityID     string `json:"identityId"`
	GCSIdentityID  string `json:"gcsIdentityId"`
	Backend        string `json:"backend"`
	Name           string `json:"name"`
	BeamSHA256     string `json:"beamSha256"`
}

var numericID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var apiKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_:.-]{8,512}$`)
var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func readPrivateJSON(path string, value any) error {
	if err := checkInstallParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return errors.New("runtime data must be a private regular file, at most 64 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid runtime data")
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicPrivateWrite(path, data)
}

func readClaimMaterial(state string) (claimBootstrap, clientPairing, error) {
	var credential claimBootstrap
	var pairing clientPairing
	if err := readPrivateJSON(filepath.Join(state, "claimed-bootstrap.json"), &credential); err != nil {
		return credential, pairing, err
	}
	if err := readPrivateJSON(filepath.Join(state, "client-pairing.json"), &pairing); err != nil {
		return credential, pairing, err
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`).MatchString(credential.Name) || !regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`).MatchString(credential.Backend) || !apiKeyPattern.MatchString(credential.APIKey) || !regexpSHA.MatchString(credential.BeamSHA256) {
		return credential, pairing, errors.New("claim credential contains invalid provisioning fields")
	}
	for _, id := range []string{credential.WorkspaceID, credential.IdentityID, credential.GCSIdentityID} {
		if !numericID.MatchString(id) {
			return credential, pairing, errors.New("claim has an invalid identity or Workspace")
		}
	}
	if credential.IdentityID == credential.GCSIdentityID || !uuidPattern.MatchString(pairing.ID) || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(pairing.Token) || !regexpSHA.MatchString(pairing.TLSFingerprint) || validateGCSAddress(pairing.GCSBaseURL) != nil {
		return credential, pairing, errors.New("invalid claim pairing")
	}
	if credential.Pairing.ID != pairing.ID || credential.Pairing.Token != pairing.Token || credential.WorkspaceID != pairing.WorkspaceID || credential.IdentityID != pairing.IdentityID || credential.GCSIdentityID != pairing.GCSIdentityID || credential.Backend != pairing.Backend || credential.Name != pairing.Name || credential.BeamSHA256 != pairing.BeamSHA256 {
		return credential, pairing, errors.New("pairing does not match the claimed identity")
	}
	return credential, pairing, nil
}

func newRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 15) | 64
	value[8] = (value[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}

func prepareRuntime(o options) (installPlan, error) {
	credential, pairing, err := readClaimMaterial(o.State)
	if err != nil {
		return installPlan{}, err
	}
	code, err := os.ReadFile(o.CodeFile)
	if err != nil || !validClaimCode(strings.TrimSpace(string(code))) {
		return installPlan{}, errors.New("invalid saved claim")
	}
	parts := strings.Split(strings.TrimSpace(string(code)), ".")
	id := parts[1]
	if pairing.TLSFingerprint != parts[3] || strings.TrimSuffix(pairing.GCSBaseURL, "/") != strings.TrimSuffix(o.GCS, "/") {
		return installPlan{}, errors.New("claim pairing differs from the selected GCS")
	}
	digest, err := fileDigest(o.Beam)
	if err != nil || digest != credential.BeamSHA256 {
		return installPlan{}, errors.New("Beam artifact does not match the approved claim")
	}
	tvpn := filepath.Join(o.Bundle, "tvpnd")
	tvpnDigest, err := fileDigest(tvpn)
	if err != nil {
		return installPlan{}, err
	}
	config := runtimeConfig{SchemaVersion: 1, State: o.State, Bundle: o.Bundle, ClaimID: id, Beam: o.Beam, BeamHome: o.BeamHome, BeamFormat: "native", BeamSHA256: digest, TVPN: filepath.Join(o.State, "bin", "tvpnd"), TVPNSHA256: tvpnDigest}
	if strings.HasSuffix(o.Beam, ".jar") {
		config.BeamFormat = "jar"
	}
	if o.BeamHome == "" {
		config.Beam = filepath.Join(o.State, "bin", filepath.Base(o.Beam))
	}
	configPath := filepath.Join(o.State, "runtime.json")
	if _, err := os.Lstat(configPath); err == nil {
		var previous runtimeConfig
		if err := readPrivateJSON(configPath, &previous); err != nil {
			return installPlan{}, err
		}
		config.RequestID = previous.RequestID
		if previous != config || !uuidPattern.MatchString(previous.RequestID) {
			return installPlan{}, errors.New("runtime configuration differs from the owned installation")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		legacyID := filepath.Join(o.State, "request-id")
		if _, err := os.Lstat(legacyID); err == nil {
			if err := checkCodeFile(legacyID); err != nil {
				return installPlan{}, err
			}
			data, err := os.ReadFile(legacyID)
			if err != nil {
				return installPlan{}, err
			}
			config.RequestID = strings.TrimSpace(string(data))
			if !uuidPattern.MatchString(config.RequestID) {
				return installPlan{}, errors.New("invalid saved join request ID")
			}
		} else if errors.Is(err, os.ErrNotExist) {
			config.RequestID, err = newRequestID()
			if err != nil {
				return installPlan{}, err
			}
		} else {
			return installPlan{}, err
		}
	} else {
		return installPlan{}, err
	}
	bin := filepath.Join(o.State, "bin")
	if err := checkInstallParents(bin); err != nil {
		return installPlan{}, err
	}
	if err := os.MkdirAll(bin, 0700); err != nil {
		return installPlan{}, err
	}
	for source, destination := range map[string]string{tvpn: config.TVPN, o.Beam: config.Beam} {
		if source == destination {
			continue
		}
		hash, err := fileDigest(source)
		if err != nil {
			return installPlan{}, err
		}
		if err := copyVerified(source, destination, hash); err != nil {
			return installPlan{}, err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return installPlan{}, err
	}
	hash, err := fileDigest(executable)
	if err != nil {
		return installPlan{}, err
	}
	if err := copyVerified(executable, filepath.Join(bin, "grid-uxs-wizard"), hash); err != nil {
		return installPlan{}, err
	}
	if err := writePrivateJSON(configPath, config); err != nil {
		return installPlan{}, err
	}
	plan := installPlan{SchemaVersion: 1, UnitName: "somewear-uxs-" + id + ".service"}
	if err := writePrivateJSON(filepath.Join(o.State, "install-plan.json"), plan); err != nil {
		return installPlan{}, err
	}
	return plan, nil
}

func (c runtimeConfig) validate() error {
	if c.SchemaVersion != 1 || !uuidPattern.MatchString(c.ClaimID) || !uuidPattern.MatchString(c.RequestID) || !regexpSHA.MatchString(c.BeamSHA256) || !regexpSHA.MatchString(c.TVPNSHA256) || (c.BeamFormat != "native" && c.BeamFormat != "jar") {
		return errors.New("invalid runtime configuration")
	}
	for _, path := range []string{c.State, c.Bundle, c.Beam, c.TVPN} {
		if !safePath.MatchString(path) || filepath.Clean(path) != path {
			return errors.New("runtime paths must be clean and absolute")
		}
		if err := checkInstallParents(path); err != nil {
			return err
		}
	}
	if c.BeamHome != "" {
		if !safePath.MatchString(c.BeamHome) || filepath.Clean(c.BeamHome) != c.BeamHome {
			return errors.New("invalid native Beam package path")
		}
		if err := filepath.WalkDir(c.BeamHome, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !safeInstallOwner(info) {
				return errors.New("unsafe native Beam package permissions")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for path, expected := range map[string]string{c.Beam: c.BeamSHA256, c.TVPN: c.TVPNSHA256} {
		actual, err := fileDigest(path)
		if err != nil || actual != expected {
			return errors.New("runtime artifact no longer matches its approved checksum")
		}
	}
	return nil
}

func validateOwnedState(state string) error {
	if err := checkInstallParents(state); err != nil {
		return err
	}
	info, err := os.Lstat(state)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("runtime requires a private managed state directory")
	}
	owner, err := os.ReadFile(filepath.Join(state, "owner"))
	if err != nil || strings.TrimSpace(string(owner)) != "uxs-install-v1" {
		return errors.New("runtime state does not belong to the UxS installer")
	}
	return nil
}
