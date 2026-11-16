# Beam Cookbook

Practical tools and examples for building on top of the [Somewear](https://somewearlabs.com) Beam daemon.

## Recipes

### [`grid-uxs-wizard/`](./grid-uxs-wizard) — Provision a Networked UxS

A standalone Go reference installer for a Windows, macOS, or Linux GCS and a
Linux mission computer. It installs Beam and TVPN and guides API-key and PIN setup.
Requires Beam's PLAT-291 claim preparation and acknowledgement support.
The installer runs Beam in the normal user's scope and TVPN in a separate
root service. Its Linux user runtime supervises Beam and uses TVPN's local API;
Beam handles the identity and pairing exchange.

Run `grid-uxs-wizard setup` on each computer and select its role. On the
configured GCS, run `grid-uxs-wizard provision-uxs` and approve in the browser.
Enter the displayed UxS API key and six-digit setup PIN on the mission computer.
The wizard discovers the GCS over TVPN, verifies the API-key proof and TLS
certificate, then redeems the setup on port 9445. Saved installations resume
their pairing; an expired setup window can be extended in the GCS fleet manager.
No SSH tunnel, backend changes, or TVPN request-signing changes are required.

On macOS and Linux, a published bootstrap can download the complete release,
install the wizard on your user PATH, and start setup:

```sh
curl -fsSL "$FILE_SERVER/install.sh" | sh -s -- setup
```

Choose GCS or UxS when prompted. Run the download as your normal user; setup
requests sudo for system installation. The wizard and its adjacent bundle/config
are stored together under `~/.local/share/grid-uxs-wizard/releases/`, with a
launcher at `~/.local/bin/grid-uxs-wizard`. Open a new terminal for the PATH
update, or run the export printed by the bootstrap. Claim commands then run as the normal user.

For a local file server or an unconfigured copy of the script, pass
`--file-server "$FILE_SERVER"`. Add `--install-only` to download and install the
wizard without changing Beam or TVPN services. Repeating an interrupted download
resumes the cached archive; SHA-256 verification precedes extraction and setup.

Publish [`bootstrap/install.sh`](./grid-uxs-wizard/bootstrap/install.sh) as
`$FILE_SERVER/install.sh`, with `gcs-OS-ARCH.tar.gz` and `uxs-linux-ARCH.tar.gz`
archives and their `.sha256` sidecars. Use `darwin`/`linux` and `arm64`/`amd64`.
Both archives contain `grid-uxs-wizard`, `grid-uxs-wizard.json`,
`THIRD_PARTY_NOTICES.txt`, and `bundle/{install.json,beam.tar.gz,tvpn.tar.gz}`.
Set the script's `DEFAULT_FILE_SERVER` when publishing to enable the one-line
command above. Windows retains the complete-download installer flow.

For installed services, run `grid-uxs-wizard update` as your normal user.
Beam-only updates do not require sudo; TVPN updates request elevation.
See the [wizard guide](./grid-uxs-wizard/README.md) for supported update targets.

```sh
cd grid-uxs-wizard
go build -o bin/grid-uxs-wizard ./cmd/grid-uxs-wizard
./bin/grid-uxs-wizard
```

Use an `.exe` output name on Windows. Place `grid-uxs-wizard.json` beside the
executable to configure `fileServer`; keep the mission-computer download's
`bundle/` directory beside its executable. Run `--help` for flags and overrides.

### [`rpc/`](./rpc) — Remote Shell over IPv4Datagram

A lightweight remote shell that lets you execute commands on a remote machine over the Somewear network (satellite or WiFi). Commands and responses travel as protobuf-serialized `Envelope` messages carried inside Beam's IPv4Datagram packet type.

**Supported platforms**

| OS | Architecture | Binary |
|----|-------------|--------|
| Linux | ARM64 / aarch64 | [`rpc/bin/rpc_linux_arm64`](./rpc/bin/rpc_linux_arm64) |
| Linux | x86\_64 | [`rpc/bin/rpc_linux_amd64`](./rpc/bin/rpc_linux_amd64) |
| macOS | Apple Silicon (ARM64) | [`rpc/bin/rpc_darwin_arm64`](./rpc/bin/rpc_darwin_arm64) |
| macOS | Intel (x86\_64) | [`rpc/bin/rpc_darwin_amd64`](./rpc/bin/rpc_darwin_amd64) |

See [`rpc/`](./rpc) for full documentation and build-from-source instructions.
