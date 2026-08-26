package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	rpcpb "somewear/rpc/proto"
	"somewear/rpc/stream"
)

func runShell(args []string) {
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	webhookPort := fs.Int("webhook-port", 8080, "Port to receive webhook responses on")
	targetUser := fs.Int64("target-user", 0, "Target Beam user account ID")
	timeout := fs.Duration("timeout", 30*time.Second, "How long to wait for a response")
	discoveryTimeout := fs.Duration("discovery-timeout", 5*time.Second, "How long to collect discovery responses")
	responseJitter := fs.Duration("response-jitter", 750*time.Millisecond, "Maximum discovery response jitter")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL for sending commands")
	channelsValue := fs.String("channels", "", "Constrain session traffic: radio,satellite,cellular,mesh")
	fs.Parse(args)
	channelsSpecified := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "channels" {
			channelsSpecified = true
		}
	})
	route := sessionRoute{}
	if channelsSpecified {
		channels, err := parseSessionChannels(*channelsValue)
		if err != nil {
			fmt.Fprintln(os.Stderr, "shell: --channels:", err)
			return
		}
		route = sessionRoute{id: randomSessionID(), channels: channels}
	}
	workspaceID, err := activeWorkspaceID(*beamURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shell:", err)
		return
	}

	var nextID atomic.Uint32
	nextID.Store(randomRequestID())
	var pendingID atomic.Uint32
	var expectedSource atomic.Int64
	responses := make(chan inboundEnvelope, 64)
	var responseIdempotency idempotencyGuard
	streamRoute := stream.Route{SessionID: route.id, Channels: route.channels}
	streams := stream.NewEndpoint(func(peerAccountID int64, outboundRoute stream.Route, constrained bool, frame *rpcpb.GridStreamFrame) error {
		return sendGridStreamFrame(*beamURL, workspaceID, peerAccountID, sessionRoute{id: outboundRoute.SessionID, channels: outboundRoute.Channels}, constrained, frame)
	})
	outputStreams := make(chan *stream.Stream, 1)
	if err := stream.RegisterCommandOutput(streams, func(output *stream.Stream) {
		outputStreams <- output
	}); err != nil {
		fmt.Fprintln(os.Stderr, "GridStream service error:", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		for _, inbound := range parseWebhookEnvelopes(body) {
			env := inbound.envelope
			if frame := env.GetGridStream(); frame != nil {
				if env.GetSessionId() == route.id && (expectedSource.Load() == 0 || inbound.sourceUserID == expectedSource.Load()) {
					streams.Handle(inbound.sourceUserID, streamRoute, frame)
				}
				continue
			}
			if env.GetResponse() != nil && env.RequestId == pendingID.Load() &&
				(expectedSource.Load() == 0 || inbound.sourceUserID == expectedSource.Load()) &&
				responseIdempotency.acceptResponse(inbound.sourceUserID, env) {
				responses <- inbound
			}
		}
	})

	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", *webhookPort), mux); err != nil {
			fmt.Fprintln(os.Stderr, "webhook server error:", err)
			os.Exit(1)
		}
	}()

	fmt.Printf("Somewear remote shell — Beam active workspace %d, webhook :%d\n", workspaceID, *webhookPort)
	selectedUser := *targetUser
	if selectedUser == 0 {
		if *discoveryTimeout <= 0 {
			fmt.Fprintln(os.Stderr, "shell: --discovery-timeout must be greater than zero")
			return
		}
		if *responseJitter < 0 || *responseJitter > maxDiscoveryJitter {
			fmt.Fprintf(os.Stderr, "shell: --response-jitter must be between 0 and %s\n", maxDiscoveryJitter)
			return
		}

		discoveryID := randomRequestID()
		pendingID.Store(discoveryID)
		if err := sendDiscoveryProbe(*beamURL, workspaceID, *responseJitter, discoveryID, route.channels); err != nil {
			pendingID.Store(0)
			fmt.Fprintln(os.Stderr, "shell: discovery failed:", err)
			return
		}
		fmt.Printf("Discovering targets for %s...\n", discoveryTimeout.String())
		discovered := collectDiscoveryResponses(discoveryID, *discoveryTimeout, responses)
		pendingID.Store(0)

		selectable := selectableShellTargets(discovered, route.channels)
		if len(selectable) == 0 {
			fmt.Println("No compatible Grid Remote Shell targets found.")
			return
		}
		selected, ok, err := selectTarget(selectable, os.Stdin, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "shell:", err)
			return
		}
		if !ok {
			fmt.Println("Selection cancelled.")
			return
		}
		selectedUser = selected.accountID
		fmt.Printf("Selected %s (account %d).\n\n", selected.response.GetHostname(), selectedUser)
	}
	if selectedUser <= 0 {
		fmt.Fprintln(os.Stderr, "shell: target account must be greater than zero")
		return
	}
	expectedSource.Store(selectedUser)

	fmt.Println("Ctrl-C or 'exit' to quit.")
	fmt.Println()

	if !doConnect(*beamURL, workspaceID, selectedUser, route, *timeout, &nextID, &pendingID, responses) {
		return
	}
	if route.id != 0 {
		defer sendDisconnect(*beamURL, workspaceID, selectedUser, route)
	}
	slashCommands := shellSlashCommands{
		stdout: os.Stdout,
		stderr: os.Stderr,
		ping: func() {
			doPing(*beamURL, workspaceID, selectedUser, route, *timeout, &nextID, &pendingID, responses, os.Stdout, os.Stderr, sendIPv4WithChannels)
		},
		put: func(path string) {
			remotePath, err := uploadFileRPC(*beamURL, workspaceID, selectedUser, route, path, *timeout, &nextID, &pendingID, responses, sendFileWithChannels)
			if err != nil {
				fmt.Fprintln(os.Stderr, "[file upload error]", err)
				return
			}
			fmt.Printf("Uploaded %s -> %s\n", path, remotePath)
		},
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			break
		}
		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue
		}
		if strings.EqualFold(command, "exit") || strings.EqualFold(command, "quit") {
			break
		}
		if slashCommands.handle(command) {
			continue
		}

		id := nextID.Add(1)
		pendingID.Store(id)

		env := &rpcpb.Envelope{
			RequestId: id,
			SessionId: route.id,
			Payload: &rpcpb.Envelope_Request{
				Request: &rpcpb.RpcRequest{
					Method: &rpcpb.RpcRequest_Exec{
						Exec: &rpcpb.ExecRequest{Command: command, StreamOutput: true},
					},
				},
			},
		}

		b64, err := marshalEnvelope(env)
		if err != nil {
			fmt.Fprintln(os.Stderr, "[encode error]", err)
			continue
		}
		if err := sendIPv4WithChannels(*beamURL, workspaceID, selectedUser, b64, route.channels); err != nil {
			fmt.Fprintln(os.Stderr, "[send error]", err)
			continue
		}

		start := time.Now()
		stopTicker := make(chan struct{})
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopTicker:
					return
				case <-ticker.C:
					fmt.Printf("\r  waiting... %.1fs", time.Since(start).Seconds())
				}
			}
		}()

		select {
		case output := <-outputStreams:
			close(stopTicker)
			elapsed := time.Since(start)
			fmt.Printf("\r  %.2fs\n", elapsed.Seconds())
			printCommandOutput(output, os.Stdout, os.Stderr)
		case resp := <-responses:
			close(stopTicker)
			fmt.Print("\r\033[K")
			printResponse(resp.envelope)
		case <-time.After(*timeout):
			close(stopTicker)
			fmt.Printf("\r  %.2fs — [no response]\n", time.Since(start).Seconds())
		}
		// Clear pendingID so Beam webhook retries between commands don't
		// queue a stale response for the next command's select.
		pendingID.Store(0)
	}
}

