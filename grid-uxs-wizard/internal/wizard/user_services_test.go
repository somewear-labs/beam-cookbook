package wizard

import (
	"context"
	"os/user"
	"strings"
	"testing"
)

func TestUserServicesKeepTVPNAndBeamSeparate(t *testing.T) {
	beam := userRuntimeService("/var/lib/somewear/uxs/mission-computer")
	if !strings.Contains(beam, "WantedBy=default.target") || strings.Contains(beam, "User=root") || strings.Contains(beam, "tvpnd") {
		t.Fatal("Beam is not a user-only service")
	}
	tvpn := rootTVPNService("/opt/tvpn/tvpnd")
	if !strings.Contains(tvpn, "User=root") || strings.Contains(tvpn, "beam") {
		t.Fatal("root service includes Beam")
	}
	if _, err := resolveInstallUser("root"); err == nil {
		t.Fatal("root accepted for Beam")
	}
}

func TestGCSBeamCommandsUseNormalUser(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if account.Uid == "0" {
		t.Skip("requires a normal test user")
	}
	previous := selectedInstallUID
	defer func() { selectedInstallUID = previous }()
	called := false
	i := gcsInstaller{opts: gcsOptions{RunUser: account.Username}, run: func(ctx context.Context, command string, args ...string) error {
		text := command + " " + strings.Join(args, " ")
		if !strings.Contains(text, "-u "+account.Username+" -- env -u SUDO_USER") || !strings.Contains(text, "BEAM_USER_HOME="+account.HomeDir) || !strings.HasSuffix(text, "/opt/beam up") {
			t.Fatal("Beam was not launched in user context")
		}
		called = true
		return nil
	}}
	if err := i.runBeam(context.Background(), "/opt/beam", "up"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Beam launch missing")
	}
}
