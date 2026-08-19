package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

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
	body, _ := json.Marshal(map[string]any{
		"workspaceId": workspaceID,
		"ipv4":        map[string]any{"payload": b64payload},
	})
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

// parseWebhookEnvelopes extracts and deserializes all Envelope protos from a Beam webhook POST body.
func parseWebhookEnvelopes(body []byte) []*rpcpb.Envelope {
	var data struct {
		Payloads []struct {
			Events []struct {
				Type    string `json:"type"`
				Payload string `json:"payload"`
			} `json:"events"`
		} `json:"payloads"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	var out []*rpcpb.Envelope
	for _, p := range data.Payloads {
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
			out = append(out, env)
		}
	}
	return out
}
