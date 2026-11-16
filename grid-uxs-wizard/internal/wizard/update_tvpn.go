package wizard

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type tvpnUpdateTarget struct {
	Binary, Service string
	Launchd         bool
}

func localTVPNTarget(ctx context.Context) (tvpnUpdateTarget, error) {
	var target tvpnUpdateTarget
	switch runtime.GOOS {
	case "darwin":
		value, err := exec.CommandContext(ctx, "plutil", "-extract", "ProgramArguments.0", "raw", "-o", "-", "/Library/LaunchDaemons/com.somewear.tvpnd.plist").Output()
		if err != nil {
			return target, errors.New("local TVPN service was not found")
		}
		target = tvpnUpdateTarget{Binary: strings.TrimSpace(string(value)), Service: "com.somewear.tvpnd", Launchd: true}
	case "linux":
		for _, name := range []string{"somewear-tvpnd.service", "tvpnd.service"} {
			value, err := exec.CommandContext(ctx, "systemctl", "show", name, "-p", "ExecStart", "--value").Output()
			if err != nil {
				continue
			}
			first, _, _ := strings.Cut(strings.TrimSpace(string(value)), ";")
			binary, ok := strings.CutPrefix(strings.TrimSpace(first), "{ path=")
			if ok && strings.TrimSpace(binary) != "" {
				target = tvpnUpdateTarget{Binary: strings.TrimSpace(binary), Service: name}
				break
			}
		}
	default:
		return target, errors.New("local TVPN updates support macOS and Linux")
	}
	path, err := filepath.EvalSymlinks(target.Binary)
	if err != nil {
		return target, errors.New("local TVPN binary was not found")
	}
	if filepath.Base(path) != "tvpnd" {
		return target, errors.New("TVPN service does not reference a supported daemon binary")
	}
	target.Binary = path
	return target, nil
}
func replaceLocalTVPN(ctx context.Context, source, sha string, target tvpnUpdateTarget, run func(context.Context, string, ...string) error) error {
	if current, err := fileDigest(target.Binary); err == nil && current == sha {
		return nil
	}
	if err := copyVerified(source, target.Binary, sha); err != nil {
		return err
	}
	if err := os.Chmod(target.Binary, 0755); err != nil {
		return err
	}
	if target.Launchd {
		return run(ctx, "launchctl", "kickstart", "-k", "system/"+target.Service)
	}
	return run(ctx, "systemctl", "restart", target.Service)
}
func executeTVPNUpdate(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("TVPN updates require sudo")
	}
	flags := flag.NewFlagSet("internal TVPN update", flag.ContinueOnError)
	source := flags.String("source", "", "")
	sha := flags.String("sha256", "", "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *source == "" || !regexpSHA.MatchString(*sha) {
		return errors.New("invalid TVPN update")
	}
	ctx, stop := setupContext()
	defer stop()
	target, err := localTVPNTarget(ctx)
	if err != nil {
		return err
	}
	if err := requireRootExecutable(target.Binary); err != nil {
		return err
	}
	if err := replaceLocalTVPN(ctx, *source, *sha, target, runCommand); err != nil {
		return fmt.Errorf("TVPN update failed: %w", err)
	}
	return nil
}
