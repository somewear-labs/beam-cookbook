package main

import (
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	rpcpb "somewear/rpc/proto"
)

const (
	discoveryProtocolVersion  uint32 = 1
	capabilityShell           uint32 = 1 << 0
	maxDiscoveryJitter               = 2 * time.Second
	maxDiscoveryHostnameRunes        = 32
	maxDiscoveryArchRunes            = 16
)

type discoveredTarget struct {
	accountID int64
	response  *rpcpb.DiscoverResponse
}

func runNmap(args []string) {
	fs := flag.NewFlagSet("nmap", flag.ExitOnError)
	webhookPort := fs.Int("webhook-port", 8080, "Port to receive discovery responses on")
	timeout := fs.Duration("timeout", 5*time.Second, "How long to collect discovery responses")
	responseJitter := fs.Duration("response-jitter", 750*time.Millisecond, "Maximum target response jitter")
	beamURL := fs.String("beam-url", defaultBeamURL, "Beam API URL for sending the probe")
	fs.Parse(args)

	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "nmap: --timeout must be greater than zero")
		return
	}
	if *responseJitter < 0 || *responseJitter > maxDiscoveryJitter {
		fmt.Fprintf(os.Stderr, "nmap: --response-jitter must be between 0 and %s\n", maxDiscoveryJitter)
		return
	}
	workspaceID, err := activeWorkspaceID(*beamURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nmap:", err)
		return
	}

	requestID := randomRequestID()
	responses := make(chan inboundEnvelope, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		for _, inbound := range parseWebhookEnvelopes(body) {
			if inbound.envelope.RequestId != requestID || inbound.envelope.GetResponse().GetDiscover() == nil {
				continue
			}
			select {
			case responses <- inbound:
			default:
			}
		}
	})

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *webhookPort))
	if err != nil {
		fmt.Fprintln(os.Stderr, "nmap: could not start webhook listener:", err)
		return
	}
	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "nmap: webhook server error:", err)
		}
	}()
	defer server.Close()

	if err := sendDiscoveryProbe(*beamURL, workspaceID, *responseJitter, requestID); err != nil {
		fmt.Fprintln(os.Stderr, "nmap: could not send discovery probe:", err)
		return
	}

	fmt.Printf("Scanning Beam active workspace %d for %s...\n", workspaceID, timeout.String())
	targets := collectDiscoveryResponses(requestID, *timeout, responses)
	printDiscoveredTargets(targets)
}

func collectDiscoveryResponses(requestID uint32, timeout time.Duration, responses <-chan inboundEnvelope) map[int64]discoveredTarget {
	targets := make(map[int64]discoveredTarget)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

collect:
	for {
		select {
		case inbound := <-responses:
			if inbound.envelope.RequestId != requestID || inbound.envelope.GetResponse().GetDiscover() == nil {
				continue
			}
			targets[inbound.sourceUserID] = discoveredTarget{
				accountID: inbound.sourceUserID,
				response:  inbound.envelope.GetResponse().GetDiscover(),
			}
		case <-timer.C:
			break collect
		}
	}
	return targets
}

func randomRequestID() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		if id := binary.LittleEndian.Uint32(b[:]); id != 0 {
			return id
		}
	}
	return uint32(time.Now().UnixNano()) | 1
}

func printDiscoveredTargets(byAccount map[int64]discoveredTarget) {
	if len(byAccount) == 0 {
		fmt.Println("No Grid Remote Shell targets found.")
		return
	}

	targets := sortedDiscoveredTargets(byAccount)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TARGET\tACCOUNT\tARCH\tVERSION\tFEATURES")
	for _, target := range targets {
		resp := target.response
		fmt.Fprintf(w, "%s\t%d\t%s\t%d\t%s\n",
			resp.GetHostname(), target.accountID, resp.GetArch(), resp.GetProtocolVersion(),
			formatCapabilities(resp.GetCapabilities()))
	}
	w.Flush()
}

func sortedDiscoveredTargets(byAccount map[int64]discoveredTarget) []discoveredTarget {
	targets := make([]discoveredTarget, 0, len(byAccount))
	for _, target := range byAccount {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		left := strings.ToLower(targets[i].response.GetHostname())
		right := strings.ToLower(targets[j].response.GetHostname())
		if left == right {
			return targets[i].accountID < targets[j].accountID
		}
		return left < right
	})
	return targets
}

func formatCapabilities(capabilities uint32) string {
	var names []string
	if capabilities&capabilityShell != 0 {
		names = append(names, "shell")
		capabilities &^= capabilityShell
	}
	if capabilities != 0 {
		names = append(names, fmt.Sprintf("0x%x", capabilities))
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ",")
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
