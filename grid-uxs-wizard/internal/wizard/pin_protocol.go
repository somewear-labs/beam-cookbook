package wizard

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type pinChallenge struct {
	ClaimID       string `json:"claimId"`
	IdentityID    string `json:"identityId"`
	GCSIdentityID string `json:"gcsIdentityId"`
	WorkspaceID   string `json:"workspaceId"`
	Backend       string `json:"backend"`
	Fingerprint   string `json:"fingerprint"`
	ExpiresAt     int64  `json:"expiresAt"`
	Phase         string `json:"phase"`
	Nonce         string `json:"nonce"`
	Proof         string `json:"proof"`
}
type pinProtocolError struct {
	Status        int
	Code, Message string
}

func (e *pinProtocolError) Error() string { return e.Message }
func pinMAC(key, text string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(text))
	return hex.EncodeToString(mac.Sum(nil))
}
func (c pinChallenge) proofText() string {
	return strings.Join([]string{"grid-uxs-pin-server-v1", c.ClaimID, c.IdentityID, c.GCSIdentityID, c.WorkspaceID, c.Backend, c.Fingerprint, strconv.FormatInt(c.ExpiresAt, 10), c.Phase, c.Nonce}, "\n")
}
func pinClientProof(id, identity, nonce, pin string) string {
	return strings.Join([]string{"grid-uxs-pin-client-v1", id, identity, nonce, pin}, "\n")
}
func normalizePIN(value string) (string, error) {
	value = strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(value))
	if len(value) != 6 {
		return "", errors.New("enter the six-digit setup PIN shown by the GCS")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return "", errors.New("enter the six-digit setup PIN shown by the GCS")
		}
	}
	return value, nil
}
func pinHTTPClient(fingerprint string) *http.Client {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	// Discovery authenticates the certificate with an API-key proof before any PIN is sent.
	tlsConfig.InsecureSkipVerify = true
	if fingerprint != "" {
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("GCS certificate is missing")
			}
			digest := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(digest[:]) != fingerprint {
				return errors.New("GCS certificate changed")
			}
			return nil
		}
	}
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func requestPINJSON(ctx context.Context, client *http.Client, origin, path string, input, output any) (*x509.Certificate, error) {
	if err := validateGCSAddress(origin); err != nil {
		return nil, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(origin, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("GCS setup endpoint is not reachable through TVPN")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return nil, errors.New("invalid GCS setup response")
	}
	if response.StatusCode != 200 {
		var failure struct{ Code, Message string }
		_ = json.Unmarshal(data, &failure)
		message := "GCS setup request was rejected"
		switch failure.Code {
		case "PIN_INCORRECT":
			message = "Incorrect setup PIN."
		case "PIN_LOCKED":
			message = "Too many incorrect PINs. Extend the setup window on the GCS, then retry."
		case "SETUP_EXPIRED":
			message = "Extend the setup window on the GCS, then retry with the same PIN."
		}
		return nil, &pinProtocolError{response.StatusCode, failure.Code, message}
	}
	if err := json.Unmarshal(data, output); err != nil {
		return nil, errors.New("invalid GCS setup response")
	}
	if response.TLS == nil || len(response.TLS.PeerCertificates) == 0 {
		return nil, errors.New("GCS certificate is missing")
	}
	return response.TLS.PeerCertificates[0], nil
}
func loadPinChallenge(ctx context.Context, origin, identity, key string) (pinChallenge, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return pinChallenge{}, err
	}
	challengeNonce := base64.RawURLEncoding.EncodeToString(nonce)
	var challenge pinChallenge
	client := pinHTTPClient("")
	defer client.CloseIdleConnections()
	cert, err := requestPINJSON(ctx, client, origin, "/claim/pin/challenge", map[string]string{"identityId": identity, "nonce": challengeNonce}, &challenge)
	if err != nil {
		return pinChallenge{}, err
	}
	digest := sha256.Sum256(cert.Raw)
	expected := pinMAC(key, challenge.proofText())
	if challenge.IdentityID != identity || challenge.Nonce != challengeNonce || !uuidPattern.MatchString(challenge.ClaimID) || !numericID.MatchString(challenge.GCSIdentityID) || !numericID.MatchString(challenge.WorkspaceID) || !backendPattern.MatchString(challenge.Backend) || challenge.Fingerprint != hex.EncodeToString(digest[:]) || !regexpSHA.MatchString(challenge.Proof) || !hmac.Equal([]byte(expected), []byte(challenge.Proof)) {
		return pinChallenge{}, errors.New("GCS setup identity or certificate proof is invalid")
	}
	return challenge, nil
}
func authorizePin(ctx context.Context, origin, key, pin string, challenge pinChallenge) (string, error) {
	if !hmac.Equal([]byte(pinMAC(key, challenge.proofText())), []byte(challenge.Proof)) {
		return "", errors.New("GCS proof is invalid")
	}
	pin, err := normalizePIN(pin)
	if err != nil {
		return "", err
	}
	var result struct {
		ClaimCode string `json:"claimCode"`
	}
	input := map[string]string{"claimId": challenge.ClaimID, "identityId": challenge.IdentityID, "nonce": challenge.Nonce, "pin": pin, "proof": pinMAC(key, pinClientProof(challenge.ClaimID, challenge.IdentityID, challenge.Nonce, pin))}
	client := pinHTTPClient(challenge.Fingerprint)
	defer client.CloseIdleConnections()
	_, err = requestPINJSON(ctx, client, origin, "/claim/pin/authorize", input, &result)
	if err != nil {
		return "", err
	}
	code, err := parseTransportClaim(result.ClaimCode)
	if err != nil || !strings.HasPrefix(code.Code, "uxs1.") || strings.Split(code.Code, ".")[1] != challenge.ClaimID || strings.Split(code.Code, ".")[3] != challenge.Fingerprint {
		return "", fmt.Errorf("GCS returned invalid setup credentials")
	}
	return result.ClaimCode, nil
}
