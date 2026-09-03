package main

import (
	"io"
	"os"
	"testing"

	rpcpb "somewear/rpc/proto"
)

func TestApplySelectorKey(t *testing.T) {
	tests := []struct {
		name     string
		selected int
		key      []byte
		want     int
		action   selectorAction
	}{
		{name: "down", selected: 0, key: []byte{'j'}, want: 1, action: selectorContinue},
		{name: "down arrow", selected: 1, key: []byte{0x1b, '[', 'B'}, want: 2, action: selectorContinue},
		{name: "down wraps", selected: 2, key: []byte{'j'}, want: 0, action: selectorContinue},
		{name: "up wraps", selected: 0, key: []byte{'k'}, want: 2, action: selectorContinue},
		{name: "up arrow", selected: 2, key: []byte{0x1b, '[', 'A'}, want: 1, action: selectorContinue},
		{name: "enter", selected: 1, key: []byte{'\r'}, want: 1, action: selectorChoose},
		{name: "quit", selected: 1, key: []byte{'q'}, want: 1, action: selectorCancel},
		{name: "control c", selected: 1, key: []byte{3}, want: 1, action: selectorCancel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, action := applySelectorKey(tt.selected, 3, tt.key)
			if got != tt.want || action != tt.action {
				t.Fatalf("applySelectorKey() = (%d, %d), want (%d, %d)", got, action, tt.want, tt.action)
			}
		})
	}
}

func TestSelectableShellTargetsFiltersAndSorts(t *testing.T) {
	targets := map[int64]discoveredTarget{
		20: discoveryTarget(20, "zulu", discoveryProtocolVersion, capabilityShell),
		10: discoveryTarget(10, "Alpha", discoveryProtocolVersion, capabilityShell),
		30: discoveryTarget(30, "future", discoveryProtocolVersion+1, capabilityShell),
		40: discoveryTarget(40, "no-shell", discoveryProtocolVersion, 0),
		0:  discoveryTarget(0, "no-account", discoveryProtocolVersion, capabilityShell),
	}

	got := selectableShellTargets(targets, nil)
	if len(got) != 2 || got[0].accountID != 10 || got[1].accountID != 20 {
		t.Fatalf("selectableShellTargets() = %+v", got)
	}
}

func TestSelectableShellTargetsRequiresSessionChannelCapability(t *testing.T) {
	targets := map[int64]discoveredTarget{
		1: discoveryTarget(1, "legacy", discoveryProtocolVersion, capabilityShell),
		2: discoveryTarget(2, "current", discoveryProtocolVersion, capabilityShell|capabilitySessionChannels, rpcpb.SessionChannel_RADIO),
		3: discoveryTarget(3, "wrong-channel", discoveryProtocolVersion, capabilityShell|capabilitySessionChannels, rpcpb.SessionChannel_CELLULAR),
	}
	got := selectableShellTargets(targets, []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO})
	if len(got) != 1 || got[0].accountID != 2 {
		t.Fatalf("selectableShellTargets() = %+v", got)
	}
}

func TestSingleTargetStillRequiresInteractiveSelection(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()

	target := discoveryTarget(10, "orin", discoveryProtocolVersion, capabilityShell)
	_, selected, err := selectTarget([]discoveredTarget{target}, readEnd, io.Discard)
	if err == nil || selected {
		t.Fatalf("selectTarget(single non-TTY) = selected %v, err %v; want interactive-selection error", selected, err)
	}
}

func discoveryTarget(accountID int64, hostname string, version, capabilities uint32, channels ...rpcpb.SessionChannel) discoveredTarget {
	return discoveredTarget{
		accountID: accountID,
		response: &rpcpb.DiscoverResponse{
			Hostname:        hostname,
			ProtocolVersion: version,
			Capabilities:    capabilities,
			Channels:        channels,
		},
	}
}
