# sensors — Military Sensor Simulator Suite for Somewear Beam

Five standalone Go programs that simulate military sensors and push data through Beam's IPv4Datagram endpoint. Each program runs in a loop, generating realistic synthetic readings and posting them at a configurable interval.

## Sensors

| Program | Type byte | Description |
|---------|-----------|-------------|
| `ugs` | 1 | Unattended Ground Sensor (UGS) at NTC Fort Irwin. Seismic-acoustic detection of vehicles, dismounted personnel, and explosions with bearing/range estimation. |
| `cbrn` | 2 | CBRN standoff detector node (JCAD). Chemical agent detection (GA/GB/VX/HD/CG/AC), radiation levels, bio indicators, and threat classification. |
| `vimu` | 4 | Vehicle IMU on an M1A2 SEPv3 Abrams MBT. 9-axis IMU with motion state (STATIONARY/MOVING/MANEUVERING/FIRING), terrain-induced vibration, and main gun recoil spikes. |
| `tws` | — | Tactical Weather Station (TWS) supporting ground and aviation ops. NTC desert climate: diurnal temp swing, dust storm events, density altitude, flight category. Sent as **raw JSON with no SWL header**. |

## Wire format

Every sensor except `tws` wraps its JSON payload in a 4-byte SWL frame before base64-encoding:

```
Byte 0: 'S' (0x53)
Byte 1: 'W' (0x57)
Byte 2: 'L' (0x4C)
Byte 3: sensor type (1–4)
Bytes 4+: JSON-encoded sensor struct
```

The base64 string is sent inside Beam's IPv4Datagram body:

```json
POST /api/package/ipv4/async
{
  "workspaceId": 76854,
  "ipv4": { "payload": "<base64>" }
}
```

`tws` sends the raw JSON bytes as base64 with no SWL header — the receiver identifies it by the absence of the magic bytes.

## Build

```bash
# Build all 4 binaries to bin/ (host platform)
make

# Build for Linux/ARM64 (Raspberry Pi)
make linux-arm64

# Build for Linux/AMD64
make linux-amd64

# Build a single sensor
make bin/ugs

# Remove built binaries
make clean
```

Requires Go 1.22+. No external dependencies — stdlib only.

## Run

Each binary takes the same flags:

```
--url        string    Beam API URL (default "http://localhost:9091")
--interval   duration  Posting interval (default 5s)
--workspace  int       Workspace ID (default 76854)
--verbose    bool      Print full JSON payload before each send
```

### Examples

```bash
# Run UGS against local Beam, posting every 2 seconds
./bin/ugs --url http://localhost:9091 --workspace 76854 --interval 2s

# Run CBRN detector against a remote Beam
./bin/cbrn --url http://192.168.1.100:9091 --workspace 76854

# Show full payloads for debugging
./bin/tws --verbose

# Run everything at once
for s in bin/*; do $s --url http://localhost:9091 --workspace 76854 & done
```

### Sample output

```
[ugs] 2026-09-17T12:00:00Z node=UGS-ALPHA-001 peak=3.14e-08 m/s² threat=NONE
[ugs] 2026-09-17T12:00:05Z node=UGS-ALPHA-001 peak=4.21e-03 m/s² threat=VEHICLE bearing=142° range=320m conf=87%

[cbrn] 2026-09-17T12:00:00Z node=CBRN-DET-001 rad=0.015_mR/hr threat=GREEN
[cbrn] 2026-09-17T12:00:05Z node=CBRN-DET-001 rad=0.018_mR/hr threat=RED *** ALARM agent=GB conc=0.0023ppb conf=91%

[vimu] 2026-09-17T12:00:00Z vehicle=A-31 hdg=62° speed=9.4m/s state=MOVING g=1.14
[vimu] 2026-09-17T12:00:05Z vehicle=A-31 hdg=63° speed=0.0m/s state=FIRING g=8.72

[tws] 2026-09-17T12:00:00Z station=TWS-BRAVO-001 270°@8kts gust=0 vis=24.3km VFR DA=4210ft
```

## How Beam sees the data

Beam treats these payloads as opaque IPv4Datagrams and routes them to any registered webhook consumer. A receiver decodes the base64 payload, checks for the `SWL` magic bytes, reads the type byte, then JSON-unmarshals the remaining bytes into the appropriate struct. `tws` payloads (no magic) are identified by the absence of the `SWL` prefix.
