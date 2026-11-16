package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const sitlUnitName = "somewear-sitl.service"

type sitlConfig struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Version        string `json:"version"`
	SystemID       int    `json:"systemId"`
	Home           string `json:"home"`
	Endpoint       string `json:"mavlinkEndpoint"`
	BinarySHA256   string `json:"binarySha256"`
	DefaultsSHA256 string `json:"defaultsSha256"`
	LicenseSHA256  string `json:"licenseSha256"`
	SourceURL      string `json:"sourceUrl"`
}

type sitlPackage struct {
	Version   string         `json:"version"`
	Binary    updateArtifact `json:"binary"`
	Defaults  updateArtifact `json:"defaults"`
	License   updateArtifact `json:"license"`
	SourceURL string         `json:"sourceUrl"`
}

type sitlRelease struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Packages      map[string]sitlPackage `json:"packages"`
}

func sitlRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local/share/grid-uxs-wizard/sitl"), nil
}

func (c sitlConfig) validate() error {
	if c.Version == "" || strings.ContainsAny(c.Version, "\r\n") || c.SchemaVersion != 1 || c.SystemID < 1 || c.SystemID > 255 {
		return errors.New("SITL requires schemaVersion 1 and a system ID from 1 to 255")
	}
	host, port, err := net.SplitHostPort(c.Endpoint)
	if err != nil || net.ParseIP(host).To4() == nil {
		return errors.New("MAVLink endpoint must be numeric IPv4:PORT")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("MAVLink port must be from 1 to 65535")
	}
	parts := strings.Split(c.Home, ",")
	if len(parts) != 4 {
		return errors.New("home must be LAT,LON,ALT,YAW")
	}
	values := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("home values must be finite numbers")
		}
		values[i] = v
	}
	if math.Abs(values[0]) > 90 || math.Abs(values[1]) > 180 || values[3] < 0 || values[3] >= 360 {
		return errors.New("home latitude, longitude or yaw is out of range")
	}
	for _, hash := range []string{c.BinarySHA256, c.DefaultsSHA256, c.LicenseSHA256} {
		if !regexpSHA.MatchString(hash) {
			return errors.New("SITL artifact checksum is invalid")
		}
	}
	source, err := url.Parse(c.SourceURL)
	if err != nil || source.Scheme != "https" || source.Host == "" || source.User != nil {
		return errors.New("SITL source URL must use HTTPS")
	}
	return nil
}

func sitlUnit(root string, c sitlConfig) string {
	args := []string{filepath.Join(root, "bin/arducopter"), "--model", "+", "--speedup", "1", "--sysid", strconv.Itoa(c.SystemID), "--home", c.Home, "--defaults", filepath.Join(root, "bin/copter.parm"), "--serial0", "udpclient:" + c.Endpoint, "--serial1", "none", "--serial2", "none"}
	for i, arg := range args {
		args[i] = strconv.Quote(strings.ReplaceAll(arg, "%", "%%"))
	}
	return "[Unit]\nDescription=Somewear ArduCopter SITL\n[Service]\nType=simple\nWorkingDirectory=" + strings.ReplaceAll(filepath.Join(root, "flight-state"), "%", "%%") + "\nExecStart=" + strings.Join(args, " ") + "\nRestart=on-failure\nRestartSec=2\nUMask=0077\nStandardOutput=append:" + strings.ReplaceAll(filepath.Join(root, "sitl.log"), "%", "%%") + "\nStandardError=inherit\n[Install]\nWantedBy=default.target\n"
}

