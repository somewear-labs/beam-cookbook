package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	rpcpb "somewear/rpc/proto"
)

type shellSlashCommands struct {
	stdout io.Writer
	stderr io.Writer
	ping   func()
}

func (c shellSlashCommands) handle(command string) bool {
	if !strings.HasPrefix(command, "/") {
		return false
	}

	name, arguments, _ := strings.Cut(command, " ")
	arguments = strings.TrimSpace(arguments)
	switch strings.ToLower(name) {
	case "/help":
		if arguments != "" {
			fmt.Fprintln(c.stderr, "usage: /help")
			return true
		}
		fmt.Fprintln(c.stdout, "Grid Remote Shell commands:")
		fmt.Fprintln(c.stdout, "  /ping                Measure a Grid round trip to the selected target")
		fmt.Fprintln(c.stdout, "  /help                Show this help")
		fmt.Fprintln(c.stdout, "  exit, quit           Close the shell")
	case "/ping":
		if arguments != "" {
			fmt.Fprintln(c.stderr, "usage: /ping")
			return true
		}
		if c.ping != nil {
			c.ping()
		}
	default:
		fmt.Fprintf(c.stderr, "unknown Grid Remote Shell command: %s (try /help)\n", name)
	}
	return true
}

func doPing(
	beamURL string,
	targetUserID int64,
	timeout time.Duration,
	stdout, stderr io.Writer,
	request datagramRequester,
) bool {
	startedAt := time.Now()
	requestID := randomRequestID()
	envelope := &rpcpb.Envelope{
		RequestId: requestID,
		Payload: &rpcpb.Envelope_Request{
			Request: &rpcpb.RpcRequest{
				Method: &rpcpb.RpcRequest_Ping{Ping: &rpcpb.PingRequest{
					ClientSendUnixMillis: startedAt.UnixMilli(),
				}},
			},
		},
	}
	data, err := marshalEnvelope(envelope)
	if err != nil {
		fmt.Fprintln(stderr, "[ping encode error]", err)
		return false
	}
	inbound, err := request(beamURL, targetUserID, data, timeout)
	if err != nil {
		fmt.Fprintln(stderr, "[ping error]", err)
		return false
	}
	responseEnvelope, err := unmarshalEnvelope(inbound.Data)
	if err != nil {
		fmt.Fprintln(stderr, "[ping decode error]", err)
		return false
	}
	if responseEnvelope.GetRequestId() != requestID {
		fmt.Fprintln(stderr, "[ping error] response did not match request")
		return false
	}
	response := responseEnvelope.GetResponse()
	if rpcError := response.GetError(); rpcError != nil {
		fmt.Fprintln(stderr, "[ping error]", rpcError.GetMessage())
		return false
	}
	ping := response.GetPing()
	if ping == nil {
		fmt.Fprintln(stderr, "[ping error] target returned an unexpected response")
		return false
	}
	clientSentAt := time.UnixMilli(startedAt.UnixMilli())
	clientReceivedAt := time.Now()
	targetReceivedAt := time.UnixMilli(ping.GetTargetReceiveUnixMillis())
	targetSentAt := time.UnixMilli(ping.GetTargetSendUnixMillis())
	if ping.GetTargetReceiveUnixMillis() <= 0 || ping.GetTargetSendUnixMillis() <= 0 || targetSentAt.Before(targetReceivedAt) {
		fmt.Fprintln(stderr, "[ping error] target returned invalid timestamps")
		return false
	}
	timings := calculateGridPingTimings(clientSentAt, targetReceivedAt, targetSentAt, clientReceivedAt)

	fmt.Fprintf(stdout, "Grid ping account %d\n", targetUserID)
	fmt.Fprintf(stdout, "  client → target  wall %s · computed %s\n", formatPingDuration(timings.clientToTargetWall), formatPingDuration(timings.clientToTargetComputed))
	fmt.Fprintf(stdout, "  target → client  wall %s · computed %s\n", formatPingDuration(timings.targetToClientWall), formatPingDuration(timings.targetToClientComputed))
	fmt.Fprintf(stdout, "  round trip       wall %s · computed %s\n", formatPingDuration(timings.roundTripWall), formatPingDuration(timings.roundTripComputed))
	fmt.Fprintf(stdout, "  clock offset     %s (%s)\n", formatSignedPingDuration(timings.clockOffset), clockOffsetDirection(timings.clockOffset))
	return true
}

type gridPingTimings struct {
	clientToTargetWall     time.Duration
	targetToClientWall     time.Duration
	roundTripWall          time.Duration
	clientToTargetComputed time.Duration
	targetToClientComputed time.Duration
	roundTripComputed      time.Duration
	clockOffset            time.Duration
}

func calculateGridPingTimings(clientSentAt, targetReceivedAt, targetSentAt, clientReceivedAt time.Time) gridPingTimings {
	clientToTargetWall := targetReceivedAt.Sub(clientSentAt)
	targetToClientWall := clientReceivedAt.Sub(targetSentAt)
	roundTripWall := clientReceivedAt.Sub(clientSentAt)
	targetProcessing := targetSentAt.Sub(targetReceivedAt)
	roundTripComputed := roundTripWall - targetProcessing
	clockOffset := (clientToTargetWall - targetToClientWall) / 2

	return gridPingTimings{
		clientToTargetWall:     clientToTargetWall,
		targetToClientWall:     targetToClientWall,
		roundTripWall:          roundTripWall,
		clientToTargetComputed: clientToTargetWall - clockOffset,
		targetToClientComputed: targetToClientWall + clockOffset,
		roundTripComputed:      roundTripComputed,
		clockOffset:            clockOffset,
	}
}

func formatPingDuration(duration time.Duration) string {
	return duration.Round(500 * time.Microsecond).String()
}

func formatSignedPingDuration(duration time.Duration) string {
	formatted := formatPingDuration(duration)
	if duration > 0 {
		return "+" + formatted
	}
	return formatted
}

func clockOffsetDirection(offset time.Duration) string {
	switch {
	case offset > 0:
		return "target ahead"
	case offset < 0:
		return "target behind"
	default:
		return "clocks aligned"
	}
}
