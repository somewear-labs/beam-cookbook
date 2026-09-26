# rpc — Remote Shell over GridDatagram

Execute commands on a remote machine over any Somewear link. The cookbook owns its protobuf application protocol. Beam carries the encoded bytes in GridDatagrams without depending on the cookbook proto.

## Architecture

<img width="3180" height="1827" alt="image" src="https://github.com/user-attachments/assets/898cf5a9-42f9-490e-81be-f276ca9f0e1e" />

## Layers

Applications send their serialized payload to Beam in a JSON API request:

```json
{"targetUserId":"384899","data":"<base64 protobuf>"}
```

The client helpers build four patterns on Beam's generic API. `Request` sends a direct datagram and waits for the first correlated response. `Broadcast` sends without a target, and `Responses` collects correlated replies. `Respond` sends a datagram whose `inResponseTo` identifies the request; Beam derives the return address from that ID. All sends use `/api/datagrams`, and response collection uses `/api/datagrams/responses`.

Ping, discovery, connect, exec, and streaming commands use this API.

`/watch` repeats `beam status` on the target. `/seismograph` starts the cookbook's
sensor simulator there and returns each protobuf reading. The shell keeps a
response collector open for each stream and prints its key. Use `/close KEY` to
stop one. Readings use GridDatagram's replaceable `streamKey`; the close command
and terminal response use key zero so they cannot replace a queued reading.

## Network setup

Both machines must be signed in to Beam and joined to the same Somewear workspace. Beam uses the active workspace as routing context. A command with `targetUserId` is direct to that user; a command without it is broadcast within the workspace.

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

Beam must forward inbound RPC events to `rpc server` via webhook. Choose any free port (e.g. 8081):

```bash
beam config set webhook-address http://localhost:8081
```

Then restart the Beam daemon if it is already running.

### 3. Start `rpc server` on the remote machine

```bash
./rpc server --port 8081
```

`--port` must match the remote webhook address.

### 4. Start `rpc shell` on the local machine

```bash
./rpc shell --target-user 384899
```

The shell collects replies through the local Beam API; it does not need a local webhook.
For one-shot sends, select a workspace in Beam first; `--workspace` cannot override GridDatagram routing.

---

## Usage

### Remote machine
```bash
./rpc server --port 8081
./rpc server --port 8081 --max-response 500
```

For `/seismograph`, install the sensor binary on the remote machine's `PATH`:

```bash
cd ../sensors
go build -o bin/seismograph ./cmd/seismograph
export PATH="$PWD/bin:$PATH"
```

### Local machine — interactive shell
```bash
./rpc shell --target-user 384899
./rpc shell --target-user 384899 --timeout 30s
```

Inside the shell:

```text
/watch 2s
/seismograph 5s
/close KEY
```

### Local machine — one-shot send
```bash
./rpc send --target-user-id 384899 "uptime"
./rpc send --target-user-id 384899 "df -h"
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
| `--max-response` *(server)* | `200` | Stdout truncation limit in bytes |
| `--timeout` *(shell)* | `30s` | Response wait timeout |

## Publishing a release

Binaries live in `bin/` and are served from `gs://get-somewear-app/atakplugin/`. After building, upload and set public ACL:

```bash
# Build all platforms
make build                          # macOS (native arch)
GOARCH=amd64 make build-linux       # Linux x86_64 → bin/rpc_linux_amd64 (manual rename needed)
make build-linux                    # Linux ARM64  → bin/rpc_linux_arm64 (manual rename needed)

# Upload binaries
gsutil -m cp -a public-read \
  bin/rpc_darwin_arm64 \
  bin/rpc_darwin_amd64 \
  bin/rpc_linux_arm64 \
  bin/rpc_linux_amd64 \
  "gs://get-somewear-app/atakplugin/"

# Upload installer script
gsutil cp -a public-read install-rpc.sh "gs://get-somewear-app/atakplugin/install-rpc.sh"
```

Requires `gcloud auth login` with a Somewear GCP account. The public installer URL is:

```
https://get.somewear.app/atakplugin/install-rpc.sh
```

## Adding a new RPC method

1. Add request and response messages to `proto/rpc.proto`.
2. Add the corresponding fields to `RpcRequest.method` and `RpcResponse.result`.
3. Run `make proto` to regenerate the Go bindings.
4. Send the encoded envelope through `requestBeamDatagram` and reply through `respondWithBeamDatagram`.

Beam remains independent of this application proto.
