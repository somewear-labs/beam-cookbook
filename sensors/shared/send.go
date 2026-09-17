// Package shared provides helpers used by all sensor simulator programs.
package shared

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	DefaultBeamURL     = "http://localhost:9091"
	DefaultWorkspaceID = 39054

	// SWL magic bytes that prefix every sensor frame (except weather, which is raw JSON).
	swlMagic0 = 'S'
	swlMagic1 = 'W'
	swlMagic2 = 'L'
)

// WrapSensorPayload builds a SWL-framed payload:
//
//	Byte 0: 'S'
//	Byte 1: 'W'
//	Byte 2: 'L'
//	Byte 3: sensorType
//	Bytes 4+: data (typically JSON-encoded sensor struct)
//
// The result is the standard-encoding base64 string sent as the IPv4Datagram payload.
func WrapSensorPayload(sensorType byte, data []byte) (string, error) {
	frame := make([]byte, 4+len(data))
	frame[0] = swlMagic0
	frame[1] = swlMagic1
	frame[2] = swlMagic2
	frame[3] = sensorType
	copy(frame[4:], data)
	return base64.StdEncoding.EncodeToString(frame), nil
}

// WrapRawPayload base64-encodes raw bytes without any SWL header (used by weather).
func WrapRawPayload(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// SendIPv4 posts a base64 payload to Beam's IPv4Datagram async endpoint.
// It is structurally identical to the sendIPv4 in rpc/wire.go.
func SendIPv4(beamURL string, workspaceID int, b64payload string) error {
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
