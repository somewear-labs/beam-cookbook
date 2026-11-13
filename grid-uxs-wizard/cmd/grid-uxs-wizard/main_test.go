package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClaimPreservesArgumentsAndSeparatesAcknowledgement(t *testing.T) {
	for _, beam := range []string{"/opt/beam/bin/beam", "/opt/beam/beam.jar"} {
		var command string
		var arguments []string
		i := installer{opts: options{Beam: beam, BeamHome: "/opt/beam", Bundle: "/opt/bundle", State: "/var/lib/uxs", GCS: "https://gcs:9445", CodeFile: "/root/claim"}, run: func(_ context.Context, name string, args ...string) error {
			command = name
			arguments = args
			return nil
		}}
		for _, mode := range []string{"--prepare-only", "--ack-only"} {
			if err := i.claim(context.Background(), mode); err != nil {
				t.Fatal(err)
			}
			expected := []string{"--non-interactive", "uxs", "claim", "--gcs", "https://gcs:9445", "--code-file", "/root/claim", "--bundle", "/opt/bundle", "--state", "/var/lib/uxs", mode, "--beam-home", "/opt/beam"}
			expectedCommand := beam
			if strings.HasSuffix(beam, ".jar") {
				expectedCommand = "java"
				expected = append([]string{"-jar", beam}, expected...)
			}
			if command != expectedCommand || !reflect.DeepEqual(arguments, expected) {
				t.Fatalf("unexpected command: %s %v", command, arguments)
			}
		}
	}
}
func TestReadPlanRejectsUnexpectedUnits(t *testing.T) {
	for _, body := range []string{`{}`, `{"schemaVersion":2,"unitName":"somewear-uxs-00000000-0000-0000-0000-000000000000.service"}`, `{"schemaVersion":1,"unitName":"somewear-beam.service"}`, `{"schemaVersion":1,"unitName":"../../evil.service"}`} {
		path := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readPlan(path); err == nil {
			t.Fatalf("accepted invalid plan %s", body)
		}
	}
	path := filepath.Join(t.TempDir(), "plan.json")
	_ = os.WriteFile(path, []byte(`{"schemaVersion":1,"unitName":"somewear-uxs-00000000-0000-0000-0000-000000000000.service"}`), 0600)
	if _, err := readPlan(path); err != nil {
		t.Fatal(err)
	}
}
func TestWriteUnitIsIdempotentAndPreservesConflictingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uxs.service")
	if err := writeUnit(path, "owned service"); err != nil {
		t.Fatal(err)
	}
	if err := writeUnit(path, "owned service"); err != nil {
		t.Fatal(err)
	}
	if err := writeUnit(path, "replacement"); err == nil {
		t.Fatal("overwrote a different service")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "owned service" {
		t.Fatal("existing service changed")
	}
	link := filepath.Join(t.TempDir(), "link.service")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := writeUnit(link, "owned service"); err == nil {
		t.Fatal("accepted a symlink")
	}
}
func TestClaimFileMustBePrivateAndRegular(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim")
	_ = os.WriteFile(path, []byte("private claim"), 0600)
	if err := checkCodeFile(path); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0644)
	if err := checkCodeFile(path); err == nil {
		t.Fatal("accepted public claim file")
	}
	_ = os.Chmod(path, 0600)
	link := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink(path, link)
	if err := checkCodeFile(link); err == nil {
		t.Fatal("accepted symlink claim file")
	}
}
func TestReadinessReportsFailureAndDoesNotTrustStaleReady(t *testing.T) {
	for _, phase := range []string{"ready", "failed"} {
		state := t.TempDir()
		path := filepath.Join(state, "status.json")
		started := time.Now().Add(-time.Second)
		_ = os.WriteFile(path, []byte(`{"phase":"`+phase+`"}`), 0600)
		var events []string
		i := installer{opts: options{State: state, Wait: 10 * time.Millisecond}, emit: func(s string) { events = append(events, s) }}
		err := i.waitReady(context.Background(), "test.service", started)
		if (err == nil) != (phase == "ready") {
			t.Fatalf("phase %s, err %v", phase, err)
		}
		if phase == "ready" && events[len(events)-1] != "ready" {
			t.Fatal("ready event missing")
		}
		old := time.Now().Add(-time.Hour)
		_ = os.Chtimes(path, old, old)
		err = i.waitReady(context.Background(), "test.service", time.Now())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stale state accepted: %v", err)
		}
	}
}
func TestServiceUnitUsesGoSupervisor(t *testing.T) {
	unit := serviceUnit("/opt/bundle", "/var/lib/uxs")
	for _, value := range []string{"ExecStart=/var/lib/uxs/bin/grid-uxs-wizard run --state /var/lib/uxs", "KillMode=control-group", "UMask=0077", "Restart=on-failure"} {
		if !strings.Contains(unit, value) {
			t.Fatalf("missing %s", value)
		}
	}
}

