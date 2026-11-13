package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	root, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err = os.MkdirTemp(root, "grid-uxs-archive-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	state := filepath.Join(root, "state")
	bundle := filepath.Join(root, "bundle")
	units := filepath.Join(root, "units")
	for _, dir := range []string{state, bundle, units} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(state, "owner"):                                                     "uxs-install-v1",
		filepath.Join(state, "install-plan.json"):                                         `{"schemaVersion":1,"unitName":"somewear-uxs-11111111-1111-1111-1111-111111111111.service"}`,
		filepath.Join(bundle, ".claim-code"):                                              "private-test-claim",
		filepath.Join(units, "somewear-uxs-11111111-1111-1111-1111-111111111111.service"): serviceUnit(bundle, state),
	} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	canonical := filepath.Join(root, "tvpn")
	if err := os.Symlink(filepath.Join(state, "tvpn"), canonical); err != nil {
		t.Fatal(err)
	}
	return state, bundle, canonical, units, filepath.Join(root, "backups")
}

func TestReplacementArchivesOnlyOwnedInstallation(t *testing.T) {
	state, bundle, canonical, units, backups := archiveFixture(t)
	unrelated := filepath.Join(units, "other.service")
	if err := os.WriteFile(unrelated, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := ""
	backup, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, func(_ context.Context, name string, args ...string) error {
		calls += name + " " + strings.Join(args, " ") + "\n"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state, bundle, canonical} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("not archived: %s", path)
		}
	}
	data, err := os.ReadFile(filepath.Join(backup, "claim-bundle", ".claim-code"))
	if err != nil || string(data) != "private-test-claim" {
		t.Fatal("prior credential not preserved")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("unrelated service changed")
	}
	if !strings.Contains(calls, "disable --now somewear-uxs-") || strings.Contains(calls, "other.service") {
		t.Fatal("incorrect service scope")
	}
	info, err := os.Stat(backup)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("backup not private")
	}
}

func TestReplacementRejectsUnmanagedServiceBeforeStoppingAnything(t *testing.T) {
	state, bundle, canonical, units, backups := archiveFixture(t)
	if err := os.WriteFile(filepath.Join(units, "somewear-uxs-11111111-1111-1111-1111-111111111111.service"), []byte("foreign service"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, func(context.Context, string, ...string) error {
		t.Fatal("stopped service before validating ownership")
		return nil
	})
	if err == nil {
		t.Fatal("accepted foreign service")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("state changed")
	}
}
