# Beam Cookbook

Practical tools and examples for building on top of the [Somewear](https://somewearlabs.com) Beam daemon.

## Recipes

### [`rpc/`](./rpc) — Remote Shell over GridDatagram

A lightweight remote shell that lets you execute commands on a remote machine over the Somewear network. Commands and responses are protobuf `Envelope` messages carried as opaque GridDatagram payloads.

**Supported platforms**

| OS | Architecture | Binary |
|----|-------------|--------|
| Linux | ARM64 / aarch64 | [`rpc/bin/rpc_linux_arm64`](./rpc/bin/rpc_linux_arm64) |
| Linux | x86\_64 | [`rpc/bin/rpc_linux_amd64`](./rpc/bin/rpc_linux_amd64) |
| macOS | Apple Silicon (ARM64) | [`rpc/bin/rpc_darwin_arm64`](./rpc/bin/rpc_darwin_arm64) |
| macOS | Intel (x86\_64) | [`rpc/bin/rpc_darwin_amd64`](./rpc/bin/rpc_darwin_amd64) |

See [`rpc/`](./rpc) for full documentation and build-from-source instructions.
