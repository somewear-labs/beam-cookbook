# Beam Cookbook

Practical tools and examples for building on top of the [Somewear](https://somewearlabs.com) Beam daemon.

## Recipes

### [`grid-uxs-wizard/`](./grid-uxs-wizard) — Provision a Networked UxS

A standalone Go reference installer for a Windows, macOS, or Linux GCS and a
Linux mission computer. It installs Beam and TVPN and guides claim-code setup.
Requires Beam's PLAT-291 claim preparation and acknowledgement support.
The installer owns the Go runtime service that supervises Beam and TVPN;
Beam handles the identity and pairing exchange.

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
