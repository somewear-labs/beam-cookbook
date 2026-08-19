# rpc — Remote Shell over Somewear IPv4Datagram

Execute shell commands on a remote machine over any Somewear link (satellite, WiFi). Commands and responses travel as protobuf `Envelope` messages inside Beam's IPv4Datagram packet type.

## Architecture

```
Local machine                  Somewear network              Remote machine
─────────────────────────────────────────────────────────────────────────────
rpc shell ──► Beam REST ──► IPv4Datagram (Exec request) ──► Beam daemon
    ▲                                                              │
    │                                                         rpc server
    │                                                              │
    └──────── webhook ◄──── IPv4Datagram (Exec response) ◄────────┘
```

## Wire format

Every IPv4Datagram payload is a proto-serialized `Envelope`:

```proto
message Envelope {
  string namespace  = 1;  // must equal "swl.rpc.v1" — rejects non-RPC datagrams
  uint32 request_id = 2;  // correlates async responses to their originating request
  oneof payload {
    RpcRequest  request  = 3;
    RpcResponse response = 4;
  }
}
```

See [`proto/rpc.proto`](./proto/rpc.proto) for the full schema.

## Usage

### Remote machine
```bash
./rpc server
./rpc server --port 9091 --workspace 39054 --max-response 500
```

### Local machine — interactive shell
```bash
./rpc shell
./rpc shell --webhook-port 8080 --timeout 30s
```

### Local machine — one-shot send
```bash
./rpc send "uptime"
./rpc send --workspace 39054 "df -h"
```

## Supported platforms

Pre-built binaries are in [`bin/`](./bin). Pick the one matching the target machine.

| OS | Architecture | Binary |
|----|-------------|--------|
| Linux | ARM64 / aarch64 | [`bin/rpc_linux_arm64`](./bin/rpc_linux_arm64) |
| Linux | x86\_64 | [`bin/rpc_linux_amd64`](./bin/rpc_linux_amd64) |
| macOS | Apple Silicon (ARM64) | [`bin/rpc_darwin_arm64`](./bin/rpc_darwin_arm64) |
| macOS | Intel (x86\_64) | [`bin/rpc_darwin_amd64`](./bin/rpc_darwin_amd64) |

The `rpc server` binary on the remote machine must match that machine's OS and CPU. Cross-compilation is handled entirely by the Go toolchain — no native cross-compiler required.

## Build from source

```bash
# macOS (auto-detects ARM64 or x86_64)
make build

# Linux ARM64 / aarch64 (e.g. Raspberry Pi, NVIDIA Jetson)
make build-linux

# Linux x86_64
GOARCH=amd64 make build-linux

# Push linux binary to remote over SSH
make push

# Regenerate protobuf bindings after editing proto/rpc.proto
make proto
```

Requires Go 1.22+ and (for `make proto`) `protoc` with `protoc-gen-go`.

## Default configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | `39054` | Somewear workspace ID |
| `--beam-url` | `http://localhost:9091` | Beam REST API |
| `--port` *(server)* | `9091` | Beam webhook port on remote |
| `--webhook-port` *(shell)* | `8080` | Local port for receiving responses |
| `--max-response` *(server)* | `200` | Stdout truncation limit in bytes |
| `--timeout` *(shell)* | `30s` | Response wait timeout |

## Adding a new RPC method

1. Add request/response messages to `proto/rpc.proto`
2. Add a new `oneof` field to `RpcRequest.method` and `RpcResponse.result`
3. Run `make proto` to regenerate Go bindings
4. Add a handler in `server.go` (`handleRequest` switch)
