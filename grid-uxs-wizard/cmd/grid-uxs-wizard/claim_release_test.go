package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseDownloadVerifiesAndCachesBeam(t *testing.T) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := os.MkdirTemp(cacheRoot, "grid-uxs-release-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cache)
	content := []byte("test Beam artifact")
	digest := sha256.Sum256(content)
	expected := hex.EncodeToString(digest[:])
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/claim-release.json":
			fmt.Fprintf(w, `{"schemaVersion":1,"beam":{"source":"beam.jar","sha256":%q}}`, expected)
		case "/beam.jar":
			hits++
			w.Write(content)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for count := 0; count < 2; count++ {
		path, err := resolveClaimArtifact(context.Background(), server.URL+"/claim-release.json", cache)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(content) {
			t.Fatal("incorrect artifact")
		}
	}
	if hits != 1 {
		t.Fatalf("artifact downloaded %d times", hits)
	}
	expected = strings.Repeat("0", 64)
	if _, err := resolveClaimArtifact(context.Background(), server.URL+"/claim-release.json", cache); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum: %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(cache, "grid-uxs-wizard", "artifacts", ".download-*"))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed download not cleaned up")
	}
}

func TestReleaseURLPolicy(t *testing.T) {
	for _, value := range []string{"https://downloads.example.com/release.json", "http://192.168.1.17:8088/release.json", "http://127.0.0.1/release.json"} {
		if _, err := releaseURL(value); err != nil {
			t.Fatalf("rejected %s: %v", value, err)
		}
	}
	for _, value := range []string{"http://downloads.example.com/release.json", "file:///tmp/release.json", "https://user:password@example.com/release.json"} {
		if _, err := releaseURL(value); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}
