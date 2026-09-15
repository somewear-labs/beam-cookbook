package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	rpcpb "somewear/rpc/proto"
)

type beamDatagramID struct {
	Timestamp    string `json:"timestamp"`
	PackageType  string `json:"packageType,omitempty"`
	SourceUserID string `json:"sourceUserId"`
	Sequence     uint64 `json:"sequence"`
}

type beamDatagram struct {
	Data         string          `json:"data"`
	DatagramID   beamDatagramID  `json:"datagramId"`
	InResponseTo *beamDatagramID `json:"inResponseTo,omitempty"`
}

type beamDatagramRequest struct {
	Data         string          `json:"data"`
	TargetUserID string          `json:"targetUserId,omitempty"`
	InResponseTo *beamDatagramID `json:"inResponseTo,omitempty"`
}

type beamDatagramResult struct {
	DatagramID beamDatagramID `json:"datagramId"`
}

type beamResponsesRequest struct {
	DatagramID beamDatagramID `json:"datagramId"`
	Limit      int            `json:"limit,omitempty"`
}

type beamResponsesResult struct {
	Responses []beamDatagram `json:"responses"`
}

type datagramRequester func(string, int64, string, time.Duration) (beamDatagram, error)

func requestBeamDatagram(beamURL string, targetUserID int64, data string, timeout time.Duration) (beamDatagram, error) {
	id, err := sendBeamDatagram(beamURL, targetUserID, data, nil)
	if err != nil {
		return beamDatagram{}, err
	}
	responses, err := responsesForBeamDatagram(beamURL, id, timeout, 1)
	if err != nil {
		return beamDatagram{}, err
	}
	if len(responses) == 0 {
		return beamDatagram{}, fmt.Errorf("GridDatagram response timed out")
	}
	return responses[0], nil
}

func broadcastBeamDatagram(beamURL, data string) (beamDatagramID, error) {
	return sendBeamDatagram(beamURL, 0, data, nil)
}

func responsesForBeamDatagram(beamURL string, requestID beamDatagramID, timeout time.Duration, limit int) ([]beamDatagram, error) {
	var result beamResponsesResult
	err := postBeamJSON(
		beamURL,
		endpointWithTimeout("/api/datagrams/responses", timeout),
		beamResponsesRequest{DatagramID: requestID, Limit: limit},
		&result,
	)
	return result.Responses, err
}

func respondWithBeamDatagram(beamURL string, requestID beamDatagramID, data string) error {
	_, err := sendBeamDatagram(beamURL, 0, data, &requestID)
	return err
}

func sendBeamDatagram(beamURL string, targetUserID int64, data string, inResponseTo *beamDatagramID) (beamDatagramID, error) {
	request := beamDatagramRequest{Data: data, InResponseTo: inResponseTo}
	if targetUserID != 0 {
		request.TargetUserID = fmt.Sprint(targetUserID)
	}
	var result beamDatagramResult
	err := postBeamJSON(beamURL, "/api/datagrams", request, &result)
	if err == nil && (result.DatagramID.Timestamp == "" || result.DatagramID.SourceUserID == "") {
		err = fmt.Errorf("Beam returned an invalid datagramId")
	}
	return result.DatagramID, err
}

func postBeamJSON(beamURL, path string, request, result any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	response, err := http.Post(strings.TrimRight(beamURL, "/")+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(response.Body).Decode(result)
}

func endpointWithTimeout(path string, timeout time.Duration) string {
	seconds := int64((timeout + time.Second - 1) / time.Second)
	query := url.Values{"timeoutSeconds": []string{fmt.Sprint(seconds)}}
	return path + "?" + query.Encode()
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
