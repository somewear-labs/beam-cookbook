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
	fmt.Println("Ctrl-C or 'exit' to quit.\n")

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
	}
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
