package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
)

func TestActiveWorkspaceID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"workspaces":[],"activeWorkspaceId":"22902"}`)
	}))
	defer server.Close()

	got, err := activeWorkspaceID(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got != 22902 {
		t.Fatalf("activeWorkspaceID() = %d, want 22902", got)
	}
}

func TestActiveWorkspaceIDRequiresBeamSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"workspaces":[],"activeWorkspaceId":null}`)
	}))
	defer server.Close()

	_, err := activeWorkspaceID(server.URL)
	if err == nil || !strings.Contains(err.Error(), "no active workspace") {
		t.Fatalf("activeWorkspaceID() error = %v, want no-active-workspace error", err)
	}
}

func TestSendIPv4ToAddsTargetOnlyForUnicast(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests = append(requests, request)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := sendIPv4(server.URL, 22902, "broadcast"); err != nil {
		t.Fatal(err)
	}
	if err := sendIPv4To(server.URL, 22902, 384899, "unicast"); err != nil {
		t.Fatal(err)
	}
	if _, exists := requests[0]["targetUserId"]; exists {
		t.Fatal("broadcast request included targetUserId")
	}
	if got := int64(requests[1]["targetUserId"].(float64)); got != 384899 {
		t.Fatalf("targetUserId = %d, want 384899", got)
	}
}

func TestSendIPv4WithChannelsUsesGenericBeamEndpoint(t *testing.T) {
	var path string
	var request struct {
		WorkspaceID  int      `json:"workspaceId"`
		TargetUserID int64    `json:"targetUserId"`
		Channels     []string `json:"channels"`
		IPv4         struct {
			Payload string `json:"payload"`
		} `json:"ipv4"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := sendIPv4WithChannels(server.URL, 22902, 384899, "payload", []rpcpb.SessionChannel{
		rpcpb.SessionChannel_RADIO,
		rpcpb.SessionChannel_MESH,
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/package/async" || request.WorkspaceID != 22902 || request.TargetUserID != 384899 || request.IPv4.Payload != "payload" {
		t.Fatalf("request path=%q body=%+v", path, request)
	}
	wantChannels := []string{"Radio", "Mesh"}
	if !reflect.DeepEqual(request.Channels, wantChannels) {
		t.Fatalf("channels = %v, want %v", request.Channels, wantChannels)
	}
}

func TestParseWebhookEnvelopesPreservesRoutingMetadata(t *testing.T) {
	payload, err := marshalEnvelope(&rpcpb.Envelope{RequestId: 7})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf(
		`{"payloads":[{"account":{"id":"383626"},"events":[{"type":"Data","payload":%q,"timestamp":"2026-08-24T15:56:31Z"}]}]}`,
		payload,
	))

	got := parseWebhookEnvelopes(body)
	if len(got) != 1 {
		t.Fatalf("parseWebhookEnvelopes() returned %d envelopes", len(got))
	}
	if got[0].sourceUserID != 383626 {
		t.Fatalf("sourceUserID = %d, want 383626", got[0].sourceUserID)
	}
	want := time.Date(2026, 8, 24, 15, 56, 31, 0, time.UTC)
	if !got[0].packageSentAt.Equal(want) {
		t.Fatalf("packageSentAt = %s, want %s", got[0].packageSentAt, want)
	}
}
