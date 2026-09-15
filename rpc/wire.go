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

var beamHTTPClient = &http.Client{Timeout: 10 * time.Second}

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

func sendIPv4(beamURL string, workspaceID int, targetUserID int64, b64payload string) error {
	bodyMap := map[string]any{
		"ipv4":        map[string]any{"payload": b64payload},
		"collapseKey": 3,
	}
	if workspaceID != 0 {
		bodyMap["workspaceId"] = workspaceID
	}
	if targetUserID != 0 {
		bodyMap["targetUserId"] = targetUserID
	}
	body, _ := json.Marshal(bodyMap)
	resp, err := http.Post(beamURL+"/api/package/ipv4/async", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// fetchSourceUserID queries the local Beam daemon for the current user's
// workspace-scoped member ID. It tries three sources in order:
//  1. /api/account/user-id: direct query (available on Beam builds that include this endpoint)
//  2. outbound payloads: sourceUserId is our member ID
//  3. inbound payloads: targetUserId is our member ID (works on fresh installs
//     that have received at least one packet but never sent)
//
// Returns 0 if unavailable (e.g. daemon unreachable, not yet signed in).
func fetchSourceUserID(beamURL string) int64 {
	if id := fetchUserIDFromAccountEndpoint(beamURL); id != 0 {
		return id
	}
	if id := fetchUserIDFromPayloads(beamURL, "outbound", "sourceUserId"); id != 0 {
		return id
	}
	return fetchUserIDFromPayloads(beamURL, "inbound", "targetUserId")
}

type inboundEnvelope struct {
	envelope     *rpcpb.Envelope
	sourceUserID int64
	receivedAt   time.Time
	datagramID   *beamDatagramID
	inResponseTo *beamDatagramID
}

type webhookEvent struct {
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload"`
	Data         string          `json:"data"`
	DatagramID   *beamDatagramID `json:"datagramId"`
	InResponseTo *beamDatagramID `json:"inResponseTo"`
}

func fetchUserIDFromAccountEndpoint(beamURL string) int64 {
	resp, err := beamHTTPClient.Get(beamURL + "/api/account/user-id")
	if err != nil || resp.StatusCode != http.StatusOK {
		return 0
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}
	var result struct {
		UserID int64 `json:"userId"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0
	}
	return result.UserID
}

func fetchUserIDFromPayloads(beamURL, direction, field string) int64 {
	resp, err := beamHTTPClient.Get(beamURL + "/api/payloads?direction=" + direction + "&limit=1")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}
	var payloads []map[string]json.RawMessage
	if err := json.Unmarshal(body, &payloads); err != nil || len(payloads) == 0 {
		return 0
	}
	var id int64
	if err := json.Unmarshal(payloads[0][field], &id); err != nil {
		return 0
	}
	return id
}

func parseWebhookEnvelopes(body []byte) []inboundEnvelope {
	var data struct {
		Payloads []struct {
			// Legacy fields — not currently emitted by Beam but kept for compatibility.
			SenderUserAccountId int64  `json:"senderUserAccountId"`
			SenderId            string `json:"senderId"`
			// Beam emits the sender under account.id.
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
			Events []webhookEvent `json:"events"`
		} `json:"payloads"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	var out []inboundEnvelope
	for _, p := range data.Payloads {
		var sourceUserID int64
		if p.SenderUserAccountId != 0 {
			sourceUserID = p.SenderUserAccountId
		} else if p.SenderId != "" {
			if id, err := strconv.ParseInt(p.SenderId, 10, 64); err == nil {
				sourceUserID = id
			}
		} else if p.Account.ID != "" {
			if id, err := strconv.ParseInt(p.Account.ID, 10, 64); err == nil {
				sourceUserID = id
			}
		}

		for _, event := range p.Events {
			var env *rpcpb.Envelope
			var err error
			switch event.Type {
			case "Data":
				var payload string
				if err = json.Unmarshal(event.Payload, &payload); err == nil {
					env, err = unmarshalEnvelope(payload)
				}
				if err == nil && env.Namespace != EnvelopeNamespace {
					continue
				}
			case "GridDatagram":
				env, err = gridDatagramEnvelope(event)
			default:
				continue
			}
			if err != nil || env == nil {
				fmt.Printf("  Could not decode RPC event: %v\n", err)
				continue
			}
			out = append(out, inboundEnvelope{
				envelope:     env,
				sourceUserID: sourceUserID,
				receivedAt:   time.Now(),
				datagramID:   event.DatagramID,
				inResponseTo: event.InResponseTo,
			})
		}
	}
	return out
}
