package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGCSUpdatePreservesConfigAndUsesOnlyUserService(t *testing.T) {
	for _, failStop := range []bool{false, true} {
		name := "success"
		if failStop {
			name = "failed-stop"
		}
		t.Run(name, func(t *testing.T) {
			fixture, beam, _ := updaterFixture(t)
			home := filepath.Join(fixture.Bundle, "home")
			t.Setenv("HOME", home)
			agentDir := filepath.Join(home, "Library/LaunchAgents")
			configDir := filepath.Join(home, ".config/somewear")
			for _, dir := range []string{agentDir, configDir} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			plist := filepath.Join(agentDir, "com.somewear.beam.plist")
			oldPlist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>com.somewear.beam</string><key>ProgramArguments</key><array><string>/old/beam</string><string>daemon</string><string>start</string></array><key>EnvironmentVariables</key><dict><key>BEAM_USER_STATE_DIR</key><string>/preserved/state</string></dict></dict></plist>`
			oldDir := filepath.Join(home, "beam")
			if err := os.MkdirAll(oldDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(oldDir, "beam.jar"), []byte("old-gcs"), 0600); err != nil {
				t.Fatal(err)
			}
			oldPlist = strings.ReplaceAll(oldPlist, "/old/beam", filepath.Join(oldDir, "beam"))
			if err := os.WriteFile(plist, []byte(oldPlist), 0600); err != nil {
				t.Fatal(err)
			}
			properties := filepath.Join(configDir, "beam.properties")
			oldProperties := "api-key=preserved-key\nuxs-provisioning-release-url=old-release\n"
			if err := os.WriteFile(properties, []byte(oldProperties), 0600); err != nil {
				t.Fatal(err)
			}
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/uxs/provisioning/mission-computers/status":
					w.Write([]byte(`{"phase":"Claimed"}`))
				case "/api/auth/state":
					w.Write([]byte(`{"state":"SignedIn"}`))
				case "/api/properties":
					if r.Method == "GET" {
						json.NewEncoder(w).Encode(map[string]string{"userFile": properties})
						return
					}
					var body struct {
						Updates map[string]string `json:"updates"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Updates) != 1 || body.Updates["uxs-provisioning-release-url"] != "new-release" {
						t.Error("unrelated settings changed")
					}
					if err := os.WriteFile(properties, []byte("api-key=preserved-key\nuxs-provisioning-release-url=new-release\n"), 0600); err != nil {
						t.Error(err)
					}
					w.Write([]byte(`{}`))
				default:
					t.Error("unexpected update API")
					w.WriteHeader(404)
				}
			}))
			defer apiServer.Close()
			api, _ := newLocalAPI(apiServer.URL)
			run := func(ctx context.Context, command string, args ...string) error {
				if command == "plutil" {
					return exec.CommandContext(ctx, command, args...).Run()
				}
				if command != "launchctl" || !strings.Contains(strings.Join(args, " "), "gui/") || strings.Contains(strings.Join(args, " "), "system/") {
					t.Fatal("not user-scoped service management")
				}
				if failStop && strings.Contains(strings.Join(args, " "), "kickstart") {
					return errors.New("stop failed")
				}
				return nil
			}
			err := applyGcsUpdate(context.Background(), beam, "new-release", api, run)
			afterProperties, _ := os.ReadFile(properties)
			if failStop {
				afterPlist, _ := os.ReadFile(plist)
				if err == nil || string(afterPlist) != oldPlist || !strings.Contains(string(afterProperties), "api-key=preserved-key") {
					t.Fatalf("failed restart changed identity/service configuration: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(afterProperties), "api-key=preserved-key") || !strings.Contains(string(afterProperties), "new-release") {
					t.Fatal("configuration was not preserved")
				}
				value, err := exec.Command("plutil", "-extract", "EnvironmentVariables.BEAM_USER_STATE_DIR", "raw", "-o", "-", plist).Output()
				if err != nil || strings.TrimSpace(string(value)) != "/preserved/state" {
					t.Fatal("service environment changed")
				}
			}
		})
	}
}
