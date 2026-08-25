# rpc — Remote Shell over Somewear IPv4Datagram

Execute shell commands on a remote machine over any Somewear link (satellite,
radio, or broadband). Commands and responses travel as protobuf
`Envelope` messages inside Beam's existing IPv4Datagram packet type. No Souvla
changes are required.

## Architecture

<img width="3180" height="1827" alt="image" src="https://github.com/user-attachments/assets/898cf5a9-42f9-490e-81be-f276ca9f0e1e" />

## Wire format

Every IPv4Datagram payload is a proto-serialized `Envelope`:

```proto
message Envelope {
  string namespace  = 1;  // must equal "swl.rpc.v1" — rejects non-RPC datagrams
  uint32 request_id = 2;  // correlates async responses to their originating request
  uint64 session_id = 6;  // binds established shell traffic to its channel policy
  oneof payload {
    RpcRequest      request     = 3;
    RpcResponse     response    = 4;
    GridStreamFrame grid_stream = 5;
  }
}
```

See [`proto/rpc.proto`](./proto/rpc.proto) for the full schema.

## GridStreams

`GridStreamFrame` provides a generic named byte-stream transport over the
existing Grid message path. It handles chunking, ordering, duplicate
suppression, flow control, close status, and reset while retaining the
authenticated peer identity supplied by Beam webhook metadata.

The remote shell keeps its features on the standard request/response RPC:
discovery, connect, ping, file-upload chunks, and each command are regular RPC
messages. A command request may ask the target to open the
`grid-command-output.v1` stream back to the client. Only command output flows
on that application stream; command input remains in the initiating request.

Grid/Souvla remains the trust server. GridStream adds no end-to-end encryption;
each frame inherits the encryption and delivery properties of its selected
Grid link.

## Network setup

Both machines must be signed in to Beam and joined to the same Somewear workspace. The active workspace ID is embedded in every IPv4Datagram — packets sent from one workspace are only delivered to nodes in the same workspace.

### 1. Find and activate the workspace on each machine

```bash
# List available workspaces and their IDs
beam workspace list

# Activate the shared workspace (do this on both machines)
beam workspace activate --name "My Workspace"
# or by ID:
beam workspace activate --id 39054
```

Grid Remote Shell reads the active workspace from each local Beam instance. To
change networks, activate the workspace in Beam; Grid Remote Shell does not
provide a separate workspace override.

### 2. Configure Beam webhook on the remote machine

Beam must forward inbound IPv4Datagrams to `rpc server` via webhook. Choose any free port (e.g. 8081):

```bash
beam config set webhook-address=http://localhost:8081
```

Then restart the Beam daemon if it is already running.

### 3. Configure Beam webhook on the local machine

The local Beam daemon must forward response packets to `rpc shell`. Use the same port you'll pass to `--webhook-port` (default 8080):

```bash
beam config set webhook-address=http://localhost:8080
```

### 4. Start `rpc server` on the remote machine

```bash
./rpc server --port 8081
```

`--port` must match the webhook address you set in step 2.

### 5. Start `rpc shell` on the local machine

```bash
./rpc shell --webhook-port 8080
```

`--webhook-port` must match the webhook address you set in step 3. The shell
discovers compatible targets and always shows an interactive selector, even
when only one machine responds. Use arrow keys or `j`/`k` to move, Enter to
connect, and `q` to cancel.

The client sends each command as a standard `ExecRequest`. The target then
opens a command-output stream back to the client, so output is printed as it
arrives without turning the shell protocol into a full-duplex stream. Press
Ctrl-C during a command to reset its output stream and interrupt the remote
process group. At the shell prompt, Ctrl-C asks for confirmation before closing
the session; `exit` and `quit` close immediately.

Commands beginning with `/` are handled locally and are never passed to the
remote system shell. `/help` lists the available commands and `/ping` measures
a compact Grid request/response, showing client-to-target, target-to-client,
and round-trip time. Directional values use peer wall clocks and therefore
require synchronized clocks. The output also applies an NTP-style clock-offset
estimate to show computed directional values; computed RTT removes target
processing time, and computed one-way legs assume symmetric path delay. A
single exchange estimates clock offset, not clock-rate drift over time.
`/put PATH` sends a regular file as acknowledged request/response chunks and
prints the resulting target path. The target stores uploads in a private
temporary directory scoped to the authenticated sender account, preserves
permission bits, and limits this POC path to 8 MiB per file.

---

## Usage

### Remote machine
```bash
./rpc server --port 8081
./rpc server --port 8081 --max-response 500
```

### Local machine — interactive shell
```bash
# Discover target account IDs first
./rpc nmap --webhook-port 8080

# Discover, select, and connect
./rpc shell --webhook-port 8080

# Skip discovery when the account ID is already known
./rpc shell --webhook-port 8080 --target-user 384899
./rpc shell --webhook-port 8080 --target-user 384899 --timeout 30s
./rpc shell --webhook-port 8080 --target-user 384899 --channels radio
```

`--channels` constrains established request/response traffic in both directions.
Discovery and Connect remain unconstrained so the peers can align before using
the requested channels. Valid values are `radio`, `satellite`,
`cellular`, and `mesh`; combine them with commas.

`nmap` sends one workspace broadcast and gathers compact, unicast responses.
The account IDs in its output come from Beam webhook metadata rather than the
RPC payload. Targets randomize their response time within the requested jitter
window and suppress duplicate responses when Beam retries a webhook.

### Local machine — one-shot send
```bash
./rpc send --target-user 384899 "uptime"
./rpc send --target-user 384899 "df -h"
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
| `--beam-url` | `http://localhost:9091` | Beam REST API |
| `--port` *(server)* | `9091` | Beam webhook port on remote |
| `--webhook-port` *(shell)* | `8080` | Local port for receiving responses |
| `--target-user` *(shell)* | `0` | Target account ID; zero discovers and selects a target |
| `--target-user` *(send)* | — | Required target account ID; commands cannot be broadcast |
| `--max-response` *(server)* | `200` | One-shot `send` response limit; streamed shell output is not truncated |
| `--timeout` *(shell)* | `30s` | Connect and command-output stream-open timeout |
| `--discovery-timeout` *(shell)* | `5s` | Target discovery collection window |
| `--timeout` *(nmap)* | `5s` | Discovery response collection window |
| `--response-jitter` *(shell/nmap)* | `750ms` | Maximum target response delay (capped at 2s) |
| `--channels` *(shell)* | unrestricted | Allowed channels for established session traffic |

## Adding a new RPC method

1. Add request/response messages to `proto/rpc.proto`
2. Add a new `oneof` field to `RpcRequest.method` and `RpcResponse.result`
3. Run `make proto` to regenerate Go bindings
4. Add a handler in `server.go` (`handleRequest` switch)
