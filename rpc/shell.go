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
	workspace := fs.Int("workspace", defaultWorkspaceID, "Somewear workspace ID")
	timeout := fs.Duration("timeout", 30*time.Second, "How long to wait for a response")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL for sending commands")
	fs.Parse(args)

	var nextID atomic.Uint32
	var pendingID atomic.Uint32
	responses := make(chan *rpcpb.Envelope, 4)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		for _, env := range parseWebhookEnvelopes(body) {
			if env.GetResponse() != nil && env.RequestId == pendingID.Load() {
				responses <- env
			}
		}
	})

	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", *webhookPort), mux); err != nil {
			fmt.Fprintln(os.Stderr, "webhook server error:", err)
			os.Exit(1)
		}
	}()

	fmt.Printf("Somewear remote shell — workspace %d, webhook :%d\n", *workspace, *webhookPort)
	fmt.Println("Ctrl-C or 'exit' to quit.")
	fmt.Println()

	doConnect(*beamURL, *workspace, *timeout, &nextID, &pendingID, responses)

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
		if err := sendIPv4(*beamURL, *workspace, b64); err != nil {
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
			printResponse(resp)
		case <-time.After(*timeout):
			close(stopTicker)
			fmt.Printf("\r  %.2fs — [no response]\n", time.Since(start).Seconds())
		}
		// Clear pendingID so Beam webhook retries between commands don't
		// queue a stale response for the next command's select.
		pendingID.Store(0)
	}
}

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorDim    = "\033[2m"
)

func doConnect(beamURL string, workspace int, timeout time.Duration, nextID, pendingID *atomic.Uint32, responses chan *rpcpb.Envelope) {
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
	if err := sendIPv4(beamURL, workspace, b64); err != nil {
		fmt.Fprintln(os.Stderr, "[connect send error]", err)
		return
	}

	fmt.Printf("%s%sconnecting...%s", colorDim, colorCyan, colorReset)

	select {
	case resp := <-responses:
		fmt.Print("\r\033[K") // clear the "connecting..." line
		if c := resp.GetResponse().GetConnect(); c != nil {
			printConnectBanner(c.Hostname, c.IpAddresses)
		}
	case <-time.After(timeout):
		fmt.Printf("\r\033[K%s[no response — is the server running?]%s\n\n", colorYellow, colorReset)
	}
}

func printConnectBanner(hostname string, ips []string) {
	const inner = 44 // visible chars between the │ borders
	bar := strings.Repeat("─", inner)

	border := func(l, r string) {
		fmt.Printf("%s%s%s%s%s%s\n", colorBold, colorCyan, l, bar, r, colorReset)
	}
	row := func(plain, colored string) {
		pad := inner - len(plain)
		if pad < 0 {
			pad = 0
		}
		fmt.Printf("%s%s│%s%s%s%s%s│%s\n",
			colorBold, colorCyan, colorReset,
			colored, strings.Repeat(" ", pad),
			colorBold, colorCyan, colorReset)
	}
	blank := func() {
		fmt.Printf("%s%s│%*s│%s\n", colorBold, colorCyan, inner, "", colorReset)
	}
	label := func(k, v string) {
		plain := fmt.Sprintf("  %-10s %s", k, v)
		colored := fmt.Sprintf("  %s%-10s%s %s%s%s", colorDim, k, colorReset, colorGreen, v, colorReset)
		row(plain, colored)
	}

	border("┌", "┐")
	row(" connected", fmt.Sprintf(" %s%sconnected%s", colorBold, colorGreen, colorReset))
	blank()
	label("host", hostname)
	for i, ip := range ips {
		k := "ip"
		if i > 0 {
			k = ""
		}
		label(k, ip)
	}
	border("└", "┘")
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
