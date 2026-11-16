package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var selectedInstallUID uint32

func resolveInstallUser(name string) (*user.User, error) {
	if name == "" {
		name = os.Getenv("SUDO_USER")
	}
	if name == "" && os.Geteuid() != 0 {
		current, err := user.Current()
		if err != nil {
			return nil, err
		}
		name = current.Username
	}
	if name == "" || name == "root" {
		return nil, errors.New("provide --user for the normal account that will run Beam; Beam must not run as root")
	}
	account, err := user.Lookup(name)
	if err != nil {
		return nil, errors.New("Beam user does not exist")
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 || !filepath.IsAbs(account.HomeDir) {
		return nil, errors.New("Beam requires a non-root Unix user account")
	}
	selectedInstallUID = uint32(uid)
	return account, nil
}

func userCommand(account *user.User, command string, args ...string) (string, []string) {
	env := []string{"-H", "-u", account.Username, "--", "env", "-u", "SUDO_USER", "-u", "SUDO_UID", "-u", "SUDO_GID", "-u", "SUDO_COMMAND", "HOME=" + account.HomeDir, "BEAM_USER_HOME=" + account.HomeDir}
	if runtime.GOOS == "linux" {
		env = append(env, "XDG_RUNTIME_DIR=/run/user/"+account.Uid, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+account.Uid+"/bus")
	}
	env = append(env, command)
	env = append(env, args...)
	if runtime.GOOS == "darwin" {
		return "/bin/launchctl", append([]string{"asuser", account.Uid, "/usr/bin/sudo"}, env...)
	}
	return "sudo", env
}

func (i *gcsInstaller) runBeam(ctx context.Context, command string, args ...string) error {
	if i.opts.RunUser == "" {
		return i.run(ctx, command, args...)
	}
	account, err := resolveInstallUser(i.opts.RunUser)
	if err != nil {
		return err
	}
	command, args = userCommand(account, command, args...)
	return i.run(ctx, command, args...)
}

type rootTVPNOwner struct {
	State      string `json:"state"`
	IdentityID string `json:"identityId"`
	Pending    bool   `json:"pending,omitempty"`
}

func ownedRootTVPN(state, canonical string) bool {
	data, err := os.ReadFile(filepath.Join(canonical, "uxs-owner.json"))
	if err != nil {
		return false
	}
	var owner rootTVPNOwner
	return json.Unmarshal(data, &owner) == nil && owner.State == state && (numericID.MatchString(owner.IdentityID) || (owner.Pending && owner.IdentityID == "" && validateOwnedState(state) == nil))
}

func rootTVPNService(command string) string {
	return fmt.Sprintf("[Unit]\nDescription=Somewear TVPN\nWants=network-online.target\nAfter=network-online.target\n[Service]\nType=simple\nUser=root\nExecStart=%s\nRestart=on-failure\nRestartSec=5\nUMask=0077\n[Install]\nWantedBy=multi-user.target\n", command)
}

func secureUserServiceParents(account *user.User) error {
	if err := checkInstallParents(account.HomeDir); err != nil {
		return err
	}
	type directory struct {
		path string
		mode os.FileMode
	}
	directories := []directory{}
	for _, relative := range []string{".config", ".config/systemd", ".config/systemd/user"} {
		path := filepath.Join(account.HomeDir, relative)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !safeInstallOwner(info) {
			return fmt.Errorf("user service directory %s must be an owned directory, not a symlink", path)
		}
		directories = append(directories, directory{path, info.Mode().Perm()})
	}
	for _, directory := range directories {
		if directory.mode&0022 != 0 {
			if err := os.Chmod(directory.path, directory.mode&^0022); err != nil {
				return err
			}
		}
	}
	return nil
}

func userRuntimeService(state string) string {
	return fmt.Sprintf("[Unit]\nDescription=Somewear UxS Beam\n[Service]\nType=simple\nExecStart=%s/bin/grid-uxs-wizard run --state %s\nRestart=on-failure\nRestartSec=5\nKillMode=control-group\nUMask=0077\n[Install]\nWantedBy=default.target\n", state, state)
}

func chownTree(path string, account *user.User) error {
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	return filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("user runtime paths must not contain links")
		}
		return os.Chown(path, uid, gid)
	})
}

func (i *installer) configureRootTVPN(ctx context.Context) error {
	credential, _, err := readClaimMaterial(i.opts.State)
	if err != nil {
		return err
	}
	command := i.opts.TVPNCommand
	if !safePath.MatchString(command) || filepath.Clean(command) != command {
		return errors.New("root TVPN requires an installed system package")
	}
	if err := requireRootExecutable(command); err != nil {
		return err
	}
	canonical := "/var/lib/somewear/tvpn"
	if _, err := os.Lstat(canonical); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(canonical, 0700); err != nil {
			return err
		}
	} else if !ownedRootTVPN(i.opts.State, canonical) {
		return errors.New("root TVPN installation ownership is unavailable")
	}
	key, _ := json.Marshal(credential.APIKey)
	host, _ := json.Marshal(credential.Backend)
	path := filepath.Join(canonical, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := mergeSection(string(data), "api", map[string]string{"key": string(key), "host": string(host)})
	if err := atomicPrivateWrite(path, []byte(content)); err != nil {
		return err
	}
	if err := writePrivateJSON(filepath.Join(canonical, "uxs-owner.json"), rootTVPNOwner{State: i.opts.State, IdentityID: credential.IdentityID}); err != nil {
		return err
	}
	unit := "/etc/systemd/system/somewear-tvpnd.service"
	body := rootTVPNService(command)
	if old, err := os.ReadFile(unit); err == nil && string(old) != body {
		return errors.New("existing TVPN service requires explicit migration")
	}
	if err := writeUnit(unit, body); err != nil {
		return err
	}
	if err := i.run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return i.run(ctx, "systemctl", "enable", "--now", "somewear-tvpnd.service")
}

