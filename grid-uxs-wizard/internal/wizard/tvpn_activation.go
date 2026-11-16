package wizard

import (
	"context"
	"errors"
	"time"
)

type tvpnConfigStatus struct {
	Latest  *uint64 `json:"latestStoredRevision"`
	Running *uint64 `json:"runningRevision"`
	Pending bool    `json:"pending"`
}

func activateTVPNConfiguration(ctx context.Context, api *localAPI, run func(context.Context, string, ...string) error, command string, revision uint64, platform string) error {
	if revision == 0 {
		return errors.New("TVPN did not acknowledge a managed configuration revision")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var status tvpnConfigStatus
	if err := api.request(ctx, "GET", "/api/config/daemon", nil, &status); err != nil {
		return err
	}
	if status.Latest == nil || *status.Latest != revision {
		return errors.New("TVPN configuration changed; rerun setup before restarting")
	}
	if status.Running != nil && *status.Running == revision && !status.Pending {
		return nil
	}
	if platform == "linux" || platform == "windows" {
		if err := api.request(ctx, "POST", "/api/config/daemon/activate", map[string]any{"baseRevision": revision}, nil); err != nil {
			return err
		}
	} else {
		if err := run(ctx, command, "down"); err != nil {
			return err
		}
		if err := run(ctx, command, "up"); err != nil {
			return err
		}
	}
	return waitUntil(ctx, func() (bool, error) {
		var current tvpnConfigStatus
		if err := api.request(ctx, "GET", "/api/config/daemon", nil, &current); err != nil {
			return false, nil
		}
		if current.Latest != nil && *current.Latest != revision {
			return false, errors.New("TVPN configuration changed while restarting; review and rerun setup")
		}
		return current.Latest != nil && current.Running != nil && *current.Latest == revision && *current.Running == revision && !current.Pending, nil
	})
}
