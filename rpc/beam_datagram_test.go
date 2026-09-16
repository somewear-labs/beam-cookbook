package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestBeamDatagramSendsThenCollectsFirstResponse(t *testing.T) {
	requestID := beamDatagramID{
		Timestamp:    "2026-09-15T12:00:00Z",
		SourceUserID: "42",
		Sequence:     1,
	}
	responseID := beamDatagramID{
		Timestamp:    "2026-09-15T12:00:01Z",
		SourceUserID: "99",
		Sequence:     2,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datagrams":
			var request beamDatagramRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if request.TargetUserID != "99" || request.Data != "AQID" {
				t.Errorf("datagram request = %+v", request)
			}
			json.NewEncoder(w).Encode(beamDatagramResult{DatagramID: requestID})
		case "/api/datagrams/responses":
			var request beamResponsesRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if request.DatagramID != requestID || request.Limit != 1 || r.URL.Query().Get("timeoutSeconds") != "2" {
				t.Errorf("responses request = %+v, query = %q", request, r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(beamResponsesResult{Responses: []beamDatagram{{
				Data:         "BA==",
				DatagramID:   responseID,
				InResponseTo: &requestID,
			}}})
		default:
			http.Error(w, fmt.Sprintf("unexpected path %s", r.URL.Path), http.StatusNotFound)
		}
	}))
	defer server.Close()

	response, err := requestBeamDatagram(server.URL, 99, "AQID", 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if response.Data != "BA==" || response.DatagramID != responseID || response.InResponseTo == nil || *response.InResponseTo != requestID {
		t.Fatalf("response = %+v", response)
	}
}
