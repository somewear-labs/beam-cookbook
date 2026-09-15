package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	rpcpb "somewear/rpc/proto"
)

func runShell(args []string) {
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	webhookPort := fs.Int("webhook-port", 8080, "Port to receive webhook responses on")
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

	var nextID atomic.Uint32
	var pendingID atomic.Uint32
	var expectedSource atomic.Int64
	responses := make(chan inboundEnvelope, 64)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		for _, inbound := range parseWebhookEnvelopes(body) {
			env := inbound.envelope
			if env.GetResponse() != nil && env.RequestId == pendingID.Load() &&
				(expectedSource.Load() == 0 || inbound.sourceUserID == expectedSource.Load()) {
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
		if err := sendDiscoveryProbe(*beamURL, workspaceID, *responseJitter, discoveryID); err != nil {
			pendingID.Store(0)
			fmt.Fprintln(os.Stderr, "shell: discovery failed:", err)
			return
		}
		fmt.Printf("Discovering targets for %s...\n", discoveryTimeout.String())
		discovered := collectDiscoveryResponses(discoveryID, *discoveryTimeout, responses)
		pendingID.Store(0)

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
	expectedSource.Store(selectedUser)

	fmt.Println("Ctrl-C or 'exit' to quit.")
	fmt.Println()

	doConnect(*beamURL, workspaceID, selectedUser, *timeout, &nextID, &pendingID, responses)
	slashCommands := shellSlashCommands{
		stdout: os.Stdout,
		stderr: os.Stderr,
		ping: func() {
			doPing(*beamURL, workspaceID, selectedUser, *timeout, &nextID, &pendingID, responses, os.Stdout, os.Stderr, sendIPv4To)
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
			Payload: &rpcpb.Envelope_Request{
				Request: &rpcpb.RpcRequest{
					Method: &rpcpb.RpcRequest_Exec{
						Exec: &rpcpb.ExecRequest{Command: command},
					},
				},
			},
		}

		b64, err := marshalEnvelope(env)
		if err != nil {
			fmt.Fprintln(os.Stderr, "[encode error]", err)
			continue
		}
		if err := sendIPv4To(*beamURL, workspaceID, selectedUser, b64); err != nil {
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
		case resp := <-responses:
			close(stopTicker)
			elapsed := time.Since(start)
			fmt.Printf("\r  %.2fs\n", elapsed.Seconds())
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

func sendDiscoveryProbe(beamURL string, workspace int, responseJitter time.Duration, requestID uint32) error {
	env := &rpcpb.Envelope{
		RequestId: requestID,
		Payload: &rpcpb.Envelope_Request{Request: &rpcpb.RpcRequest{
			Method: &rpcpb.RpcRequest_Discover{Discover: &rpcpb.DiscoverRequest{
				ResponseJitterMs: uint32(responseJitter.Milliseconds()),
			}},
		}},
	}
	b64, err := marshalEnvelope(env)
	if err != nil {
		return fmt.Errorf("encode probe: %w", err)
	}
	if err := sendIPv4To(beamURL, workspace, 0, b64); err != nil {
		return fmt.Errorf("send probe: %w", err)
	}
	return nil
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

func doConnect(beamURL string, workspace int, targetUserID int64, timeout time.Duration, nextID, pendingID *atomic.Uint32, responses chan inboundEnvelope) {
	id := nextID.Add(1)
	pendingID.Store(id)
	defer pendingID.Store(0)

	env := &rpcpb.Envelope{
		RequestId: id,
		Payload: &rpcpb.Envelope_Request{
			Request: &rpcpb.RpcRequest{
				Method: &rpcpb.RpcRequest_Connect{
					Connect: &rpcpb.ConnectRequest{},
				},
			},
		},
	}
	b64, err := marshalEnvelope(env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[connect encode error]", err)
		return
	}
	if err := sendIPv4To(beamURL, workspace, targetUserID, b64); err != nil {
		fmt.Fprintln(os.Stderr, "[connect send error]", err)
		return
	}

	fmt.Printf("%s%sconnecting...%s", colorDim, colorCyan, colorReset)

	select {
	case resp := <-responses:
		fmt.Print("\r\033[K") // clear the "connecting..." line
		if c := resp.envelope.GetResponse().GetConnect(); c != nil {
			printConnectBanner(c)
		}
	case <-time.After(timeout):
		fmt.Printf("\r\033[K%s[no response — is the server running?]%s\n\n", colorYellow, colorReset)
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
