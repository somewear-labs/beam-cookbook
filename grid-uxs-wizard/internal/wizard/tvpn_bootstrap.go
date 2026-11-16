package wizard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type tvpnPeer struct {
	IdentityID     wireID `json:"identityId"`
	Hostname       string `json:"hostname"`
	OverlayAddress string `json:"overlayAddress"`
}

func resolveGCSPeer(peers []tvpnPeer, claim transportClaim) (string, error) {
	var address string
	for _, peer := range peers {
		if string(peer.IdentityID) != claim.GCSIdentityID || peer.Hostname != claim.Hostname {
			continue
		}
		ip := net.ParseIP(peer.OverlayAddress)
		if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() {
			return "", errors.New("GCS peer has an invalid TVPN address")
		}
		candidate := "https://" + net.JoinHostPort(ip.String(), "9445")
		if address != "" {
			return "", errors.New("multiple TVPN peers match this GCS; stop duplicate GCS instances before retrying")
		}
		address = candidate
	}
	return address, nil
}

func validateTransportCredential(state string, claim transportClaim) error {
	credential, _, err := readClaimMaterial(state)
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(state, "bootstrap-api-key"))
	if err != nil {
		return err
	}
	if credential.APIKey != string(key) || credential.Backend != claim.Backend || credential.WorkspaceID != claim.WorkspaceID || credential.GCSIdentityID != claim.GCSIdentityID || credential.IdentityID != claim.IdentityID {
		return errors.New("claimed identity does not match the supplied UxS API key and TVPN claim; setup stopped")
	}
	return nil
}

func (i *installer) bootstrapTVPN(ctx context.Context, claim transportClaim) (func(), error) {
	if i.pinBootstrapStop != nil {
		return i.pinBootstrapStop, nil
	}
	if _, err := os.Lstat(filepath.Join(i.opts.State, "claimed-bootstrap.json")); err == nil {
		if err := validateTransportCredential(i.opts.State, claim); err != nil {
			return nil, err
		}
		_, pairing, err := readClaimMaterial(i.opts.State)
		if err != nil {
			return nil, err
		}
		i.opts.GCS = pairing.GCSBaseURL
		return func() {}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := i.prepareBootstrapState(); err != nil {
		return nil, err
	}
	key, err := i.bootstrapAPIKey()
	if err != nil {
		return nil, err
	}
	api, process, stop, err := i.startBootstrapTVPN(ctx, key, claim.Backend, claim.IdentityID)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			stop()
		}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := connectBootstrapPeer(waitCtx, api, claim, process.Exited, func(address string) { i.opts.GCS = address }); err != nil {
		return nil, err
	}
	i.emit("waiting-for-gcs")
	if err := waitForClaimTransport(waitCtx, i.opts.GCS); err != nil {
		return nil, err
	}
	ready = true
	return stop, nil
}

func connectBootstrapPeer(ctx context.Context, api *localAPI, claim transportClaim, exited func() bool, resolved func(string)) error {
	if err := waitUntil(ctx, func() (bool, error) {
		if exited() {
			return false, errors.New("TVPN bootstrap stopped; inspect its private log")
		}
		var state struct {
			Workspaces []struct {
				ID wireID `json:"id"`
			} `json:"workspaces"`
		}
		if api.request(ctx, "GET", "/api/workspace", nil, &state) != nil {
			return false, nil
		}
		for _, workspace := range state.Workspaces {
			if string(workspace.ID) == claim.WorkspaceID {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return errors.New("TVPN could not load the claim Workspace; check the UxS API key and broadband access, then retry")
	}
	if err := api.request(ctx, "PUT", "/api/workspace/activate", map[string]string{"workspaceId": claim.WorkspaceID}, nil); err != nil {
		return err
	}
	if err := waitUntil(ctx, func() (bool, error) {
		if exited() {
			return false, errors.New("TVPN bootstrap stopped; inspect its private log")
		}
		var state struct {
			Active wireID `json:"activeWorkspaceId"`
		}
		if api.request(ctx, "GET", "/api/workspace", nil, &state) != nil || string(state.Active) != claim.WorkspaceID {
			return false, nil
		}
		var identity struct {
			ID wireID `json:"identityId"`
		}
		if api.request(ctx, "GET", "/api/identity", nil, &identity) != nil {
			return false, nil
		}
		if string(identity.ID) == "" {
			return false, nil
		}
		if string(identity.ID) != claim.IdentityID {
			return false, errors.New("supplied API key belongs to a different UxS identity")
		}
		var list struct {
			Peers []tvpnPeer `json:"peers"`
		}
		if api.request(ctx, "GET", "/api/net/peer", nil, &list) != nil {
			return false, nil
		}
		address, err := resolveGCSPeer(list.Peers, claim)
		if err != nil {
			return false, err
		}
		if address == "" {
			return false, nil
		}
		resolved(address)
		return true, nil
	}); err != nil {
		return fmt.Errorf("could not discover the expected GCS through TVPN: %w", err)
	}
	return nil
}

func waitForClaimTransport(ctx context.Context, origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || validateGCSAddress(origin) != nil {
		return errors.New("invalid paired GCS address")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	err = waitUntil(ctx, func() (bool, error) {
		dialer := net.Dialer{Timeout: 2 * time.Second}
		connection, err := dialer.DialContext(ctx, "tcp", parsed.Host)
		if err != nil {
			return false, nil
		}
		connection.Close()
		return true, nil
	})
	if err != nil {
		return errors.New("GCS provisioning connection is not reachable through TVPN; retry using the saved setup code and state")
	}
	return nil
}
