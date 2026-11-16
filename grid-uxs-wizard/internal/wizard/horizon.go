package wizard

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"time"
)

const horizonAPIPath = "/api/uxs/provisioning/horizon"

type horizonContext struct {
	Backend     string `json:"backend"`
	WorkspaceID string `json:"workspaceId"`
	IdentityID  string `json:"identityId"`
	AccountID   string `json:"accountId"`
}

type horizonUSBDevice struct {
	Serial string `json:"serial"`
	USB    string `json:"usb"`
}

type horizonSyncProfile struct {
	IdentityID string       `json:"identityId"`
	Serial     string       `json:"serial"`
	AccountID  string       `json:"accountId"`
	KeyID      string       `json:"keyId"`
	Revision   int64        `json:"revision"`
	Radio      horizonRadio `json:"radio"`
}

type horizonSnapshot struct {
	Backend       string             `json:"backend"`
	WorkspaceID   string             `json:"workspaceId"`
	GCSIdentityID string             `json:"gcsIdentityId"`
	Profile       horizonSyncProfile `json:"profile"`
}

type horizonSyncRequest struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Serial           string `json:"serial,omitempty"`
}

type horizonProvisionRequest struct {
	USB         string       `json:"usb"`
	Serial      string       `json:"serial"`
	Role        string       `json:"role"`
	WorkspaceID string       `json:"workspaceId"`
	IdentityID  string       `json:"identityId"`
	AccountID   string       `json:"accountId"`
	KeyID       string       `json:"keyId"`
	Radio       horizonRadio `json:"radio"`
}

type horizonProvisioning struct {
	api          *localAPI
	selectDevice func([]horizonUSBDevice) (horizonUSBDevice, error)
	confirm      func() (bool, error)
	out          io.Writer
}

func executeHorizon(args []string) error {
	if len(args) == 0 {
		args = []string{"provision"}
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Run grid-uxs-wizard horizon provision to synchronize radio settings from the paired GCS and configure a connected Horizon.")
		return flag.ErrHelp
	}
	if args[0] != "provision" {
		return errors.New("use grid-uxs-wizard horizon provision")
	}
	f := flag.NewFlagSet("horizon provision", flag.ContinueOnError)
	base := f.String("beam-api", "http://127.0.0.1:9091", "Local Beam API origin")
	usb := f.String("usb", "", "Select a connected Horizon by its USB port; otherwise choose interactively")
	yes := f.Bool("yes", false, "Apply the displayed configuration without confirmation")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected Horizon provisioning arguments")
	}
	if os.Geteuid() == 0 {
		return errors.New("run Horizon provisioning as the normal Beam user, without sudo")
	}
	api, err := newLocalAPI(*base)
	if err != nil {
		return err
	}
	defer api.client.CloseIdleConnections()
	api.client.Timeout = 3 * time.Minute
	flow := horizonProvisioning{api: api, out: os.Stdout}
	flow.selectDevice = func(devices []horizonUSBDevice) (horizonUSBDevice, error) {
		if *usb != "" {
			for _, device := range devices {
				if device.USB == *usb {
					return device, nil
				}
			}
			return horizonUSBDevice{}, errors.New("selected USB port is not a connected Horizon")
		}
		if len(devices) == 1 {
			return devices[0], nil
		}
		choices := make([]menuOption, 0, len(devices))
		for _, device := range devices {
			choices = append(choices, menuOption{device.Serial + " (" + device.USB + ")", device.USB})
		}
		selected, err := promptChoice("Select the connected Horizon", choices)
		if err != nil {
			return horizonUSBDevice{}, err
		}
		for _, device := range devices {
			if device.USB == selected {
				return device, nil
			}
		}
		return horizonUSBDevice{}, errors.New("no Horizon selected")
	}
	flow.confirm = func() (bool, error) {
		if *yes {
			return true, nil
		}
		selected, err := promptChoice("Apply this Horizon configuration?", []menuOption{{"Cancel without writing the device", "cancel"}, {"Apply registration, encryption and radio settings", "apply"}})
		return selected == "apply", err
	}
	ctx, cancel := setupContext()
	defer cancel()
	return flow.provision(ctx)
}

func validHorizonContext(c horizonContext) bool {
	return c.Backend != "" && numericID.MatchString(c.WorkspaceID) && numericID.MatchString(c.IdentityID) && numericID.MatchString(c.AccountID)
}

func validateHorizonSnapshot(s horizonSnapshot, c horizonContext) error {
	p := s.Profile
	r := p.Radio
	if s.Backend != c.Backend || s.WorkspaceID != c.WorkspaceID || !numericID.MatchString(s.GCSIdentityID) || p.IdentityID != c.IdentityID || p.AccountID != c.AccountID {
		return errors.New("GCS radio profile does not belong to this UxS installation")
	}
	if p.Revision < 1 || !regexpSHA.MatchString(p.KeyID) || r.LowHz < 868000000 || r.HighHz > 928000000 || r.HighHz <= r.LowHz || r.Channels < 1 || r.Channels > 50 || r.NetID < 0 || r.NetID > 255 || r.WindowMS < 25 || r.WindowMS > 400 {
		return errors.New("GCS returned an invalid radio profile")
	}
	return nil
}