func sendDiscoveryProbe(beamURL string, workspace int, responseJitter time.Duration, requestID uint32, channels []rpcpb.SessionChannel) error {
	env := &rpcpb.Envelope{
		RequestId: requestID,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Discover{Discover: &rpcpb.DiscoverRequest{
				ResponseJitterMs: uint32(responseJitter.Milliseconds()),
				Channels:         channels,
			}},
		}},
	}
	b64, err := marshalEnvelope(env)
	if err != nil {
		return fmt.Errorf("encode probe: %w", err)
	}
	if err := sendIPv4WithChannels(beamURL, workspace, 0, b64, channels); err != nil {
		return fmt.Errorf("send probe: %w", err)
	}
	return nil
}

func selectableShellTargets(discovered map[int64]discoveredTarget, requiredChannels []rpcpb.SessionChannel) []discoveredTarget {
	compatible := make(map[int64]discoveredTarget)
	for accountID, target := range discovered {
		resp := target.response
		requiredCapabilities := capabilityShell | capabilityStreamOutput
		if len(requiredChannels) > 0 {
			requiredCapabilities |= capabilitySessionChannels
		}
		if accountID > 0 && resp.GetProtocolVersion() == discoveryProtocolVersion &&
			resp.GetCapabilities()&requiredCapabilities == requiredCapabilities &&
			(len(requiredChannels) == 0 || sameSessionChannels(resp.GetChannels(), requiredChannels)) {
			compatible[accountID] = target
		}
	}
	return sortedDiscoveredTargets(compatible)
}

func printCommandOutput(output *stream.Stream, stdout, stderr io.Writer) {
	writer := &terminalStreamWriter{writer: stdout}
	if _, err := io.Copy(writer, output); err != nil {
		fmt.Fprintln(stderr, "[stream read error]", err)
		return
	}
	if writer.needsNewline() {
		fmt.Fprintln(stdout)
	}
	if exitCode, message, closed := output.CloseStatus(); closed && exitCode != 0 {
		if message == "" {
			fmt.Fprintf(stderr, "  [exit %d]\n", exitCode)
		} else {
			fmt.Fprintf(stderr, "  [exit %d: %s]\n", exitCode, message)
		}
	}
}

type terminalStreamWriter struct {
	writer   io.Writer
	wrote    bool
	lastByte byte
}

func (w *terminalStreamWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.wrote = true
		w.lastByte = p[n-1]
	}
	return n, err
}

