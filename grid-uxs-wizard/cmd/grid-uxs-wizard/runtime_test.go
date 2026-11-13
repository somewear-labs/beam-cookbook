package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeManagedProcess struct{ exited, stopped bool }

func (p *fakeManagedProcess) Exited() bool { return p.exited }
func (p *fakeManagedProcess) Stop()        { p.stopped = true }

func runtimeFixture(t *testing.T) (runtimeConfig, claimBootstrap, clientPairing) {
	t.Helper()
	root, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err = os.MkdirTemp(root, "grid-uxs-runtime-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	state := filepath.Join(root, "state")
	bundle := filepath.Join(root, "bundle")
	bin := filepath.Join(state, "bin")
	for _, path := range []string{state, bundle, bin} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"beam.jar", "tvpnd"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "owner"), []byte("uxs-install-v1"), 0600); err != nil {
		t.Fatal(err)
	}
	beamHash, _ := fileDigest(filepath.Join(bin, "beam.jar"))
	tvpnHash, _ := fileDigest(filepath.Join(bin, "tvpnd"))
	requestID, err := newRequestID()
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeConfig{SchemaVersion: 1, State: state, Bundle: bundle, ClaimID: "11111111-1111-1111-1111-111111111111", RequestID: requestID, Beam: filepath.Join(bin, "beam.jar"), BeamFormat: "jar", BeamSHA256: beamHash, TVPN: filepath.Join(bin, "tvpnd"), TVPNSHA256: tvpnHash}
	credential := claimBootstrap{Name: "test-uxs", Backend: "api.example.com", WorkspaceID: "75605", GCSIdentityID: "100", IdentityID: "200", APIKey: "test:key-secret", BeamSHA256: beamHash}
	credential.Pairing.ID = "22222222-2222-2222-2222-222222222222"
	credential.Pairing.Token = strings.Repeat("t", 43)
	pairing := clientPairing{ID: credential.Pairing.ID, Token: credential.Pairing.Token, GCSBaseURL: "https://192.168.1.17:9445", TLSFingerprint: strings.Repeat("a", 64), WorkspaceID: credential.WorkspaceID, IdentityID: credential.IdentityID, GCSIdentityID: credential.GCSIdentityID, Backend: credential.Backend, Name: credential.Name, BeamSHA256: beamHash}
	for path, value := range map[string]any{filepath.Join(state, "claimed-bootstrap.json"): credential, filepath.Join(state, "client-pairing.json"): pairing} {
		if err := writePrivateJSON(path, value); err != nil {
			t.Fatal(err)
		}
	}
	return config, credential, pairing
}