func executeSITL(args []string) error {
	if runtime.GOOS != "linux" {
		return errors.New("SITL runs on the Linux UxS mission computer")
	}
	if os.Geteuid() == 0 {
		return errors.New("run SITL commands as the normal user, without sudo")
	}
	root, err := sitlRoot()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		choices := []menuOption{{"Install simulator", "install"}, {"Status", "status"}, {"Start", "start"}, {"Stop", "stop"}, {"Restart", "restart"}, {"View recent logs", "logs"}}
		selected, err := promptChoice("ArduCopter SITL", choices)
		if err != nil {
			return err
		}
		args = []string{selected}
	}
	if args[0] == "install" {
		return installSITL(args[1:], root)
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Println("SITL commands: install, status, start, stop, restart, logs. Run sitl install --help for options.")
		return flag.ErrHelp
	}
	if len(args) != 1 {
		return errors.New("SITL management commands take no arguments")
	}
	c, err := readSITLConfig(root)
	if errors.Is(err, os.ErrNotExist) && args[0] == "status" {
		fmt.Println("SITL is not installed. Run grid-uxs-wizard sitl install.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("SITL is not configured; run grid-uxs-wizard sitl install: %w", err)
	}
	if args[0] == "logs" {
		f, err := os.Open(filepath.Join(root, "sitl.log"))
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Size() > 16384 {
			_, err = f.Seek(-16384, io.SeekEnd)
			if err != nil {
				return err
			}
		}
		_, err = io.Copy(os.Stdout, f)
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unit := filepath.Join(home, ".config/systemd/user", sitlUnitName)
	if err := verifySITLUnit(unit, root, c); err != nil {
		return err
	}
	ctx, stop := setupContext()
	defer stop()
	switch args[0] {
	case "status":
		output, err := exec.CommandContext(ctx, "systemctl", "--user", "show", sitlUnitName, "--property=ActiveState", "--property=SubState", "--property=ExecMainStatus").CombinedOutput()
		if err != nil {
			return fmt.Errorf("could not inspect SITL service: %w", err)
		}
		fmt.Printf("ArduCopter %s, system ID %d, MAVLink %s\n%s", c.Version, c.SystemID, c.Endpoint, output)
		return nil
	case "start", "restart":
		for path, hash := range map[string]string{"bin/arducopter": c.BinarySHA256, "bin/copter.parm": c.DefaultsSHA256} {
			actual, err := fileDigest(filepath.Join(root, path))
			if err != nil || actual != hash {
				return errors.New("installed SITL files are missing or modified; run sitl install")
			}
		}
	case "stop":
	default:
		return errors.New("unknown SITL command; choose install, status, start, stop, restart or logs")
	}
	if err := runCommand(ctx, "systemctl", "--user", args[0], sitlUnitName); err != nil {
		return err
	}
	fmt.Println("SITL:", args[0])
	return nil
}

func readSITLConfig(root string) (sitlConfig, error) {
	var c sitlConfig
	if err := checkInstallParents(root); err != nil {
		return c, err
	}
	err := readPrivateJSON(filepath.Join(root, "config.json"), &c)
	if err != nil {
		return c, err
	}
	return c, c.validate()
}

func verifySITLUnit(path, root string, c sitlConfig) error {
	if err := checkInstallParents(path); err != nil {
		return err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(body) != sitlUnit(root, c) {
		return errors.New("SITL service differs from this wizard's configuration; existing service preserved")
	}
	return nil
}

func installSITL(args []string, root string) error {
	f := flag.NewFlagSet("sitl install", flag.ContinueOnError)
	systemID := f.Int("sysid", 1, "MAVLink system ID, 1..255")
	home := f.String("home", "37.7595,-122.418,20,0", "Simulator home: LAT,LON,ALT,YAW")
	endpoint := f.String("mavlink-endpoint", "127.0.0.1:14550", "Beam's local MAVLink receiver")
	server := f.String("file-server", "", "Override configured file server")
	config := f.String("config", "", "Wizard configuration file")
	yes := f.Bool("yes", false, "Install using the displayed defaults without confirmation")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected SITL install arguments")
	}
	claimURL, err := configuredClaimRelease(*config, *server, "")
	if err != nil {
		return err
	}
	base, err := releaseURL(claimURL)
	if err != nil {
		return err
	}
	manifestURL := base.ResolveReference(&url.URL{Path: "sitl-release.json"})
	ctx, stop := setupContext()
	defer stop()
	client := updateClient()
	defer client.CloseIdleConnections()
	response, err := releaseGet(ctx, client, manifestURL.String())
	if err != nil {
		return err
	}
	var release sitlRelease
	decode := json.NewDecoder(io.LimitReader(response.Body, 65536))
	decode.DisallowUnknownFields()
	err = decode.Decode(&release)
	response.Body.Close()
	if err != nil {
		return err
	}
	p, ok := release.Packages[runtime.GOOS+"-"+runtime.GOARCH]
	if release.SchemaVersion != 1 || !ok {
		return errors.New("SITL is unavailable for this operating system and architecture")
	}
	c := sitlConfig{1, p.Version, *systemID, *home, *endpoint, p.Binary.SHA256, p.Defaults.SHA256, p.License.SHA256, p.SourceURL}
	if err := c.validate(); err != nil {
		return err
	}
	fmt.Printf("Install ArduCopter %s\nSystem ID: %d\nHome: %s\nMAVLink: %s\n", c.Version, c.SystemID, c.Home, c.Endpoint)
	if !*yes {
		choice, err := promptChoice("Install and start the simulator?", []menuOption{{"Cancel", "cancel"}, {"Install and start", "install"}})
		if err != nil {
			return err
		}
		if choice != "install" {
			return nil
		}
	}
	if err := checkInstallParents(root); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	unlock, err := lockSITL(root)
	if err != nil {
		return err
	}
	defer unlock()
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cache = filepath.Join(cache, "grid-uxs-wizard/sitl")
	fmt.Println("Downloading and verifying simulator files...")
	binary, err := downloadUpdate(ctx, manifestURL, p.Binary, cache)
	if err != nil {
		return err
	}
	defaults, err := downloadUpdate(ctx, manifestURL, p.Defaults, cache)
	if err != nil {
		return err
	}
	license, err := downloadUpdate(ctx, manifestURL, p.License, cache)
	if err != nil {
		return err
	}
	if err := checkArchitecture(binary, runtime.GOARCH); err != nil {
		return err
	}
	account, err := user.Current()
	if err != nil {
		return err
	}
	if exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", "somewear-sitl-los.service").Run() == nil {
		return errors.New("legacy SITL service is running; stop it before installing the wizard-managed simulator")
	}
	return applySITLInstall(ctx, root, c, binary, defaults, license, account, runCommand)
}

