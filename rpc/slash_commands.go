package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	rpcpb "somewear/rpc/proto"
)

type shellSlashCommands struct {
	stdout io.Writer
	stderr io.Writer
	ping   func()
	put    func(string)
}

type packageSender func(string, int, int64, string, []rpcpb.SessionChannel) error

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
		fmt.Fprintln(c.stdout, "  /put PATH            Upload a local file to the selected target")
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
	case "/put":
		if arguments == "" {
			fmt.Fprintln(c.stderr, "usage: /put PATH")
			return true
		}
		info, err := os.Stat(arguments)
		if err != nil {
			fmt.Fprintf(c.stderr, "/put: %v\n", err)
			return true
		}
		if !info.Mode().IsRegular() {
			fmt.Fprintln(c.stderr, "/put: path must name a regular file")
			return true
		}
		if c.put == nil {
			fmt.Fprintln(c.stderr, "/put: upload service is unavailable")
			return true
		}
		c.put(arguments)
	default:
		fmt.Fprintf(c.stderr, "unknown Grid Remote Shell command: %s (try /help)\n", name)
	}
	return true
}

func doPing(
	beamURL string,
	workspace int,
	targetUserID int64,
	route sessionRoute,
	timeout time.Duration,
	nextID, pendingID *atomic.Uint32,
	responses <-chan inboundEnvelope,
	stdout, stderr io.Writer,
	send packageSender,
) bool {
	id := nextID.Add(1)
	pendingID.Store(id)
	defer pendingID.Store(0)

	startedAt := time.Now()
	envelope := &rpcpb.Envelope{
		RequestId: id,
		SessionId: route.id,
		Payload: &rpcpb.Envelope_Request{
			Request: &rpcpb.RpcRequest{
				Method: &rpcpb.RpcRequest_Ping{Ping: &rpcpb.PingRequest{
					ClientSendUnixMillis: startedAt.UnixMilli(),
				}},
			},
		},
	}
	payload, err := marshalEnvelope(envelope)
	if err != nil {
		fmt.Fprintln(stderr, "[ping encode error]", err)
		return false
	}
	if err := send(beamURL, workspace, targetUserID, payload, route.channels); err != nil {
		fmt.Fprintln(stderr, "[ping send error]", err)
		return false
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case inbound := <-responses:
			if inbound.envelope.GetRequestId() != id {
				continue
			}
			if inbound.envelope.GetSessionId() != route.id {
				continue
			}
			response := inbound.envelope.GetResponse()
			if ping := response.GetPing(); ping != nil {
				receivedAt := inbound.receivedAt
				if receivedAt.IsZero() {
					receivedAt = time.Now()
				}
				clientSentAt := time.UnixMilli(startedAt.UnixMilli())
				clientReceivedAt := time.UnixMilli(receivedAt.UnixMilli())
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
				requestPackageSentAt := time.Unix(ping.GetClientPackageSendUnixSeconds(), 0)
				if ping.GetClientPackageSendUnixSeconds() > 0 && ping.GetClientAccountId() > 0 && !inbound.packageSentAt.IsZero() {
					fmt.Fprintf(
						stdout,
						"  datagrams        request %s · response %s\n",
						ipv4DatagramID(requestPackageSentAt, ping.GetClientAccountId()),
						ipv4DatagramID(inbound.packageSentAt, targetUserID),
					)
				}
				return true
			}
			if rpcError := response.GetError(); rpcError != nil {
				fmt.Fprintln(stderr, "[ping error]", rpcError.GetMessage())
				return false
			}
		case <-timer.C:
			fmt.Fprintf(stderr, "[ping timeout after %s]\n", timeout)
			return false
		}
	}
}

func ipv4DatagramID(timestamp time.Time, sourceAccountID int64) string {
	buffer := make([]byte, 0, 16)
	timestampBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(timestampBytes, uint32(timestamp.Unix()))
	buffer = append(buffer, timestampBytes...)
	buffer = append(buffer, byte(55)) // PackageType.IPv4Datagram

	var varint [10]byte
	n := binary.PutUvarint(varint[:], uint64(sourceAccountID))
	buffer = append(buffer, varint[:n]...)
	n = binary.PutUvarint(varint[:], 0) // datagram_sequence
	buffer = append(buffer, varint[:n]...)
	return hex.EncodeToString(buffer)
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
