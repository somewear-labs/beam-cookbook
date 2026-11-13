package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func atomicPrivateWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".wizard-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func copyVerified(source, destination, digest string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(destination), ".wizard-artifact-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, h), input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("artifact changed during staging")
	}
	if err := os.Chmod(output.Name(), 0700); err != nil {
		return err
	}
	return os.Rename(output.Name(), destination)
}
