package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rpcpb "somewear/rpc/proto"
)

func TestShellSlashCommandsAreHandledLocally(t *testing.T) {
	var stdout, stderr bytes.Buffer
	pingCalls := 0
	uploadedPath := ""
	commands := shellSlashCommands{
		stdout: &stdout,
		stderr: &stderr,
		ping:   func() { pingCalls++ },
		put:    func(path string) { uploadedPath = path },
	}

	if commands.handle("echo /ping") {
		t.Fatal("ordinary shell command was intercepted")
	}
	if !commands.handle("/ping") || pingCalls != 1 {
		t.Fatalf("/ping calls = %d", pingCalls)
	}
	if !commands.handle("/help") || !strings.Contains(stdout.String(), "/put PATH") {
		t.Fatalf("/help output = %q", stdout.String())
	}
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !commands.handle("/put "+path) || uploadedPath != path {
		t.Fatalf("/put path = %q", uploadedPath)
	}
	if !commands.handle("/unknown") || !strings.Contains(stderr.String(), "unknown Grid Remote Shell command") {
		t.Fatalf("unknown command output = %q", stderr.String())
	}
}

func TestFileUploadSlashCommandRequiresUploadService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte("grid file"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	commands := shellSlashCommands{stdout: &stdout, stderr: &stderr}

	if !commands.handle("/put " + path) {
		t.Fatal("/put was not intercepted")
	}
	if got := stderr.String(); !strings.Contains(got, "upload service is unavailable") {
		t.Fatalf("/put stderr = %q", got)
	}
}

func TestDoPingReportsRoundTrip(t *testing.T) {
	requestSent := make(chan int64, 1)
	route := sessionRoute{id: 77, channels: []rpcpb.SessionChannel{rpcpb.SessionChannel_RADIO}}
	send := func(_ string, workspace int, targetUserID int64, payload string, channels []rpcpb.SessionChannel) error {
		if workspace != 7 || targetUserID != 99 {
			t.Errorf("send target = workspace %d, account %d", workspace, targetUserID)
		}
		envelope, err := unmarshalEnvelope(payload)
		if err != nil {
			t.Errorf("decode PingRequest: %v", err)
			return nil
		}
		ping := envelope.GetRequest().GetPing()
		if ping == nil {
			t.Error("send payload is not PingRequest")
			return nil
		}
		if envelope.GetSessionId() != route.id || !sameSessionChannels(channels, route.channels) {
			t.Errorf("ping route = session %d, channels %v", envelope.GetSessionId(), channels)
		}
		requestSent <- ping.GetClientSendUnixMillis()
		return nil
	}

	var nextID, pendingID atomic.Uint32
	nextID.Store(41)
	responses := make(chan inboundEnvelope, 1)
	go func() {
		clientSentAt := time.UnixMilli(<-requestSent)
		requestPackageSentAt := clientSentAt.Truncate(time.Second)
		responsePackageSentAt := clientSentAt.Add(12 * time.Millisecond).Truncate(time.Second)
		responses <- inboundEnvelope{envelope: &rpcpb.Envelope{
			RequestId: 42,
			SessionId: route.id,
			Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Ping{Ping: &rpcpb.PingResponse{
					TargetReceiveUnixMillis:      clientSentAt.Add(10 * time.Millisecond).UnixMilli(),
					TargetSendUnixMillis:         clientSentAt.Add(12 * time.Millisecond).UnixMilli(),
					ClientPackageSendUnixSeconds: requestPackageSentAt.Unix(),
					ClientAccountId:              88,
				}},
			}},
		}, packageSentAt: responsePackageSentAt, receivedAt: clientSentAt.Add(30 * time.Millisecond)}
	}()

	var stdout, stderr bytes.Buffer
	if !doPing("beam", 7, 99, route, time.Second, &nextID, &pendingID, responses, &stdout, &stderr, send) {
		t.Fatalf("doPing failed: %s", stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "client → target  wall 10ms · computed 14ms") ||
		!strings.Contains(got, "target → client  wall 18ms · computed 14ms") ||
		!strings.Contains(got, "round trip       wall 30ms · computed 28ms") ||
		!strings.Contains(got, "clock offset     -4ms (target behind)") ||
		!strings.Contains(got, "datagrams        request ") ||
		!strings.Contains(got, " · response ") {
		t.Fatalf("doPing output = %q", got)
	}
	if got := pendingID.Load(); got != 0 {
		t.Fatalf("pending ID = %d", got)
	}
}

func TestIPv4DatagramIDMatchesGridEncoding(t *testing.T) {
	got := ipv4DatagramID(time.Unix(1_700_000_000, 0), 300)
	want := "6553f10037ac0200"
	if got != want {
		t.Fatalf("ipv4DatagramID() = %q, want %q", got, want)
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
