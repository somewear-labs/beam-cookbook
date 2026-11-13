package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (i *installer) installTargetPackages(ctx context.Context) error {
	i.emit("checking-packages")
	if i.opts.State == "" || !filepath.IsAbs(i.opts.State) {
		return errors.New("provide an absolute UxS state directory")
	}
	bundle, err := readSoftwareBundle(i.opts.Bundle)
	if err != nil {
		return err
	}
	if bundle.Beam.Format != "tar.gz" || bundle.TVPN.Format != "tar.gz" {
		return errors.New("UxS bundles require native archives so installation does not start standard GCS services")
	}
	cache, err := os.MkdirTemp("", "grid-uxs-packages-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)
	beam, err := prepareSoftware(ctx, i.opts.Bundle, cache, bundle.Beam)
	if err != nil {
		return fmt.Errorf("Beam: %w", err)
	}
	tvpn, err := prepareSoftware(ctx, i.opts.Bundle, cache, bundle.TVPN)
	if err != nil {
		return fmt.Errorf("TVPN: %w", err)
	}
	if i.opts.DryRun {
		i.emit("packages-verified")
		return nil
	}
	i.emit("installing-beam")
	beamCommand, err := installSoftware(ctx, beam, i.run)
	if err != nil {
		return err
	}
	i.emit("installing-tvpn")
	tvpnCommand, err := installSoftware(ctx, tvpn, i.run)
	if err != nil {
		return err
	}
	claimBundle := filepath.Clean(i.opts.State) + "-bundle"
	if err := checkInstallParents(claimBundle); err != nil {
		return err
	}
	marker := filepath.Join(claimBundle, ".grid-uxs-wizard-bundle")
	fingerprint, err := fileDigest(filepath.Join(i.opts.Bundle, "install.json"))
	if err != nil {
		return err
	}
	if _, err := os.Lstat(claimBundle); err == nil {
		content, err := os.ReadFile(marker)
		if err != nil || string(content) != fingerprint {
			return errors.New("claim bundle belongs to another installation")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(claimBundle, 0700); err != nil {
			return err
		}
		if err := atomicPrivateWrite(marker, []byte(fingerprint)); err != nil {
			return err
		}
	} else {
		return err
	}
	sources := map[string]string{"tvpnd": filepath.Join(filepath.Dir(tvpnCommand), "tvpnd")}
	home := filepath.Dir(filepath.Dir(beamCommand))
	if strings.HasSuffix(beamCommand, ".jar") {
		sources["beam.jar"] = beamCommand
	} else if _, err := os.Stat(filepath.Join(home, "package.json")); err == nil {
		i.opts.BeamHome = home
	} else {
		sources["beam"] = beamCommand
	}
	for name, source := range sources {
		digest, err := fileDigest(source)
		if err != nil {
			return fmt.Errorf("installed %s binary unavailable", name)
		}
		if err := copyVerified(source, filepath.Join(claimBundle, name), digest); err != nil {
			return err
		}
	}
	for _, name := range []string{"horizon-profile.json", "horizon-key.hex"} {
		source := filepath.Join(i.opts.Bundle, name)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		digest, err := fileDigest(source)
		if err != nil {
			return err
		}
		if err := copyVerified(source, filepath.Join(claimBundle, name), digest); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(claimBundle, name), 0600); err != nil {
			return err
		}
	}
	i.opts.Beam = beamCommand
	i.opts.Bundle = claimBundle
	return nil
}
