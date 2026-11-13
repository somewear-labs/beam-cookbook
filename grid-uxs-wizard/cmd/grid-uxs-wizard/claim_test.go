package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaimGuidesApprovalAndDisplaysOnlyReadyCode(t *testing.T) {
	for _, phase := range []string{"Available", "Failed", "Expired"} {
		t.Run(phase, func(t *testing.T) {
			var output bytes.Buffer
			approved := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				fmt.Fprintf(w, `{ "phase":%q, "claimCode":"test-single-use-code" }`, phase)
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			i := gcsInstaller{opts: gcsOptions{AircraftName: "Test craft", UxsArtifact: "/beam.jar"}, beam: api, emit: func(string) {}, claimOutput: &output, authorize: func(link string) { approved = link == "https://somewear.app/approve" }}
			err := i.prepareClaim(context.Background())
			if phase != "Available" {
				if err == nil || strings.Contains(output.String(), "test-single-use-code") {
					t.Fatal("failed authorization exposed a code")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"Test craft", "Copy this entire claim code:", "test-single-use-code", "Paste the code", "single-use", "\n\n  sudo ./grid-uxs-wizard setup --role uxs\n\n", "selected automatically"} {
				if !strings.Contains(output.String(), text) {
					t.Errorf("missing guidance %q", text)
				}
			}
		})
	}
}

func TestResumePendingClaimNeverCreatesAnotherAccount(t *testing.T) {
	for _, phase := range []string{"Available", "Claimed", "Delivering", "Expired"} {
		t.Run(phase, func(t *testing.T) {
			var output bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/uxs/provisioning/claim/status" {
					t.Error("resume tried to create or mutate a claim")
					w.WriteHeader(500)
					return
				}
				fmt.Fprintf(w, `{"phase":%q,"name":"existing-uxs","claimCode":"test-code"}`, phase)
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			i := gcsInstaller{beam: api, emit: func(string) {}, authorize: func(string) { t.Fatal("unexpected approval") }, claimOutput: &output}
			err := i.resumeClaim(context.Background())
			if phase == "Available" {
				if err != nil || !strings.Contains(output.String(), "existing-uxs") || !strings.Contains(output.String(), "test-code") {
					t.Fatalf("resume: %v %s", err, output.String())
				}
			} else if err == nil || strings.Contains(output.String(), "test-code") {
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
