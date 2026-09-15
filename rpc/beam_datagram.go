package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	rpcpb "somewear/rpc/proto"
)

type beamDatagramID struct {
	Timestamp    string `json:"timestamp"`
	PackageType  string `json:"packageType,omitempty"`
	SourceUserID string `json:"sourceUserId"`
	Sequence     uint64 `json:"sequence"`
}

type beamDatagramRequest struct {
	Data         string          `json:"data"`
	TargetUserID string          `json:"targetUserId,omitempty"`
	InResponseTo *beamDatagramID `json:"inResponseTo,omitempty"`
}

type beamDatagramResult struct {
	DatagramID beamDatagramID `json:"datagramId"`
}

type datagramSender func(string, int64, string) (beamDatagramID, error)

func sendBeamDatagram(beamURL string, targetUserID int64, data string) (beamDatagramID, error) {
	request := beamDatagramRequest{Data: data}
	if targetUserID != 0 {
		request.TargetUserID = fmt.Sprint(targetUserID)
	}
	return postBeamDatagram(beamURL, request)
}

func respondWithBeamDatagram(beamURL string, requestID beamDatagramID, data string) error {
	_, err := postBeamDatagram(beamURL, beamDatagramRequest{Data: data, InResponseTo: &requestID})
	return err
}

func postBeamDatagram(beamURL string, request beamDatagramRequest) (beamDatagramID, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return beamDatagramID{}, err
	}
	response, err := http.Post(strings.TrimRight(beamURL, "/")+"/api/datagrams", "application/json", bytes.NewReader(body))
	if err != nil {
		return beamDatagramID{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return beamDatagramID{}, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	var result beamDatagramResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return beamDatagramID{}, err
	}
	if result.DatagramID.Timestamp == "" || result.DatagramID.SourceUserID == "" {
		return beamDatagramID{}, fmt.Errorf("Beam returned an invalid datagramId")
	}
	return result.DatagramID, nil
}

func gridDatagramEnvelope(event webhookEvent) (*rpcpb.Envelope, error) {
	envelope, err := unmarshalEnvelope(event.Data)
	if err != nil {
		return nil, err
	}
	if envelope.Namespace != EnvelopeNamespace {
		return nil, fmt.Errorf("unexpected RPC namespace %q", envelope.Namespace)
	}
	return envelope, nil
}
