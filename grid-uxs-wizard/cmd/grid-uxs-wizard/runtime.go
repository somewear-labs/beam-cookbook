package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type wireID string

func (value *wireID) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*value = ""
		return nil
	}
	text := string(data)
	if strings.HasPrefix(text, "\"") {
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
	}
	if !numericID.MatchString(text) {
		return errors.New("invalid numeric identity")
	}
	*value = wireID(text)
	return nil
}

type runtimeStatus struct {
	Phase       string `json:"phase"`
	Stage       string `json:"stage,omitempty"`
	IdentityID  string `json:"identityId,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}
type setupManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	RequestID     string `json:"requestId"`
	IdentityID    wireID `json:"identityId"`
	GCSIdentityID string `json:"gcsIdentityId"`
	WorkspaceID   string `json:"workspaceId"`
	Name          string `json:"name"`
	Profile       string `json:"profile"`
	Backend       string `json:"backend"`
	BeamSHA256    string `json:"beamSha256"`
}
type runtimeJoin struct {
	Phase       string        `json:"phase"`
	OperationID string        `json:"operationId"`
	Manifest    setupManifest `json:"manifest"`
}
type runtimeReceipt struct {
	runtimeJoin
	ReadyAt                int64  `json:"readyAt"`
	RequestID              string `json:"requestId"`
	PackageStreamOwner     string `json:"packageStreamOwner"`
	PackageStreamTransport string `json:"packageStreamTransport"`
}

type runtimeSupervisor struct {
	config     runtimeConfig
	credential claimBootstrap
	pairing    clientPairing
	beam, tvpn *localAPI
	start      processStarter
	children   []managedProcess
	phase      string
	poll       time.Duration
	canonical  string
	checkPorts func(context.Context) error
	onReady    func()
}

func executeRuntime(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	var state string
	flags.StringVar(&state, "state", "", "Private managed UxS state directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || state == "" {
		return errors.New("runtime requires --state")
	}
	if runtime.GOOS != "linux" {
		return errors.New("UxS runtime requires Linux")
	}
	if err := requireElevated(); err != nil {
		return err
	}
	if err := validateOwnedState(state); err != nil {
		return err
	}
	var config runtimeConfig
	if err := readPrivateJSON(filepath.Join(state, "runtime.json"), &config); err != nil {
		return err
	}
	if config.State != state {
		return errors.New("runtime state path differs from installed configuration")
	}
	if err := config.validate(); err != nil {
		return err
	}
	if config.BeamFormat == "jar" {
		if err := requireJava21(); err != nil {
			return err
		}
	}
	credential, pairing, err := readClaimMaterial(state)
	if err != nil {
		return err
	}
	unlock, err := lockRuntime(state)
	if err != nil {
		return err
	}
	defer unlock()
	ctx, stop := setupContext()
	defer stop()
	beam, _ := newLocalAPI("http://127.0.0.1:9091")
	tvpn, _ := newLocalAPI("http://127.0.0.1:9094")
	supervisor := runtimeSupervisor{config: config, credential: credential, pairing: pairing, beam: beam, tvpn: tvpn, start: startRuntimeProcess, poll: 2 * time.Second, canonical: "/var/lib/somewear/tvpn"}
	supervisor.checkPorts = supervisor.rejectForeignAPIs
	return supervisor.run(ctx)
}

func (s *runtimeSupervisor) setPhase(phase string) error {
	s.phase = phase
	return writePrivateJSON(filepath.Join(s.config.State, "status.json"), runtimeStatus{Phase: phase, IdentityID: s.credential.IdentityID, WorkspaceID: s.credential.WorkspaceID})
}
func (s *runtimeSupervisor) alive() error {
	for _, child := range s.children {
		if child.Exited() {
			return errors.New("a managed process stopped; inspect private Beam and TVPN logs")
		}
	}
	return nil
}
func (s *runtimeSupervisor) wait(ctx context.Context, limit time.Duration, condition func(context.Context) bool) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		if err := s.alive(); err != nil {
			return err
		}
		if condition(ctx) {
			return nil
		}
		timer := time.NewTimer(s.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("timed out or cancelled during %s: %w", s.phase, ctx.Err())
		case <-timer.C:
		}
	}
}
func (s *runtimeSupervisor) rejectForeignAPIs(ctx context.Context) error {
	for api, path := range map[*localAPI]string{s.tvpn: "/api/health", s.beam: "/api/auth/state"} {
		req, err := http.NewRequestWithContext(ctx, "GET", api.base+path, nil)
		if err != nil {
			return err
		}
		response, err := api.client.Do(req)
		if err == nil {
			response.Body.Close()
			return errors.New("local Beam or TVPN API is already running; refusing to adopt an unrelated service")
		}
	}
	return nil
}

func (s *runtimeSupervisor) prepareFiles() error {
	c := s.config
	credential := s.credential
	if c.BeamSHA256 != credential.BeamSHA256 {
		return errors.New("runtime Beam does not match the claimed build")
	}
	for _, dir := range []string{"beam", "tvpn"} {
		path := filepath.Join(c.State, dir)
		if err := checkInstallParents(path); err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	if err := checkInstallParents(filepath.Dir(s.canonical)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.canonical), 0700); err != nil {
		return err
	}
	if err := validateTVPNState(c.State, s.canonical); err != nil {
		return err
	}
	if _, err := os.Lstat(s.canonical); errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(filepath.Join(c.State, "tvpn"), s.canonical); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	tvpnPath := filepath.Join(c.State, "tvpn", "config.toml")
	content := ""
	if data, err := os.ReadFile(tvpnPath); err == nil {
		content = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	encodedKey, _ := json.Marshal(credential.APIKey)
	encodedHost, _ := json.Marshal(credential.Backend)
	content = mergeSection(content, "api", map[string]string{"key": string(encodedKey), "host": string(encodedHost)})
	if !strings.Contains(content, "[package-stream]") {
		content = mergeSection(content, "package-stream", map[string]string{"owner": "\"tvpn\""})
	}
	if err := atomicPrivateWrite(tvpnPath, []byte(content)); err != nil {
		return err
	}
	properties := fmt.Sprintf("host=%s\nport=443\napi-key=%s\ndisable-horizon=true\ndisable-peer-tunneling=true\nuxs-solution=networked\n", credential.Backend, credential.APIKey)
	extra, err := horizonProperties(c.Bundle)
	if err != nil {
		return err
	}
	properties += extra
	if err := atomicPrivateWrite(filepath.Join(c.State, "beam", "beam.properties"), []byte(properties)); err != nil {
		return err
	}
	return writePrivateJSON(filepath.Join(c.State, "beam", "uxs-pairing.json"), s.pairing)
}

func (s *runtimeSupervisor) run(ctx context.Context) (result error) {
	defer func() {
		for i := len(s.children) - 1; i >= 0; i-- {
			s.children[i].Stop()
		}
		if ctx.Err() != nil {
			result = nil
		}
		if result != nil {
			_ = writePrivateJSON(filepath.Join(s.config.State, "status.json"), runtimeStatus{Phase: "failed", Stage: s.phase, IdentityID: s.credential.IdentityID, WorkspaceID: s.credential.WorkspaceID})
		} else {
			_ = s.setPhase("stopped")
		}
	}()
	if err := validateOwnedState(s.config.State); err != nil {
		return err
	}
	if err := s.config.validate(); err != nil {
		return err
	}
	if err := s.setPhase("starting"); err != nil {
		return err
	}
	if err := s.checkPorts(ctx); err != nil {
		return err
	}
	if err := s.prepareFiles(); err != nil {
		return err
	}
	if err := s.initialize(ctx); err != nil {
		return err
	}
	if s.onReady != nil {
		s.onReady()
	}
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.alive(); err != nil {
				return err
			}
		}
	}
}

func (s *runtimeSupervisor) launch(command string, args []string, env map[string]string, logName string) error {
	child, err := s.start(command, args, env, filepath.Join(s.config.State, logName))
	if err != nil {
		return errors.New("could not launch managed " + logName + " process")
	}
	s.children = append(s.children, child)
	return nil
}

func (s *runtimeSupervisor) initialize(ctx context.Context) error {
	c := s.config
	credential := s.credential
	if err := s.setPhase("starting-tvpn"); err != nil {
		return err
	}
	if err := s.launch(c.TVPN, nil, map[string]string{"CONFIG_PATH": filepath.Join(c.State, "tvpn", "config.toml")}, "tvpn.log"); err != nil {
		return err
	}
	if err := s.wait(ctx, 3*time.Minute, func(ctx context.Context) bool { return s.tvpn.request(ctx, "GET", "/api/health", nil, nil) == nil }); err != nil {
		return err
	}
	var auth struct {
		HasKey bool `json:"hasApiKey"`
	}
	if err := s.tvpn.request(ctx, "GET", "/api/auth/sign-in/status", nil, &auth); err != nil {
		return err
	}
	if !auth.HasKey {
		return errors.New("TVPN did not load the claim credential")
	}
	if err := s.setPhase("selecting-workspace"); err != nil {
		return err
	}
	type workspaceStatus struct {
		Workspaces []struct {
			ID wireID `json:"id"`
		} `json:"workspaces"`
		Active wireID `json:"activeWorkspaceId"`
	}
	if err := s.wait(ctx, 5*time.Minute, func(ctx context.Context) bool {
		var result workspaceStatus
		if s.tvpn.request(ctx, "GET", "/api/workspace", nil, &result) != nil {
			return false
		}
		for _, item := range result.Workspaces {
			if string(item.ID) == credential.WorkspaceID {
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	workspace := map[string]string{"workspaceId": credential.WorkspaceID}
	if err := s.tvpn.request(ctx, "PUT", "/api/workspace/activate", workspace, nil); err != nil {
		return err
	}
	if err := s.wait(ctx, 3*time.Minute, func(ctx context.Context) bool {
		var result workspaceStatus
		return s.tvpn.request(ctx, "GET", "/api/workspace", nil, &result) == nil && string(result.Active) == credential.WorkspaceID
	}); err != nil {
		return err
	}
	if err := s.waitIdentity(ctx); err != nil {
		return err
	}
	if err := s.setPhase("starting-beam"); err != nil {
		return err
	}
	command, args, env := s.beamCommand([]string{"--non-interactive", "--disable-peer-tunneling", "daemon", "start"})
	if err := s.launch(command, args, env, "beam.log"); err != nil {
		return err
	}
	if err := s.wait(ctx, 3*time.Minute, func(ctx context.Context) bool {
		var result struct {
			State string `json:"state"`
		}
		return s.beam.request(ctx, "GET", "/api/auth/state", nil, &result) == nil && result.State == "SignedIn"
	}); err != nil {
		return err
	}
	if err := s.beam.request(ctx, "POST", "/api/workspace/activate", workspace, nil); err != nil {
		return err
	}
	if err := s.wait(ctx, 3*time.Minute, func(ctx context.Context) bool {
		var result struct {
			Active wireID `json:"activeWorkspaceId"`
		}
		return s.beam.request(ctx, "GET", "/api/workspaces", nil, &result) == nil && string(result.Active) == credential.WorkspaceID
	}); err != nil {
		return err
	}
	if err := s.setPhase("delegating-package-stream"); err != nil {
		return err
	}
	if err := delegatePackageStream(ctx, s.tvpn); err != nil {
		return err
	}
	if err := s.wait(ctx, 3*time.Minute, func(ctx context.Context) bool { return s.tvpn.request(ctx, "GET", "/api/health", nil, nil) == nil }); err != nil {
		return err
	}
	if err := s.waitIdentity(ctx); err != nil {
		return err
	}
	if err := s.setPhase("joining"); err != nil {
		return err
	}
	var started runtimeJoin
	if err := s.beam.request(ctx, "POST", "/api/uxs/provisioning/join", map[string]string{"gcsIdentityId": credential.GCSIdentityID, "name": credential.Name, "requestId": c.RequestID}, &started); err != nil {
		return err
	}
	if started.OperationID == "" {
		return errors.New("Beam join did not return an operation ID")
	}
	var receipt runtimeJoin
	if err := s.wait(ctx, 11*time.Minute, func(ctx context.Context) bool {
		return s.beam.request(ctx, "GET", "/api/uxs/provisioning/join/status", nil, &receipt) == nil && (receipt.Phase == "Ready" || receipt.Phase == "Failed")
	}); err != nil {
		return err
	}
	if receipt.OperationID != started.OperationID || receipt.Phase != "Ready" {
		return errors.New("Beam pairing failed or belongs to a different operation")
	}
	if err := s.validateManifest(receipt.Manifest); err != nil {
		return err
	}
	if err := s.provisionHorizon(ctx); err != nil {
		return err
	}
	if err := writePrivateJSON(filepath.Join(c.State, "receipt.json"), runtimeReceipt{runtimeJoin: receipt, ReadyAt: time.Now().Unix(), RequestID: c.RequestID, PackageStreamOwner: "beam", PackageStreamTransport: "sse"}); err != nil {
		return err
	}
	return s.setPhase("ready")
}

func (s *runtimeSupervisor) waitIdentity(ctx context.Context) error {
	var result struct {
		ID wireID `json:"identityId"`
	}
	if err := s.wait(ctx, 3*time.Minute, func(ctx context.Context) bool {
		return s.tvpn.request(ctx, "GET", "/api/identity", nil, &result) == nil && result.ID != ""
	}); err != nil {
		return err
	}
	if string(result.ID) != s.credential.IdentityID || string(result.ID) == s.credential.GCSIdentityID {
		return errors.New("TVPN identity differs from the claimed UxS")
	}
	return nil
}
func (s *runtimeSupervisor) validateManifest(m setupManifest) error {
	c := s.credential
	if m.SchemaVersion != 1 || m.RequestID != s.config.RequestID || string(m.IdentityID) != c.IdentityID || m.GCSIdentityID != c.GCSIdentityID || m.WorkspaceID != c.WorkspaceID || m.Name != c.Name || m.Backend != c.Backend || m.BeamSHA256 != c.BeamSHA256 {
		return errors.New("pairing receipt does not match the requested UxS installation")
	}
	return nil
}
func (s *runtimeSupervisor) beamCommand(args []string) (string, []string, map[string]string) {
	env := map[string]string{"BEAM_IS_JAR": "false", "BEAM_IS_DAEMON": "true"}
	for _, key := range []string{"BEAM_STATE_DIR", "BEAM_CONFIG_DIR", "BEAM_USER_STATE_DIR", "BEAM_USER_CONFIG_DIR"} {
		env[key] = filepath.Join(s.config.State, "beam")
	}
	if s.config.BeamFormat == "jar" {
		env["BEAM_IS_JAR"] = "true"
		return "java", append([]string{"-XX:TieredStopAtLevel=1", "-jar", s.config.Beam}, args...), env
	}
	return s.config.Beam, append([]string{"--daemon-path", s.config.Beam}, args...), env
}

func delegatePackageStream(ctx context.Context, tvpn *localAPI) error {
	var current struct {
		Document *struct {
			Revision uint64 `json:"revision"`
			Schema   int    `json:"schemaVersion"`
			Format   string `json:"contentFormat"`
			Content  string `json:"content"`
		} `json:"document"`
	}
	if err := tvpn.request(ctx, "GET", "/api/config/daemon/document", nil, &current); err != nil {
		return err
	}
	if current.Document == nil || current.Document.Format != "toml" {
		return errors.New("TVPN did not return a TOML configuration document")
	}
	document := current.Document
	content := mergeSection(document.Content, "package-stream", map[string]string{"owner": "\"beam\""})
	if content == document.Content {
		return nil
	}
	var replacement struct {
		Revision uint64 `json:"revision"`
	}
	if err := tvpn.request(ctx, "PUT", "/api/config/daemon", map[string]any{"baseRevision": document.Revision, "schemaVersion": document.Schema, "contentFormat": document.Format, "content": content}, &replacement); err != nil {
		return err
	}
	return tvpn.request(ctx, "POST", "/api/config/daemon/activate", map[string]any{"baseRevision": replacement.Revision}, nil)
}
