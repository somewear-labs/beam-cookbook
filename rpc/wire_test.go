package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestSendIPv4AddsTargetOnlyForUnicast(t *testing.T) {
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

	if err := sendIPv4(server.URL, 22902, 0, "broadcast"); err != nil {
		t.Fatal(err)
	}
	if err := sendIPv4(server.URL, 22902, 384899, "unicast"); err != nil {
		t.Fatal(err)
	}
	if _, exists := requests[0]["targetUserId"]; exists {
		t.Fatal("broadcast request included targetUserId")
	}
	if got := int64(requests[1]["targetUserId"].(float64)); got != 384899 {
		t.Fatalf("targetUserId = %d, want 384899", got)
	}
	for i, req := range requests {
		if got := int(req["collapseKey"].(float64)); got != 3 {
			t.Fatalf("requests[%d] collapseKey = %d, want 3", i, got)
		}
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

	got := parseWebhookEnvelopesWithSender(body)
	if len(got) != 1 {
		t.Fatalf("parseWebhookEnvelopesWithSender() returned %d envelopes", len(got))
	}
	if got[0].SourceUserID != 383626 {
		t.Fatalf("SourceUserID = %d, want 383626", got[0].SourceUserID)
	}
}
