package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	rpcpb "somewear/rpc/proto"
)

func runShell(args []string) {
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	fs.Int("webhook-port", 8080, "Deprecated; GridDatagram shell does not use a local webhook")
	targetUser := fs.Int64("target-user", 0, "Target Beam user account ID")
	timeout := fs.Duration("timeout", 30*time.Second, "How long to wait for a response")
	discoveryTimeout := fs.Duration("discovery-timeout", 5*time.Second, "How long to collect discovery responses")
	responseJitter := fs.Duration("response-jitter", 750*time.Millisecond, "Maximum discovery response jitter")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL for sending commands")
	fs.Parse(args)
	workspaceID, err := activeWorkspaceID(*beamURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shell:", err)
		return
	}

	fmt.Printf("Somewear remote shell — Beam active workspace %d\n", workspaceID)
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
		requestDatagramID, err := sendDiscoveryProbe(*beamURL, *responseJitter, discoveryID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "shell: discovery failed:", err)
			return
		}
		fmt.Printf("Discovering targets for %s...\n", discoveryTimeout.String())
		discoveryResponses, err := responsesForBeamDatagram(*beamURL, requestDatagramID, *discoveryTimeout, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "shell: discovery failed:", err)
			return
		}
		discovered := collectDiscoveryResponses(discoveryID, discoveryResponses)

		selectable := selectableShellTargets(discovered)
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
	fmt.Println("Ctrl-C or 'exit' to quit.")
	fmt.Println()

	if !doConnect(*beamURL, selectedUser, *timeout) {
		return
	}
	streams := &streamClient{beamURL: *beamURL, targetUserID: selectedUser, stdout: os.Stdout, stderr: os.Stderr}
	defer streams.closeAll()
	slashCommands := shellSlashCommands{
		stdout: os.Stdout,
		stderr: os.Stderr,
		ping: func() {
			doPing(*beamURL, selectedUser, *timeout, os.Stdout, os.Stderr, requestBeamDatagram)
		},
		start: streams.start,
		close: streams.close,
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

		env := &rpcpb.Envelope{
			RequestId: randomRequestID(),
			Payload: &rpcpb.Envelope_Request{
				Request: &rpcpb.RpcRequest{
					Method: &rpcpb.RpcRequest_Exec{
						Exec: &rpcpb.ExecRequest{Command: command},
					},
				},
			},
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

		response, err := requestRPC(*beamURL, selectedUser, env, *timeout)
		close(stopTicker)
		fmt.Printf("\r  %.2fs\n", time.Since(start).Seconds())
		if err != nil {
			fmt.Fprintln(os.Stderr, "[command error]", err)
			continue
		}
		printResponse(response)
	}
}

func sendDiscoveryProbe(beamURL string, responseJitter time.Duration, requestID uint32) (beamDatagramID, error) {
	envelope := &rpcpb.Envelope{
		RequestId: requestID,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Discover{Discover: &rpcpb.DiscoverRequest{
				ResponseJitterMs: uint32(responseJitter.Milliseconds()),
			}},
		}},
	}
	data, err := marshalEnvelope(envelope)
	if err != nil {
		return beamDatagramID{}, fmt.Errorf("encode probe: %w", err)
	}
	id, err := broadcastBeamDatagram(beamURL, data)
	if err != nil {
		return beamDatagramID{}, fmt.Errorf("send probe: %w", err)
	}
	return id, nil
}

func selectableShellTargets(discovered map[int64]discoveredTarget) []discoveredTarget {
	compatible := make(map[int64]discoveredTarget)
	for accountID, target := range discovered {
		resp := target.response
		if accountID > 0 && resp.GetProtocolVersion() == discoveryProtocolVersion && resp.GetCapabilities()&capabilityShell != 0 {
			compatible[accountID] = target
		}
	}
	return sortedDiscoveredTargets(compatible)
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

func doConnect(beamURL string, targetUserID int64, timeout time.Duration) bool {
	env := &rpcpb.Envelope{
		RequestId: randomRequestID(),
		Payload: &rpcpb.Envelope_Request{
			Request: &rpcpb.RpcRequest{
				Method: &rpcpb.RpcRequest_Connect{
					Connect: &rpcpb.ConnectRequest{},
				},
			},
		},
	}
	fmt.Printf("%s%sconnecting...%s", colorDim, colorCyan, colorReset)
	response, err := requestRPC(beamURL, targetUserID, env, timeout)
	fmt.Print("\r\033[K")
	if err != nil {
		fmt.Fprintln(os.Stderr, "[connect error]", err)
		return false
	}
	connect := response.GetResponse().GetConnect()
	if connect == nil {
		fmt.Fprintln(os.Stderr, "[connect error] target returned an unexpected response")
		return false
	}
	printConnectBanner(connect)
	return true
}

func requestRPC(beamURL string, targetUserID int64, request *rpcpb.Envelope, timeout time.Duration) (*rpcpb.Envelope, error) {
	data, err := marshalEnvelope(request)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	response, err := requestBeamDatagram(beamURL, targetUserID, data, timeout)
	if err != nil {
		return nil, err
	}
	if response.DatagramID.SourceUserID != strconv.FormatInt(targetUserID, 10) {
		return nil, fmt.Errorf("response came from unexpected account %s", response.DatagramID.SourceUserID)
	}
	envelope, err := gridDatagramEnvelope(webhookEvent{Data: response.Data})
	if err != nil {
		return nil, err
	}
	if envelope.GetRequestId() != request.GetRequestId() || envelope.GetResponse() == nil {
		return nil, fmt.Errorf("response did not match request")
	}
	return envelope, nil
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
