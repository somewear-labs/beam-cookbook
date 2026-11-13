package wizard

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testRunner(t *testing.T, role string) (wizardRunner, *[][]string, *int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "setup.json")
	if role != "" {
		if err := saveSetupRole(path, role); err != nil {
			t.Fatal(err)
		}
	}
	var calls [][]string
	prompts := 0
	runner := wizardRunner{path,
		func(args []string) error { calls = append(calls, append([]string(nil), args...)); return nil },
		func() (string, error) { prompts++; return "gcs", nil },
		func(_ string, choices []menuOption) (string, error) { return choices[0].Value, nil },
	}
	return runner, &calls, &prompts
}

func TestInitialSetupRemembersRoleAndLaterProvisioningInfersIt(t *testing.T) {
	r, calls, prompts := testRunner(t, "")
	if err := r.run(nil); err != nil {
		t.Fatal(err)
	}
	if *prompts != 1 {
		t.Fatal("initial role was not prompted")
	}
	role, err := readSetupRole(r.statePath)
	if err != nil || role != "gcs" {
		t.Fatalf("%q: %v", role, err)
	}
	if err := r.run([]string{"provision-uxs", "--name", "craft"}); err != nil {
		t.Fatal(err)
	}
	if err := r.run([]string{"setup"}); err != nil {
		t.Fatal(err)
	}
	if *prompts != 1 {
		t.Fatal("asked for role after initialization")
	}
	want := [][]string{{"gcs"}, {"provision-uxs", "--name", "craft"}, {"gcs"}}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("%v", *calls)
	}
}

func TestUxSSetupSavesItsRoleAndRejectsGCSProvisioning(t *testing.T) {
	r, calls, prompts := testRunner(t, "")
	if err := r.run([]string{"setup", "--role", "uxs"}); err != nil {
		t.Fatal(err)
	}
	if *prompts != 0 {
		t.Fatal("prompted despite explicit initial role")
	}
	if err := r.run([]string{"provision-uxs"}); err == nil {
		t.Fatal("UxS accepted GCS provisioning")
	}
	if err := r.run([]string{"resume-claim"}); err == nil {
		t.Fatal("UxS accepted GCS resume")
	}
	if err := r.run([]string{"setup"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*calls, [][]string{{"uxs"}, {"uxs"}}) {
		t.Fatalf("%v", *calls)
	}
}

func TestSetupFailureDryRunAndHelpDoNotSaveRole(t *testing.T) {
	for _, args := range [][]string{{"setup", "--role=gcs", "--dry-run"}, {"setup", "--role=gcs", "--dry-run=true"}, {"setup", "--help"}} {
		r, _, _ := testRunner(t, "")
		err := r.run(args)
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			t.Fatal(err)
		}
		if _, err := os.Stat(r.statePath); !os.IsNotExist(err) {
			t.Fatal("saved uncompleted setup")
		}
	}
	r, _, _ := testRunner(t, "")
	r.execute = func([]string) error { return errors.New("installation failed") }
	if err := r.run([]string{"setup", "--role=gcs"}); err == nil {
		t.Fatal("ignored failure")
	}
	if role, err := readSetupRole(r.statePath); err != nil || role != "" {
		t.Fatal("saved failed setup")
	}
}

func TestUnknownOrConflictingRoleDoesNotExecute(t *testing.T) {
	r, calls, _ := testRunner(t, "")
	if err := r.run([]string{"provision-uxs"}); err == nil {
		t.Fatal("provisioned before init")
	}
	if len(*calls) != 0 {
		t.Fatal("executed before init")
	}
	if err := saveSetupRole(r.statePath, "uxs"); err != nil {
		t.Fatal(err)
	}
	if err := r.run([]string{"setup", "--role=gcs"}); err == nil {
		t.Fatal("changed installed role")
	}
	if len(*calls) != 0 {
		t.Fatal("executed conflicting role")
	}
}

func TestRuntimeStartsBeforeSetupRecordExists(t *testing.T) {
	r, calls, _ := testRunner(t, "")
	if err := r.run([]string{"run", "--state", "/var/lib/uxs"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*calls, [][]string{{"run", "--state", "/var/lib/uxs"}}) {
		t.Fatal(*calls)
	}
}

func TestSavedRoleControlsMenuAndHelp(t *testing.T) {
	for _, role := range []string{"gcs", "uxs"} {
		r, calls, prompts := testRunner(t, role)
		if err := r.run(nil); err != nil {
			t.Fatal(err)
		}
		if *prompts != 0 {
			t.Fatal("asked for role")
		}
		want := "provision-uxs"
		if role == "uxs" {
			want = "uxs"
		}
		if len(*calls) != 1 || (*calls)[0][0] != want {
			t.Fatalf("%s: %v", role, *calls)
		}
		if err := r.run([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
			t.Fatal(err)
		}
	}
}

func TestSetupRoleRecordValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.json")
	for _, body := range []string{`{}`, `{"schemaVersion":2,"role":"gcs"}`, `{"schemaVersion":1,"role":"other"}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSetupRole(path); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestResetAvailableForIncompleteSetupAndUxSMenu(t *testing.T) {
	for _, role := range []string{"", "uxs"} {
		r, calls, _ := testRunner(t, role)
		if err := r.run([]string{"reset"}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*calls, [][]string{{"reset"}}) {
			t.Fatal("reset was not routed")
		}
	}
	found := false
	for _, item := range roleTasks("uxs") {
		if item.Value == "reset" {
			found = true
		}
	}
	if !found {
		t.Fatal("reset is missing from UxS menu")
	}
	for _, item := range roleTasks("gcs") {
		if item.Value == "reset" {
			t.Fatal("UxS reset is exposed in GCS menu")
		}
	}
}