func (w *terminalStreamWriter) needsNewline() bool {
	return w.wrote && w.lastByte != '\n'
}

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorDim    = "\033[2m"
	colorBlue   = "\033[34m"
)

func doConnect(beamURL string, workspace int, targetUserID int64, route sessionRoute, timeout time.Duration, nextID, pendingID *atomic.Uint32, responses <-chan inboundEnvelope) bool {
	id := nextID.Add(1)
	pendingID.Store(id)
	defer pendingID.Store(0)

	env := &rpcpb.Envelope{
		RequestId: id,
		SessionId: route.id,
		Payload: &rpcpb.Envelope_Request{
			Request: &rpcpb.RpcRequest{
				Method: &rpcpb.RpcRequest_Connect{
					Connect: &rpcpb.ConnectRequest{SessionId: route.id, Channels: route.channels},
				},
			},
		},
	}
	b64, err := marshalEnvelope(env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[connect encode error]", err)
		return false
	}
	if err := sendIPv4To(beamURL, workspace, targetUserID, b64); err != nil {
		fmt.Fprintln(os.Stderr, "[connect send error]", err)
		return false
	}

	fmt.Printf("%s%sconnecting...%s", colorDim, colorCyan, colorReset)

	select {
	case inbound := <-responses:
		fmt.Print("\r\033[K") // clear the "connecting..." line
		if c := inbound.envelope.GetResponse().GetConnect(); c != nil {
			accepted, err := validateConnectSession(route, c)
			if err != nil {
				fmt.Fprintln(os.Stderr, "[connect error]", err)
				return false
			}
			printConnectBanner(c)
			if len(accepted) > 0 {
				fmt.Printf("  %schannels%s  %s\n\n", colorDim, colorReset, formatSessionChannels(accepted))
			}
			return true
		}
		if rpcError := inbound.envelope.GetResponse().GetError(); rpcError != nil {
			fmt.Fprintln(os.Stderr, "[connect error]", rpcError.GetMessage())
		}
	case <-time.After(timeout):
		fmt.Printf("\r\033[K%s[no response — is the server running?]%s\n\n", colorYellow, colorReset)
	}
	return false
}

func validateConnectSession(route sessionRoute, response *rpcpb.ConnectResponse) ([]rpcpb.SessionChannel, error) {
	if route.id == 0 {
		return nil, nil
	}
	accepted, err := validateSessionChannels(response.GetChannels())
	if err != nil {
		return nil, fmt.Errorf("target did not accept requested channels: %w", err)
	}
	if response.GetSessionId() != route.id {
		return nil, errors.New("target returned a different session ID")
	}
	if !sameSessionChannels(route.channels, accepted) {
		return nil, fmt.Errorf("target returned channels %s; requested %s", formatSessionChannels(accepted), formatSessionChannels(route.channels))
	}
	return accepted, nil
}

func sendDisconnect(beamURL string, workspace int, targetUserID int64, route sessionRoute) {
	envelope := &rpcpb.Envelope{
		SessionId: route.id,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Disconnect{Disconnect: &rpcpb.DisconnectRequest{}},
		}},
	}
	payload, err := marshalEnvelope(envelope)
	if err == nil {
		_ = sendIPv4WithChannels(beamURL, workspace, targetUserID, payload, route.channels)
	}
}

func printConnectBanner(c *rpcpb.ConnectResponse) {
	sep := fmt.Sprintf("  %s·%s  ", colorDim, colorReset)

	// line 1: hostname + IPs
	fmt.Printf("  %shost%s  %s%s%s", colorDim, colorReset, colorBold, c.Hostname, colorReset)
	for _, ip := range c.IpAddresses {
		fmt.Printf("%s%s%s%s", sep, colorGreen, ip, colorReset)
	}
	fmt.Println()

	// line 2: arch + cpu
	var chips []string
	if c.Arch != "" {
		chips = append(chips, fmt.Sprintf("%s%s%s", colorBlue, c.Arch, colorReset))
	}
	if c.CpuModel != "" {
		chips = append(chips, fmt.Sprintf("%s%s%s", colorDim, c.CpuModel, colorReset))
	}
	if c.CpuCount > 0 {
		chips = append(chips, fmt.Sprintf("%s%d cores%s", colorDim, c.CpuCount, colorReset))
	}
	if len(chips) > 0 {
		fmt.Printf("  %s", strings.Join(chips, sep))
		fmt.Println()
	}

	fmt.Println()
}

func printResponse(env *rpcpb.Envelope) {
	switch r := env.GetResponse().Result.(type) {
	case *rpcpb.RpcResponse_Exec:
		exec := r.Exec
		fmt.Print(exec.Output)
		if !strings.HasSuffix(exec.Output, "\n") {
			fmt.Println()
		}
		if exec.Truncated {
			fmt.Println("  [output truncated]")
		}
		if exec.ExitCode != 0 {
			fmt.Printf("  [exit %d]\n", exec.ExitCode)
		}
	case *rpcpb.RpcResponse_Error:
		fmt.Fprintf(os.Stderr, "[rpc error] %s\n", r.Error.Message)
	}
}
