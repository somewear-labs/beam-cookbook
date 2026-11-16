package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

func (i *installer) prepareBootstrapState() error {
	if _, err := os.Lstat(i.opts.State); errors.Is(err, os.ErrNotExist) {
		if err := checkInstallParents(i.opts.State); err != nil {
			return err
		}
		if err := os.MkdirAll(i.opts.State, 0700); err != nil {
			return err
		}
		if err := atomicPrivateWrite(filepath.Join(i.opts.State, "owner"), []byte("uxs-install-v1\n")); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return validateOwnedState(i.opts.State)
}
func (i *installer) bootstrapAPIKey() (string, error) {
	path := filepath.Join(i.opts.State, "bootstrap-api-key")
	key := ""
	if _, err := os.Lstat(path); err == nil {
		if err := checkCodeFile(path); err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		key = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	value, err := secretAPIKeyInput(key != "")
	if err != nil {
		return "", err
	}
	if value != "" {
		key = value
	}
	if !apiKeyPattern.MatchString(key) {
		return "", errors.New("invalid UxS API key")
	}
	if err := atomicPrivateWrite(path, []byte(key)); err != nil {
		return "", err
	}
	return key, nil
}
func (i *installer) startBootstrapTVPN(ctx context.Context, key, backend, identity string) (*localAPI, managedProcess, func(), error) {
	api, _ := newLocalAPI("http://127.0.0.1:9094")
	if api.request(ctx, "GET", "/api/health", nil, nil) == nil {
		return nil, nil, nil, errors.New("TVPN is already running; stop the existing installation before bootstrap")
	}
	dir := "/var/lib/somewear/tvpn"
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, nil, nil, err
		}
	} else if err != nil {
		return nil, nil, nil, err
	} else if !ownedRootTVPN(i.opts.State, dir) {
		return nil, nil, nil, errors.New("TVPN belongs to another installation; migrate it before setup")
	}
	owner := rootTVPNOwner{State: i.opts.State, IdentityID: identity, Pending: identity == ""}
	if err := writePrivateJSON(filepath.Join(dir, "uxs-owner.json"), owner); err != nil {
		return nil, nil, nil, err
	}
	encoded, _ := json.Marshal(key)
	content := fmt.Sprintf("[api]\nkey = %s\n", encoded)
	if backend != "" {
		if !backendPattern.MatchString(backend) {
			return nil, nil, nil, errors.New("invalid API host")
		}
		host, _ := json.Marshal(backend)
		content += fmt.Sprintf("host = %s\n", host)
	}
	content += "\n[package-stream]\nowner = \"tvpn\"\n"
	path := filepath.Join(dir, "config.toml")
	if err := atomicPrivateWrite(path, []byte(content)); err != nil {
		return nil, nil, nil, err
	}
	if err := requireRootExecutable(i.opts.TVPNCommand); err != nil {
		return nil, nil, nil, err
	}
	i.emit("connecting-tvpn")
	process, err := startRuntimeProcess(i.opts.TVPNCommand, nil, map[string]string{"CONFIG_PATH": path}, filepath.Join(i.opts.State, "tvpn-bootstrap.log"))
	if err != nil {
		return nil, nil, nil, errors.New("could not start TVPN bootstrap; inspect the private bootstrap log")
	}
	var once sync.Once
	stop := func() { once.Do(process.Stop) }
	return api, process, stop, nil
}

type pinGCS struct {
	Peer      tvpnPeer
	Challenge pinChallenge
	Origin    string
}

