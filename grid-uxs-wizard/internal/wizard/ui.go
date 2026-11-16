package wizard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

type presentationOptions struct {
	NonInteractive bool
	JSONProgress   bool
}

var presentation presentationOptions

type menuOption struct{ Label, Value string }

func presentationArgs(args []string) []string {
	remaining := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--non-interactive":
			presentation.NonInteractive = true
		case "--json-progress":
			presentation.JSONProgress = true
		default:
			remaining = append(remaining, arg)
		}
	}
	return remaining
}

func requirePrompt() error {
	if presentation.NonInteractive || !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("missing setup input; use an interactive terminal or supply the required flags")
	}
	return nil
}

func requiredText(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("enter a value")
	}
	if len(value) > 512 {
		return errors.New("use at most 512 characters")
	}
	return nil
}

func promptText(title, description string, validate func(string) error) (string, error) {
	if err := requirePrompt(); err != nil {
		return "", err
	}
	if description != "" {
		fmt.Fprintln(os.Stderr, description)
	}
	original, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), original)
	console := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stderr}, title+": ")
	for {
		value, err := console.ReadLine()
		if err != nil {
			return "", fmt.Errorf("input cancelled: %w", err)
		}
		value = strings.TrimSpace(value)
		if err := validate(value); err != nil {
			fmt.Fprintln(console, err)
			continue
		}
		return value, nil
	}
}

func promptChoice(title string, options []menuOption) (string, error) {
	if err := requirePrompt(); err != nil {
		return "", err
	}
	if len(options) == 0 {
		return "", errors.New("no choices available")
	}
	fmt.Fprintf(os.Stderr, "\n%s\n\n", title)
	for i, option := range options {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, option.Label)
	}
	value, err := promptText("Choose a number", "", func(value string) error {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > len(options) {
			return fmt.Errorf("choose 1 to %d", len(options))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	n, _ := strconv.Atoi(value)
	return options[n-1].Value, nil
}

func promptTask() (string, error) {
	return promptChoice("Grid Networked UxS — what would you like to do?", []menuOption{
		{"GCS: provision a UxS", "provision-uxs"},
		{"GCS: continue UxS provisioning", "resume-claim"},
		{"UxS: set up this mission computer", "uxs"},
		{"GCS: install Beam and TVPN", "gcs"},
	})
}

func promptRole() (string, error) {
	return promptChoice("Which computer are you setting up?", []menuOption{{"GCS", "gcs"}, {"UxS mission computer", "uxs"}})
}

func validateGCSAddress(value string) error {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("enter the GCS HTTPS address, for example https://gcs.example:9445")
	}
	return nil
}

func secretClaimInput(allowResume bool) (string, error) {
	if err := requirePrompt(); err != nil {
		return "", err
	}
	fmt.Fprintln(os.Stderr, "\nPaste the entire setup code from the GCS. If the code expired, extend its setup window in the GCS fleet manager and keep the same code. Input is hidden.")
	if allowResume {
		fmt.Fprintln(os.Stderr, "Press Enter without a code to resume the saved installation, including an interrupted acknowledgement.")
	}
	original, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), original)
	console := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stderr}, "")
	for {
		value, err := console.ReadPassword("Setup code: ")
		if err != nil {
			return "", fmt.Errorf("claim input cancelled: %w", err)
		}
		value = strings.TrimSpace(value)
		if (value == "" && allowResume) || validClaimCode(value) {
			return value, nil
		}
		fmt.Fprint(console, "Paste the complete setup code shown by the GCS.\n")
	}
}

func secretAPIKeyInput(allowResume bool) (string, error) {
	if err := requirePrompt(); err != nil {
		return "", err
	}
	fmt.Fprintln(os.Stderr, "Paste only the UxS API key displayed by the GCS, not the setup PIN or file path. Input is hidden.")
	if allowResume {
		fmt.Fprintln(os.Stderr, "Press Enter to reuse this installation's saved API key.")
	}
	original, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), original)
	console := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stderr}, "")
	for {
		value, err := console.ReadPassword("UxS API key: ")
		if err != nil {
			return "", errors.New("API key input cancelled")
		}
		value = strings.TrimSpace(value)
		if (value == "" && allowResume) || apiKeyInputError(value) == nil {
			return value, nil
		}
		fmt.Fprintln(console, apiKeyInputError(value))
	}
}

