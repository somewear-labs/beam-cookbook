package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHorizonProvisioningSyncsBeforeWritingSelectedDevice(t *testing.T) {
	for _, scenario := range []string{"success", "cancel", "wrong scope", "profile changed", "identity changed", "device removed", "wrong receipt", "sync denied", "write failed"} {
		t.Run(scenario, func(t *testing.T) {
			active := horizonContext{"api.example.com", "75605", "200", "300"}
			device := horizonUSBDevice{"H34", "/dev/ttyACM0"}
			radio := horizonRadio{902000000, 928000000, 10, 7, 100}
			key := strings.Repeat("a", 64)
			snapshot := horizonSnapshot{active.Backend, active.WorkspaceID, "100", horizonSyncProfile{active.IdentityID, "", active.AccountID, key, 1, radio}}
			var events []string
			syncs, contexts, scans, writes := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				events = append(events, r.Method+" "+r.URL.Path)
				switch r.URL.Path {
				case horizonAPIPath + "/context":
					contexts++
					copy := active
					if scenario == "identity changed" && contexts > 1 {
						copy.IdentityID = "999"
					}
					json.NewEncoder(w).Encode(copy)
				case horizonAPIPath + "/devices":
					scans++
					devices := []horizonUSBDevice{device}
					if scenario == "device removed" && scans > 1 {
						devices = nil
					}
					json.NewEncoder(w).Encode(devices)
				case horizonAPIPath + "/sync":
					syncs++
					if scenario == "sync denied" {
						http.Error(w, "private server detail", 403)
						return
					}
					var request horizonSyncRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					copy := snapshot
					if syncs == 1 {
						if request.ExpectedRevision != 0 || request.Serial != "" {
							t.Error("initial sync must not bind a device")
						}
					} else {
						if request.ExpectedRevision != 1 || request.Serial != device.Serial {
							t.Errorf("wrong binding request: %+v", request)
						}
						copy.Profile.Serial = device.Serial
						copy.Profile.Revision = 2
						if scenario == "profile changed" {
							copy.Profile.Radio.NetID++
						}
					}
					if scenario == "wrong scope" {
						copy.Profile.IdentityID = "999"
					}
					json.NewEncoder(w).Encode(copy)
				case horizonAPIPath + "/provision":
					writes++
					if scenario == "write failed" {
						http.Error(w, "private device detail", 409)
						return
					}
					var request horizonProvisionRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					expected := horizonProvisionRequest{device.USB, device.Serial, "uxs", active.WorkspaceID, active.IdentityID, active.AccountID, key, radio}
					if request != expected {
						t.Errorf("wrong provisioning target: %+v", request)
					}
					receipt := horizonReceipt{Serial: device.Serial, WorkspaceID: active.WorkspaceID, IdentityID: active.IdentityID, AccountID: active.AccountID, IMEI: "301434062209850", KeyFingerprint: key[:15], Radio: radio}
					if scenario == "wrong receipt" {
						receipt.Radio.NetID++
					}
					json.NewEncoder(w).Encode(receipt)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			api, err := newLocalAPI(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer api.client.CloseIdleConnections()
			var output bytes.Buffer
			confirmations := 0
			flow := horizonProvisioning{api: api, out: &output, selectDevice: func(devices []horizonUSBDevice) (horizonUSBDevice, error) { return devices[0], nil }, confirm: func() (bool, error) {
				confirmations++
				events = append(events, "confirm")
				return scenario != "cancel", nil
			}}
			err = flow.provision(context.Background())
			if scenario == "success" || scenario == "cancel" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("accepted unsafe or failed provisioning")
			}
			expectedWrites := 0
			if scenario == "success" || scenario == "wrong receipt" || scenario == "write failed" {
				expectedWrites = 1
			}
			if writes != expectedWrites {
				t.Fatalf("%d device writes; expected %d", writes, expectedWrites)
			}
			if scenario == "cancel" && (syncs != 1 || confirmations != 1) {
				t.Fatal("cancel must prevent device binding and write")
			}
			if scenario == "success" {
				if syncs != 2 || contexts != 2 || scans != 2 {
					t.Fatalf("missing sync or revalidation: %v", events)
				}
				if !strings.Contains(output.String(), "settings verified") {
					t.Fatal("missing verification message")
				}
			}
			if strings.Contains(output.String(), key) {
				t.Fatal("printed full encryption key identifier")
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("server diagnostic leaked")
			}
		})
	}
}

func TestHorizonSelectionAndProfileValidation(t *testing.T) {
	active := horizonContext{"api.example.com", "1", "2", "3"}
	snapshot := horizonSnapshot{active.Backend, active.WorkspaceID, "4", horizonSyncProfile{active.IdentityID, "", active.AccountID, strings.Repeat("a", 64), 1, horizonRadio{902000000, 928000000, 10, 0, 100}}}
	if err := validateHorizonSnapshot(snapshot, active); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*horizonSnapshot){
		func(s *horizonSnapshot) { s.Profile.Radio.WindowMS = 1 },
		func(s *horizonSnapshot) { s.Profile.Radio.HighHz = s.Profile.Radio.LowHz },
		func(s *horizonSnapshot) { s.Profile.AccountID = "9" },
		func(s *horizonSnapshot) { s.Profile.KeyID = "short" },
		func(s *horizonSnapshot) { s.Profile.Revision = 0 },
	} {
		copy := snapshot
		mutate(&copy)
		if validateHorizonSnapshot(copy, active) == nil {
			t.Fatal("accepted invalid profile")
		}
	}
	if _, err := roleCommand("uxs", []string{"horizon", "provision"}); err != nil {
		t.Fatal(err)
	}
	if _, err := roleCommand("gcs", []string{"horizon", "provision"}); err == nil {
		t.Fatal("GCS must not use UxS profile-sync flow")
	}
}

func TestHorizonWithoutConnectedDeviceDoesNotSync(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case horizonAPIPath + "/context":
			io.WriteString(w, `{"backend":"api.example.com","workspaceId":"1","identityId":"2","accountId":"3"}`)
		case horizonAPIPath + "/devices":
			io.WriteString(w, `[]`)
		default:
			t.Error("unexpected call", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	defer api.client.CloseIdleConnections()
	flow := horizonProvisioning{api: api, out: io.Discard}
	if err := flow.provision(context.Background()); err == nil || !strings.Contains(err.Error(), "no Horizon connected") {
		t.Fatalf("expected actionable no-device error, got %v", err)
	}
}
