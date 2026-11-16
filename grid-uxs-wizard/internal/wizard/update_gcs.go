package wizard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func requireNoPendingSetup(ctx context.Context, api *localAPI) error {
	var status struct {
		Phase string `json:"phase"`
	}
	err := api.request(ctx, "GET", "/api/uxs/provisioning/mission-computers/status", nil, &status)
	if err != nil {
		var failure *localAPIError
		if errors.As(err, &failure) && (failure.status == 404 || failure.status == 403) {
			return nil
		}
		return errors.New("could not check pending GCS setup")
	}
	switch status.Phase {
	case "", "Claimed", "Failed", "Cancelled":
		return nil
	default:
		return errors.New("finish or cancel the pending UxS setup before restarting GCS Beam")
	}
}
func applyGcsUpdate(ctx context.Context, beam, claimRelease string, api *localAPI, run func(context.Context, string, ...string) error) error {
	if api == nil {
		api, _ = newLocalAPI("http://127.0.0.1:9091")
	}
	if err := requireNoPendingSetup(ctx, api); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plist := filepath.Join(home, "Library/LaunchAgents/com.somewear.beam.plist")
	wrapper, err := exec.CommandContext(ctx, "plutil", "-extract", "ProgramArguments.0", "raw", "-o", "-", plist).Output()
	if err != nil {
		return errors.New("user-scoped GCS Beam service was not found")
	}
	target := filepath.Join(filepath.Dir(strings.TrimSpace(string(wrapper))), "beam.jar")
	if err := checkInstallParents(target); err != nil {
		return err
	}
	sha, err := fileDigest(beam)
	if err != nil {
		return err
	}
	if err := api.request(ctx, "POST", "/api/properties", map[string]any{"updates": map[string]string{"uxs-provisioning-release-url": claimRelease}, "target": "user"}, nil); err != nil {
		return err
	}
	if err := copyVerified(beam, target, sha); err != nil {
		return err
	}
	uid := strconv.Itoa(os.Getuid())
	if err := run(ctx, "launchctl", "asuser", uid, "launchctl", "kickstart", "-k", "gui/"+uid+"/com.somewear.beam"); err != nil {
		return fmt.Errorf("Beam updated, but restarting its user service failed: %w", err)
	}
	return nil
}
