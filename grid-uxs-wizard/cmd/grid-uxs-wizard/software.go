package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type softwareBundle struct {
	SchemaVersion int             `json:"schemaVersion"`
	OS            string          `json:"os"`
	Arch          string          `json:"arch"`
	Beam          softwarePackage `json:"beam"`
	TVPN          softwarePackage `json:"tvpn"`
}
type packageLauncher struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

type softwarePackage struct {
	Source      string            `json:"source"`
	SHA256      string            `json:"sha256"`
	Format      string            `json:"format"`
	Destination string            `json:"destination,omitempty"`
	Command     string            `json:"command"`
	Launchers   []packageLauncher `json:"launchers,omitempty"`
}
type preparedPackage struct {
	softwarePackage
	Path string
}

func readSoftwareBundle(dir string) (softwareBundle, error) {
	var bundle softwareBundle
	f, err := os.Open(filepath.Join(dir, "install.json"))
	if err != nil {
		return bundle, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 65536))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return bundle, err
	}
	if bundle.SchemaVersion != 1 || bundle.OS != runtime.GOOS || bundle.Arch != runtime.GOARCH {
		return bundle, errors.New("installation bundle does not match this operating system and architecture")
	}
	for name, p := range map[string]softwarePackage{"Beam": bundle.Beam, "TVPN": bundle.TVPN} {
		if err := p.validate(runtime.GOOS); err != nil {
			return bundle, fmt.Errorf("%s package: %w", name, err)
		}
	}
	return bundle, nil
}
func (p softwarePackage) validate(platform string) error {
	if !regexpSHA.MatchString(p.SHA256) || p.Source == "" || p.Command == "" {
		return errors.New("source, SHA256 and installed command are required")
	}
	for _, launcher := range p.Launchers {
		if p.Format != "tar.gz" || !filepath.IsAbs(launcher.Path) || filepath.Clean(launcher.Path) != launcher.Path || !safeRelative(launcher.Target) {
			return errors.New("archive launchers require absolute installation paths and relative targets")
		}
	}

	switch p.Format {
	case "msi":
		if platform != "windows" {
			return errors.New("MSI packages require Windows")
		}
	case "pkg":
		if platform != "darwin" {
			return errors.New("PKG packages require macOS")
		}
	case "deb", "rpm":
		if platform != "linux" {
			return errors.New("DEB/RPM packages require Linux")
		}
	case "tar.gz":
		if platform == "windows" {
			return errors.New("Windows packages must install their service wrappers through MSI")
		}
		if !filepath.IsAbs(p.Destination) || filepath.Clean(p.Destination) != p.Destination || p.Destination == string(filepath.Separator) {
			return errors.New("archive destination must be an absolute version directory")
		}
		if !safeRelative(p.Command) {
			return errors.New("archive command must be relative to its destination")
		}
	default:
		return errors.New("supported package formats: msi, pkg, deb, rpm, tar.gz")
	}
	if p.Format != "tar.gz" && !filepath.IsAbs(p.Command) {
		return errors.New("installed command must be an absolute path")
	}
	return nil
}
func safeRelative(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.VolumeName(path) == "" && !strings.Contains(path, "\\") && filepath.IsLocal(filepath.FromSlash(path))
}
func prepareSoftware(ctx context.Context, dir, cache string, p softwarePackage) (preparedPackage, error) {
	path := p.Source
	if parsed, err := url.Parse(p.Source); err == nil && parsed.IsAbs() {
		if parsed.Scheme != "https" || parsed.User != nil {
			return preparedPackage{}, errors.New("package downloads require HTTPS without embedded credentials")
		}
		staged, err := os.CreateTemp(cache, "download-*."+p.Format)
		if err != nil {
			return preparedPackage{}, err
		}
		path = staged.Name()
		if err := staged.Close(); err != nil {
			return preparedPackage{}, err
		}
		client := &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.User != nil || len(via) >= 5 {
				return errors.New("unsafe package redirect")
			}
			return nil
		}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Source, nil)
		if err != nil {
			return preparedPackage{}, errors.New("invalid download URL")
		}
		resp, err := client.Do(req)
		if err != nil {
			return preparedPackage{}, errors.New("package download failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return preparedPackage{}, fmt.Errorf("package download failed (HTTP %d)", resp.StatusCode)
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return preparedPackage{}, err
		}
		size, copyErr := io.Copy(out, io.LimitReader(resp.Body, 2<<30+1))
		closeErr := out.Close()
		if copyErr != nil {
			return preparedPackage{}, copyErr
		}
		if closeErr != nil {
			return preparedPackage{}, closeErr
		}
		if size > 2<<30 {
			return preparedPackage{}, errors.New("package exceeds 2 GiB")
		}
	} else {
		if !safeRelative(path) {
			return preparedPackage{}, errors.New("local package source must be relative to the bundle")
		}
		source, err := os.Open(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			return preparedPackage{}, err
		}
		defer source.Close()
		staged, err := os.CreateTemp(cache, "local-*."+p.Format)
		if err != nil {
			return preparedPackage{}, err
		}
		path = staged.Name()
		size, copyErr := io.Copy(staged, io.LimitReader(source, 2<<30+1))
		closeErr := staged.Close()
		if copyErr != nil {
			return preparedPackage{}, copyErr
		}
		if closeErr != nil {
			return preparedPackage{}, closeErr
		}
		if size > 2<<30 {
			return preparedPackage{}, errors.New("package exceeds 2 GiB")
		}
	}
	digest, err := fileDigest(path)
	if err != nil {
		return preparedPackage{}, err
	}
	if digest != p.SHA256 {
		return preparedPackage{}, errors.New("software package checksum mismatch")
	}
	return preparedPackage{p, path}, nil
}
func packageCommand(platform string, p preparedPackage) (string, []string, error) {
	switch p.Format {
	case "msi":
		return "msiexec.exe", []string{"/i", p.Path, "/qn", "/norestart"}, nil
	case "pkg":
		return "/usr/sbin/installer", []string{"-pkg", p.Path, "-target", "/"}, nil
	case "deb":
		return "dpkg", []string{"--install", p.Path}, nil
	case "rpm":
		return "rpm", []string{"--upgrade", "--replacepkgs", p.Path}, nil
	}
	return "", nil, errors.New("archive packages are installed directly")
}
func installSoftware(ctx context.Context, p preparedPackage, run func(context.Context, string, ...string) error) (string, error) {
	if p.Format == "tar.gz" {
		if err := installArchive(p); err != nil {
			return "", err
		}
		for _, launcher := range p.Launchers {
			if err := installLauncher(p.Destination, launcher); err != nil {
				return "", err
			}
		}
		return filepath.Join(p.Destination, filepath.FromSlash(p.Command)), nil
	}
	command, args, err := packageCommand(runtime.GOOS, p)
	if err != nil {
		return "", err
	}
	if err := run(ctx, command, args...); err != nil {
		return "", fmt.Errorf("%s installation failed: %w", p.Format, err)
	}
	if info, err := os.Stat(p.Command); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("package did not install its declared command")
	}
	return p.Command, nil
}
func installArchive(p preparedPackage) error {
	if err := checkInstallParents(p.Destination); err != nil {
		return err
	}
	marker := filepath.Join(p.Destination, ".networked-uxs-package")
	if info, err := os.Lstat(p.Destination); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("archive destination is not a real directory")
		}
		saved, err := os.ReadFile(marker)
		if err == nil && string(saved) == p.SHA256 {
			return nil
		}
		return errors.New("archive destination already exists; choose a new version directory")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(p.Destination)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".networked-uxs-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := unpackSoftwareArchive(p.Path, stage, p.Command); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, ".networked-uxs-package"), []byte(p.SHA256), 0600); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	return os.Rename(stage, p.Destination)
}
func checkInstallParents(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !safeInstallOwner(info)) {
			return errors.New("installation parents must not be symlinks or writable by other users")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}

