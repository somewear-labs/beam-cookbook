package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"

	"google.golang.org/protobuf/proto"
)

func TestDiscoveryEnvelopeRoundTrip(t *testing.T) {
	want := &rpcpb.Envelope{
		RequestId: 42,
		Payload: &rpcpb.Envelope_Response{
			Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Discover{
					Discover: &rpcpb.DiscoverResponse{
						ProtocolVersion: discoveryProtocolVersion,
						Hostname:        "orin",
						Arch:            "aarch64",
						Capabilities:    capabilityShell,
					},
				},
			},
		},
	}

	encoded, err := marshalEnvelope(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := unmarshalEnvelope(encoded)
	if err != nil {
		t.Fatal(err)
	}
	discovery := got.GetResponse().GetDiscover()
	if got.GetNamespace() != EnvelopeNamespace || got.GetRequestId() != 42 || discovery.GetHostname() != "orin" {
		t.Fatalf("unexpected round trip: %+v", got)
	}
}

func TestDiscoveryPayloadsStayWithinSatelliteBudget(t *testing.T) {
	probe := &rpcpb.Envelope{
		RequestId: 1,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Discover{Discover: &rpcpb.DiscoverRequest{
				ResponseJitterMs: 750,
			}},
		}},
	}
	if _, err := marshalEnvelope(probe); err != nil {
		t.Fatal(err)
	}
	if size := proto.Size(probe); size > 32 {
		t.Fatalf("discovery probe is %d bytes; want <= 32", size)
	}

	response := &rpcpb.Envelope{
		RequestId: 1,
		Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
			Result: &rpcpb.RpcResponse_Discover{Discover: &rpcpb.DiscoverResponse{
				ProtocolVersion: discoveryProtocolVersion,
				Hostname:        strings.Repeat("h", maxDiscoveryHostnameRunes),
				Arch:            strings.Repeat("a", maxDiscoveryArchRunes),
				Capabilities:    capabilityShell,
			}},
		}},
	}
	if _, err := marshalEnvelope(response); err != nil {
		t.Fatal(err)
	}
	if size := proto.Size(response); size > 80 {
		t.Fatalf("maximum discovery response is %d bytes; want <= 80", size)
	}
}

func TestDiscoveryProbeUsesBeamDatagram(t *testing.T) {
	var request struct {
		Data         string `json:"data"`
		TargetUserID string `json:"targetUserId"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datagrams" {
			t.Errorf("path = %q, want /api/datagrams", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"datagramId":{"timestamp":"2026-09-15T12:00:00Z","sourceUserId":"42","sequence":1}}`)
	}))
	defer server.Close()

	if err := sendDiscoveryProbe(server.URL, 0, 42); err != nil {
		t.Fatal(err)
	}
	if request.TargetUserID != "" {
		t.Fatalf("targetUserId = %q, want broadcast", request.TargetUserID)
	}
	envelope, err := unmarshalEnvelope(request.Data)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.RequestId != 42 || envelope.GetRequest().GetDiscover() == nil {
		t.Fatalf("application protobuf = %+v", envelope)
	}
}

func TestDiscoveryResponseReferencesRequestDatagram(t *testing.T) {
	var request struct {
		InResponseTo beamDatagramID `json:"inResponseTo"`
		Data         string         `json:"data"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datagrams" {
			t.Errorf("path = %q, want /api/datagrams", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"datagramId":{"timestamp":"2026-09-15T12:00:01Z","sourceUserId":"99","sequence":2}}`)
	}))
	defer server.Close()

	s := rpcServer{beamURL: server.URL}
	requestID := beamDatagramID{Timestamp: "2026-09-15T12:00:00Z", SourceUserID: "384899", Sequence: 1}
	s.handleDiscover(42, 384899, &rpcpb.DiscoverRequest{}, &requestID)

	if request.InResponseTo != requestID {
		t.Fatalf("datagram response = %+v", request)
	}
	envelope, err := unmarshalEnvelope(request.Data)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.RequestId != 42 || envelope.GetResponse().GetDiscover() == nil {
		t.Fatalf("application protobuf = %+v", envelope)
	}
}

func TestAcceptDiscoveryDeduplicatesWebhookRetries(t *testing.T) {
	s := &rpcServer{discoveries: make(map[discoveryKey]time.Time)}
	if !s.acceptDiscovery(100, 7) {
		t.Fatal("first discovery was rejected")
	}
	if s.acceptDiscovery(100, 7) {
		t.Fatal("duplicate discovery was accepted")
	}
	if !s.acceptDiscovery(101, 7) {
		t.Fatal("same request ID from another account was rejected")
	}
}

func TestFormatCapabilities(t *testing.T) {
	if got := formatCapabilities(capabilityShell); got != "shell" {
		t.Fatalf("formatCapabilities(shell) = %q", got)
	}
	if got := formatCapabilities(capabilityShell | 8); got != "shell,0x8" {
		t.Fatalf("formatCapabilities(shell|unknown) = %q", got)
	}
	if got := formatCapabilities(0); got != "-" {
		t.Fatalf("formatCapabilities(0) = %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("orín-edge", 4); got != "orín" {
		t.Fatalf("truncateRunes() = %q", got)
	}
}
