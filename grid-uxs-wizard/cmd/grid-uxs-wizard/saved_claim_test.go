package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testClaim(letter string) string {
	return "uxs1.11111111-1111-1111-1111-111111111111." + strings.Repeat(letter, 43) + "." + strings.Repeat("a", 64)
}

func TestClaimInputCanSkipToResumeOrSelectNewClaim(t *testing.T) {
	saved := filepath.Join(t.TempDir(), ".claim-code")
	previous := testClaim("a")
	if err := os.WriteFile(saved, []byte(previous), 0600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"", previous, testClaim("b")} {
		asked := false
		selection, err := selectClaim(saved, "", func(allowResume bool) (string, error) {
			asked = true
			if !allowResume {
				t.Fatal("resume not offered")
			}
			return entry, nil
		})
		if err != nil || !asked {
			t.Fatalf("claim input: %v", err)
		}
		if entry == "" || entry == previous {
			if selection.Path != saved || selection.Replace {
				t.Fatal("skip did not resume")
			}
		} else if !selection.Replace || selection.Code != entry || selection.Path != "" {
			t.Fatal("new claim silently resumed")
		}
		data, err := os.ReadFile(saved)
		if err != nil || string(data) != previous {
			t.Fatal("selection changed stored credential")
		}
	}
}

func TestFreshSetupRequiresNewClaim(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := selectClaim(missing, "", func(allowResume bool) (string, error) {
		if allowResume {
			t.Fatal("nothing to resume")
		}
		return "", nil
	})
	if err == nil {
		t.Fatal("empty claim accepted on fresh setup")
	}
}

func TestExplicitClaimSelectsReplacementWithoutOverwritingOldFile(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	fresh := filepath.Join(dir, "new")
	if err := os.WriteFile(old, []byte(testClaim("a")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte(testClaim("b")), 0600); err != nil {
		t.Fatal(err)
	}
	selection, err := selectClaim(old, fresh, func(bool) (string, error) { t.Fatal("unexpected prompt"); return "", nil })
	if err != nil || !selection.Replace || selection.Code != testClaim("b") {
		t.Fatalf("replacement selection: %v", err)
	}
	data, _ := os.ReadFile(old)
	if string(data) != testClaim("a") {
		t.Fatal("selection overwrote prior credential")
	}
}
