package main

import (
	"encoding/json"
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
				Channels:         []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO},
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
				Channels:        []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO},
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

func TestConstrainedDiscoveryProbeUsesRequestedChannels(t *testing.T) {
	var request struct {
		WorkspaceID int      `json:"workspaceId"`
		Channels    []string `json:"channels"`
		IPv4        struct {
			Payload string `json:"payload"`
		} `json:"ipv4"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/package/async" {
			t.Errorf("path = %q, want /api/package/async", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	channels := []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}
	if err := sendDiscoveryProbe(server.URL, 22902, 0, 42, channels); err != nil {
		t.Fatal(err)
	}
	if request.WorkspaceID != 22902 || len(request.Channels) != 1 || request.Channels[0] != "Radio" {
		t.Fatalf("routing request = %+v", request)
	}
	envelope, err := unmarshalEnvelope(request.IPv4.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSessionChannels(envelope.GetRequest().GetDiscover().GetChannels(), channels) {
		t.Fatalf("probe channels = %v, want %v", envelope.GetRequest().GetDiscover().GetChannels(), channels)
	}
}

func TestConstrainedDiscoveryResponseUsesRequestedChannels(t *testing.T) {
	var request struct {
		TargetUserID int64    `json:"targetUserId"`
		Channels     []string `json:"channels"`
		IPv4         struct {
			Payload string `json:"payload"`
		} `json:"ipv4"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	channels := []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}
	s := rpcServer{workspaceID: 22902, beamURL: server.URL}
	s.handleDiscover(42, 384899, &rpcpb.DiscoverRequest{Channels: channels})

	if request.TargetUserID != 384899 || len(request.Channels) != 1 || request.Channels[0] != "Radio" {
		t.Fatalf("routing request = %+v", request)
	}
	envelope, err := unmarshalEnvelope(request.IPv4.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSessionChannels(envelope.GetResponse().GetDiscover().GetChannels(), channels) {
		t.Fatalf("response channels = %v, want %v", envelope.GetResponse().GetDiscover().GetChannels(), channels)
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
