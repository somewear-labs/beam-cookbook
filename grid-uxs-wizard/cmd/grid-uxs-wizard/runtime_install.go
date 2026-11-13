package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type claimAcknowledgement struct {
	SchemaVersion int    `json:"schemaVersion"`
	ClaimID       string `json:"claimId"`
	IdentityID    string `json:"identityId"`
	BeamSHA256    string `json:"beamSha256"`
}

func currentAcknowledgement(o options) (claimAcknowledgement, error) {
	data, err := os.ReadFile(o.CodeFile)
	if err != nil || !validClaimCode(strings.TrimSpace(string(data))) {
		return claimAcknowledgement{}, errors.New("invalid saved claim")
	}
	credential, _, err := readClaimMaterial(o.State)
	if err != nil {
		return claimAcknowledgement{}, err
	}
	return claimAcknowledgement{SchemaVersion: 1, ClaimID: strings.Split(strings.TrimSpace(string(data)), ".")[1], IdentityID: credential.IdentityID, BeamSHA256: credential.BeamSHA256}, nil
}
func claimAcknowledged(o options) (bool, error) {
	path := filepath.Join(o.State, "claim-acknowledged.json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var saved claimAcknowledgement
	if err := readPrivateJSON(path, &saved); err != nil {
		return false, err
	}
	expected, err := currentAcknowledgement(o)
	if err != nil {
		return false, err
	}
	if saved != expected {
		return false, errors.New("saved claim acknowledgement belongs to another installation")
	}
	return true, nil
}
func saveClaimAcknowledgement(o options) error {
	value, err := currentAcknowledgement(o)
	if err != nil {
		return err
	}
	return writePrivateJSON(filepath.Join(o.State, "claim-acknowledged.json"), value)
}

func (i *installer) writeRuntimeUnit(ctx context.Context, path, body, unit string) error {
	existing, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return writeUnit(path, body)
	}
	if err != nil {
		return err
	}
	if !existing.Mode().IsRegular() {
		return errors.New("service unit must be a regular file")
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(previous) == body {
		return nil
	}
	if string(previous) != legacyServiceUnit(i.opts.Bundle, i.opts.State) {
		return errors.New("existing service is not owned by this installation")
	}
	backup := filepath.Join(i.opts.State, "service-before-go.unit")
	if data, err := os.ReadFile(backup); err == nil {
		if string(data) != string(previous) {
			return errors.New("legacy service backup differs; refusing overwrite")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := atomicPrivateWrite(backup, previous); err != nil {
			return err
		}
	} else {
		return err
	}
	if err := i.run(ctx, "systemctl", "stop", unit); err != nil {
		return err
	}
	return atomicPrivateWrite(path, []byte(body))
}
