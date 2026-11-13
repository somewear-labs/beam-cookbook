package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func simulatorOptions(t *testing.T) sitlOptions {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(binary)
	if err != nil {
		t.Fatal(err)
	}
	return sitlOptions{Binary: binary, SHA256: digest, Endpoint: "127.0.0.1:14550", Vehicle: "boat", Home: "42.3,-71.0,10,0", State: t.TempDir(), SystemID: 2, Instance: 1}
}
func TestSITLRejectsInvalidControlConfiguration(t *testing.T) {
	good := simulatorOptions(t)
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	cases := []sitlOptions{}
	for _, modify := range []func(*sitlOptions){
		func(o *sitlOptions) { o.Home = "NaN,-71,10,0" },
		func(o *sitlOptions) { o.Home = "91,-71,10,0" },
		func(o *sitlOptions) { o.Home = "42,-71,10,360" },
		func(o *sitlOptions) { o.Endpoint = "localhost:14550" },
		func(o *sitlOptions) { o.Endpoint = "127.0.0.1:65536" },
		func(o *sitlOptions) { o.SystemID = 0 },
		func(o *sitlOptions) { o.Instance = 101 },
		func(o *sitlOptions) { o.Vehicle = "unknown" },
		func(o *sitlOptions) { o.SHA256 = "bad" },
	} {
		candidate := good
		modify(&candidate)
		cases = append(cases, candidate)
	}
	for _, candidate := range cases {
		if err := candidate.validate(); err == nil {
			t.Fatalf("accepted invalid configuration %+v", candidate)
		}
	}
}
func TestSITLStagingResumesAndRejectsConfigurationOrArtifactChanges(t *testing.T) {
	o := simulatorOptions(t)
	o.Defaults = filepath.Join(t.TempDir(), "defaults.parm")
	_ = os.WriteFile(o.Defaults, []byte("SYSID_THISMAV 2\n"), 0600)
	binary, err := stageSITL(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stageSITL(o); err != nil {
		t.Fatal(err)
	}
	changed := o
	changed.SystemID = 1
	if _, err := stageSITL(changed); err == nil {
		t.Fatal("resumed with another system ID")
	}
	_ = os.WriteFile(binary, []byte("modified"), 0700)
	if _, err := stageSITL(o); err == nil {
		t.Fatal("accepted changed installed simulator")
	}
}
func TestSITLLockPreventsConcurrentRunnersAndReleases(t *testing.T) {
	state := t.TempDir()
	release, err := lockSITL(state)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockSITL(state); err == nil {
		second()
		t.Fatal("acquired a second lock")
	}
	release()
	third, err := lockSITL(state)
	if err != nil {
		t.Fatal(err)
	}
	third()
}
func TestSITLForwardsOptionsAndWritesLog(t *testing.T) {
	t.Setenv("GRID_WIZARD_TEST_CHILD", "exit")
	o := simulatorOptions(t)
	if err := runSITL(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(o.State, "sitl.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"--model motorboat", "--sysid 2", "--instance 1", "--serial0 udpclient:127.0.0.1:14550"} {
		if !strings.Contains(string(log), value) {
			t.Fatalf("missing simulator argument %s", value)
		}
	}
}
func TestSITLCancellationStopsChildAndReleasesState(t *testing.T) {
	t.Setenv("GRID_WIZARD_TEST_CHILD", "wait")
	o := simulatorOptions(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := runSITL(ctx, o); err == nil {
		t.Fatal("expected interrupted child to report failure")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("child did not terminate promptly")
	}
	release, err := lockSITL(o.State)
	if err != nil {
		t.Fatal("state remained locked:", err)
	}
	release()
}
