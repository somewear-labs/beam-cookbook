package main

import (
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type options struct {
	Beam, BeamHome, Bundle, State, GCS, CodeFile string
	DryRun                                       bool
	Wait                                         time.Duration
}
type installPlan struct {
	SchemaVersion int    `json:"schemaVersion"`
	UnitName      string `json:"unitName"`
}
type event struct {
	Time      string `json:"time"`
	Actor     string `json:"actor"`
	Phase     string `json:"phase"`
	ElapsedMS int64  `json:"elapsedMs"`
}
type installer struct {
	opts options
	run  func(context.Context, string, ...string) error
	emit func(string)
}

const defaultUxsState = "/var/lib/somewear/uxs/mission-computer"

var safePath = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
var unitName = regexp.MustCompile(`^somewear-uxs-[0-9a-f-]{36}\.service$`)

func main() {
	if err := execute(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "grid-uxs-wizard:", err)
		os.Exit(1)
	}
}
func execute(args []string) error {
	args = presentationArgs(args)
	if len(args) == 0 {
		action, err := promptTask()
		if err != nil {
			return err
		}
		return execute([]string{action})
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(os.Stdout, wizardUsage)
		return flag.ErrHelp
	}
	if len(args) > 0 && args[0] == "run" {
		return executeRuntime(args[1:])
	}
	if len(args) > 0 && args[0] == "resume-claim" {
		return executeResumeClaim(args[1:])
	}
	if len(args) > 0 && args[0] == "prepare-claim" {
		return executePrepareClaim(args[1:])
	}
	if len(args) > 0 && args[0] == "setup" {
		provided := false
		for _, arg := range args[1:] {
			if arg == "--help" || arg == "-h" {
				fmt.Fprint(os.Stdout, wizardUsage)
				return flag.ErrHelp
			}
			if arg == "--role" || strings.HasPrefix(arg, "--role=") {
				provided = true
			}
		}
		if !provided {
			role, err := promptRole()
			if err != nil {
				return err
			}
			args = append(args, "--role", role)
		}
		role, remaining, err := setupRole(args[1:])
		if err != nil {
			return err
		}
		return execute(append([]string{role}, remaining...))
	}
	if len(args) > 0 && args[0] == "gcs" {
		return executeGCS(args[1:])
	}
	if len(args) > 0 && args[0] == "sitl" {
		return executeSITL(args[1:])
	}
	if len(args) == 0 || args[0] != "uxs" {
		return errors.New("unknown command; run grid-uxs-wizard --help")
	}
	flags := flag.NewFlagSet("uxs", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), uxsUsage)
		flags.PrintDefaults()
	}
	var o options
	flags.StringVar(&o.Beam, "beam", "", "Existing Beam binary/JAR; omitted when bundle contains install.json")
	flags.StringVar(&o.BeamHome, "beam-home", "", "Installed native Beam package directory")
	flags.StringVar(&o.Bundle, "bundle", "", "Installation bundle (defaults to bundle beside the wizard)")
	flags.StringVar(&o.State, "state", "", "Private UxS state directory (chosen automatically)")
	flags.StringVar(&o.GCS, "gcs", "", "GCS HTTPS origin")
	flags.StringVar(&o.CodeFile, "code-file", "", "Private claim file; omit to enter the code at the terminal")
	flags.BoolVar(&o.DryRun, "dry-run", false, "Check prerequisites without redeeming the claim or changing services")
	flags.DurationVar(&o.Wait, "wait-timeout", 20*time.Minute, "Maximum wait for paired setup readiness")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if o.Wait <= 0 {
		return errors.New("provide a positive wait-timeout")
	}
	if runtime.GOOS != "linux" {
		return errors.New("UxS installation targets Linux; run this binary on the mission computer")
	}
	if os.Geteuid() != 0 {
		return errors.New("run the installer as root for TVPN networking and system services")
	}
	if o.GCS == "" {
		var err error
		o.GCS, err = promptText("GCS address", "Enter the GCS LAN HTTPS address, not the file server address.", validateGCSAddress)
		if err != nil {
			return err
		}
	}
	if err := validateGCSAddress(o.GCS); err != nil {
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
		if _, err := os.Stat(filepath.Join(o.Bundle, "install.json")); err != nil {
			return fmt.Errorf("installation bundle missing beside the wizard; extract the complete download or provide --bundle: %w", err)
		}
	}
	if o.State == "" {
		var err error
		o.State, err = selectUxsState("/var/lib/somewear/tvpn", filepath.Dir(defaultUxsState))
		if err != nil {
			return err
		}
	}
	if !o.DryRun {
		if !safePath.MatchString(o.State) || filepath.Clean(o.State) != o.State {
			return errors.New("use a clean absolute UxS state path")
		}
		unlock, err := lockUxS(filepath.Dir(o.State))
		if err != nil {
			return err
		}
		defer unlock()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	i := installer{opts: o, run: runCommand, emit: progressOutput()}
	if !o.DryRun && o.State != "" {
		if err := validateTVPNState(o.State, "/var/lib/somewear/tvpn"); err != nil {
			return err
		}
	}
	enteredCode := ""
	if !o.DryRun {
		claimBundle := o.Bundle
		if _, err := os.Stat(filepath.Join(o.Bundle, "install.json")); err == nil {
			claimBundle = filepath.Clean(o.State) + "-bundle"
		}
		selection, err := selectClaim(filepath.Join(claimBundle, ".claim-code"), o.CodeFile, secretClaimInput)
		if err != nil {
			return err
		}
		if selection.Replace {
			choice, err := promptChoice("A new claim will replace this computer's existing UxS installation", []menuOption{{"Keep the existing installation and stop", "stop"}, {"Back up the existing installation and use the new claim", "replace"}})
			if err != nil {
				return err
			}
			if choice != "replace" {
				return errNewClaimRequiresFreshState
			}
			if _, err := os.Stat(filepath.Join(o.Bundle, "install.json")); err != nil {
				return errors.New("automatic replacement requires the downloaded installation bundle")
			}
			verify := i
			verify.opts.DryRun = true
			if err := verify.installTargetPackages(ctx); err != nil {
				return err
			}
			backup, err := archiveManagedInstallation(ctx, o.State, claimBundle, "/var/lib/somewear/tvpn", "/etc/systemd/system", "/var/lib/somewear/uxs-backups", runCommand)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "Previous installation backed up:", backup)
		}
		i.opts.CodeFile = selection.Path
		enteredCode = selection.Code
	}

	if _, err := os.Stat(filepath.Join(o.Bundle, "install.json")); err == nil {
		if err := i.installTargetPackages(ctx); err != nil {
			return err
		}
		if o.DryRun {
			return nil
		}
	}
	if !o.DryRun && i.opts.CodeFile == "" {
		path, err := saveClaim(i.opts.Bundle, enteredCode)
		if err != nil {
			return err
		}
		i.opts.CodeFile = path
	}
	return i.install(ctx)
}
func (i *installer) preflight() error {
	o := i.opts
	if o.GCS == "" || o.CodeFile == "" || o.Beam == "" || o.Bundle == "" || o.State == "" || o.Wait <= 0 {
		return errors.New("beam, bundle, state, gcs, code-file and a positive wait-timeout are required")
	}
	for _, p := range []string{o.Beam, o.Bundle, o.State} {
		if !safePath.MatchString(p) || filepath.Clean(p) != p {
			return fmt.Errorf("use a clean absolute path: %s", p)
		}
	}
	if o.State == o.Bundle {
		return errors.New("bundle and state must be separate directories")
	}
	if err := checkCodeFile(o.CodeFile); err != nil {
		return err
	}
	for _, tool := range []string{"systemctl", "id"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("missing prerequisite: %s", tool)
		}
	}
	if strings.HasSuffix(o.Beam, ".jar") {
		if err := requireJava21(); err != nil {
			return err
		}
	} else if err := checkArchitecture(o.Beam, runtime.GOARCH); err != nil {
		return err
	}
	if err := checkArchitecture(filepath.Join(o.Bundle, "tvpnd"), runtime.GOARCH); err != nil {
		return err
	}
	return validateTVPNState(o.State, "/var/lib/somewear/tvpn")
}
func checkCodeFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("claim file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 512 || info.Size() == 0 {
		return errors.New("claim file must be a private regular file (mode 0600), at most 512 bytes")
	}
	return nil
}
func checkArchitecture(path, arch string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s must be a Linux ELF executable: %w", path, err)
	}
	defer f.Close()
	expected := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}
	machine, ok := expected[arch]
	if !ok {
		return fmt.Errorf("unsupported Linux architecture: %s", arch)
	}
	if f.Machine != machine {
		return fmt.Errorf("%s architecture %s does not match %s", path, f.Machine, arch)
	}
	return nil
}
func (i *installer) claim(ctx context.Context, mode string) error {
	o := i.opts
	args := []string{"--non-interactive", "uxs", "claim", "--gcs", o.GCS, "--code-file", o.CodeFile, "--bundle", o.Bundle, "--state", o.State, mode}
	if o.BeamHome != "" {
		args = append(args, "--beam-home", o.BeamHome)
	}
	if strings.HasSuffix(o.Beam, ".jar") {
		return i.run(ctx, "java", append([]string{"-jar", o.Beam}, args...)...)
	}
	return i.run(ctx, o.Beam, args...)
}
func (i *installer) install(ctx context.Context) error {
	i.emit("preflight")
	if err := i.preflight(); err != nil {
		return err
	}
	for _, unit := range []string{"somewear-beam.service", "somewear-beam-root.service", "somewear-tvpnd.service", "tvpnd.service"} {
		if err := i.run(ctx, "systemctl", "cat", unit); err == nil {
			return fmt.Errorf("existing service %s requires explicit migration", unit)
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if i.opts.DryRun {
		i.emit("preflight-complete")
		return nil
	}
	acknowledged, err := claimAcknowledged(i.opts)
	if err != nil {
		return err
	}
	if !acknowledged {
		i.emit("preparing-claim")
		if err := i.claim(ctx, "--prepare-only"); err != nil {
			return fmt.Errorf("claim preparation failed; preserve the original code and state to retry: %w", err)
		}
	}
	plan, err := prepareRuntime(i.opts)
	if err != nil {
		return err
	}
	body := serviceUnit(i.opts.Bundle, i.opts.State)
	path := filepath.Join("/etc/systemd/system", plan.UnitName)
	i.emit("installing-service")
	if err := i.writeRuntimeUnit(ctx, path, body, plan.UnitName); err != nil {
		return err
	}
	if err := i.run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	started := time.Now()
	if i.run(ctx, "systemctl", "is-active", "--quiet", plan.UnitName) == nil {
		started = time.Time{}
	}
	if err := i.run(ctx, "systemctl", "enable", "--now", plan.UnitName); err != nil {
		return fmt.Errorf("service start failed; preserve state and inspect journalctl -u %s", plan.UnitName)
	}
	if !acknowledged {
		i.emit("acknowledging-claim")
		if err := i.claim(ctx, "--ack-only"); err != nil {
			return fmt.Errorf("service installed but claim acknowledgement failed; retry with the original code and state: %w", err)
		}
		if err := saveClaimAcknowledgement(i.opts); err != nil {
			return err
		}
	}
	i.emit("waiting-for-paired-setup")
	return i.waitReady(ctx, plan.UnitName, started)
}
func readPlan(path string) (installPlan, error) {
	var p installPlan
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	if err := json.NewDecoder(io.LimitReader(f, 4096)).Decode(&p); err != nil {
		return p, err
	}
	if p.SchemaVersion != 1 || !unitName.MatchString(p.UnitName) {
		return p, errors.New("invalid Beam installation plan")
	}
	return p, nil
}
func serviceUnit(bundle, state string) string {
	return fmt.Sprintf("[Unit]\nDescription=Somewear UxS mission computer\nWants=network-online.target\nAfter=network-online.target\n[Service]\nType=simple\nExecStart=%s/bin/grid-uxs-wizard run --state %s\nRestart=on-failure\nRestartSec=5\nKillMode=control-group\nUMask=0077\n[Install]\nWantedBy=multi-user.target\n", state, state)
}
func legacyServiceUnit(bundle, state string) string {
	return fmt.Sprintf("[Unit]\nDescription=Somewear UxS mission computer\nWants=network-online.target\nAfter=network-online.target\n[Service]\nType=simple\nExecStart=/bin/sh %s/initialize-linux.sh %s %s\nRestart=on-failure\nRestartSec=5\nKillMode=control-group\nUMask=0077\n[Install]\nWantedBy=multi-user.target\n", state, bundle, state)
}
func writeUnit(path, body string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("service unit must be a regular file")
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(existing) != body {
			return errors.New("existing service differs from this installation; refusing overwrite")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".uxs-unit-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), path)
}
func (i *installer) waitReady(ctx context.Context, unit string, started time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, i.opts.Wait)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	previous := ""
	for {
		path := filepath.Join(i.opts.State, "status.json")
		info, err := os.Stat(path)
		if err == nil && !info.ModTime().Before(started) {
			var status struct {
				Phase string `json:"phase"`
			}
			f, err := os.Open(path)
			if err == nil {
				err = json.NewDecoder(io.LimitReader(f, 4096)).Decode(&status)
				f.Close()
			}
			if err == nil && status.Phase != "" {
				if status.Phase != previous {
					i.emit("runtime:" + status.Phase)
					previous = status.Phase
				}
				if status.Phase == "ready" {
					i.emit("ready")
					return nil
				}
				if status.Phase == "failed" {
					return fmt.Errorf("runtime setup failed; inspect private logs in %s", i.opts.State)
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("setup wait ended; service remains installed, inspect journalctl -u %s: %w", unit, ctx.Err())
		case <-ticker.C:
		}
	}
}

type commandError struct {
	program string
	cause   error
	logPath string
}

func (e *commandError) Error() string {
	message := fmt.Sprintf("%s failed: %v", e.program, e.cause)
	if e.logPath != "" {
		message += "; private diagnostic log: " + e.logPath
	}
	return message
}
func (e *commandError) Unwrap() error { return e.cause }

type commandOutput struct {
	mutex sync.Mutex
	data  []byte
}

func (output *commandOutput) Write(data []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	const limit = 65536
	count := len(data)
	if count >= limit {
		output.data = append(output.data[:0], data[count-limit:]...)
	} else {
		if overflow := len(output.data) + count - limit; overflow > 0 {
			output.data = output.data[overflow:]
		}
		output.data = append(output.data, data...)
	}
	return count, nil
}
func runCommand(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	output := &commandOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if err == nil {
		return nil
	}
	failure := &commandError{program: filepath.Base(name), cause: err}
	directory, logErr := os.MkdirTemp("", "grid-uxs-wizard-command-*")
	if logErr == nil {
		path := filepath.Join(directory, "output.log")
		if os.WriteFile(path, output.data, 0600) == nil {
			failure.logPath = path
		} else {
			_ = os.RemoveAll(directory)
		}
	}
	return failure
}

func validateTVPNState(state, canonical string) error {
	info, err := os.Lstat(canonical)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return errors.New("existing TVPN state requires explicit migration before UxS setup; the claim has not been redeemed")
	}
	target, err := os.Readlink(canonical)
	if err != nil {
		return err
	}
	if target != filepath.Join(state, "tvpn") {
		return fmt.Errorf("TVPN belongs to another UxS installation (%s); stop and archive that installation before setup. The claim has not been redeemed", target)
	}
	return nil
}

func setupRole(args []string) (string, []string, error) {
	role := ""
	remaining := []string{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--role" || strings.HasPrefix(argument, "--role=") {
			if role != "" {
				return "", nil, errors.New("specify role once")
			}
			if argument == "--role" {
				index++
				if index == len(args) {
					return "", nil, errors.New("role requires gcs or uxs")
				}
				role = args[index]
			} else {
				role = strings.TrimPrefix(argument, "--role=")
			}
		} else {
			remaining = append(remaining, argument)
		}
	}
	if role != "gcs" && role != "uxs" {
		return "", nil, errors.New("setup requires --role gcs or --role uxs")
	}
	return role, remaining, nil
}

func saveClaim(bundle, code string) (string, error) {
	if err := checkInstallParents(bundle); err != nil {
		return "", err
	}
	path := filepath.Join(bundle, ".claim-code")
	if _, err := os.Lstat(path); err == nil {
		if err := checkCodeFile(path); err != nil {
			return "", err
		}
		return "", errors.New("a saved claim appeared during setup; rerun and explicitly choose whether to resume")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if !validClaimCode(code) {
		return "", errors.New("invalid claim format; paste the complete code from GCS Beam")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(code + "\n")
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
}

func validClaimCode(value string) bool {
	return regexp.MustCompile(`^uxs1\.[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}\.[A-Za-z0-9_-]{43}\.[a-f0-9]{64}$`).MatchString(value)
}

func selectUxsState(canonical, root string) (string, error) {
	target, err := os.Readlink(canonical)
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Join(root, "mission-computer"), nil
	}
	if err != nil {
		return "", fmt.Errorf("existing TVPN installation needs explicit migration: %w", err)
	}
	state := filepath.Dir(target)
	if !filepath.IsAbs(target) || filepath.Clean(target) != target || filepath.Base(target) != "tvpn" || filepath.Dir(state) != root {
		return "", errors.New("existing TVPN link is outside managed UxS state; provide --state or migrate explicitly")
	}
	if err := checkInstallParents(state); err != nil {
		return "", err
	}
	owner, err := os.ReadFile(filepath.Join(state, "owner"))
	if err != nil || strings.TrimSpace(string(owner)) != "uxs-install-v1" {
		return "", errors.New("existing TVPN link does not identify a managed UxS installation")
	}
	return state, nil
}
