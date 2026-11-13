package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMissingPrimaryInputsFailWithoutSideEffectsWhenNonInteractive(t *testing.T) {
	old := presentation
	presentation = presentationOptions{NonInteractive: true}
	defer func() { presentation = old }()
	for _, args := range [][]string{nil, {"setup"}, {"prepare-claim"}} {
		err := execute(args)
		if err == nil || !strings.Contains(err.Error(), "supply the required flags") {
			t.Fatalf("missing input should not proceed: %v", err)
		}
	}
}

func TestProgressIsReadableUnlessMachineOutputRequested(t *testing.T) {
	var output bytes.Buffer
	emit := newProgress(&output, false)
	emit("checking-packages")
	emit("checking-packages")
	emit("runtime:joining")
	emit("ready")
	text := output.String()
	if strings.Count(text, "Checking installation files") != 1 || !strings.Contains(text, "Linking the UxS to the GCS") || !strings.Contains(text, "UxS ready") {
		t.Fatalf("unclear progress %q", text)
	}
	if strings.Contains(text, "elapsedMs") || strings.Contains(text, "\"actor\"") {
		t.Fatal("raw event output in human progress")
	}
	output.Reset()
	newProgress(&output, true)("checking-packages")
	var e event
	if err := json.Unmarshal(output.Bytes(), &e); err != nil || e.Phase != "checking-packages" {
		t.Fatalf("machine event: %v", err)
	}
}

func TestGCSAddressMustBeAnHTTPSOrigin(t *testing.T) {
	for _, value := range []string{"https://192.168.1.17:9445", "https://gcs.example.com/"} {
		if err := validateGCSAddress(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"http://192.168.1.17:8088", "gcs", "https://user:password@gcs", "https://gcs/claim", "https://gcs?code=x"} {
		if err := validateGCSAddress(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