func findPinGCS(ctx context.Context, api *localAPI, key string, exited func() bool, probe func(context.Context, string, string, string) (pinChallenge, error)) (pinGCS, error) {
	var found []pinGCS
	err := waitUntil(ctx, func() (bool, error) {
		if exited() {
			return false, errors.New("TVPN bootstrap stopped; inspect its private log")
		}
		var workspaces struct {
			Active     wireID `json:"activeWorkspaceId"`
			Workspaces []struct {
				ID wireID `json:"id"`
			} `json:"workspaces"`
		}
		if api.request(ctx, "GET", "/api/workspace", nil, &workspaces) != nil {
			return false, nil
		}
		sort.SliceStable(workspaces.Workspaces, func(a, b int) bool {
			return workspaces.Workspaces[a].ID == workspaces.Active && workspaces.Workspaces[b].ID != workspaces.Active
		})
		found = nil
		for _, workspace := range workspaces.Workspaces {
			if !numericID.MatchString(string(workspace.ID)) {
				continue
			}
			if workspace.ID != workspaces.Active && api.request(ctx, "PUT", "/api/workspace/activate", map[string]string{"workspaceId": string(workspace.ID)}, nil) != nil {
				continue
			}
			activeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := waitUntil(activeCtx, func() (bool, error) {
				var state struct {
					Active wireID `json:"activeWorkspaceId"`
				}
				return api.request(activeCtx, "GET", "/api/workspace", nil, &state) == nil && state.Active == workspace.ID, nil
			})
			cancel()
			if err != nil {
				continue
			}
			discoveryCtx, stopDiscovery := context.WithTimeout(ctx, 15*time.Second)
			discoveryErr := waitUntil(discoveryCtx, func() (bool, error) {
				var identity struct {
					ID wireID `json:"identityId"`
				}
				var peers struct {
					Peers []tvpnPeer `json:"peers"`
				}
				if api.request(discoveryCtx, "GET", "/api/identity", nil, &identity) != nil || !numericID.MatchString(string(identity.ID)) || api.request(discoveryCtx, "GET", "/api/net/peer", nil, &peers) != nil {
					return false, nil
				}
				for _, peer := range peers.Peers {
					if peer.IdentityID == identity.ID {
						continue
					}
					ip := net.ParseIP(peer.OverlayAddress)
					if ip == nil || ip.To4() == nil || !isOverlayIP(ip) {
						continue
					}
					origin := "https://" + net.JoinHostPort(ip.String(), "9445")
					challenge, err := probe(discoveryCtx, origin, string(identity.ID), key)
					if err != nil {
						continue
					}
					if challenge.GCSIdentityID != string(peer.IdentityID) || challenge.WorkspaceID != string(workspace.ID) {
						continue
					}
					found = append(found, pinGCS{peer, challenge, origin})
				}
				return len(found) > 0, nil
			})
			stopDiscovery()
			if discoveryErr == nil {
				return true, nil
			}

		}
		return len(found) > 0, nil
	})
	if err != nil {
		return pinGCS{}, errors.New("no matching pending setup was found on the GCS; check the API key, start provisioning on the GCS and retry")
	}
	if len(found) == 1 {
		return found[0], nil
	}
	options := make([]menuOption, len(found))
	for index, candidate := range found {
		options[index] = menuOption{Label: fmt.Sprintf("%s (Workspace %s)", candidate.Peer.Hostname, candidate.Challenge.WorkspaceID), Value: candidate.Origin}
	}
	selected, err := promptChoice("Choose the GCS provisioning this UxS", options)
	if err != nil {
		return pinGCS{}, err
	}
	for _, candidate := range found {
		if candidate.Origin == selected {
			return candidate, nil
		}
	}
	return pinGCS{}, errors.New("GCS selection was cancelled")
}
func isOverlayIP(ip net.IP) bool {
	v := ip.To4()
	return v != nil && v[0] == 100 && v[1] >= 64 && v[1] <= 127
}
func (i *installer) obtainPINClaim(ctx context.Context) (string, error) {
	if err := i.prepareBootstrapState(); err != nil {
		return "", err
	}
	key, err := i.bootstrapAPIKey()
	if err != nil {
		return "", err
	}
	pin, err := secretPINInput()
	if err != nil {
		return "", err
	}
	api, process, stop, err := i.startBootstrapTVPN(ctx, key, i.opts.APIHost, "")
	if err != nil {
		return "", err
	}
	ready := false
	defer func() {
		if !ready {
			stop()
		}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	i.emit("discovering-gcs")
	selected, err := findPinGCS(waitCtx, api, key, process.Exited, loadPinChallenge)
	if err != nil {
		return "", err
	}
	if err := api.request(ctx, "PUT", "/api/workspace/activate", map[string]string{"workspaceId": selected.Challenge.WorkspaceID}, nil); err != nil {
		return "", err
	}
	if err := waitUntil(waitCtx, func() (bool, error) {
		if process.Exited() {
			return false, errors.New("TVPN bootstrap stopped")
		}
		var workspace struct {
			Active wireID `json:"activeWorkspaceId"`
		}
		var identity struct {
			ID wireID `json:"identityId"`
		}
		return api.request(waitCtx, "GET", "/api/workspace", nil, &workspace) == nil && string(workspace.Active) == selected.Challenge.WorkspaceID && api.request(waitCtx, "GET", "/api/identity", nil, &identity) == nil && string(identity.ID) == selected.Challenge.IdentityID, nil
	}); err != nil {
		return "", errors.New("TVPN has not selected the GCS Workspace and UxS identity; retry setup")
	}
	if err := writePrivateJSON("/var/lib/somewear/tvpn/uxs-owner.json", rootTVPNOwner{State: i.opts.State, IdentityID: selected.Challenge.IdentityID}); err != nil {
		return "", err
	}
	i.opts.GCS = selected.Origin
	i.emit("waiting-for-gcs")
	if err := waitForClaimTransport(ctx, selected.Origin); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "GCS: %s\n", selected.Peer.Hostname)
	for {
		challenge, err := loadPinChallenge(ctx, selected.Origin, selected.Challenge.IdentityID, key)
		if err != nil {
			return "", err
		}
		if challenge.GCSIdentityID != selected.Challenge.GCSIdentityID || challenge.WorkspaceID != selected.Challenge.WorkspaceID {
			return "", errors.New("GCS setup context changed; retry from the GCS")
		}
		if challenge.Phase == "Expired" {
			return "", errors.New("extend the setup window on the GCS, then rerun setup with the same API key and PIN")
		}
		i.emit("authorizing-pin")
		code, err := authorizePin(ctx, selected.Origin, key, pin, challenge)
		if err != nil {
			var rejection *pinProtocolError
			if errors.As(err, &rejection) && rejection.Code == "PIN_INCORRECT" {
				fmt.Fprintln(os.Stderr, "Incorrect setup PIN. Try again.")
				pin, err = secretPINInput()
				if err != nil {
					return "", err
				}
				continue
			}
			return "", err
		}
		portable, err := encodeTransportClaim(transportClaim{Code: code, Hostname: selected.Peer.Hostname, Backend: challenge.Backend, WorkspaceID: challenge.WorkspaceID, GCSIdentityID: challenge.GCSIdentityID, IdentityID: challenge.IdentityID})
		if err != nil {
			return "", err
		}
		i.pinBootstrapStop = stop
		ready = true
		return portable, nil
	}
}
