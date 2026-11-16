package wizard

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sitlFixture(t *testing.T) (string, sitlConfig, []string, *user.User) {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(cache, "grid-sitl-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	root := filepath.Join(home, "simulator")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	hashes := []string{}
	for _, name := range []string{"binary", "defaults", "license"} {
		path := filepath.Join(home, name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		hash, err := fileDigest(path)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		hashes = append(hashes, hash)
	}
	c := sitlConfig{1, "4.7.2", 1, "37.7595,-122.418,20,0", "127.0.0.1:14550", hashes[0], hashes[1], hashes[2], "https://github.com/ArduPilot/ardupilot"}
	return root, c, paths, &user.User{HomeDir: home}
}

func TestSITLValidatesFlightAndDownloadConfiguration(t *testing.T) {
	_, c, _, _ := sitlFixture(t)
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*sitlConfig){
		"system ID": func(c *sitlConfig) { c.SystemID = 0 },
		"home":      func(c *sitlConfig) { c.Home = "NaN,0,0,0" },
		"latitude":  func(c *sitlConfig) { c.Home = "91,0,0,0" },
		"port":      func(c *sitlConfig) { c.Endpoint = "127.0.0.1:65536" },
		"hostname":  func(c *sitlConfig) { c.Endpoint = "localhost:14550" },
		"checksum":  func(c *sitlConfig) { c.BinarySHA256 = "broken" },
		"source":    func(c *sitlConfig) { c.SourceURL = "http://example.com" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := c
			change(&copy)
			if copy.validate() == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestSITLInstallEnablesUserServiceAndPreservesFlightState(t *testing.T) {
	root, c, paths, account := sitlFixture(t)
	var calls []string
	run := func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	apply := func() {
		t.Helper()
		if err := applySITLInstall(context.Background(), root, c, paths[0], paths[1], paths[2], account, run); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable --now " + sitlUnitName, "systemctl --user is-active --quiet " + sitlUnitName}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("service commands: %v", calls)
	}
	got, err := readSITLConfig(root)
	if err != nil || got != c {
		t.Fatalf("configuration: %v %v", got, err)
	}
	for i, name := range []string{"arducopter", "copter.parm", "COPYING.txt"} {
		b, err := os.ReadFile(filepath.Join(root, "bin", name))
		source, _ := os.ReadFile(paths[i])
		if err != nil || string(b) != string(source) {
			t.Fatal("artifact not installed", name, err)
		}
	}
	state := filepath.Join(root, "flight-state", "eeprom.bin")
	if err := os.WriteFile(state, []byte("flight state"), 0600); err != nil {
		t.Fatal(err)
	}
	calls = nil
	c.SystemID = 2
	apply()
	if calls[0] != "systemctl --user stop "+sitlUnitName {
		t.Fatal("existing service was not stopped before replacement")
	}
	if b, err := os.ReadFile(state); err != nil || string(b) != "flight state" {
		t.Fatal("flight state replaced")
	}
	unit := filepath.Join(account.HomeDir, ".config/systemd/user", sitlUnitName)
	if err := verifySITLUnit(unit, root, c); err != nil {
		t.Fatal(err)
	}
}

func TestSITLRejectsCorruptionAndForeignServiceBeforeCommands(t *testing.T) {
	for _, kind := range []string{"checksum", "foreign unit"} {
		t.Run(kind, func(t *testing.T) {
			root, c, paths, account := sitlFixture(t)
			if kind == "checksum" {
				os.WriteFile(paths[0], []byte("changed"), 0600)
			} else {
				dir := filepath.Join(account.HomeDir, ".config/systemd/user")
				os.MkdirAll(dir, 0700)
				os.WriteFile(filepath.Join(dir, sitlUnitName), []byte("unrelated service"), 0600)
			}
			calls := 0
			err := applySITLInstall(context.Background(), root, c, paths[0], paths[1], paths[2], account, func(context.Context, string, ...string) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatalf("expected rejection before mutation, got %v, %d commands", err, calls)
			}
		})
	}
}

func TestSITLUnitUsesDirectUDPAndEscapesSystemdSpecifiers(t *testing.T) {
	_, c, _, _ := sitlFixture(t)
	unit := sitlUnit("/home/test 100%/simulator", c)
	for _, part := range []string{`"/home/test 100%%/simulator/bin/arducopter"`, `"--sysid" "1"`, `"--serial0" "udpclient:127.0.0.1:14550"`, "WantedBy=default.target", "WorkingDirectory=/home/test 100%%/simulator/flight-state", "UMask=0077"} {
		if !strings.Contains(unit, part) {
			t.Fatal("missing service argument", part)
		}
	}
	if strings.Contains(unit, "/bin/sh") {
		t.Fatal("service must execute simulator directly")
	}
	if _, err := roleCommand("uxs", []string{"sitl", "status"}); err != nil {
		t.Fatal(err)
	}
	if _, err := roleCommand("gcs", []string{"sitl", "status"}); err == nil {
		t.Fatal("GCS accepted simulator command")
	}
}
