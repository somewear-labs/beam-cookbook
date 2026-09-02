package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	rpcpb "somewear/rpc/proto"
)

const defaultMaxResponse = 200

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.Int("port", 9091, "Port to listen on")
	maxResponse := fs.Int("max-response", defaultMaxResponse, "Max stdout bytes to return")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL for sending responses")
	fs.Parse(args)
	workspaceID, err := activeWorkspaceID(*beamURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	s := &rpcServer{
		workspaceID: workspaceID,
		maxResponse: *maxResponse,
		beamURL:     *beamURL,
		cwd:         cwd,
		discoveries: make(map[discoveryKey]time.Time),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWebhook)

	fmt.Printf("RPC server listening on :%d\n", *port)
	fmt.Printf("  Beam workspace: %d\n", workspaceID)
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
	cwd         string
	cwdMu       sync.Mutex
	discoveryMu sync.Mutex
	discoveries map[discoveryKey]time.Time
}

type discoveryKey struct {
	sourceUserID int64
	requestID    uint32
}

func (s *rpcServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	fmt.Printf("POST body: %s\n", body)
	w.WriteHeader(http.StatusOK)

	for _, inbound := range parseWebhookEnvelopes(body) {
		env := inbound.envelope
		switch env.Payload.(type) {
		case *rpcpb.Envelope_Request:
			s.handleRequest(env, inbound.sourceUserID)
		case *rpcpb.Envelope_Response:
			fmt.Printf("[req %d] Received response (ignoring)\n", env.RequestId)
		default:
			fmt.Printf("[req %d] Unknown envelope payload type\n", env.RequestId)
		}
	}
}

func (s *rpcServer) handleRequest(env *rpcpb.Envelope, sourceUserID int64) {
	req := env.GetRequest()
	switch req.Method.(type) {
	case *rpcpb.RpcRequest_Exec:
		s.handleExec(env.RequestId, sourceUserID, req.GetExec())
	case *rpcpb.RpcRequest_Connect:
		s.handleConnect(env.RequestId, sourceUserID)
	case *rpcpb.RpcRequest_Discover:
		if s.acceptDiscovery(sourceUserID, env.RequestId) {
			go s.handleDiscover(env.RequestId, sourceUserID, req.GetDiscover())
		}
	default:
		s.sendError(env.RequestId, sourceUserID, fmt.Sprintf("unknown method: %T", req.Method))
	}
}

func (s *rpcServer) acceptDiscovery(sourceUserID int64, requestID uint32) bool {
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()

	now := time.Now()
	for key, seenAt := range s.discoveries {
		if now.Sub(seenAt) > time.Minute {
			delete(s.discoveries, key)
		}
	}
	key := discoveryKey{sourceUserID: sourceUserID, requestID: requestID}
	if _, exists := s.discoveries[key]; exists {
		return false
	}
	s.discoveries[key] = now
	return true
}

func (s *rpcServer) handleDiscover(reqID uint32, targetUserID int64, req *rpcpb.DiscoverRequest) {
	jitter := time.Duration(req.GetResponseJitterMs()) * time.Millisecond
	if jitter > maxDiscoveryJitter {
		jitter = maxDiscoveryJitter
	}
	if jitter > 0 {
		time.Sleep(time.Duration(rand.Int64N(int64(jitter) + 1)))
	}

	hostname, _ := os.Hostname()
	arch, _ := collectCPUInfo()
	resp := &rpcpb.Envelope{
		RequestId: reqID,
		Payload: &rpcpb.Envelope_Response{Response: &rpcpb.RpcResponse{
			Result: &rpcpb.RpcResponse_Discover{Discover: &rpcpb.DiscoverResponse{
				ProtocolVersion: discoveryProtocolVersion,
				Hostname:        truncateRunes(hostname, maxDiscoveryHostnameRunes),
				Arch:            truncateRunes(arch, maxDiscoveryArchRunes),
				Capabilities:    capabilityShell,
			}},
		}},
	}
	b64, err := marshalEnvelope(resp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to marshal discovery response:", err)
		return
	}
	if err := sendIPv4To(s.beamURL, s.workspaceID, targetUserID, b64); err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to send discovery response:", err)
		return
	}
	fmt.Printf("[req %d] Discovery response sent to account %d\n", reqID, targetUserID)
}

