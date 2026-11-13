package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var regexpSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

type gcsOptions struct {
	Bundle, Workspace, BeamProperties, AircraftName, UxsArtifact, ClaimOutput string
	DryRun, NoBrowser                                                         bool
	Wait                                                                      time.Duration
}
type localAPIError struct {
	path   string
	status int
}

func (e *localAPIError) Error() string {
	return fmt.Sprintf("local API %s returned HTTP %d", e.path, e.status)
}

type localAPI struct {
	base   string
	client *http.Client
}

func newLocalAPI(base string) (*localAPI, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("expected a loopback HTTP API origin")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("setup APIs must be loopback-only")
	}
	return &localAPI{base: base, client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (api *localAPI) request(ctx context.Context, method, path string, body, response any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, api.base+path, input)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	result, err := api.client.Do(req)
	if err != nil {
		return fmt.Errorf("local API %s unavailable", path)
	}
	defer result.Body.Close()
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return &localAPIError{path, result.StatusCode}
	}
	if response == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(result.Body, 131072)).Decode(response)
}
func waitUntil(ctx context.Context, condition func() (bool, error)) error {
	for {
		ready, err := condition()
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type gcsInstaller struct {
	opts            gcsOptions
	beam, tvpn      *localAPI
	run             func(context.Context, string, ...string) error
	emit            func(string)
	authorize       func(string)
	chooseWorkspace func([]workspace) (string, error)
	claimOutput     io.Writer
}
type workspace struct {
	ID   string `json:"workspaceId"`
	Name string `json:"name"`
}
type workspaceList struct {
	Workspaces []workspace `json:"workspaces"`
	Active     string      `json:"activeWorkspaceId"`
}

func executeGCS(args []string) error {
	flags := flag.NewFlagSet("setup --role gcs", flag.ContinueOnError)
	var o gcsOptions
	flags.StringVar(&o.Bundle, "bundle", "", "Directory with install.json and Beam/TVPN packages, or HTTPS package sources")
	flags.StringVar(&o.Workspace, "workspace", "", "Workspace ID; otherwise retain active or select interactively")
	flags.StringVar(&o.BeamProperties, "beam-properties", "", "Override the active Beam credential file for TVPN synchronization")
	flags.StringVar(&o.AircraftName, "aircraft-name", "", "Optionally prepare a UxS identity and claim")
	flags.StringVar(&o.UxsArtifact, "uxs-beam-artifact", "", "Target Linux Beam artifact to approve for the optional claim")
	flags.StringVar(&o.ClaimOutput, "claim-output", "", "Private output file for the optional claim code")
	flags.BoolVar(&o.DryRun, "dry-run", false, "Verify both software packages without installing or authorizing")
	flags.BoolVar(&o.NoBrowser, "no-browser", false, "Print authorization URLs without opening a browser")
	flags.DurationVar(&o.Wait, "wait-timeout", 20*time.Minute, "Maximum setup and user authorization time after package installation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || o.Wait <= 0 {
		return errors.New("provide a positive wait-timeout")
	}
	if err := requireElevated(); err != nil {
		return err
	}
	if o.Bundle == "" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return err
		}
		o.Bundle = filepath.Join(filepath.Dir(executable), "bundle")
	}
	ctx, stop := setupContext()
	defer stop()
	beam, _ := newLocalAPI("http://127.0.0.1:9091")
	tvpn, _ := newLocalAPI("http://127.0.0.1:9094")
	installer := gcsInstaller{opts: o, beam: beam, tvpn: tvpn, run: runCommand, emit: progressOutput(), authorize: func(link string) {
		fmt.Fprintln(os.Stderr, "Authorize this GCS in your browser:", link)
		if !o.NoBrowser {
			_ = openBrowser(ctx, link)
		}
	}, chooseWorkspace: promptWorkspace}
	return installer.install(ctx)
}
func promptWorkspace(items []workspace) (string, error) {
	choices := make([]menuOption, 0, len(items))
	for _, item := range items {
		choices = append(choices, menuOption{item.Name + " (" + item.ID + ")", item.ID})
	}
	return promptChoice("Choose a Workspace", choices)
}
func (i *gcsInstaller) install(ctx context.Context) error {
	i.emit("checking-packages")
	bundle, err := readSoftwareBundle(i.opts.Bundle)
	if err != nil {
		return err
	}
	cache, err := os.MkdirTemp("", "networked-uxs-download-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)
	beamPackage, err := prepareSoftware(ctx, i.opts.Bundle, cache, bundle.Beam)
	if err != nil {
		return fmt.Errorf("Beam: %w", err)
	}
	tvpnPackage, err := prepareSoftware(ctx, i.opts.Bundle, cache, bundle.TVPN)
	if err != nil {
		return fmt.Errorf("TVPN: %w", err)
	}
	if i.opts.DryRun {
		i.emit("packages-verified")
		return nil
	}
	i.emit("installing-beam")
	beamCommand, err := installSoftware(ctx, beamPackage, i.run)
	if err != nil {
		return err
	}
	i.emit("installing-tvpn")
	tvpnCommand, err := installSoftware(ctx, tvpnPackage, i.run)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, i.opts.Wait)
	defer cancel()
	return i.configure(ctx, beamCommand, tvpnCommand)
}
func (i *gcsInstaller) configure(ctx context.Context, beamCommand, tvpnCommand string) error {
	if strings.HasSuffix(beamCommand, ".jar") {
		return errors.New("GCS bundles require a native Beam command or a system installer with its service wrapper")
	}
	i.emit("starting-beam")
	if err := i.run(ctx, beamCommand, "--daemon-path", beamCommand, "--non-interactive", "up"); err != nil {
		return err
	}
	if err := waitUntil(ctx, func() (bool, error) {
		var state struct {
			State string `json:"state"`
		}
		err := i.beam.request(ctx, "GET", "/api/auth/state", nil, &state)
		return err == nil, nil
	}); err != nil {
		return err
	}
	i.emit("configuring-networked-mode")
	var configuration struct {
		RequiresRestart bool `json:"requiresRestart"`
	}
	if err := i.beam.request(ctx, "POST", "/api/properties", map[string]any{"updates": map[string]string{"uxs-solution": "networked"}, "target": "system"}, &configuration); err != nil {
		return err
	}
	if configuration.RequiresRestart {
		if err := i.run(ctx, beamCommand, "--daemon-path", beamCommand, "--non-interactive", "down"); err != nil {
			return err
		}
		if err := i.run(ctx, beamCommand, "--daemon-path", beamCommand, "--non-interactive", "up"); err != nil {
			return err
		}
		if err := waitUntil(ctx, func() (bool, error) {
			return i.beam.request(ctx, "GET", "/api/auth/state", nil, &struct{}{}) == nil, nil
		}); err != nil {
			return err
		}
	}
	i.emit("authorizing-gcs")
	if err := i.signIn(ctx); err != nil {
		return err
	}
	if err := i.selectWorkspace(ctx); err != nil {
		return err
	}
	i.emit("starting-tvpn")
	if err := i.run(ctx, tvpnCommand, "up"); err != nil {
		return err
	}
	if err := waitUntil(ctx, func() (bool, error) { return i.tvpn.request(ctx, "GET", "/api/health", nil, &struct{}{}) == nil, nil }); err != nil {
		return err
	}
	i.emit("sharing-gcs-identity")
	if err := i.configureTVPN(ctx, tvpnCommand); err != nil {
		return err
	}
	i.emit("selecting-tvpn-workspace")
	if err := waitUntil(ctx, func() (bool, error) {
		var list struct {
			Workspaces []struct {
				ID string `json:"id"`
			} `json:"workspaces"`
		}
		if err := i.tvpn.request(ctx, "GET", "/api/workspace", nil, &list); err != nil {
			return false, nil
		}
		for _, workspace := range list.Workspaces {
			if workspace.ID == i.opts.Workspace {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	if err := i.tvpn.request(ctx, "PUT", "/api/workspace/activate", map[string]string{"workspaceId": i.opts.Workspace}, nil); err != nil {
		return err
	}
	if err := waitUntil(ctx, func() (bool, error) {
		var current struct {
			Active string `json:"activeWorkspaceId"`
		}
		return i.tvpn.request(ctx, "GET", "/api/workspace", nil, &current) == nil && current.Active == i.opts.Workspace, nil
	}); err != nil {
		return err
	}
	if err := waitUntil(ctx, func() (bool, error) {
		var current struct {
			Identity string `json:"identityId"`
		}
		return i.tvpn.request(ctx, "GET", "/api/identity", nil, &current) == nil && current.Identity != "", nil
	}); err != nil {
		return err
	}
	if i.opts.AircraftName != "" {
		if err := i.prepareClaim(ctx); err != nil {
			return err
		}
	}
	i.emit("gcs-ready")
	return nil
}
func (i *gcsInstaller) signIn(ctx context.Context) error {
	var state struct {
		State     string `json:"state"`
		HasAPIKey bool   `json:"hasApiKey"`
	}
	if err := i.beam.request(ctx, "GET", "/api/auth/state", nil, &state); err != nil {
		return err
	}
	if state.State == "SignedIn" {
		return nil
	}
	if state.HasAPIKey {
		return errors.New("GCS already has a credential but is not signed in; resolve its authentication before retrying")
	}
	var pending struct {
		URL string `json:"url"`
	}
	if err := i.beam.request(ctx, "POST", "/api/auth/sign-in/start", map[string]any{}, &pending); err != nil {
		return err
	}
	if err := validateAuthorizationURL(pending.URL); err != nil {
		return err
	}
	i.authorize(pending.URL)
	return waitUntil(ctx, func() (bool, error) {
		var result struct {
			State string `json:"state"`
		}
		if err := i.beam.request(ctx, "GET", "/api/auth/sign-in/status", nil, &result); err != nil {
			return false, err
		}
		switch result.State {
		case "Ready":
			var auth struct {
				State string `json:"state"`
			}
			err := i.beam.request(ctx, "GET", "/api/auth/state", nil, &auth)
			return err == nil && auth.State == "SignedIn", nil
		case "Failed", "Expired":
			return false, errors.New("GCS authorization failed or expired; rerun setup")
		}
		return false, nil
	})
}
func (i *gcsInstaller) selectWorkspace(ctx context.Context) error {
	var list workspaceList
	if err := i.beam.request(ctx, "GET", "/api/workspaces", nil, &list); err != nil {
		return err
	}
	selected := i.opts.Workspace
	if selected == "" {
		selected = list.Active
	}
	if selected == "" && len(list.Workspaces) == 1 {
		selected = list.Workspaces[0].ID
	}
	if selected == "" {
		var err error
		selected, err = i.chooseWorkspace(list.Workspaces)
		if err != nil {
			return err
		}
	}
	for _, workspace := range list.Workspaces {
		if workspace.ID == selected {
			if err := i.beam.request(ctx, "POST", "/api/workspace/activate", map[string]string{"workspaceId": selected}, nil); err != nil {
				return err
			}
			i.opts.Workspace = selected
			return nil
		}
	}
	return errors.New("selected Workspace is not available to this GCS")
}
func (i *gcsInstaller) configureTVPN(ctx context.Context, command string) error {
	dir, err := os.MkdirTemp("", "networked-uxs-credentials-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "tvpn.toml")
	var properties struct {
		UserFile   string `json:"userFile"`
		SystemFile string `json:"systemFile"`
		Properties []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Source string `json:"source"`
		} `json:"properties"`
	}
	if err := i.beam.request(ctx, "GET", "/api/properties", nil, &properties); err != nil {
		return err
	}
	input := i.opts.BeamProperties
	host := "api.somewear.co"
	for _, property := range properties.Properties {
		if property.Key == "host" && property.Value != "" {
			host = property.Value
		}
		if property.Key == "api-key" && input == "" {
			switch property.Source {
			case "user":
				input = properties.UserFile
			case "system":
				input = properties.SystemFile
			}
		}
	}
	if input == "" {
		return errors.New("active Beam credential file is unavailable")
	}
	if err := i.run(ctx, command, "config", "migrate-beam-config", "--input", input, "--output", output); err != nil {
		return errors.New("could not import the system Beam credential into TVPN")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return err
	}
	api := sectionValues(string(data), "api")
	var key string
	if err := json.Unmarshal([]byte(api["key"]), &key); err != nil || !regexp.MustCompile(`^[A-Za-z0-9_:.-]{8,512}$`).MatchString(key) {
		return errors.New("Beam credential is missing or invalid in imported configuration")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9.-]+$`).MatchString(host) {
		return errors.New("unsupported Beam backend hostname")
	}
	encodedHost, _ := json.Marshal(host)
	api["host"] = string(encodedHost)
	var result struct {
		Document *struct {
			Revision      uint64 `json:"revision"`
			SchemaVersion int    `json:"schemaVersion"`
			ContentFormat string `json:"contentFormat"`
			Content       string `json:"content"`
		} `json:"document"`
	}
	if err := i.tvpn.request(ctx, "GET", "/api/config/daemon/document", nil, &result); err != nil {
		return err
	}
	content := ""
	revision := uint64(0)
	schema := 1
	if result.Document != nil {
		if result.Document.ContentFormat != "toml" {
			return errors.New("unsupported TVPN configuration format")
		}
		content = result.Document.Content
		revision = result.Document.Revision
		schema = result.Document.SchemaVersion
	}
	content = mergeSection(content, "api", api)
	content = mergeSection(content, "package-stream", map[string]string{"owner": "\"beam\""})
	var replacement struct {
		Revision uint64 `json:"revision"`
	}
	if err := i.tvpn.request(ctx, "PUT", "/api/config/daemon", map[string]any{"baseRevision": revision, "schemaVersion": schema, "contentFormat": "toml", "content": content}, &replacement); err != nil {
		return err
	}
	if err := i.tvpn.request(ctx, "POST", "/api/config/daemon/activate", map[string]any{"baseRevision": replacement.Revision}, nil); err != nil {
		return err
	}
	return waitUntil(ctx, func() (bool, error) {
		var status struct {
			HasKey bool `json:"hasApiKey"`
		}
		return i.tvpn.request(ctx, "GET", "/api/auth/sign-in/status", nil, &status) == nil && status.HasKey, nil
	})
}
func sectionValues(content, section string) map[string]string {
	values := map[string]string{}
	active := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			active = trimmed == "["+section+"]"
			continue
		}
		if active {
			key, value, ok := strings.Cut(trimmed, "=")
			key = strings.TrimSpace(key)
			if ok && (key == "key" || key == "host") {
				values[key] = strings.TrimSpace(value)
			}
		}
	}
	return values
}
func mergeSection(content, section string, updates map[string]string) string {
	lines := strings.Split(content, "\n")
	start := -1
	end := len(lines)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "["+section+"]" {
			start = index
			continue
		}
		if start >= 0 && strings.HasPrefix(trimmed, "[") {
			end = index
			break
		}
	}
	if start < 0 {
		lines = append(lines, "["+section+"]")
		start = len(lines) - 1
		end = len(lines)
	}
	seen := map[string]bool{}
	for index := start + 1; index < end; index++ {
		key, _, ok := strings.Cut(strings.TrimSpace(lines[index]), "=")
		key = strings.TrimSpace(key)
		if value, replace := updates[key]; ok && replace {
			lines[index] = key + " = " + value
			seen[key] = true
		}
	}
	missing := []string{}
	for _, key := range []string{"host", "key", "owner"} {
		if value, ok := updates[key]; ok && !seen[key] {
			missing = append(missing, key+" = "+value)
		}
	}
	return strings.Join(append(append(append([]string{}, lines[:end]...), missing...), lines[end:]...), "\n") + "\n"
}
func validateAuthorizationURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("invalid authorization URL")
	}
	return nil
}
func openBrowser(ctx context.Context, link string) error {
	switch runtime.GOOS {
	case "darwin":
		return runCommand(ctx, "/usr/bin/open", link)
	case "windows":
		return runCommand(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", link)
	default:
		return runCommand(ctx, "xdg-open", link)
	}
}
func (i *gcsInstaller) prepareClaim(ctx context.Context) error {
	if i.opts.UxsArtifact == "" {
		release, err := configuredClaimRelease("", "", "")
		if err != nil {
			return err
		}
		if release == "" {
			return errors.New("configure fileServer before creating a UxS claim")
		}
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		i.opts.UxsArtifact, err = resolveClaimArtifact(ctx, release, cache)
		if err != nil {
			return err
		}
	}
	i.emit("preparing-aircraft-claim")
	var status claimStatus
	if err := i.beam.request(ctx, "POST", "/api/uxs/provisioning/claim/prepare", map[string]string{"name": i.opts.AircraftName, "beamArtifact": i.opts.UxsArtifact}, &status); err != nil {
		var apiError *localAPIError
		if errors.As(err, &apiError) && apiError.status == 409 {
			var existing claimStatus
			if lookup := i.beam.request(ctx, "GET", "/api/uxs/provisioning/claim/status", nil, &existing); lookup != nil {
				return lookup
			}
			return fmt.Errorf("a claim for %q is already pending; choose Resume a pending UxS claim in the wizard, or run grid-uxs-wizard resume-claim", existing.Name)
		}
		return err
	}
	return i.awaitClaim(ctx, status)
}

func (i *gcsInstaller) resumeClaim(ctx context.Context) error {
	var status claimStatus
	if err := i.beam.request(ctx, "GET", "/api/uxs/provisioning/claim/status", nil, &status); err != nil {
		return err
	}
	if status.Name != "" {
		i.opts.AircraftName = status.Name
	}
	return i.awaitClaim(ctx, status)
}

func (i *gcsInstaller) awaitClaim(ctx context.Context, status claimStatus) error {
	if status.URL != "" {
		if err := validateAuthorizationURL(status.URL); err != nil {
			return err
		}
		i.authorize(status.URL)
	}
	if err := waitUntil(ctx, func() (bool, error) {
		switch status.Phase {
		case "Available":
			if status.Code == "" {
				return false, errors.New("pending claim has no code available; check its status on the GCS")
			}
			return true, nil
		case "Failed", "Expired", "Cancelled":
			return false, errors.New("the pending claim is no longer usable; create a new claim")
		case "Claimed":
			return false, errors.New("this claim was already completed; resume the installation on the mission computer")
		case "Delivering":
			return false, errors.New("this claim was redeemed; resume on the original mission computer using its saved claim")
		case "", "Idle":
			return false, errors.New("there is no pending claim; create a UxS claim first")
		}
		var next claimStatus
		if err := i.beam.request(ctx, "GET", "/api/uxs/provisioning/claim/status", nil, &next); err != nil {
			return false, err
		}
		status = next
		return status.Phase == "Available" && status.Code != "", nil
	}); err != nil {
		return err
	}
	if i.opts.ClaimOutput != "" {
		f, err := os.OpenFile(i.opts.ClaimOutput, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(status.Code + "\n")
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else {
		out := i.claimOutput
		if out == nil {
			out = os.Stdout
		}
		fmt.Fprintf(out, "\nClaim ready for %s.\n\nCopy this entire claim code:\n\n%s\n\nOn the Linux mission computer, after extracting the complete UxS download, run:\n\n  sudo ./grid-uxs-wizard setup --role uxs\n\nInstallation files and state are selected automatically.\nWhen prompted, enter the GCS HTTPS address reachable from the mission computer, not the file-server address.\nPaste the code at the claim-code prompt. Treat it as a password; it is single-use.\n", i.opts.AircraftName, status.Code)
	}
	i.emit("aircraft-claim-ready")
	return nil
}
