package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type horizonRadio struct {
	LowHz    int `json:"lowHz"`
	HighHz   int `json:"highHz"`
	Channels int `json:"channels"`
	NetID    int `json:"netId"`
	WindowMS int `json:"windowMs"`
}
type horizonProfile struct {
	SchemaVersion int    `json:"schemaVersion"`
	USB           string `json:"usb"`
	Serial        string `json:"serial"`
	KeyID         string `json:"keyId"`
	horizonRadio
	NodePort      string `json:"nodePort"`
	AutopilotPort string `json:"autopilotPort"`
}
type horizonReceipt struct {
	Serial         string       `json:"serial"`
	WorkspaceID    string       `json:"workspaceId"`
	IdentityID     string       `json:"identityId"`
	IMEI           string       `json:"imei"`
	AccountID      string       `json:"accountId"`
	KeyFingerprint string       `json:"keyFingerprint"`
	Radio          horizonRadio `json:"radio"`
}

func readHorizonProfile(bundle string) (*horizonProfile, error) {
	path := filepath.Join(bundle, "horizon-profile.json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var profile horizonProfile
	if err := readPrivateJSON(path, &profile); err != nil {
		return nil, err
	}
	devicePath := func(value string) bool {
		return regexp.MustCompile(`^/dev/[A-Za-z0-9_./:-]+$`).MatchString(value) && filepath.Clean(value) == value
	}
	autopilotValid := devicePath(profile.AutopilotPort)
	if strings.HasPrefix(profile.AutopilotPort, "udp://") {
		host, port, err := net.SplitHostPort(strings.TrimPrefix(profile.AutopilotPort, "udp://"))
		number, parseErr := strconv.Atoi(port)
		autopilotValid = err == nil && parseErr == nil && host == "127.0.0.1" && number > 0 && number <= 65535
	}
	if profile.SchemaVersion != 1 || !devicePath(profile.USB) || !devicePath(profile.NodePort) || !autopilotValid || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(profile.Serial) || !regexpSHA.MatchString(profile.KeyID) || profile.LowHz <= 0 || profile.HighHz < profile.LowHz || profile.Channels < 1 || profile.NetID < 0 || profile.WindowMS < 1 {
		return nil, errors.New("invalid optional Horizon profile")
	}
	return &profile, nil
}
func horizonProperties(bundle string) (string, error) {
	profile, err := readHorizonProfile(bundle)
	if err != nil || profile == nil {
		return "", err
	}
	value := ""
	if profile.NodePort != "" {
		value += fmt.Sprintf("mavlink-autopilot-node-port=%s\n", profile.NodePort)
	}
	if profile.AutopilotPort != "" {
		value += fmt.Sprintf("mavlink-autopilot-serial-port=%s\n", profile.AutopilotPort)
	}
	return value, nil
}

func (s *runtimeSupervisor) provisionHorizon(ctx context.Context) error {
	profile, err := readHorizonProfile(s.config.Bundle)
	if err != nil || profile == nil {
		return err
	}
	if err := s.setPhase("provisioning-horizon"); err != nil {
		return err
	}
	args := []string{"--non-interactive", "uxs", "provision-horizon", "--usb", profile.USB, "--serial", profile.Serial, "--role", "uxs", "--workspace", s.credential.WorkspaceID, "--identity", s.credential.IdentityID, "--key-id", profile.KeyID}
	for key, value := range map[string]int{"--low-hz": profile.LowHz, "--high-hz": profile.HighHz, "--channels": profile.Channels, "--net-id": profile.NetID, "--window-ms": profile.WindowMS} {
		args = append(args, key, strconv.Itoa(value))
	}
	keyPath := filepath.Join(s.config.Bundle, "horizon-key.hex")
	if _, err := os.Lstat(keyPath); err == nil {
		if err := checkInstallParents(keyPath); err != nil {
			return err
		}
		args = append(args, "--key-file", keyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	command, args, environment := s.beamCommand(args)
	environment["BEAM_IS_DAEMON"] = "false"
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = runtimeEnvironment(environment)
	output := &commandOutput{}
	log, err := os.OpenFile(filepath.Join(s.config.State, "horizon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd.Stdout = output
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return errors.New("Horizon provisioning failed; inspect private horizon.log")
	}
	if err := s.alive(); err != nil {
		return err
	}
	var receipt horizonReceipt
	decoder := json.NewDecoder(bytes.NewReader(output.data))
	if err := decoder.Decode(&receipt); err != nil {
		return errors.New("Horizon did not return a verification receipt")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errors.New("Horizon returned more than one receipt")
	}
	if err := validateHorizonReceipt(receipt, *profile, s.credential); err != nil {
		return err
	}
	if err := writePrivateJSON(filepath.Join(s.config.State, "horizon-receipt.json"), receipt); err != nil {
		return err
	}
	if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validateHorizonReceipt(r horizonReceipt, p horizonProfile, c claimBootstrap) error {
	if r.Serial != p.Serial || r.WorkspaceID != c.WorkspaceID || r.IdentityID != c.IdentityID || !regexp.MustCompile(`^[1-9][0-9]{14}$`).MatchString(r.IMEI) || !numericID.MatchString(r.AccountID) || len(p.KeyID) < 15 || r.KeyFingerprint != p.KeyID[:15] || r.Radio != p.horizonRadio {
		return errors.New("Horizon verification receipt does not match this installation")
	}
	return nil
}
