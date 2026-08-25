# Two-node broadband Docker harness

This harness runs two isolated Beam identities on one Docker host. Traffic
between them still traverses Somewear Grid/Souvla over broadband; the services
do not exchange RPC payloads over their Docker network.

```text
grid-client                          grid-target
  Beam A                               Beam B
  RPC + output-stream client          RPC + output-stream host
       |                                    |
       +---------- Grid/Souvla -------------+
```

The RPC implementation can discover target account IDs with one workspace
broadcast. The Grid server authenticates delivery and Beam supplies the source
account in webhook metadata; payloads cannot assert their own identity.

> **POC security:** Grid/Souvla is the trust server. RPC messages and stream
> frames inherit the authentication, link encryption, and delivery properties
> of the existing Grid path.

## Prerequisites

- A current Beam jar built at `beam/app/build/libs/somewear-beam.jar`.
- Two distinct Beam integration API keys that are not simultaneously running
  in another Beam process.
- Both integration identities are members of the same workspace.

Do not reuse one identity in both Docker and an existing host or Orin Beam
daemon. Multiple concurrent clients for one identity are not currently a
supported deployment model.

## Configure

Copy the example without committing the resulting `.env`:

```bash
cd beam-cookbook/rpc/docker-harness
cp .env.example .env
```

Set absolute paths for the Beam jar and two API-key files. A credential file
may contain either a raw API key or an existing Beam properties file with an
`api-key=...` entry. Credential files should be mode `0600`.

## Start

```bash
docker compose up --build -d --wait
docker compose ps
docker compose logs -f grid-client grid-target
```

Each Beam instance has its own named state volume and activates `WORKSPACE_ID`
after becoming healthy. No host ports, devices, TUN interfaces, or privileged
container permissions are required.

## Discover the target

```bash
docker compose exec grid-client sh -lc \
  'exec grid-remote-shell nmap \
    --beam-url http://127.0.0.1:9091 \
    --webhook-port 8080'
```

Use the `ACCOUNT` value from the result for unicast shell commands. Discovery
responses intentionally omit that ID from the satellite payload because Beam
already provides it in webhook metadata.

## Run the shell

```bash
docker compose exec grid-client sh -lc \
  'exec grid-remote-shell shell \
    --beam-url http://127.0.0.1:9091 \
    --webhook-port 8080'
```

The selector appears even when only one target responds. Run the command from
a TTY (the default for `docker compose exec`) and use arrow keys or `j`/`k` to
select one. Pass `--target-user "$TARGET_ACCOUNT_ID"` to skip discovery in a
script or other non-interactive session.

Each command starts as a standard request/response RPC. The target opens a
`grid-command-output.v1` stream back to the client, which prints output as it
arrives without creating a full-duplex remote-shell stream. Ctrl-C resets that
output stream and interrupts the corresponding remote process group.

One-shot command:

```bash
docker compose exec grid-client sh -lc \
  'exec grid-remote-shell send \
    --target-user "$TARGET_ACCOUNT_ID" \
    --beam-url http://127.0.0.1:9091 \
    hostname'
```

## Test file upload and execution

The hello-world test creates an executable inside the client container,
uploads it to the target as acknowledged request/response chunks, executes it
through a standard command request, reads the target-originated output stream,
and asserts the marker:

```bash
./test-file-upload.sh
```

No Docker volume or direct container copy connects the client file to the
target; both the upload and command output traverse Grid/Souvla.

## Inspect and stop

```bash
docker compose logs grid-client grid-target
docker compose down
```

`docker compose down` preserves both Beam state volumes. Removing the volumes
is intentionally not part of the normal workflow.
