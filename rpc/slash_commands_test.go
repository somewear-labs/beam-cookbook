package main

import (
	"bytes"
	"strings"
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
	request := func(_ string, targetUserID int64, data string, timeout time.Duration) (beamDatagram, error) {
		if targetUserID != 99 {
			t.Errorf("request account = %d", targetUserID)
		}
		if timeout != time.Second {
			t.Errorf("timeout = %s", timeout)
		}
		envelope, err := unmarshalEnvelope(data)
		if err != nil {
			t.Errorf("decode ping: %v", err)
			return beamDatagram{}, nil
		}
		ping := envelope.GetRequest().GetPing()
		if ping == nil {
			t.Errorf("ping envelope = %+v", envelope)
			return beamDatagram{}, nil
		}
		clientSentAt := time.UnixMilli(ping.ClientSendUnixMillis)
		time.Sleep(30 * time.Millisecond)
		response, err := marshalEnvelope(&rpcpb.Envelope{
			RequestId: envelope.RequestId,
			Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Ping{Ping: &rpcpb.PingResponse{
					TargetReceiveUnixMillis: clientSentAt.Add(10 * time.Millisecond).UnixMilli(),
					TargetSendUnixMillis:    clientSentAt.Add(12 * time.Millisecond).UnixMilli(),
				}},
			}},
		})
		return beamDatagram{Data: response}, err
	}

	var stdout, stderr bytes.Buffer
	if !doPing("beam", 99, time.Second, &stdout, &stderr, request) {
		t.Fatalf("doPing failed: %s", stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "Grid ping account 99") ||
		!strings.Contains(got, "round trip") || !strings.Contains(got, "clock offset") {
		t.Fatalf("doPing output = %q", got)
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
