package main

import (
	"reflect"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
)

func TestParseSessionChannelsCanonicalizesNames(t *testing.T) {
	got, err := parseSessionChannels("MESH, radio,Cellular")
	if err != nil {
		t.Fatal(err)
	}
	want := []rpcpb.SessionChannel{
		rpcpb.SessionChannel_RADIO,
		rpcpb.SessionChannel_CELLULAR,
		rpcpb.SessionChannel_MESH,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSessionChannels() = %v, want %v", got, want)
	}
	if formatted := formatSessionChannels(got); formatted != "radio,cellular,mesh" {
		t.Fatalf("formatSessionChannels() = %q", formatted)
	}
}

func TestParseSessionChannelsRejectsInvalidLists(t *testing.T) {
	for _, value := range []string{"", "radio,radio", "radio,wifi"} {
		if _, err := parseSessionChannels(value); err == nil {
			t.Errorf("parseSessionChannels(%q) succeeded", value)
		}
	}
}

func TestValidateConnectSessionRequiresExactAgreement(t *testing.T) {
	route := sessionRoute{id: 42, channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}}
	if _, err := validateConnectSession(route, &rpcpb.ConnectResponse{
		SessionId: 42,
		Channels:  []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO},
	}); err != nil {
		t.Fatal(err)
	}
	for _, response := range []*rpcpb.ConnectResponse{
		{SessionId: 41, Channels: route.channels},
		{SessionId: 42, Channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_MESH}},
		{},
	} {
		if _, err := validateConnectSession(route, response); err == nil {
			t.Errorf("validateConnectSession(%+v) succeeded", response)
		}
	}
}

func TestSessionRegistrySeparatesPeersAndExpiresSessions(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	registry := newSessionRegistry()
	registry.now = func() time.Time { return now }
	radio := sessionRoute{id: 7, channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}}
	mesh := sessionRoute{id: 7, channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_MESH}}
	registry.put(100, radio)
	registry.put(200, mesh)

	got, ok := registry.get(100, 7)
	if !ok || !sameSessionChannels(got.channels, radio.channels) {
		t.Fatalf("peer 100 route = %+v, %v", got, ok)
	}
	got, ok = registry.get(200, 7)
	if !ok || !sameSessionChannels(got.channels, mesh.channels) {
		t.Fatalf("peer 200 route = %+v, %v", got, ok)
	}

	now = now.Add(sessionTTL)
	if _, ok := registry.get(100, 7); ok {
		t.Fatal("expired session remained available")
	}
}