func applySITLInstall(ctx context.Context, root string, c sitlConfig, binary, defaults, license string, account *user.User, run func(context.Context, string, ...string) error) error {
	if err := c.validate(); err != nil {
		return err
	}
	for _, entry := range []struct{ path, hash string }{{binary, c.BinarySHA256}, {defaults, c.DefaultsSHA256}, {license, c.LicenseSHA256}} {
		actual, err := fileDigest(entry.path)
		if err != nil {
			return err
		}
		if actual != entry.hash {
			return errors.New("SITL artifact checksum does not match")
		}
	}
	unitDir := filepath.Join(account.HomeDir, ".config/systemd/user")
	unit := filepath.Join(unitDir, sitlUnitName)
	if err := secureUserServiceParents(account); err != nil {
		return err
	}
	if err := os.MkdirAll(unitDir, 0700); err != nil {
		return err
	}
	previous, err := readSITLConfig(root)
	if err == nil {
		if err := verifySITLUnit(unit, root, previous); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if _, err := os.Lstat(unit); !errors.Is(err, os.ErrNotExist) {
		return errors.New("existing SITL service is not owned by this installation")
	}
	if err := checkInstallParents(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "flight-state"), 0700); err != nil {
		return err
	}
	if err == nil {
		if err := run(ctx, "systemctl", "--user", "stop", sitlUnitName); err != nil {
			return err
		}
	}
	for _, entry := range []struct{ source, name, hash string }{{binary, "arducopter", c.BinarySHA256}, {defaults, "copter.parm", c.DefaultsSHA256}, {license, "COPYING.txt", c.LicenseSHA256}} {
		if err := copyVerified(entry.source, filepath.Join(root, "bin", entry.name), entry.hash); err != nil {
			return err
		}
	}
	if err := writePrivateJSON(filepath.Join(root, "config.json"), c); err != nil {
		return err
	}
	if err := atomicPrivateWrite(unit, []byte(sitlUnit(root, c))); err != nil {
		return err
	}
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", sitlUnitName}} {
		if err := run(ctx, "systemctl", args...); err != nil {
			return err
		}
	}
	if err := run(ctx, "systemctl", "--user", "is-active", "--quiet", sitlUnitName); err != nil {
		return errors.New("SITL service did not start; inspect grid-uxs-wizard sitl logs")
	}
	fmt.Println("SITL installed and running as your user. It will start with your user services after reboot.")
	return nil
}
