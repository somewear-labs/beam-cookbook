package wizard

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type updateArtifact struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}
type updateRelease struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Build         string                    `json:"build"`
	Beam          updateArtifact            `json:"beam"`
	TVPN          map[string]updateArtifact `json:"tvpn,omitempty"`
}

func updateClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many update redirects")
		}
		if _, err := releaseURL(req.URL.String()); err != nil {
			return err
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("update redirect must retain HTTPS")
		}
		return nil
	}}
}
func resolveUpdateURL(base *url.URL, source string) (string, error) {
	u, err := url.Parse(source)
	if err != nil {
		return "", err
	}
	u = base.ResolveReference(u)
	if base.Scheme == "https" && u.Scheme != "https" {
		return "", errors.New("update must retain HTTPS")
	}
	_, err = releaseURL(u.String())
	return u.String(), err
}
func readUpdateRelease(ctx context.Context, manifest string) (updateRelease, *url.URL, error) {
	var result updateRelease
	base, err := releaseURL(manifest)
	if err != nil {
		return result, nil, err
	}
	client := updateClient()
	defer client.CloseIdleConnections()
	response, err := releaseGet(ctx, client, manifest)
	if err != nil {
		return result, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return result, nil, errors.New("invalid update release size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, nil, errors.New("invalid update release")
	}
	if result.SchemaVersion != 1 || !regexpSHA.MatchString(result.Beam.SHA256) || result.Beam.Source == "" {
		return result, nil, errors.New("invalid Beam update metadata")
	}
	return result, base, nil
}
func downloadUpdate(ctx context.Context, base *url.URL, artifact updateArtifact, cache string) (string, error) {
	if !regexpSHA.MatchString(artifact.SHA256) {
		return "", errors.New("invalid update checksum")
	}
	source, err := resolveUpdateURL(base, artifact.Source)
	if err != nil {
		return "", err
	}
	if err := checkInstallParents(cache); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cache, 0700); err != nil {
		return "", err
	}
	destination := filepath.Join(cache, artifact.SHA256)
	if hash, err := fileDigest(destination); err == nil && hash == artifact.SHA256 {
		return destination, nil
	}
	client := updateClient()
	defer client.CloseIdleConnections()
	response, err := releaseGet(ctx, client, source)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	temp, err := os.CreateTemp(cache, ".update-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	size, copyErr := io.Copy(temp, io.LimitReader(response.Body, 512*1024*1024+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if size > 512*1024*1024 {
		return "", errors.New("update is too large")
	}
	if hash, err := fileDigest(temp.Name()); err != nil || hash != artifact.SHA256 {
		return "", errors.New("update checksum mismatch")
	}
	if err := os.Rename(temp.Name(), destination); err != nil {
		return "", err
	}
	return destination, nil
}
func requireUpdateJar(path string) error {
	jar, err := zip.OpenReader(path)
	if err != nil {
		return errors.New("this updater requires a Beam JAR release")
	}
	defer jar.Close()
	for _, file := range jar.File {
		if file.Name == "beam/app/MainKt.class" {
			return nil
		}
	}
	return errors.New("update is not a Beam JAR")
}
func executeUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	state := flags.String("state", defaultUxsState, "Advanced: managed UxS state directory")
	manifest := flags.String("release-url", "", "Advanced: update release manifest")
	config := flags.String("config", "", "Wizard configuration file")
	server := flags.String("file-server", "", "Override the configured file server")
	yes := flags.Bool("yes", false, "Apply the displayed update without confirmation")
	check := flags.Bool("check", false, "Show available updates without applying")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected update arguments")
	}
	if os.Geteuid() == 0 {
		return errors.New("run updates as the normal Beam user, without sudo")
	}
	if *manifest == "" {
		claim, err := configuredClaimRelease(*config, *server, "")
		if err != nil {
			return err
		}
		base, err := releaseURL(claim)
		if err != nil {
			return err
		}
		*manifest = base.ResolveReference(&url.URL{Path: "update-release.json"}).String()
	}
	ctx, stop := setupContext()
	defer stop()
	release, base, err := readUpdateRelease(ctx, *manifest)
	if err != nil {
		return err
	}
	role, err := readSetupRole(wizardSetupPath())
	if err != nil {
		return err
	}
	var current runtimeConfig
	if role == "uxs" || (role == "" && runtime.GOOS == "linux" && readPrivateJSON(filepath.Join(*state, "runtime.json"), &current) == nil) {
		role = "uxs"
		if runtime.GOOS != "linux" {
			return errors.New("UxS updates require Linux")
		}
		if err := validateOwnedState(*state); err != nil {
			return err
		}
		if err := readPrivateJSON(filepath.Join(*state, "runtime.json"), &current); err != nil {
			return err
		}
	} else {
		role = "gcs"
		if runtime.GOOS != "darwin" {
			return errors.New("GCS software-only updates currently support macOS user services")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	beamPath := ""
	if role == "uxs" {
		beamPath = current.Beam
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		plist := filepath.Join(home, "Library/LaunchAgents/com.somewear.beam.plist")
		wrapper, err := exec.CommandContext(ctx, "plutil", "-extract", "ProgramArguments.0", "raw", "-o", "-", plist).Output()
		if err != nil {
			return errors.New("user-scoped GCS Beam service was not found")
		}
		beamPath = filepath.Join(filepath.Dir(strings.TrimSpace(string(wrapper))), "beam.jar")
	}
	beamHash, _ := fileDigest(beamPath)
	runnerChanged := false
	if role == "uxs" {
		currentRunner, _ := fileDigest(filepath.Join(*state, "bin/grid-uxs-wizard"))
		localRunner, _ := fileDigest(executable)
		runnerChanged = currentRunner != localRunner
	}
	beamChanged := beamHash != release.Beam.SHA256 || runnerChanged
	tvpnArtifact, tvpnIncluded := release.TVPN[runtime.GOOS+"-"+runtime.GOARCH]
	tvpnChanged := false
	if tvpnIncluded {
		if !regexpSHA.MatchString(tvpnArtifact.SHA256) {
			return errors.New("invalid TVPN update checksum")
		}
		target, err := localTVPNTarget(ctx)
		if err != nil {
			return err
		}
		hash, _ := fileDigest(target.Binary)
		tvpnChanged = hash != tvpnArtifact.SHA256
	}
	if !beamChanged && !tvpnChanged {
		fmt.Println("Everything is up to date.")
		return nil
	}
	fmt.Println("Available updates:")
	if beamChanged {
		fmt.Println("  Beam (no sudo)")
	}
	if tvpnChanged {
		fmt.Println("  TVPN (requires sudo)")
	}
	if *check {
		return nil
	}
	if !*yes {
		choice, err := promptChoice("Apply these updates?", []menuOption{{"Keep the current installation", "stop"}, {"Apply updates", "apply"}})
		if err != nil {
			return err
		}
		if choice != "apply" {
			return nil
		}
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cache := filepath.Join(cacheRoot, "grid-uxs-wizard/updates")
	emit := progressOutput()
	emit("downloading-updates")
	beam := beamPath
	if beamChanged && beamHash != release.Beam.SHA256 {
		beam, err = downloadUpdate(ctx, base, release.Beam, cache)
		if err != nil {
			return err
		}
	}
	if beamChanged {
		if err := requireUpdateJar(beam); err != nil {
			return err
		}
	}
	var tvpn string
	if tvpnChanged {
		tvpn, err = downloadUpdate(ctx, base, tvpnArtifact, cache)
		if err != nil {
			return err
		}
	}

	if beamChanged {
		if role == "uxs" {
			if err := applyUxsUpdate(ctx, *state, beam, executable, runCommand); err != nil {
				return err
			}
		} else {
			if err := applyGcsUpdate(ctx, beam, base.ResolveReference(&url.URL{Path: "claim-release.json"}).String(), nil, runCommand); err != nil {
				return err
			}
		}
	}
	if tvpnChanged {
		fmt.Println("Updating TVPN; sudo is required for its daemon.")
		if err := runCommand(ctx, "sudo", "--", executable, "__update-tvpn", "--source", tvpn, "--sha256", tvpnArtifact.SHA256); err != nil {
			return err
		}
	}
	fmt.Println("Update complete. Identity, pairing and configuration preserved.")
	return nil
}
func applyUxsUpdate(ctx context.Context, state, beam, runner string, run func(context.Context, string, ...string) error) error {
	if err := validateOwnedState(state); err != nil {
		return err
	}
	unlock, err := lockUpdate(state)
	if err != nil {
		return err
	}
	defer unlock()
	var config runtimeConfig
	if err := readPrivateJSON(filepath.Join(state, "runtime.json"), &config); err != nil {
		return err
	}
	if !config.TVPNExternal {
		return errors.New("this update requires user-scoped Beam")
	}
	if err := config.validate(); err != nil {
		return err
	}
	if _, _, err := readClaimMaterial(state); err != nil {
		return err
	}
	plan, err := readPlan(filepath.Join(state, "install-plan.json"))
	if err != nil {
		return err
	}
	beamSHA, err := fileDigest(beam)
	if err != nil {
		return err
	}
	runnerSHA, err := fileDigest(runner)
	if err != nil {
		return err
	}
	if err := run(ctx, "systemctl", "--user", "stop", plan.UnitName); err != nil {
		return err
	}
	target := filepath.Join(state, "bin/beam.jar")
	if err := copyVerified(beam, target, beamSHA); err != nil {
		return err
	}
	if err := copyVerified(runner, filepath.Join(state, "bin/grid-uxs-wizard"), runnerSHA); err != nil {
		return err
	}
	config.Beam = target
	config.BeamFormat = "jar"
	config.BeamHome = ""
	config.InstalledBeamSHA256 = beamSHA
	if err := writePrivateJSON(filepath.Join(state, "runtime.json"), config); err != nil {
		return err
	}
	if err := run(ctx, "systemctl", "--user", "start", plan.UnitName); err != nil {
		return fmt.Errorf("Beam updated, but restarting its user service failed: %w", err)
	}
	return nil
}
