package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

var defaultClaimReleaseURL string

type claimRelease struct {
	SchemaVersion int `json:"schemaVersion"`
	Beam          struct {
		Source string `json:"source"`
		SHA256 string `json:"sha256"`
	} `json:"beam"`
}

func releaseURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid release download URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && (ip.IsPrivate() || ip.IsLoopback())) {
		return nil, errors.New("release downloads require HTTPS; HTTP is supported only for a local IP address")
	}
	return u, nil
}

func releaseGet(ctx context.Context, client *http.Client, value string) (*http.Response, error) {
	if _, err := releaseURL(value); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", value, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("release server unavailable; check its address and network connection")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("release server returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func resolveClaimArtifact(ctx context.Context, manifestURL, cache string) (string, error) {
	base, err := releaseURL(manifestURL)
	if err != nil {
		return "", err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many release redirects")
		}
		if _, err := releaseURL(req.URL.String()); err != nil {
			return err
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("release redirect must retain HTTPS")
		}
		return nil
	}}
	response, err := releaseGet(ctx, client, manifestURL)
	if err != nil {
		return "", err
	}
	var manifest claimRelease
	decoder := json.NewDecoder(io.LimitReader(response.Body, 65536))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&manifest)
	response.Body.Close()
	if err != nil {
		return "", fmt.Errorf("invalid claim release manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || !regexpSHA.MatchString(manifest.Beam.SHA256) || manifest.Beam.Source == "" {
		return "", errors.New("claim release requires schemaVersion 1 and a Beam source/SHA256")
	}
	source, err := url.Parse(manifest.Beam.Source)
	if err != nil {
		return "", errors.New("invalid Beam release source")
	}
	artifactURL := base.ResolveReference(source)
	if base.Scheme == "https" && artifactURL.Scheme != "https" {
		return "", errors.New("Beam release must retain HTTPS")
	}
	dir := filepath.Join(cache, "grid-uxs-wizard", "artifacts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := checkInstallParents(dir); err != nil {
		return "", err
	}
	destination := filepath.Join(dir, manifest.Beam.SHA256+".beam")
	if digest, err := fileDigest(destination); err == nil && digest == manifest.Beam.SHA256 {
		return destination, nil
	}
	response, err = releaseGet(ctx, client, artifactURL.String())
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	temp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	hash := sha256.New()
	const maxSize = 512 << 20
	n, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, maxSize+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > maxSize {
		return "", errors.New("Beam release exceeds maximum download size")
	}
	if hex.EncodeToString(hash.Sum(nil)) != manifest.Beam.SHA256 {
		return "", errors.New("Beam release checksum mismatch")
	}
	if err := os.Rename(temp.Name(), destination); err != nil {
		return "", err
	}
	return destination, nil
}