func TestExistingTVPNInstallationMustMatchTargetState(t *testing.T) {
	canonical := filepath.Join(t.TempDir(), "tvpn")
	if err := validateTVPNState("/var/lib/new-uxs", canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/var/lib/old-uxs/tvpn", canonical); err != nil {
		t.Fatal(err)
	}
	if err := validateTVPNState("/var/lib/new-uxs", canonical); err == nil || !strings.Contains(err.Error(), "claim has not been redeemed") {
		t.Fatal("missing actionable old-installation error:", err)
	}
	if err := validateTVPNState("/var/lib/old-uxs", canonical); err != nil {
		t.Fatal("blocked same-installation retry:", err)
	}
}
func TestCommandFailurePreservesPrivateDiagnostics(t *testing.T) {
	t.Setenv("GRID_WIZARD_TEST_CHILD", "fail")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = runCommand(context.Background(), binary)
	var failure *commandError
	if !errors.As(err, &failure) || failure.logPath == "" {
		t.Fatal("private diagnostic path missing:", err)
	}
	defer os.RemoveAll(filepath.Dir(failure.logPath))
	if strings.Contains(err.Error(), "private diagnostic sample") {
		t.Fatal("command output leaked to public error")
	}
	data, readErr := os.ReadFile(failure.logPath)
	if readErr != nil || !strings.Contains(string(data), "private diagnostic sample") {
		t.Fatal("diagnostic output missing:", readErr)
	}
	info, statErr := os.Stat(failure.logPath)
	if statErr != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("diagnostic log is not private")
	}
}
func TestCommandOutputKeepsBoundedTail(t *testing.T) {
	output := &commandOutput{}
	_, _ = output.Write([]byte(strings.Repeat("x", 70000)))
	_, _ = output.Write([]byte("last error"))
	if len(output.data) != 65536 || !strings.HasSuffix(string(output.data), "last error") {
		t.Fatal("diagnostic tail was not bounded/preserved")
	}
}

func TestClaimFormatRejectsTruncatedPasteBeforePersisting(t *testing.T) {
	code := "uxs1.00000000-0000-0000-0000-000000000000." + strings.Repeat("A", 43) + "." + strings.Repeat("b", 64)
	if !validClaimCode(code) {
		t.Fatal("rejected complete claim")
	}
	for _, value := range []string{"uxs1.example", code[:len(code)-1], code + "extra"} {
		if validClaimCode(value) {
			t.Fatal("accepted incomplete/invalid claim")
		}
	}
}

func TestSelectManagedUxSState(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "canonical")
	got, err := selectUxsState(canonical, root)
	if err != nil || got != filepath.Join(root, "mission-computer") {
		t.Fatalf("fresh default: %s %v", got, err)
	}
	state := filepath.Join(root, "old-install")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(state, "tvpn"), canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := selectUxsState(canonical, root); err == nil {
		t.Fatal("accepted unmarked state")
	}
	if err := os.WriteFile(filepath.Join(state, "owner"), []byte("uxs-install-v1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkInstallParents(state); err == nil {
		got, err = selectUxsState(canonical, root)
		if err != nil || got != state {
			t.Fatalf("resume state: %s %v", got, err)
		}
	}
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/outside/tvpn", canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := selectUxsState(canonical, root); err == nil {
		t.Fatal("accepted outside state")
	}
}
