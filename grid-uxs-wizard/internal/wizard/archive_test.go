package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
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
	backup, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, nil, func(_ context.Context, name string, args ...string) error {
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
	_, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, nil, func(context.Context, string, ...string) error {
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

func rootTVPNArchiveFixture(t *testing.T) (string, string, string, string, string, *user.User) {
	t.Helper()
	state, bundle, canonical, units, backups := archiveFixture(t)
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(canonical, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(filepath.Join(canonical, "uxs-owner.json"), rootTVPNOwner{State: state, IdentityID: "200"}); err != nil {
		t.Fatal(err)
	}
	name := "somewear-uxs-11111111-1111-1111-1111-111111111111.service"
	if err := os.Remove(filepath.Join(units, name)); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(filepath.Dir(state), "user-home")
	account := &user.User{Username: "testuser", Uid: fmt.Sprint(os.Getuid()), Gid: fmt.Sprint(os.Getgid()), HomeDir: home}
	userUnits := filepath.Join(home, ".config/systemd/user")
	if err := os.MkdirAll(userUnits, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userUnits, name), []byte(userRuntimeService(state)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(units, "somewear-tvpnd.service"), []byte(rootTVPNService("/opt/test/tvpnd")), 0600); err != nil {
		t.Fatal(err)
	}
	return state, bundle, canonical, units, backups, account
}

func TestReplacementArchivesOwnedRootTVPNAndUserBeam(t *testing.T) {
	state, bundle, canonical, units, backups, account := rootTVPNArchiveFixture(t)
	calls := ""
	backup, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, account, func(_ context.Context, command string, args ...string) error {
		calls += command + " " + strings.Join(args, " ") + "\n"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls, "systemctl --user disable --now somewear-uxs-") || !strings.Contains(calls, "systemctl disable --now somewear-tvpnd.service") {
		t.Fatal("both service scopes were not stopped")
	}
	for _, path := range []string{state, bundle, canonical, filepath.Join(units, "somewear-tvpnd.service"), filepath.Join(account.HomeDir, ".config/systemd/user/somewear-uxs-11111111-1111-1111-1111-111111111111.service")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("not archived: %s", path)
		}
	}
	if !ownedRootTVPN(state, filepath.Join(backup, "tvpn")) {
		t.Fatal("TVPN ownership/configuration not preserved")
	}
	data, err := os.ReadFile(filepath.Join(backup, "reset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest archivedInstallation
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Services) != 2 {
		t.Fatal("missing service recovery records")
	}
	for _, service := range manifest.Services {
		if _, err := os.Stat(service.Backup); err != nil {
			t.Fatal("service body not backed up")
		}
	}
}

func TestReplacementRejectsForeignRootTVPNBeforeStoppingServices(t *testing.T) {
	state, bundle, canonical, units, backups, account := rootTVPNArchiveFixture(t)
	if err := writePrivateJSON(filepath.Join(canonical, "uxs-owner.json"), rootTVPNOwner{State: state + "-other", IdentityID: "201"}); err != nil {
		t.Fatal(err)
	}
	_, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, account, func(context.Context, string, ...string) error {
		t.Fatal("service touched before ownership validation")
		return nil
	})
	if err == nil {
		t.Fatal("foreign TVPN accepted")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("state changed")
	}
}

func TestReplacementRejectsForeignRootServiceBeforeStoppingAnything(t *testing.T) {
	state, bundle, canonical, units, backups, account := rootTVPNArchiveFixture(t)
	if err := os.WriteFile(filepath.Join(units, "somewear-tvpnd.service"), []byte("foreign service"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, account, func(context.Context, string, ...string) error {
		t.Fatal("service touched before service validation")
		return nil
	})
	if err == nil {
		t.Fatal("foreign service accepted")
	}
}

func TestReplacementSecuresOwnedUserServiceDirectories(t *testing.T) {
	state, bundle, canonical, units, backups, account := rootTVPNArchiveFixture(t)
	parents := []string{filepath.Join(account.HomeDir, ".config/systemd"), filepath.Join(account.HomeDir, ".config/systemd/user")}
	for _, path := range parents {
		if err := os.Chmod(path, 0775); err != nil {
			t.Fatal(err)
		}
	}
	_, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, account, func(context.Context, string, ...string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range parents {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("unsafe service directory: %s", path)
		}
	}
}

func TestUserServiceDirectoryRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	state, _, _, _, _, account := rootTVPNArchiveFixture(t)
	systemd := filepath.Join(account.HomeDir, ".config/systemd")
	if err := os.RemoveAll(systemd); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(state), "foreign-systemd")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0775); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, systemd); err != nil {
		t.Fatal(err)
	}
	if err := secureUserServiceParents(account); err == nil {
		t.Fatal("symlink accepted")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0775 {
		t.Fatal("symlink target changed")
	}
}

func TestResetArchivesInterruptedSetupWithoutClaimBundle(t *testing.T) {
	state, bundle, canonical, units, backups, account := rootTVPNArchiveFixture(t)
	if err := os.RemoveAll(bundle); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(filepath.Join(canonical, "uxs-owner.json"), rootTVPNOwner{State: state, Pending: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "bootstrap-api-key"), []byte("private-key"), 0600); err != nil {
		t.Fatal(err)
	}
	backup, err := archiveManagedInstallation(context.Background(), state, bundle, canonical, units, backups, account, func(context.Context, string, ...string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state, canonical} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("installation was not reset")
		}
	}
	value, err := os.ReadFile(filepath.Join(backup, "state/bootstrap-api-key"))
	if err != nil || string(value) != "private-key" {
		t.Fatal("bootstrap credential was not backed up")
	}
	var manifest archivedInstallation
	if err := readPrivateJSON(filepath.Join(backup, "reset.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Moved) != 2 {
		t.Fatal("unexpected archive paths")
	}
}