func (s *rpcServer) handleExec(reqID uint32, targetUserID int64, req *rpcpb.ExecRequest) {
	s.cwdMu.Lock()
	cwd := s.cwd
	s.cwdMu.Unlock()

	fmt.Printf("[req %d] Exec (cwd=%s): %q\n", reqID, cwd, req.Command)

	// Wrap the user command so that:
	//   1. We start in the tracked cwd (best-effort; falls back to wherever we are if it's gone).
	//   2. After the command we print a NUL sentinel + the new pwd to stdout so we
	//      can capture directory changes (cd, pushd, etc.) without touching the
	//      server process's own working directory.
	wrapped := fmt.Sprintf(
		"cd %s 2>/dev/null; { %s; }; __ec=$?; printf '\\000%%s' \"$(pwd)\"; exit $__ec",
		shellQuote(cwd), req.Command,
	)

	cmd := exec.Command("sh", "-c", wrapped)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var exitCode int
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	// Split stdout on the NUL sentinel to extract the new working directory.
	stdoutBytes := stdout.Bytes()
	if idx := bytes.IndexByte(stdoutBytes, 0); idx >= 0 {
		newCwd := string(stdoutBytes[idx+1:])
		if newCwd != "" {
			s.cwdMu.Lock()
			s.cwd = newCwd
			s.cwdMu.Unlock()
		}
		stdoutBytes = stdoutBytes[:idx]
	}

	out := stdoutBytes
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
	if err := sendIPv4To(s.beamURL, s.workspaceID, targetUserID, b64); err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to send response:", err)
		return
	}
	fmt.Printf("  [req %d] Response sent, %d bytes, exit=%d\n", reqID, len(out), exitCode)
}

func (s *rpcServer) handleConnect(reqID uint32, targetUserID int64) {
	hostname, _ := os.Hostname()

	var ips []string
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil {
				ips = append(ips, ip.String())
			}
		}
	}

	arch, cpuModel := collectCPUInfo()
	cpuCount := int32(runtime.NumCPU())

	fmt.Printf("[connect] hostname=%s ips=%v arch=%s cpu=%s cores=%d\n",
		hostname, ips, arch, cpuModel, cpuCount)

	resp := &rpcpb.Envelope{
		RequestId: reqID,
		Payload: &rpcpb.Envelope_Response{
			Response: &rpcpb.RpcResponse{
				Result: &rpcpb.RpcResponse_Connect{
					Connect: &rpcpb.ConnectResponse{
						Hostname:    hostname,
						IpAddresses: ips,
						Arch:        arch,
						CpuModel:    cpuModel,
						CpuCount:    cpuCount,
					},
				},
			},
		},
	}
	b64, err := marshalEnvelope(resp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to marshal connect response:", err)
		return
	}
	if err := sendIPv4To(s.beamURL, s.workspaceID, targetUserID, b64); err != nil {
		fmt.Fprintln(os.Stderr, "  Failed to send connect response:", err)
	}
}

// shellQuote wraps s in single quotes, escaping any single quotes within.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func collectCPUInfo() (arch, model string) {
	// arch: prefer uname -m (gives "aarch64", "x86_64") over Go's runtime name
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		arch = strings.TrimSpace(string(out))
	}
	if arch == "" {
		arch = runtime.GOARCH
	}

	// CPU model: try /proc/cpuinfo first (Linux), fall back to sysctl (macOS)
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, field := range []string{"Model name", "model name", "Hardware"} {
			for _, line := range strings.Split(string(data), "\n") {
				before, after, ok := strings.Cut(line, ":")
				if ok && strings.TrimSpace(before) == field {
					if v := strings.TrimSpace(after); v != "" {
						return arch, v
					}
				}
			}
		}
	}
	if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return arch, v
		}
	}
	return arch, ""
}

func (s *rpcServer) sendError(reqID uint32, targetUserID int64, message string) {
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
	sendIPv4To(s.beamURL, s.workspaceID, targetUserID, b64)
}
