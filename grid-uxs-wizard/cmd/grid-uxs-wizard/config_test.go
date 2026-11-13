package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWizardFileServerConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wizard.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"fileServer":"http://192.168.1.17:8088/networked-uxs"}`), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := configuredClaimRelease(path, "", "")
	if err != nil || value != "http://192.168.1.17:8088/networked-uxs/claim-release.json" {
		t.Fatalf("config: %q %v", value, err)
	}
	value, err = configuredClaimRelease(path, "https://downloads.example.com/releases", "")
	if err != nil || value != "https://downloads.example.com/releases/claim-release.json" {
		t.Fatalf("override: %q %v", value, err)
	}
	for _, body := range []string{`{"schemaVersion":2,"fileServer":"https://example.com"}`, `{"schemaVersion":1,"fileServer":"http://example.com"}`, `{"schemaVersion":1,"fileServer":"https://example.com","typo":true}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWizardConfig(path, true); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if _, err := loadWizardConfig(path+"-missing", true); err == nil {
		t.Fatal("ignored explicit missing config")
	}
}
