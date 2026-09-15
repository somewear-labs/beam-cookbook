package main

import (
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	rpcpb "somewear/rpc/proto"
)

const (
	discoveryProtocolVersion  uint32 = 1
	capabilityShell           uint32 = 1 << 0
	capabilitySessionChannels uint32 = 1 << 1
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
	requestDatagramID, err := sendDiscoveryProbe(*beamURL, *responseJitter, requestID, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nmap: could not send discovery probe:", err)
		return
	}

	fmt.Printf("Scanning Beam active workspace %d for %s...\n", workspaceID, timeout.String())
	responses, err := responsesForBeamDatagram(*beamURL, requestDatagramID, *timeout, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nmap: could not collect discovery responses:", err)
		return
	}
	targets := collectDiscoveryResponses(requestID, responses)
	printDiscoveredTargets(targets)
}

func collectDiscoveryResponses(requestID uint32, responses []beamDatagram) map[int64]discoveredTarget {
	targets := make(map[int64]discoveredTarget)
	for _, response := range responses {
		envelope, err := unmarshalEnvelope(response.Data)
		if err != nil || envelope.RequestId != requestID || envelope.GetResponse().GetDiscover() == nil {
			continue
		}
		accountID, err := strconv.ParseInt(response.DatagramID.SourceUserID, 10, 64)
		if err != nil || accountID <= 0 {
			continue
		}
		targets[accountID] = discoveredTarget{
			accountID: accountID,
			response:  envelope.GetResponse().GetDiscover(),
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
	if capabilities&capabilitySessionChannels != 0 {
		names = append(names, "session-channels")
		capabilities &^= capabilitySessionChannels
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
