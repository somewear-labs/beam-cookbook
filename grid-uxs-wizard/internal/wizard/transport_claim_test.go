package wizard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testRawClaim() string {
	return "uxs1.11111111-1111-1111-1111-111111111111." + strings.Repeat("s", 43) + "." + strings.Repeat("a", 64)
}
func testTransportClaim() transportClaim {
	return transportClaim{Code: testRawClaim(), Hostname: "gcs.local", Backend: "api.somewear.co", WorkspaceID: "75605", GCSIdentityID: "298002", IdentityID: "351092"}
}

func TestPortableClaimKeepsPinAndRejectsInvalidMetadata(t *testing.T) {
	claim := testTransportClaim()
	code, err := encodeTransportClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := parseTransportClaim(code)
	if err != nil || actual != claim {
		t.Fatal("portable claim lost metadata", err)
	}
	for _, mutate := range []func(*transportClaim){func(c *transportClaim) { c.IdentityID = c.GCSIdentityID }, func(c *transportClaim) { c.Hostname = "https://attacker/path" }, func(c *transportClaim) { c.Backend = "host\nkey=bad" }, func(c *transportClaim) { c.Code = "invalid" }, func(c *transportClaim) { c.WorkspaceID = "0" }} {
		invalid := claim
		mutate(&invalid)
		data, _ := json.Marshal(invalid)
		if validClaimCode("uxst1." + base64.RawURLEncoding.EncodeToString(data)) {
			t.Fatal("accepted invalid claim metadata")
		}
	}
	raw, err := parseTransportClaim(testRawClaim())
	if err != nil || raw.Code != claim.Code || raw.Hostname != "" {
		t.Fatal("legacy claim no longer supported")
	}
}

func TestResolveGCSRequiresIdentityAndHostnameAndUniquePeer(t *testing.T) {
	claim := testTransportClaim()
	matching := tvpnPeer{IdentityID: wireID(claim.GCSIdentityID), Hostname: claim.Hostname, OverlayAddress: "10.128.0.8"}
	wrongID := matching
	wrongID.IdentityID = "123"
	wrongHost := matching
	wrongHost.Hostname = "other"
	address, err := resolveGCSPeer([]tvpnPeer{wrongID, wrongHost}, claim)
	if err != nil || address != "" {
		t.Fatal("unrelated peer selected")
	}
	address, err = resolveGCSPeer([]tvpnPeer{wrongID, matching}, claim)
	if err != nil || address != "https://10.128.0.8:9445" {
		t.Fatal(address, err)
	}
	if _, err := resolveGCSPeer([]tvpnPeer{matching, matching}, claim); err == nil {
		t.Fatal("ambiguous GCS selected")
	}
	matching.OverlayAddress = "127.0.0.1"
	if _, err := resolveGCSPeer([]tvpnPeer{matching}, claim); err == nil {
		t.Fatal("loopback accepted")
	}
}

func TestBootstrapActivatesWorkspaceAndValidatesIdentityBeforeResolving(t *testing.T) {
	for _, identity := range []string{"351092", "999999"} {
		t.Run(identity, func(t *testing.T) {
			active := false
			queried := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/workspace":
					if active {
						json.NewEncoder(w).Encode(map[string]any{"workspaces": []map[string]any{{"id": 75605}}, "activeWorkspaceId": 75605})
					} else {
						json.NewEncoder(w).Encode(map[string]any{"workspaces": []map[string]string{{"id": "75605"}}})
					}
				case "/api/workspace/activate":
					if r.Method != "PUT" {
						t.Error("wrong activation method")
					}
					active = true
					w.WriteHeader(204)
				case "/api/identity":
					fmtIdentity := map[string]string{"identityId": identity}
					json.NewEncoder(w).Encode(fmtIdentity)
				case "/api/net/peer":
					queried = true
					json.NewEncoder(w).Encode(map[string]any{"peers": []tvpnPeer{{IdentityID: "298002", Hostname: "gcs.local", OverlayAddress: "10.128.0.8"}}})
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			api, _ := newLocalAPI(server.URL)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			address := ""
			err := connectBootstrapPeer(ctx, api, testTransportClaim(), func() bool { return false }, func(value string) { address = value })
			if identity == "351092" {
				if err != nil || address != "https://10.128.0.8:9445" || !active {
					t.Fatal(address, err)
				}
			} else if err == nil || queried || address != "" {
				t.Fatal("wrong key reached peer discovery")
			}
		})
	}
}

func TestBootstrapKeyFileIsPrivateIdempotentAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir() + "/claims"
	path, err := saveBootstrapKey(dir, "11111111-1111-1111-1111-111111111111", "testkey:secret")
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential not private")
	}
	if _, err := saveBootstrapKey(dir, "11111111-1111-1111-1111-111111111111", "testkey:secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := saveBootstrapKey(dir, "11111111-1111-1111-1111-111111111111", "different:secret"); err == nil {
		t.Fatal("credential overwritten")
	}
}
