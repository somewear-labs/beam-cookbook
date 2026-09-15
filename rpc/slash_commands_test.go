package main

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
)

func TestShellSlashCommandsAreHandledLocally(t *testing.T) {
	var stdout, stderr bytes.Buffer
	pingCalls := 0
	commands := shellSlashCommands{
		stdout: &stdout,
		stderr: &stderr,
		ping:   func() { pingCalls++ },
	}

	if commands.handle("echo /ping") {
		t.Fatal("ordinary shell command was intercepted")
	}
	if !commands.handle("/ping") || pingCalls != 1 {
		t.Fatalf("/ping calls = %d", pingCalls)
	}
	if !commands.handle("/help") || !strings.Contains(stdout.String(), "/ping") {
		t.Fatalf("/help output = %q", stdout.String())
	}
	if !commands.handle("/unknown") || !strings.Contains(stderr.String(), "unknown Grid Remote Shell command") {
		t.Fatalf("unknown command output = %q", stderr.String())
	}
}

func TestDoPingReportsRoundTrip(t *testing.T) {
	type sentPing struct {
		requestID uint32
		sentAt    int64
	}
	requestSent := make(chan sentPing, 1)
	route := sessionRoute{id: 77, channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}}
	requestDatagramID := beamDatagramID{Timestamp: "2026-09-15T12:00:00Z", SourceUserID: "42", Sequence: 3}
	send := func(_ string, targetUserID int64, data string) (beamDatagramID, error) {
		if targetUserID != 99 {
			t.Errorf("send account = %d", targetUserID)
		}
		envelope, err := unmarshalEnvelope(data)
		if err != nil {
			t.Errorf("decode ping: %v", err)
			return beamDatagramID{}, nil
		}
		ping := envelope.GetRequest().GetPing()
		if ping == nil || envelope.SessionId != route.id {
			t.Errorf("ping envelope = %+v", envelope)
			return beamDatagramID{}, nil
		}
		requestSent <- sentPing{envelope.RequestId, ping.ClientSendUnixMillis}
		return requestDatagramID, nil
	}

	var pendingID atomic.Uint32
	responses := make(chan inboundEnvelope, 1)
	go func() {
		sent := <-requestSent
		clientSentAt := time.UnixMilli(sent.sentAt)
		responses <- inboundEnvelope{envelope: &rpcpb.Envelope{
			RequestId: sent.requestID,
			SessionId: route.id,
			Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Ping{Ping: &rpcpb.PingResponse{
					TargetReceiveUnixMillis: clientSentAt.Add(10 * time.Millisecond).UnixMilli(),
					TargetSendUnixMillis:    clientSentAt.Add(12 * time.Millisecond).UnixMilli(),
				}},
			}},
		}, inResponseTo: &requestDatagramID, receivedAt: clientSentAt.Add(30 * time.Millisecond)}
	}()

	var stdout, stderr bytes.Buffer
	if !doPing("beam", 99, route, time.Second, &pendingID, responses, &stdout, &stderr, send) {
		t.Fatalf("doPing failed: %s", stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "client → target  wall 10ms · computed 14ms") ||
		!strings.Contains(got, "target → client  wall 18ms · computed 14ms") ||
		!strings.Contains(got, "round trip       wall 30ms · computed 28ms") ||
		!strings.Contains(got, "clock offset     -4ms (target behind)") {
		t.Fatalf("doPing output = %q", got)
	}
	if got := pendingID.Load(); got != 0 {
		t.Fatalf("pending ID = %d", got)
	}
}

func TestCalculateGridPingTimings(t *testing.T) {
	clientSentAt := time.Unix(0, 0)
	timings := calculateGridPingTimings(
		clientSentAt,
		clientSentAt.Add(15*time.Millisecond),
		clientSentAt.Add(20*time.Millisecond),
		clientSentAt.Add(45*time.Millisecond),
	)

	if timings.clientToTargetWall != 15*time.Millisecond ||
		timings.targetToClientWall != 25*time.Millisecond ||
		timings.roundTripWall != 45*time.Millisecond ||
		timings.clientToTargetComputed != 20*time.Millisecond ||
		timings.targetToClientComputed != 20*time.Millisecond ||
		timings.roundTripComputed != 40*time.Millisecond ||
		timings.clockOffset != -5*time.Millisecond {
		t.Fatalf("timings = %+v", timings)
	}
}
