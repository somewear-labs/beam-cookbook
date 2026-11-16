package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveBootstrap(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/uxs/provisioning/claim/bootstrap" {
		return false
	}
	if r.Method != "POST" {
		t.Error("bootstrap must be POST")
	}
	var request struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.ID != "11111111-1111-1111-1111-111111111111" {
		t.Error("bootstrap must bind the displayed claim")
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"pin": "123456", "claimCode": testRawClaim(), "credential": map[string]string{"apiKey": "testkey:secret", "backend": "api.somewear.co", "workspaceId": "75605", "gcsIdentityId": "298002", "identityId": "351092"}})
	return true
}
func TestClaimGuidesApprovalAndDisplaysOnlyReadyCode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	for _, phase := range []string{"Available", "Failed", "Expired"} {
		t.Run(phase, func(t *testing.T) {
			var output bytes.Buffer
			approved := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveBootstrap(t, w, r) {
					return
				}
				if r.URL.Path == "/api/uxs/provisioning/claim/prepare" && r.Method == "POST" {
					fmt.Fprint(w, `{ "phase":"AwaitingAuthorization", "authorizationUrl":"https://somewear.app/approve" }`)
					return
				}
				if r.URL.Path != "/api/uxs/provisioning/claim/status" || r.Method != "GET" {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				if !approved {
					t.Error("approval instructions were not shown")
				}
				fmt.Fprintf(w, `{ "id":"11111111-1111-1111-1111-111111111111", "port":9445, "phase":%q, "claimCode":%q }`, phase, testRawClaim())
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			i := gcsInstaller{opts: gcsOptions{AircraftName: "Test craft", UxsArtifact: "/beam.jar"}, beam: api, emit: func(string) {}, claimOutput: &output, authorize: func(link string) { approved = link == "https://somewear.app/approve" }}
			err := i.prepareClaim(context.Background())
			if phase != "Available" {
				if err == nil || strings.Contains(output.String(), "Setup PIN:") || strings.Contains(output.String(), "testkey:secret") {
					t.Fatal("failed authorization exposed a code")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "\n\nUxS API key:\n\ntestkey:secret\n\n") {
				t.Fatal("ready claim must display the UxS API key on its own line")
			}
			for _, text := range []string{"Test craft", "Setup PIN:", "123-456", "Enter the API key", "API key", "\n\n  grid-uxs-wizard setup\n\n"} {
				if !strings.Contains(output.String(), text) {
					t.Errorf("missing guidance %q", text)
				}
			}
		})
	}
}

func TestResumePendingClaimNeverCreatesAnotherAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	for _, phase := range []string{"Available", "Claimed", "Delivering", "Expired"} {
		t.Run(phase, func(t *testing.T) {
			var output bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveBootstrap(t, w, r) {
					return
				}
				if r.Method != "GET" || r.URL.Path != "/api/uxs/provisioning/claim/status" {
					t.Error("resume tried to create or mutate a claim")
					w.WriteHeader(500)
					return
				}
				fmt.Fprintf(w, `{"id":"11111111-1111-1111-1111-111111111111","port":9445,"phase":%q,"name":"existing-uxs","claimCode":%q}`, phase, testRawClaim())
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			i := gcsInstaller{beam: api, emit: func(string) {}, authorize: func(string) { t.Fatal("unexpected approval") }, claimOutput: &output}
			err := i.resumeClaim(context.Background())
			if phase == "Available" {
				if err != nil || !strings.Contains(output.String(), "existing-uxs") || !strings.Contains(output.String(), "Setup PIN:") {
					t.Fatalf("resume: %v %s", err, output.String())
				}
			} else if err == nil || strings.Contains(output.String(), "Setup PIN:") {
				t.Fatalf("unusable claim exposed as available: %v", err)
			}
		})
	}
}

func TestPrepareConflictPointsToResume(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(409)
			return
		}
		fmt.Fprint(w, `{"phase":"Available","name":"existing-uxs"}`)
	}))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	i := gcsInstaller{opts: gcsOptions{UxsArtifact: "/beam.jar", AircraftName: "new-uxs"}, beam: api, emit: func(string) {}}
	err := i.prepareClaim(context.Background())
	if err == nil || !strings.Contains(err.Error(), "existing-uxs") || !strings.Contains(err.Error(), "resume-claim") {
		t.Fatalf("unclear conflict: %v", err)
	}
}