func (f horizonProvisioning) provision(ctx context.Context) error {
	var active horizonContext
	if err := f.api.request(ctx, "GET", horizonAPIPath+"/context", nil, &active); err != nil {
		return fmt.Errorf("read Beam identity: %w", err)
	}
	if !validHorizonContext(active) {
		return errors.New("sign in to Beam and select a Workspace before provisioning Horizon")
	}
	var devices []horizonUSBDevice
	if err := f.api.request(ctx, "GET", horizonAPIPath+"/devices", nil, &devices); err != nil {
		return err
	}
	if len(devices) == 0 {
		return errors.New("no Horizon connected; connect a powered Horizon by USB and retry")
	}
	device, err := f.selectDevice(devices)
	if err != nil {
		return err
	}
	serialPattern := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	found := false
	for _, candidate := range devices {
		if candidate == device {
			found = true
		}
	}
	if !found || !serialPattern.MatchString(device.Serial) || device.USB == "" {
		return errors.New("invalid connected Horizon selection")
	}
	fmt.Fprintln(f.out, "Synchronizing this UxS's radio profile from the paired GCS...")
	var snapshot horizonSnapshot
	if err := f.api.request(ctx, "POST", horizonAPIPath+"/sync", horizonSyncRequest{}, &snapshot); err != nil {
		return fmt.Errorf("could not sync radio settings for UxS identity %s; check that the paired GCS has its radio profile and TVPN is connected: %w", active.IdentityID, err)
	}
	if err := validateHorizonSnapshot(snapshot, active); err != nil {
		return err
	}
	p := snapshot.Profile
	r := p.Radio
	fmt.Fprintf(f.out, "\nHorizon: %s (%s)\nUxS identity: %s\nWorkspace: %s\nRadio: %.3f–%.3f MHz, %d channels\nNetwork ID: %d; TDMA window: %d ms\nEncryption fingerprint: %s\n\n", device.Serial, device.USB, active.IdentityID, active.WorkspaceID, float64(r.LowHz)/1e6, float64(r.HighHz)/1e6, r.Channels, r.NetID, r.WindowMS, p.KeyID[:15])
	confirmed, err := f.confirm()
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}
	var bound horizonSnapshot
	if err := f.api.request(ctx, "POST", horizonAPIPath+"/sync", horizonSyncRequest{p.Revision, device.Serial}, &bound); err != nil {
		return fmt.Errorf("bind the selected Horizon to its GCS radio profile: %w", err)
	}
	if err := validateHorizonSnapshot(bound, active); err != nil {
		return err
	}
	b := bound.Profile
	if bound.GCSIdentityID != snapshot.GCSIdentityID || b.Serial != device.Serial || b.KeyID != p.KeyID || b.Radio != p.Radio || (b.Revision != p.Revision && (p.Revision == math.MaxInt64 || b.Revision != p.Revision+1)) {
		return errors.New("GCS radio profile changed; rerun Horizon provisioning to review the current settings")
	}
	var current horizonContext
	if err := f.api.request(ctx, "GET", horizonAPIPath+"/context", nil, &current); err != nil {
		return err
	}
	if current != active {
		return errors.New("Beam identity or Workspace changed; device was not written")
	}
	var connected []horizonUSBDevice
	if err := f.api.request(ctx, "GET", horizonAPIPath+"/devices", nil, &connected); err != nil {
		return err
	}
	found = false
	for _, candidate := range connected {
		if candidate == device {
			found = true
		}
	}
	if !found {
		return errors.New("selected Horizon disconnected or changed USB port; device was not written")
	}
	request := horizonProvisionRequest{device.USB, device.Serial, "uxs", active.WorkspaceID, active.IdentityID, active.AccountID, p.KeyID, p.Radio}
	fmt.Fprintln(f.out, "Applying registration, encryption and radio settings...")
	var receipt horizonReceipt
	if err := f.api.request(ctx, "POST", horizonAPIPath+"/provision", request, &receipt); err != nil {
		return fmt.Errorf("Horizon provisioning failed; inspect Beam logs before retrying: %w", err)
	}
	if receipt.Serial != device.Serial || receipt.WorkspaceID != active.WorkspaceID || receipt.IdentityID != active.IdentityID || receipt.AccountID != active.AccountID || receipt.Radio != p.Radio || receipt.KeyFingerprint != p.KeyID[:15] || !regexp.MustCompile(`^[1-9][0-9]{14}$`).MatchString(receipt.IMEI) {
		return errors.New("Horizon verification receipt differs from the selected device or GCS profile")
	}
	fmt.Fprintf(f.out, "Horizon %s provisioned. Registration, encryption and radio settings verified.\n", device.Serial)
	return nil
}