func installLauncher(destination string, launcher packageLauncher) error {
	target := filepath.Join(destination, filepath.FromSlash(launcher.Target))
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("launcher target must be an installed executable")
	}
	if err := checkInstallParents(filepath.Dir(launcher.Path)); err != nil {
		return err
	}
	if info, err := os.Lstat(launcher.Path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			existing, err := os.Readlink(launcher.Path)
			if err == nil && existing == target {
				return nil
			}
		}
		return errors.New("existing launcher requires explicit migration")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(launcher.Path), 0755); err != nil {
		return err
	}
	return os.Symlink(target, launcher.Path)
}

func unpackSoftwareArchive(source, stage, command string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	zipped, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer zipped.Close()
	reader := tar.NewReader(zipped)
	var total int64
	count := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name == "." || name == "" {
			continue
		}
		if !safeRelative(name) {
			return errors.New("archive contains an unsafe path")
		}
		count++
		total += header.Size
		if count > 20000 || header.Size < 0 || total > 2<<30 {
			return errors.New("archive extraction limit exceeded")
		}
		target := filepath.Join(stage, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode)&0755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return errors.New("archive links and special files are not supported")
		}
	}
	if info, err := os.Stat(filepath.Join(stage, filepath.FromSlash(command))); err != nil || !info.Mode().IsRegular() || (!strings.HasSuffix(command, ".jar") && info.Mode().Perm()&0111 == 0) {
		return errors.New("archive lacks its declared command")
	}
	return nil
}