func TestGoRuntimeJoinsAndSupervisesWithoutShell(t *testing.T) {
	for _, scenario := range []string{"ready", "wrong-identity", "wrong-receipt", "child-exit", "post-ready-exit"} {
		t.Run(scenario, func(t *testing.T) {
			config, credential, pairing := runtimeFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var activated, delegated atomic.Bool
			tvpnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/health":
					fmt.Fprint(w, `{}`)
				case "/api/auth/sign-in/status":
					fmt.Fprint(w, `{"hasApiKey":true}`)
				case "/api/workspace":
					if activated.Load() {
						fmt.Fprint(w, `{"workspaces":[{"id":75605}],"activeWorkspaceId":75605}`)
					} else {
						fmt.Fprint(w, `{"workspaces":[{"id":75605}],"activeWorkspaceId":null}`)
					}
				case "/api/workspace/activate":
					if r.Method != "PUT" {
						t.Error("wrong Workspace activation method")
					}
					activated.Store(true)
					fmt.Fprint(w, `{}`)
				case "/api/identity":
					if scenario == "wrong-identity" {
						fmt.Fprint(w, `{"identityId":100}`)
					} else {
						fmt.Fprint(w, `{"identityId":200}`)
					}
				case "/api/config/daemon/document":
					_ = json.NewEncoder(w).Encode(map[string]any{"document": map[string]any{"revision": 3, "schemaVersion": 1, "contentFormat": "toml", "content": "[tun]\nmtu = 1300\n[package-stream]\nowner = \"tvpn\"\n"}})
				case "/api/config/daemon":
					var body struct {
						Base    uint64 `json:"baseRevision"`
						Content string `json:"content"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.Base != 3 || !strings.Contains(body.Content, `owner = "beam"`) || !strings.Contains(body.Content, "mtu = 1300") {
						t.Error("delegation lost revision or unrelated settings")
					}
					fmt.Fprint(w, `{"revision":4}`)
				case "/api/config/daemon/activate":
					delegated.Store(true)
					fmt.Fprint(w, `{}`)
				default:
					t.Errorf("unexpected TVPN route %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer tvpnServer.Close()
			beamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/auth/state":
					fmt.Fprint(w, `{"state":"SignedIn"}`)
				case "/api/workspace/activate":
					fmt.Fprint(w, `{}`)
				case "/api/workspaces":
					fmt.Fprint(w, `{"activeWorkspaceId":"75605"}`)
				case "/api/uxs/provisioning/join":
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					if !delegated.Load() || body["requestId"] != config.RequestID || body["gcsIdentityId"] != credential.GCSIdentityID {
						t.Error("join preceded delegation or changed request identity")
					}
					fmt.Fprint(w, `{"operationId":"join-1"}`)
				case "/api/uxs/provisioning/join/status":
					identity := wireID(credential.IdentityID)
					if scenario == "wrong-receipt" {
						identity = "999"
					}
					_ = json.NewEncoder(w).Encode(runtimeJoin{Phase: "Ready", OperationID: "join-1", Manifest: setupManifest{SchemaVersion: 1, RequestID: config.RequestID, IdentityID: identity, GCSIdentityID: credential.GCSIdentityID, WorkspaceID: credential.WorkspaceID, Name: credential.Name, Backend: credential.Backend, BeamSHA256: credential.BeamSHA256}})
				default:
					t.Errorf("unexpected Beam route %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer beamServer.Close()
			beam, _ := newLocalAPI(beamServer.URL)
			tvpn, _ := newLocalAPI(tvpnServer.URL)
			children := []*fakeManagedProcess{}
			ready := false
			supervisor := runtimeSupervisor{config: config, credential: credential, pairing: pairing, beam: beam, tvpn: tvpn, poll: time.Millisecond, canonical: filepath.Join(filepath.Dir(config.State), "canonical-tvpn"), checkPorts: func(context.Context) error { return nil }, onReady: func() {
				ready = true
				if scenario == "post-ready-exit" {
					children[0].exited = true
				} else {
					cancel()
				}
			}}
			supervisor.start = func(command string, args []string, env map[string]string, log string) (managedProcess, error) {
				if command == "sh" || command == "curl" || command == "jq" {
					t.Fatal("runtime uses shell utilities")
				}
				if strings.Contains(strings.Join(args, " "), credential.APIKey) {
					t.Fatal("credential exposed in arguments")
				}
				if len(children) == 1 && (command != "java" || env["BEAM_STATE_DIR"] != filepath.Join(config.State, "beam")) {
					t.Error("Beam launch contract changed")
				}
				child := &fakeManagedProcess{}
				children = append(children, child)
				if scenario == "child-exit" {
					child.exited = true
				}
				return child, nil
			}
			err := supervisor.run(ctx)
			if scenario == "ready" {
				if err != nil || !ready {
					t.Fatalf("runtime never ready: %v", err)
				}
				var receipt runtimeReceipt
				if err := readPrivateJSON(filepath.Join(config.State, "receipt.json"), &receipt); err != nil || receipt.PackageStreamOwner != "beam" || receipt.RequestID != config.RequestID {
					t.Fatalf("receipt: %v", err)
				}
			} else if scenario == "post-ready-exit" {
				if err == nil || !ready {
					t.Fatalf("watchdog failed after readiness: %v", err)
				}
			} else if err == nil || ready {
				t.Fatalf("invalid runtime reached ready: %v", err)
			}
			for _, child := range children {
				if !child.stopped {
					t.Fatal("child not cleaned up")
				}
			}
			var status runtimeStatus
			if err := readPrivateJSON(filepath.Join(config.State, "status.json"), &status); err != nil {
				t.Fatal(err)
			}
			if scenario != "ready" && status.Phase != "failed" {
				t.Fatal("failure status missing")
			}
		})
	}
}

func TestClaimMaterialRejectsIdentityAndCertificateMismatch(t *testing.T) {
	config, credential, pairing := runtimeFixture(t)
	if _, _, err := readClaimMaterial(config.State); err != nil {
		t.Fatal(err)
	}
	pairing.IdentityID = "999"
	if err := writePrivateJSON(filepath.Join(config.State, "client-pairing.json"), pairing); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readClaimMaterial(config.State); err == nil {
		t.Fatal("accepted mismatched identity")
	}
	pairing.IdentityID = credential.IdentityID
	pairing.TLSFingerprint = "bad"
	_ = writePrivateJSON(filepath.Join(config.State, "client-pairing.json"), pairing)
	if _, _, err := readClaimMaterial(config.State); err == nil {
		t.Fatal("accepted invalid fingerprint")
	}
}

func TestRuntimeRejectsChangedBinaryAndForeignListener(t *testing.T) {
	config, credential, pairing := runtimeFixture(t)
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Beam, []byte("modified"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := config.validate(); err == nil {
		t.Fatal("accepted changed artifact")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	supervisor := runtimeSupervisor{config: config, credential: credential, pairing: pairing, beam: api, tvpn: api}
	if err := supervisor.rejectForeignAPIs(context.Background()); err == nil {
		t.Fatal("adopted unrelated unauthorized server")
	}
}

func TestRuntimeCancellationAndJavaVersion(t *testing.T) {
	for _, version := range []string{"openjdk version \"21.0.12\"", "warning line\nopenjdk version \"21.0.12\""} {
		if err := validateJavaVersion([]byte(version)); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateJavaVersion([]byte(`openjdk version "17.0.1"`)); err == nil {
		t.Fatal("accepted Java 17")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	supervisor := runtimeSupervisor{poll: time.Millisecond}
	if err := supervisor.wait(ctx, time.Second, func(context.Context) bool { return false }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestPrepareRuntimeOwnsPlanAndRetainsJoinID(t *testing.T) {
	config, _, pairing := runtimeFixture(t)
	source := filepath.Join(config.Bundle, "tvpnd")
	data, err := os.ReadFile(config.TVPN)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0700); err != nil {
		t.Fatal(err)
	}
	code := filepath.Join(config.Bundle, ".claim-code")
	if err := os.WriteFile(code, []byte(testClaim("a")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.State, "request-id"), []byte(config.RequestID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := options{Beam: config.Beam, Bundle: config.Bundle, State: config.State, GCS: pairing.GCSBaseURL, CodeFile: code}
	first, err := prepareRuntime(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !unitName.MatchString(first.UnitName) {
		t.Fatal("invalid installer-owned plan")
	}
	var prior runtimeConfig
	if err := readPrivateJSON(filepath.Join(config.State, "runtime.json"), &prior); err != nil {
		t.Fatal(err)
	}
	if prior.RequestID != config.RequestID {
		t.Fatal("legacy join request ID changed during migration")
	}
	if _, err := prepareRuntime(opts); err != nil {
		t.Fatal(err)
	}
	var resumed runtimeConfig
	if err := readPrivateJSON(filepath.Join(config.State, "runtime.json"), &resumed); err != nil || prior.RequestID != resumed.RequestID {
		t.Fatalf("changed join identity on resume: %v", err)
	}
	if strings.Contains(serviceUnit(config.Bundle, config.State), "/bin/sh") || !strings.Contains(serviceUnit(config.Bundle, config.State), "grid-uxs-wizard run --state") {
		t.Fatal("service still depends on shell")
	}
	if _, err := os.Stat(filepath.Join(config.State, "initialize-linux.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("initializer script still required")
	}
}

func TestHorizonReceiptRequiresMatchingRegistrationAndRadio(t *testing.T) {
	profile := horizonProfile{Serial: "34", KeyID: strings.Repeat("a", 64), horizonRadio: horizonRadio{LowHz: 902000000, HighHz: 928000000, Channels: 10, NetID: 1, WindowMS: 100}}
	credential := claimBootstrap{WorkspaceID: "75605", IdentityID: "200"}
	good := horizonReceipt{Serial: profile.Serial, WorkspaceID: credential.WorkspaceID, IdentityID: credential.IdentityID, IMEI: "301434062209850", AccountID: "123", KeyFingerprint: profile.KeyID[:15], Radio: profile.horizonRadio}
	if err := validateHorizonReceipt(good, profile, credential); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*horizonReceipt){func(r *horizonReceipt) { r.IMEI = "0" }, func(r *horizonReceipt) { r.AccountID = "0" }, func(r *horizonReceipt) { r.IdentityID = "999" }, func(r *horizonReceipt) { r.Radio.NetID++ }, func(r *horizonReceipt) { r.KeyFingerprint = "wrong" }} {
		bad := good
		change(&bad)
		if err := validateHorizonReceipt(bad, profile, credential); err == nil {
			t.Fatal("accepted wrong Horizon registration or radio receipt")
		}
	}
}

func TestLegacyServiceUpgradeIsScopedAndBackedUp(t *testing.T) {
	config, _, _ := runtimeFixture(t)
	path := filepath.Join(filepath.Dir(config.State), "test.service")
	old := legacyServiceUnit(config.Bundle, config.State)
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	i := installer{opts: options{State: config.State, Bundle: config.Bundle}, run: func(_ context.Context, cmd string, args ...string) error {
		calls = append(calls, cmd+" "+strings.Join(args, " "))
		return nil
	}}
	body := serviceUnit(config.Bundle, config.State)
	if err := i.writeRuntimeUnit(context.Background(), path, body, "owned.service"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "systemctl stop owned.service" {
		t.Fatal("upgrade stopped unexpected services")
	}
	backup, err := os.ReadFile(filepath.Join(config.State, "service-before-go.unit"))
	if err != nil || string(backup) != old {
		t.Fatal("legacy unit not preserved")
	}
	contents, _ := os.ReadFile(path)
	if string(contents) != body {
		t.Fatal("Go unit not installed")
	}
	if err := os.WriteFile(path, []byte("unrelated unit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.writeRuntimeUnit(context.Background(), path, body, "owned.service"); err == nil {
		t.Fatal("overwrote unrelated service")
	}
	if len(calls) != 1 {
		t.Fatal("stopped unrelated service")
	}
}

func TestCompletedAcknowledgementIsBoundToClaimAndIdentity(t *testing.T) {
	config, _, pairing := runtimeFixture(t)
	code := filepath.Join(config.Bundle, ".claim-code")
	if err := os.WriteFile(code, []byte(testClaim("a")), 0600); err != nil {
		t.Fatal(err)
	}
	opts := options{State: config.State, CodeFile: code, GCS: pairing.GCSBaseURL}
	if ready, err := claimAcknowledged(opts); err != nil || ready {
		t.Fatalf("new acknowledgement: %v", err)
	}
	if err := saveClaimAcknowledgement(opts); err != nil {
		t.Fatal(err)
	}
	if ready, err := claimAcknowledged(opts); err != nil || !ready {
		t.Fatalf("completed acknowledgement: %v", err)
	}
	var saved claimAcknowledgement
	if err := readPrivateJSON(filepath.Join(config.State, "claim-acknowledged.json"), &saved); err != nil {
		t.Fatal(err)
	}
	saved.IdentityID = "999"
	if err := writePrivateJSON(filepath.Join(config.State, "claim-acknowledged.json"), saved); err != nil {
		t.Fatal(err)
	}
	if ready, err := claimAcknowledged(opts); err == nil || ready {
		t.Fatal("acknowledgement accepted for another identity")
	}
}

func TestHorizonPortsPreserveSerialAndLoopbackUDPAddresses(t *testing.T) {
	config, _, _ := runtimeFixture(t)
	profile := horizonProfile{SchemaVersion: 1, USB: "/dev/ttyACM0", Serial: "H34", KeyID: strings.Repeat("a", 64), horizonRadio: horizonRadio{LowHz: 902000000, HighHz: 928000000, Channels: 10, NetID: 1, WindowMS: 100}, NodePort: "/dev/ttyACM0", AutopilotPort: "udp://127.0.0.1:14560"}
	path := filepath.Join(config.Bundle, "horizon-profile.json")
	if err := writePrivateJSON(path, profile); err != nil {
		t.Fatal(err)
	}
	properties, err := horizonProperties(config.Bundle)
	if err != nil || !strings.Contains(properties, "mavlink-autopilot-node-port=/dev/ttyACM0") || !strings.Contains(properties, "mavlink-autopilot-serial-port=udp://127.0.0.1:14560") {
		t.Fatalf("Horizon port settings: %v %s", err, properties)
	}
	profile.AutopilotPort = "udp://192.168.1.17:14560"
	if err := writePrivateJSON(path, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := horizonProperties(config.Bundle); err == nil {
		t.Fatal("accepted non-loopback MAVLink receiver")
	}
}

func TestRuntimeProcessStopsChildAndKeepsLogsPrivate(t *testing.T) {
	config, _, _ := runtimeFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(config.State, "child.log")
	child, err := startRuntimeProcess(executable, nil, map[string]string{"GRID_WIZARD_TEST_CHILD": "wait"}, log)
	if err != nil {
		t.Fatal(err)
	}
	child.Stop()
	if !child.Exited() {
		t.Fatal("runtime left child alive")
	}
	info, err := os.Stat(log)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("child log is not private")
	}
}
