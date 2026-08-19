package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"

	rpcpb "somewear/rpc/proto"
)

const defaultMaxResponse = 200

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.Int("port", 9091, "Port to listen on")
	workspace := fs.Int("workspace", defaultWorkspaceID, "Somewear workspace ID")
	maxResponse := fs.Int("max-response", defaultMaxResponse, "Max stdout bytes to return")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL for sending responses")
	fs.Parse(args)

	s := &rpcServer{
		workspaceID: *workspace,
		maxResponse: *maxResponse,
		beamURL:     *beamURL,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWebhook)

	fmt.Printf("RPC server listening on :%d\n", *port)
	fmt.Printf("  Workspace ID : %d\n", *workspace)
	fmt.Printf("  Max response : %d bytes\n", *maxResponse)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type rpcServer struct {
	workspaceID int
	maxResponse int
	beamURL     string
}

func (s *rpcServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	fmt.Printf("POST body: %s\n", body)
	w.WriteHeader(http.StatusOK)

	for _, env := range parseWebhookEnvelopes(body) {
		switch env.Payload.(type) {
		case *rpcpb.Envelope_Request:
			s.handleRequest(env)
		case *rpcpb.Envelope_Response:
			fmt.Printf("[req %d] Received response (ignoring)\n", env.RequestId)
		default:
			fmt.Printf("[req %d] Unknown envelope payload type\n", env.RequestId)
		}
	}
}

func (s *rpcServer) handleRequest(env *rpcpb.Envelope) {
	req := env.GetRequest()
	switch req.Method.(type) {
	case *rpcpb.RpcRequest_Exec:
		s.handleExec(env.RequestId, req.GetExec())
	default:
		s.sendError(env.RequestId, fmt.Sprintf("unknown method: %T", req.Method))
	}
}

func (s *rpcServer) handleExec(reqID uint32, req *rpcpb.ExecRequest) {
	fmt.Printf("[req %d] Exec: %q\n", reqID, req.Command)

	cmd := exec.Command("sh", "-c", req.Command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var exitCode int
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	out := stdout.Bytes()
	if len(out) == 0 {
		out = stderr.Bytes()
	}

	truncated := len(out) > s.maxResponse
	if truncated {
		out = out[:s.maxResponse]
	}

	resp := &rpcpb.Envelope{
		RequestId: reqID,
		Payload: &rpcpb.Envelope_Response{
			Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Exec{
					Exec: &rpcpb.ExecResponse{
						Output:    string(out),
						ExitCode:  int32(exitCode),
						Truncated: truncated,
					},
				},
			},
		},
	}

	b64, err := marshalEnvelope(resp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to marshal response:", err)
		return
	}
	if err := sendIPv4(s.beamURL, s.workspaceID, b64); err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to send response:", err)
		return
	}
	fmt.Printf("  [req %d] Response sent, %d bytes, exit=%d\n", reqID, len(out), exitCode)
}

func (s *rpcServer) sendError(reqID uint32, message string) {
	resp := &rpcpb.Envelope{
		RequestId: reqID,
		Payload: &rpcpb.Envelope_Response{
			Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Error{
					Error: &rpcpb.RpcError{Message: message},
				},
			},
		},
	}
	b64, err := marshalEnvelope(resp)
	if err != nil {
		return
	}
	sendIPv4(s.beamURL, s.workspaceID, b64)
}
