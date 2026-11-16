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
