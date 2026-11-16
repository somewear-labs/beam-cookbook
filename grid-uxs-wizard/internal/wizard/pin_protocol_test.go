package wizard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pinFixture(t *testing.T, mutate func(*pinChallenge)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	key := "testkey:secret"
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("API key must not be sent as a header")
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request body")
			w.WriteHeader(400)
			return
		}
		fingerprint := sha256.Sum256(server.Certificate().Raw)
		fp := hex.EncodeToString(fingerprint[:])
		if r.URL.Path == "/claim/pin/challenge" {
			if len(body) != 2 || body["identityId"] != "200" || len(body["nonce"]) != 43 {
				t.Error("discovery must contain only public identity and nonce")
			}
			c := pinChallenge{ClaimID: "11111111-1111-1111-1111-111111111111", IdentityID: "200", GCSIdentityID: "100", WorkspaceID: "75605", Backend: "api.somewear.co", Fingerprint: fp, ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Phase: "Available", Nonce: body["nonce"]}
			if mutate != nil {
				mutate(&c)
			}
			c.Proof = pinMAC(key, c.proofText())
			_ = json.NewEncoder(w).Encode(c)
			return
		}
		if r.URL.Path == "/claim/pin/authorize" {
			calls.Add(1)
			expected := pinMAC(key, pinClientProof(body["claimId"], body["identityId"], body["nonce"], body["pin"]))
			if body["proof"] != expected {
				t.Error("client API-key proof mismatch")
				w.WriteHeader(401)
				return
			}
			if body["pin"] != "123456" {
				w.WriteHeader(401)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "PIN_INCORRECT", "message": "ignore remote message"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"claimCode": "uxs1.11111111-1111-1111-1111-111111111111." + strings.Repeat("a", 43) + "." + fp})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(server.Close)
	return server, calls
}
func TestPINProtocolAuthenticatesCertificateThenAuthorizes(t *testing.T) {
	server, calls := pinFixture(t, nil)
	c, err := loadPinChallenge(context.Background(), server.URL, "200", "testkey:secret")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("PIN sent during discovery")
	}
	code, err := authorizePin(context.Background(), server.URL, "testkey:secret", "123-456", c)
	if err != nil || !validClaimCode(code) || calls.Load() != 1 {
		t.Fatalf("PIN exchange failed: %v", err)
	}
}
func TestPINDiscoveryRejectsWrongKeyIdentityOrCertificate(t *testing.T) {
	for _, variant := range []string{"key", "identity", "certificate"} {
		t.Run(variant, func(t *testing.T) {
			server, calls := pinFixture(t, func(c *pinChallenge) {
				if variant == "identity" {
					c.IdentityID = "201"
				}
				if variant == "certificate" {
					c.Fingerprint = strings.Repeat("a", 64)
				}
			})
			key := "testkey:secret"
			if variant == "key" {
				key = "otherkey:secret"
			}
			if _, err := loadPinChallenge(context.Background(), server.URL, "200", key); err == nil {
				t.Fatal("unverified GCS accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("PIN sent to unverified GCS")
			}
		})
	}
}
func TestPINAuthorizationReportsWrongPINWithoutReplacingSetup(t *testing.T) {
	server, _ := pinFixture(t, nil)
	c, err := loadPinChallenge(context.Background(), server.URL, "200", "testkey:secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = authorizePin(context.Background(), server.URL, "testkey:secret", "000000", c)
	var rejection *pinProtocolError
	if !errors.As(err, &rejection) || rejection.Code != "PIN_INCORRECT" || strings.Contains(err.Error(), "ignore remote") {
		t.Fatalf("wrong PIN error: %v", err)
	}
}
func TestPINAuthorizationPinsCertificateBeforeSendingSecret(t *testing.T) {
	server, calls := pinFixture(t, nil)
	c, err := loadPinChallenge(context.Background(), server.URL, "200", "testkey:secret")
	if err != nil {
		t.Fatal(err)
	}
	c.Fingerprint = strings.Repeat("a", 64)
	c.Proof = pinMAC("testkey:secret", c.proofText())
	if _, err := authorizePin(context.Background(), server.URL, "testkey:secret", "123456", c); err == nil {
		t.Fatal("changed certificate accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("PIN sent before certificate validation")
	}
}
func TestPINFormat(t *testing.T) {
	for _, value := range []string{"123456", "123-456", "123 456"} {
		if pin, err := normalizePIN(value); err != nil || pin != "123456" {
			t.Fatal("valid PIN rejected")
		}
	}
	for _, value := range []string{"12345", "1234567", "abcdef", "１２３４５６"} {
		if _, err := normalizePIN(value); err == nil {
			t.Fatal("invalid PIN accepted")
		}
	}
}

func TestPINProofMatchesBeamInteroperabilityVector(t *testing.T) {
	c := pinChallenge{ClaimID: "11111111-1111-1111-1111-111111111111", IdentityID: "200", GCSIdentityID: "100", WorkspaceID: "75605", Backend: "api.somewear.co", Fingerprint: strings.Repeat("a", 64), ExpiresAt: 1234567890000, Phase: "Available", Nonce: strings.Repeat("n", 43)}
	if pinMAC("testkey:secret", c.proofText()) != "a41ac157df2e835151ef73a9c5cdb3b429a061a454ed321b563d59e400578901" {
		t.Fatal("server proof format differs from Beam")
	}
	if pinMAC("testkey:secret", pinClientProof(c.ClaimID, "200", c.Nonce, "123456")) != "e8dd7acfdaee81146cd03875702f49cc4820fd90032b2c8b53d079d7ad361ced" {
		t.Fatal("client proof format differs from Beam")
	}
}

func TestPINDiscoverySelectsOnlyMatchingWorkspaceAndPeer(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "wrong-workspace"}[matching], func(t *testing.T) {
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/workspace":
					w.Write([]byte(`{"activeWorkspaceId":"75605","workspaces":[{"id":"75605"}]}`))
				case "/api/workspace/activate":
					w.Write([]byte(`{}`))
				case "/api/identity":
					w.Write([]byte(`{"identityId":"200"}`))
				case "/api/net/peer":
					w.Write([]byte(`{"peers":[{"identityId":"100","hostname":"gcs","overlayAddress":"100.69.99.108"},{"identityId":"201","hostname":"external","overlayAddress":"8.8.8.8"}]}`))
				default:
					t.Error("unexpected TVPN API request")
					w.WriteHeader(404)
				}
			}))
			defer apiServer.Close()
			api, _ := newLocalAPI(apiServer.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			candidate, err := findPinGCS(ctx, api, "testkey:secret", func() bool { return false }, func(_ context.Context, origin, identity, key string) (pinChallenge, error) {
				if origin != "https://100.69.99.108:9445" || identity != "200" || key != "testkey:secret" {
					t.Error("discovery escaped expected TVPN peer/identity")
				}
				workspace := "75605"
				if !matching {
					workspace = "999"
				}
				return pinChallenge{GCSIdentityID: "100", WorkspaceID: workspace}, nil
			})
			if matching && (err != nil || candidate.Origin != "https://100.69.99.108:9445") {
				t.Fatalf("matching discovery: %v", err)
			}
			if !matching && err == nil {
				t.Fatal("wrong Workspace accepted")
			}
		})
	}
}

func TestPINDiscoveryWaitsInActiveWorkspaceWithoutReactivating(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspace":
			fmt.Fprint(w, `{"activeWorkspaceId":"75605","workspaces":[{"id":"22902"},{"id":"75605"}]}`)
		case "/api/workspace/activate":
			t.Error("active Workspace was replaced before discovery settled")
			w.WriteHeader(500)
		case "/api/identity":
			fmt.Fprint(w, `{"identityId":"200"}`)
		case "/api/net/peer":
			if polls.Add(1) < 2 {
				fmt.Fprint(w, `{"peers":[]}`)
			} else {
				fmt.Fprint(w, `{"peers":[{"identityId":"100","hostname":"gcs","overlayAddress":"100.69.99.108"}]}`)
			}
		default:
			t.Error("unexpected route")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api, _ := newLocalAPI(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	result, err := findPinGCS(ctx, api, "testkey:secret", func() bool { return false }, func(context.Context, string, string, string) (pinChallenge, error) {
		return pinChallenge{GCSIdentityID: "100", WorkspaceID: "75605"}, nil
	})
	if err != nil || result.Challenge.GCSIdentityID != "100" {
		t.Fatalf("delayed discovery: %v", err)
	}
}
