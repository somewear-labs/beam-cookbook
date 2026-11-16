# Grid Networked UxS wizard

One executable configures either a GCS or a UxS mission computer. Initial setup
asks which role the installation serves and remembers the choice:

```sh
grid-uxs-wizard setup
```

For a new mission computer, enter the API key shown by the GCS, then the six-digit
setup PIN. The wizard discovers the pending GCS through TVPN and verifies its
certificate before sending the PIN. A saved installation resumes without another
PIN. Extending the setup window on the GCS keeps the same PIN and identity.

On the configured GCS:

```sh
grid-uxs-wizard provision-uxs
grid-uxs-wizard resume-claim
```

Run without arguments for initial setup or the installed role's task menu.
Subsequent commands infer the role; no GCS/UxS prefix is required. Automated
initial setup can supply `--role gcs` or `--role uxs`. A failed setup or dry run
does not save a new role; a configured installation cannot switch roles here.

| Directory | Responsibility |
|---|---|
| [`bootstrap/`](bootstrap/) | Download and install the wizard and its user PATH launcher. |
| [`cmd/grid-uxs-wizard/`](cmd/grid-uxs-wizard/) | The single CLI entry point. |
| [`internal/wizard/`](internal/wizard/) | GCS/UxS flows and shared package, service, configuration, and pairing implementation and tests. |

The bootstrap selects the appropriate installation bundle, then invokes setup
with that initial role. Both archives contain `grid-uxs-wizard` and its adjacent
`grid-uxs-wizard.json` file-server configuration.

Non-secret installation role metadata is stored separately in
`/var/lib/grid-uxs-wizard/setup.json` on Linux,
`/Library/Application Support/grid-uxs-wizard/setup.json` on macOS, or
`%ProgramData%/grid-uxs-wizard/setup.json` on Windows. Initial setup
writes it with elevated privileges; normal-user commands can read it. It holds
no identity credentials or radio settings.

The current target is the local installation. In a future remote flow, the
wizard may run on a third computer: role and configuration must belong to the
selected target installation, not the computer executing the wizard. Remote
target selection and execution are not implemented here. UxS configuration
continues to be managed by the GCS; Grid-managed storage is exploratory only.

Build and test from this directory:

```sh
go build -o bin/grid-uxs-wizard ./cmd/grid-uxs-wizard
go test ./...
```

See the [cookbook guide](../README.md) for installation and release layout.
To check for software updates, run as your normal user:

```sh
grid-uxs-wizard update
```

The wizard compares installed Beam and TVPN checksums with `update-release.json`
on the configured file server. Beam-only updates require no sudo. A TVPN update
requests elevation for the system service. Updates currently support a macOS
GCS and Linux mission computer with Beam JAR releases. Windows and native Beam
updates are not implemented.

On a configured Linux mission computer, install the optional ArduCopter simulator
as your normal user:

```sh
grid-uxs-wizard sitl install
```

The wizard downloads and verifies the simulator from the configured file server,
then enables a persistent user service. Defaults send MAVLink to local Beam on
UDP port 14550, with system ID 1. Set `--sysid` and `--home LAT,LON,ALT,YAW` when
installing additional test aircraft.

```sh
grid-uxs-wizard sitl status
grid-uxs-wizard sitl stop
grid-uxs-wizard sitl start
grid-uxs-wizard sitl restart
grid-uxs-wizard sitl logs
```

These commands require no sudo. Simulator files and flight state live under
`~/.local/share/grid-uxs-wizard/sitl`; Beam and TVPN remain unchanged. Installation
requires a `sitl-release.json` beside the file server's claim release, with a
versioned package for the target platform and SHA-256 checksums for its binary,
default parameters and license. This local release provides Linux ARM64.
