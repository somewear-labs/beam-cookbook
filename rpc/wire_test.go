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
}

func TestParseGridDatagramWebhookPreservesApplicationProto(t *testing.T) {
	payload, err := marshalEnvelope(&rpcpb.Envelope{
		RequestId: 9,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Ping{Ping: &rpcpb.PingRequest{ClientSendUnixMillis: 123}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf(
		`{"payloads":[{"account":{"id":"383626"},"events":[{"type":"GridDatagram","data":%q,"datagramId":{"timestamp":"2026-08-24T15:56:31Z","sourceUserId":"383626","sequence":2},"inResponseTo":{"timestamp":"2026-08-24T15:55:00Z","sourceUserId":"99","sequence":1},"timestamp":"2026-08-24T15:56:31Z"}]}]}`,
		payload,
	))

	got := parseWebhookEnvelopes(body)
	if len(got) != 1 || got[0].envelope.GetRequestId() != 9 ||
		got[0].envelope.GetRequest().GetPing().GetClientSendUnixMillis() != 123 {
		t.Fatalf("GridDatagram webhook = %+v", got)
	}
	if got[0].datagramID == nil || got[0].datagramID.SourceUserID != "383626" ||
		got[0].inResponseTo == nil || got[0].inResponseTo.SourceUserID != "99" {
		t.Fatalf("GridDatagram routing metadata = %+v", got[0])
	}
}
