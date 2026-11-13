package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSetupRoleRoutesBothInstallations(t *testing.T) {
	for _, role := range []string{"gcs", "uxs"} {
		selected, args, err := setupRole([]string{"--bundle", "bundle", "--role=" + role, "--workspace", "1"})
		if err != nil || selected != role || strings.Join(args, " ") != "--bundle bundle --workspace 1" {
			t.Fatalf("role routing: %s %v %v", selected, args, err)
		}
	}
	for _, args := range [][]string{{}, {"--role"}, {"--role", "other"}, {"--role", "gcs", "--role", "uxs"}} {
		if _, _, err := setupRole(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestLocalAPIRejectsNonLoopbackAndRedirects(t *testing.T) {
	for _, base := range []string{"http://192.168.1.1:9091", "http://localhost:9091", "https://127.0.0.1:9091", "http://user@127.0.0.1:9091", "http://127.0.0.1:9091/path"} {
		if _, err := newLocalAPI(base); err == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com", http.StatusFound)
	}))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	if err := api.request(context.Background(), "GET", "/api/test", nil, nil); err == nil {
		t.Fatal("followed redirect")
	}
}
func TestGCSConfigureInstallsSharedCredentialAndPreservesOtherSettings(t *testing.T) {
	var configured bool
	var commands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/state":
			fmt.Fprint(w, `{"state":"SignedIn","hasApiKey":true}`)
		case "/api/properties":
			if r.Method == "POST" {
				var request struct {
					Updates map[string]string `json:"updates"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if request.Updates["uxs-solution"] != "networked" {
					t.Error("Networked mode not configured")
				}
				fmt.Fprint(w, `{"requiresRestart":false}`)
			} else {
				fmt.Fprint(w, `{"systemFile":"/trusted/beam.properties","properties":[{"key":"api-key","source":"system","value":"masked"},{"key":"host","value":"api.somewear.co"}]}`)
			}
		case "/api/workspaces":
			fmt.Fprint(w, `{"activeWorkspaceId":"1","workspaces":[{"workspaceId":"1","name":"Demo"}]}`)
		case "/api/workspace/activate":
			fmt.Fprint(w, `{}`)
		case "/api/health":
			fmt.Fprint(w, `{}`)
		case "/api/config/daemon/document":
			_ = json.NewEncoder(w).Encode(map[string]any{"document": map[string]any{"revision": 2, "schemaVersion": 1, "contentFormat": "toml", "content": "[tun]\nmtu = 1300\n[package-stream]\nowner = \"tvpn\"\n"}})
		case "/api/config/daemon":
			var request struct {
				Base    uint64 `json:"baseRevision"`
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			for _, value := range []string{"mtu = 1300", "owner = \"beam\"", "key = \"testkey:secret\"", "host = \"api.somewear.co\""} {
				if !strings.Contains(request.Content, value) {
					t.Errorf("lost required configuration %s", value)
				}
			}
			if request.Base != 2 {
				t.Error("revision guard missing")
			}
			configured = true
			fmt.Fprint(w, `{"revision":3}`)
		case "/api/config/daemon/activate":
			fmt.Fprint(w, `{}`)
		case "/api/auth/sign-in/status":
			fmt.Fprint(w, `{"hasApiKey":true}`)
		case "/api/workspace":
			fmt.Fprint(w, `{"activeWorkspaceId":"1","workspaces":[{"id":"1"}]}`)
		case "/api/identity":
			fmt.Fprint(w, `{"identityId":"42"}`)
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	i := gcsInstaller{opts: gcsOptions{}, beam: api, tvpn: api, emit: func(string) {}, authorize: func(string) { t.Error("existing GCS identity was reauthorized") }, chooseWorkspace: func([]workspace) (string, error) { t.Fatal("active Workspace not retained"); return "", nil }, run: func(_ context.Context, command string, args ...string) error {
		commands = append(commands, command+" "+strings.Join(args, " "))
		if len(args) > 1 && args[0] == "config" {
			return os.WriteFile(args[len(args)-1], []byte("[api]\nkey = \"testkey:secret\"\n"), 0600)
		}
		return nil
	}}
	if err := i.configure(context.Background(), "beam", "tvpn"); err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("TVPN was not configured")
	}
	if i.opts.Workspace != "1" {
		t.Fatal("Workspace not selected")
	}
	for _, command := range commands {
		if strings.Contains(command, "secret") {
			t.Fatal("credential leaked into command arguments")
		}
	}
}
func TestGCSRejectsInvalidExistingCredentialWithoutReplacingIt(t *testing.T) {
	started := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/state" {
			fmt.Fprint(w, `{"state":"SignedOut","hasApiKey":true}`)
		} else {
			started = true
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	i := gcsInstaller{beam: api}
	if err := i.signIn(context.Background()); err == nil || started {
		t.Fatal("replaced an existing GCS credential")
	}
}
func TestGCSBrowserAuthorizationIsExplicitAndPreservesFailure(t *testing.T) {
	authorized := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/state":
			fmt.Fprint(w, `{"state":"SignedOut","hasApiKey":false}`)
		case "/api/auth/sign-in/start":
			fmt.Fprint(w, `{"url":"https://somewear.app/?auth_ticket=example"}`)
		case "/api/auth/sign-in/status":
			fmt.Fprint(w, `{"state":"Expired"}`)
		}
	}))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	i := gcsInstaller{beam: api, authorize: func(string) { authorized = true }}
	if err := i.signIn(context.Background()); err == nil || !authorized {
		t.Fatal("expired authorization was accepted or never presented")
	}
}
func TestTOMLMergePreservesAdjacentSections(t *testing.T) {
	original := "[api]\nkey = \"old\"\nextra = 7\n[tun]\nmtu = 1300\n"
	merged := mergeSection(original, "api", map[string]string{"key": "\"new\"", "host": "\"api.somewear.co\""})
	for _, value := range []string{"extra = 7", "[tun]\nmtu = 1300", "key = \"new\"", "host = \"api.somewear.co\""} {
		if !strings.Contains(merged, value) {
			t.Fatalf("lost %s", value)
		}
	}
	if strings.Contains(merged, "\"old\"") {
		t.Fatal("retained obsolete credential")
	}
}
