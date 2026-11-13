package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
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
	if _, err := os.Lstat(i.opts.State); errors.Is(err, os.ErrNotExist) {
		if err := checkInstallParents(i.opts.State); err != nil {
			return nil, err
		}
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
	keyFile := filepath.Join(i.opts.State, "bootstrap-api-key")
	key := ""
	if _, err := os.Lstat(keyFile); err == nil {
		if err := checkCodeFile(keyFile); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, err
		}
		key = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	value, err := secretAPIKeyInput(key != "")
	if err != nil {
		return nil, err
	}
	if value != "" {
		key = value
	}
	if !apiKeyPattern.MatchString(key) {
		return nil, errors.New("invalid UxS API key")
	}
	if err := atomicPrivateWrite(keyFile, []byte(key)); err != nil {
		return nil, err
	}
	api, _ := newLocalAPI("http://127.0.0.1:9094")
	if api.request(ctx, "GET", "/api/health", nil, nil) == nil {
		return nil, errors.New("TVPN is already running; stop the existing installation before bootstrap")
	}
	dir := "/var/lib/somewear/tvpn"
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	} else if !ownedRootTVPN(i.opts.State, dir) {
		return nil, errors.New("TVPN belongs to another installation; migrate it before setup")
	}
	if err := writePrivateJSON(filepath.Join(dir, "uxs-owner.json"), rootTVPNOwner{i.opts.State, claim.IdentityID}); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.toml")
	encodedKey, _ := json.Marshal(key)
	encodedHost, _ := json.Marshal(claim.Backend)
	content := fmt.Sprintf("[api]\nkey = %s\nhost = %s\n\n[package-stream]\nowner = \"tvpn\"\n", encodedKey, encodedHost)
	if err := atomicPrivateWrite(path, []byte(content)); err != nil {
		return nil, err
	}
	i.emit("connecting-tvpn")
	command := i.opts.TVPNCommand
	if command == "" {
		return nil, errors.New("TVPN bootstrap requires the installed root-owned system package")
	}
	if err := requireRootExecutable(command); err != nil {
		return nil, err
	}
	process, err := startRuntimeProcess(command, nil, map[string]string{"CONFIG_PATH": path}, filepath.Join(i.opts.State, "tvpn-bootstrap.log"))
	if err != nil {
		return nil, errors.New("could not start TVPN bootstrap; inspect the private bootstrap log")
	}
	var once sync.Once
	stop := func() { once.Do(process.Stop) }
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
		return errors.New("UxS service started but TVPN has not reconnected; retry using the original claim and state")
	}
	return nil
}
