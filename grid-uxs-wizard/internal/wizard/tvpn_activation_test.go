package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTVPNConfigurationActivationUsesSupportedLifecycle(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			var running atomic.Uint64
			running.Store(2)
			activations := 0
			var commands []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/config/daemon":
					json.NewEncoder(w).Encode(map[string]any{"latestStoredRevision": 3, "runningRevision": running.Load(), "pending": running.Load() != 3})
				case "/api/config/daemon/activate":
					activations++
					if platform == "darwin" {
						t.Error("macOS activation endpoint must not be called")
						w.WriteHeader(400)
						return
					}
					var body map[string]uint64
					json.NewDecoder(r.Body).Decode(&body)
					if body["baseRevision"] != 3 {
						t.Error("activation revision guard missing")
					}
					running.Store(3)
					w.Write([]byte(`{}`))
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			run := func(_ context.Context, command string, args ...string) error {
				commands = append(commands, command+" "+strings.Join(args, " "))
				if args[0] == "up" {
					running.Store(3)
				}
				return nil
			}
			if err := activateTVPNConfiguration(context.Background(), api, run, "tvpn", 3, platform); err != nil {
				t.Fatal(err)
			}
			if platform == "darwin" {
				if strings.Join(commands, ",") != "tvpn down,tvpn up" || activations != 0 {
					t.Fatal("wrong macOS restart sequence")
				}
			} else if len(commands) != 0 || activations != 1 {
				t.Fatal("supported platform did not use guarded API activation")
			}
		})
	}
}

func TestTVPNActivationPreservesRunningRevisionAndPropagatesRestartFailure(t *testing.T) {
	for _, scenario := range []string{"already-running", "changed-revision", "restart-failed"} {
		t.Run(scenario, func(t *testing.T) {
			latest := uint64(3)
			running := uint64(3)
			if scenario == "changed-revision" {
				latest = 4
			}
			if scenario == "restart-failed" {
				running = 2
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"latestStoredRevision": latest, "runningRevision": running, "pending": running != latest})
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			calls := 0
			failure := errors.New("stop failed")
			run := func(context.Context, string, ...string) error { calls++; return failure }
			err := activateTVPNConfiguration(context.Background(), api, run, "tvpn", 3, "darwin")
			if scenario == "already-running" {
				if err != nil || calls != 0 {
					t.Fatal("restarted an already applied revision")
				}
			} else if scenario == "changed-revision" {
				if err == nil || calls != 0 {
					t.Fatal("restarted a different revision")
				}
			} else if !errors.Is(err, failure) || calls != 1 {
				t.Fatal("restart failure was swallowed")
			}
		})
	}
}