func (i *installer) installUserRuntime(ctx context.Context, plan installPlan, acknowledged bool) error {
	account, err := resolveInstallUser(i.opts.RunUser)
	if err != nil {
		return err
	}
	// Runtime preparation runs as root; hand ownership back before a later step can fail.
	for _, path := range []string{i.opts.State, i.opts.Bundle} {
		if err := chownTree(path, account); err != nil {
			return err
		}
	}
	if err := i.configureRootTVPN(ctx); err != nil {
		return err
	}
	if !acknowledged {
		i.emit("reconnecting-tvpn")
		if err := waitForClaimTransport(ctx, i.opts.GCS); err != nil {
			return err
		}
		i.emit("acknowledging-claim")
		if err := i.claim(ctx, "--ack-only"); err != nil {
			return err
		}
		if err := saveClaimAcknowledgement(i.opts); err != nil {
			return err
		}
	}
	for _, path := range []string{"/var/lib/somewear", "/var/lib/somewear/uxs"} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid system state parent")
		}
		if info.Mode().Perm()&0001 == 0 {
			if err := os.Chmod(path, info.Mode().Perm()|0011); err != nil {
				return err
			}
		}
	}
	for _, path := range []string{i.opts.State, i.opts.Bundle} {
		if err := chownTree(path, account); err != nil {
			return err
		}
	}
	if err := secureUserServiceParents(account); err != nil {
		return err
	}
	unitDir := filepath.Join(account.HomeDir, ".config/systemd/user")
	if err := os.MkdirAll(unitDir, 0700); err != nil {
		return err
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	for current := unitDir; strings.HasPrefix(current, filepath.Join(account.HomeDir, ".config")); current = filepath.Dir(current) {
		if err := os.Chown(current, uid, gid); err != nil {
			return err
		}
	}
	unitPath := filepath.Join(unitDir, plan.UnitName)
	body := userRuntimeService(i.opts.State)
	if old, err := os.ReadFile(unitPath); err == nil && string(old) != body {
		return errors.New("existing user Beam service requires explicit migration")
	}
	if err := writeUnit(unitPath, body); err != nil {
		return err
	}
	if err := os.Chown(unitPath, uid, gid); err != nil {
		return err
	}
	if err := i.run(ctx, "loginctl", "enable-linger", account.Username); err != nil {
		return err
	}
	if err := i.run(ctx, "systemctl", "start", "user@"+account.Uid+".service"); err != nil {
		return err
	}
	command, args := userCommand(account, "systemctl", "--user", "daemon-reload")
	if err := i.run(ctx, command, args...); err != nil {
		return err
	}
	command, args = userCommand(account, "systemctl", "--user", "enable", "--now", plan.UnitName)
	i.emit("installing-service")
	if err := i.run(ctx, command, args...); err != nil {
		return err
	}
	i.emit("waiting-for-paired-setup")
	return i.waitReady(ctx, plan.UnitName, time.Time{})
}

func openBrowserForUser(ctx context.Context, name, link string) error {
	if name == "" {
		return openBrowser(ctx, link)
	}
	account, err := resolveInstallUser(name)
	if err != nil {
		return err
	}
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "/usr/bin/open"
	}
	command, args := userCommand(account, command, link)
	return runCommand(ctx, command, args...)
}

func (i *installer) prepareUserClaimState() (*user.User, error) {
	account, err := resolveInstallUser(i.opts.RunUser)
	if err != nil {
		return nil, err
	}
	for _, parent := range []string{"/var/lib/somewear", "/var/lib/somewear/uxs"} {
		if err := os.MkdirAll(parent, 0711); err != nil {
			return nil, err
		}
		info, err := os.Lstat(parent)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("invalid system state parent")
		}
		if info.Mode().Perm()&0001 == 0 {
			if err := os.Chmod(parent, info.Mode().Perm()|0011); err != nil {
				return nil, err
			}
		}
	}
	if _, err := os.Lstat(i.opts.State); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(i.opts.State, 0700); err != nil {
			return nil, err
		}
		if err := atomicPrivateWrite(filepath.Join(i.opts.State, "owner"), []byte("uxs-install-v1\n")); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := validateOwnedState(i.opts.State); err != nil {
		return nil, err
	}
	for _, path := range []string{i.opts.State, i.opts.Bundle} {
		if err := chownTree(path, account); err != nil {
			return nil, err
		}
	}
	return account, nil
}
