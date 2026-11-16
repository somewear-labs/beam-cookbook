package wizard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func updaterFixture(t *testing.T) (runtimeConfig, string, string) {
	t.Helper()
	config, _, _ := runtimeFixture(t)
	config.TVPNExternal = true
	if err := writePrivateJSON(filepath.Join(config.State, "runtime.json"), config); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(filepath.Join(config.State, "install-plan.json"), installPlan{SchemaVersion: 1, UnitName: "somewear-uxs-" + config.ClaimID + ".service"}); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{filepath.Join(config.State, "bin/grid-uxs-wizard"): "old-runner", filepath.Join(config.Bundle, "new-beam.jar"): "new-beam", filepath.Join(config.Bundle, "new-wizard"): "new-runner"} {
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return config, filepath.Join(config.Bundle, "new-beam.jar"), filepath.Join(config.Bundle, "new-wizard")
}
func TestUxSUpdatePreservesIdentityPairingAndClaimBinding(t *testing.T) {
	config, beam, runner := updaterFixture(t)
	originals := map[string][]byte{}
	for _, name := range []string{"claimed-bootstrap.json", "client-pairing.json", "bin/tvpnd"} {
		data, err := os.ReadFile(filepath.Join(config.State, name))
		if err != nil {
			t.Fatal(err)
		}
		originals[name] = data
	}
	calls := []string{}
	run := func(_ context.Context, command string, args ...string) error {
		if command != "systemctl" || len(args) < 2 || args[0] != "--user" {
			t.Fatal("privileged or unexpected update command")
		}
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	err := applyUxsUpdate(context.Background(), config.State, beam, runner, run)
	if err != nil {
		t.Fatal(err)
	}
	var after runtimeConfig
	if err := readPrivateJSON(filepath.Join(config.State, "runtime.json"), &after); err != nil {
		t.Fatal(err)
	}
	expected, _ := fileDigest(beam)
	if after.BeamSHA256 != config.BeamSHA256 || after.InstalledBeamSHA256 != expected || after.ClaimID != config.ClaimID || after.RequestID != config.RequestID {
		t.Fatal("provisioning binding changed")
	}
	if err := after.validate(); err != nil {
		t.Fatal(err)
	}
	for name, before := range originals {
		data, _ := os.ReadFile(filepath.Join(config.State, name))
		if string(data) != string(before) {
			t.Fatalf("changed preserved material: %s", name)
		}
	}
	if len(calls) != 2 || !strings.Contains(calls[0], "stop somewear-uxs-") || !strings.Contains(calls[1], "start somewear-uxs-") {
		t.Fatalf("wrong service scope: %v", calls)
	}
}
func TestUxSUpdateReportsFailedRestartWithoutChangingIdentity(t *testing.T) {
	config, beam, runner := updaterFixture(t)
	before, _ := os.ReadFile(filepath.Join(config.State, "client-pairing.json"))
	err := applyUxsUpdate(context.Background(), config.State, beam, runner, func(_ context.Context, _ string, args ...string) error {
		if len(args) > 1 && args[1] == "start" {
			return errors.New("start failed")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "restarting its user service failed") {
		t.Fatalf("restart error: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(config.State, "client-pairing.json"))
	if string(before) != string(after) {
		t.Fatal("pairing changed")
	}
}
func TestGCSUpdateBlocksPendingSetup(t *testing.T) {
	for _, phase := range []string{"Available", "Delivering", "Expired", "Claimed", ""} {
		t.Run(phase, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "" {
					w.WriteHeader(204)
					return
				}
				w.Write([]byte(`{"phase":"` + phase + `"}`))
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			err := requireNoPendingSetup(context.Background(), api)
			blocked := phase != "" && phase != "Claimed"
			if (err != nil) != blocked {
				t.Fatalf("phase %s update eligibility: %v", phase, err)
			}
		})
	}
}
func TestUpdateDownloadRejectsChecksumMismatch(t *testing.T) {
	config, _, _ := runtimeFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tampered")) }))
	defer server.Close()
	base, _ := releaseURL(server.URL + "/update-release.json")
	cache := filepath.Join(config.State, "download-cache")
	_, err := downloadUpdate(context.Background(), base, updateArtifact{Source: "beam.jar", SHA256: strings.Repeat("0", 64)}, cache)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("bad release accepted: %v", err)
	}
	entries, _ := os.ReadDir(cache)
	if len(entries) != 0 {
		t.Fatal("failed update left an artifact")
	}
}

func TestTVPNUpdateReplacesOnlyDaemonAndRestartsRootService(t *testing.T) {
	config, _, _ := runtimeFixture(t)
	target := filepath.Join(config.State, "tvpnd")
	source := filepath.Join(config.Bundle, "tvpnd-new")
	settings := filepath.Join(config.State, "config.toml")
	for path, text := range map[string]string{target: "old-tvpn", source: "new-tvpn", settings: "preserved-config"} {
		if err := os.WriteFile(path, []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	hash, _ := fileDigest(source)
	calls := 0
	run := func(_ context.Context, name string, args ...string) error {
		calls++
		if name != "systemctl" || strings.Join(args, " ") != "restart somewear-tvpnd.service" {
			t.Fatal("wrong TVPN service scope")
		}
		return nil
	}
	err := replaceLocalTVPN(context.Background(), source, hash, tvpnUpdateTarget{Binary: target, Service: "somewear-tvpnd.service"}, run)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(settings)
	if string(content) != "preserved-config" {
		t.Fatal("TVPN configuration changed")
	}
	if err := replaceLocalTVPN(context.Background(), source, hash, tvpnUpdateTarget{Binary: target, Service: "somewear-tvpnd.service"}, run); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("unchanged TVPN was restarted")
	}
}
func TestTVPNUpdateRejectsBadChecksumBeforeReplacingDaemon(t *testing.T) {
	config, _, _ := runtimeFixture(t)
	target := filepath.Join(config.State, "tvpnd")
	source := filepath.Join(config.Bundle, "tvpnd-new")
	os.WriteFile(target, []byte("old"), 0700)
	os.WriteFile(source, []byte("new"), 0700)
	err := replaceLocalTVPN(context.Background(), source, strings.Repeat("0", 64), tvpnUpdateTarget{Binary: target}, func(context.Context, string, ...string) error { t.Fatal("restart before verification"); return nil })
	if err == nil {
		t.Fatal("bad checksum accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "old" {
		t.Fatal("old TVPN replaced before verification")
	}
}
