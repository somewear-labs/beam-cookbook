package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	rpcpb "somewear/rpc/proto"

	"google.golang.org/protobuf/proto"
)

const (
	defaultBeamURL     = "http://localhost:9091"
	defaultWorkspaceID = 39054

	// EnvelopeNamespace is stamped on every outbound Envelope and checked on every
	// inbound one. Any IPv4Datagram that doesn't carry this exact value is discarded
	// before dispatch, preventing accidental execution of non-RPC packets.
	EnvelopeNamespace = "swl.rpc.v1"
)

func activeWorkspaceID(beamURL string) (int, error) {
	resp, err := http.Get(strings.TrimRight(beamURL, "/") + "/api/workspaces")
	if err != nil {
		return 0, fmt.Errorf("query Beam workspaces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("query Beam workspaces: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var workspaces struct {
		ActiveWorkspaceID *string `json:"activeWorkspaceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&workspaces); err != nil {
		return 0, fmt.Errorf("decode Beam workspaces: %w", err)
	}
	if workspaces.ActiveWorkspaceID == nil || *workspaces.ActiveWorkspaceID == "" {
		return 0, fmt.Errorf("Beam has no active workspace; activate one with Beam before using Grid Remote Shell")
	}

	workspaceID, err := strconv.Atoi(*workspaces.ActiveWorkspaceID)
	if err != nil || workspaceID <= 0 {
		return 0, fmt.Errorf("Beam returned invalid active workspace ID %q", *workspaces.ActiveWorkspaceID)
	}
	return workspaceID, nil
}

func marshalEnvelope(env *rpcpb.Envelope) (string, error) {
	env.Namespace = EnvelopeNamespace
	b, err := proto.Marshal(env)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func unmarshalEnvelope(b64 string) (*rpcpb.Envelope, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	var env rpcpb.Envelope
	if err := proto.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

func sendIPv4(beamURL string, workspaceID int, b64payload string) error {
	return sendIPv4To(beamURL, workspaceID, 0, b64payload)
}

func sendIPv4To(beamURL string, workspaceID int, targetUserID int64, b64payload string) error {
	request := map[string]any{
		"workspaceId": workspaceID,
		"ipv4":        map[string]any{"payload": b64payload},
	}
	if targetUserID != 0 {
		request["targetUserId"] = targetUserID
	}
	body, _ := json.Marshal(request)
	resp, err := http.Post(strings.TrimRight(beamURL, "/")+"/api/package/ipv4/async", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

type inboundEnvelope struct {
	envelope      *rpcpb.Envelope
	sourceUserID  int64
	packageSentAt time.Time
	receivedAt    time.Time
}

// parseWebhookEnvelopes extracts and deserializes all Envelope protos from a Beam webhook POST body.
func parseWebhookEnvelopes(body []byte) []inboundEnvelope {
	var data struct {
		Payloads []struct {
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
			Events []struct {
				Type      string `json:"type"`
				Payload   string `json:"payload"`
				Timestamp string `json:"timestamp"`
			} `json:"events"`
		} `json:"payloads"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	var out []inboundEnvelope
	for _, p := range data.Payloads {
		sourceUserID, _ := strconv.ParseInt(p.Account.ID, 10, 64)
		for _, event := range p.Events {
			if event.Type != "Data" {
				continue
			}
			env, err := unmarshalEnvelope(event.Payload)
			if err != nil {
				fmt.Printf("  Could not decode envelope: %v\n", err)
				continue
			}
			if env.Namespace != EnvelopeNamespace {
				fmt.Printf("  Ignoring non-RPC IPv4Datagram (namespace=%q)\n", env.Namespace)
				continue
			}
			packageSentAt, _ := time.Parse(time.RFC3339, event.Timestamp)
			out = append(out, inboundEnvelope{
				envelope:      env,
				sourceUserID:  sourceUserID,
				packageSentAt: packageSentAt,
				receivedAt:    time.Now(),
			})
		}
	}
	return out
}