func apiKeyInputError(value string) error {
	if strings.HasPrefix(value, "uxst1.") || strings.HasPrefix(value, "uxs1.") {
		return errors.New("That is the setup code. Paste the separate value under UxS API key on the GCS.")
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") || strings.Contains(value, "\\") {
		return errors.New("That looks like a file path. Paste the API key itself, not the path.")
	}
	if strings.HasPrefix(value, "api-key=") || strings.HasPrefix(value, "SW_API_KEY=") {
		return errors.New("Paste only the key value, without the setting name or quotes.")
	}
	if !apiKeyPattern.MatchString(value) {
		return errors.New("Paste the complete API key value from the GCS, without a label or quotes.")
	}
	return nil
}

func newProgress(out io.Writer, machine bool) func(string) {
	started := time.Now()
	previous := ""
	return func(phase string) {
		if phase == previous {
			return
		}
		previous = phase
		if machine {
			_ = json.NewEncoder(out).Encode(event{time.Now().UTC().Format(time.RFC3339Nano), "installer", phase, time.Since(started).Milliseconds()})
			return
		}
		label := progressLabel(phase)
		if phase == "ready" || phase == "gcs-ready" {
			fmt.Fprintf(out, "\n%s (%s)\n", label, time.Since(started).Round(time.Second))
			return
		}
		fmt.Fprintf(out, "  %s...\n", label)
	}
}

func progressLabel(phase string) string {
	labels := map[string]string{
		"checking-packages": "Checking installation files", "packages-verified": "Installation files verified",
		"installing-beam": "Installing Beam", "installing-tvpn": "Installing TVPN", "preflight": "Checking prerequisites",
		"preflight-complete": "Prerequisites verified", "connecting-tvpn": "Connecting to the GCS through TVPN",
		"reconnecting-tvpn": "Waiting for the managed TVPN link",
		"discovering-gcs":   "Finding the GCS through TVPN",
		"authorizing-pin":   "Confirming the setup PIN",
		"waiting-for-gcs":   "Waiting for the GCS provisioning connection",
		"preparing-claim":   "Redeeming your claim", "installing-service": "Starting the UxS service",
		"acknowledging-claim": "Confirming enrollment", "waiting-for-paired-setup": "Waiting for paired setup", "ready": "UxS ready", "gcs-ready": "GCS ready",
		"starting-beam": "Starting Beam", "configuring-networked-mode": "Configuring Networked UxS", "authorizing-gcs": "Authorizing the GCS",
		"starting-tvpn": "Starting TVPN", "sharing-gcs-identity": "Configuring TVPN identity", "connecting-package-stream": "Connecting TVPN to Beam's package stream", "activating-tvpn": "Applying TVPN configuration", "selecting-tvpn-workspace": "Connecting TVPN to the Workspace",
		"preparing-aircraft-claim": "Provisioning your UxS", "aircraft-claim-ready": "Setup PIN ready",
		"runtime:starting": "Starting UxS setup", "runtime:starting-tvpn": "Starting TVPN", "runtime:selecting-workspace": "Connecting to the Workspace",
		"runtime:starting-beam": "Starting UxS Beam", "runtime:delegating-package-stream": "Connecting Beam and TVPN",
		"runtime:joining": "Linking the UxS to the GCS", "runtime:provisioning-horizon": "Configuring Horizon", "runtime:ready": "Services connected",
	}
	if label, ok := labels[phase]; ok {
		return label
	}
	return strings.ReplaceAll(strings.TrimPrefix(phase, "runtime:"), "-", " ")
}

func progressOutput() func(string) {
	var out io.Writer = os.Stderr
	if presentation.JSONProgress {
		out = os.Stdout
	}
	return newProgress(out, presentation.JSONProgress)
}

func secretPINInput() (string, error) {
	if err := requirePrompt(); err != nil {
		return "", err
	}
	original, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), original)
	console := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stderr}, "")
	for {
		value, err := console.ReadPassword("GCS setup PIN: ")
		if err != nil {
			return "", errors.New("PIN input cancelled")
		}
		pin, err := normalizePIN(value)
		if err == nil {
			return pin, nil
		}
		fmt.Fprintln(console, err)
	}
}
