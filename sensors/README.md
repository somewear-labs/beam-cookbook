# sensors — Sensor Simulator Suite for Somewear Beam

Five standalone Go programs that simulate physical sensors and push data through Beam's IPv4Datagram endpoint. Each program runs in a loop, generating realistic synthetic readings and posting them at a configurable interval.

## Sensors

| Program | Type byte | Description |
|---------|-----------|-------------|
| `seismograph` | 1 | Broadband seismometer near Ridgecrest, CA. Ambient microseismic noise with 1% chance of a synthetic P+S-wave earthquake event per interval. |
| `airquality` | 2 | Urban air quality monitor. PM2.5, PM10, CO₂, VOC, NO₂, O₃, AQI. Levels follow a diurnal traffic pattern with morning/evening peaks. |
| `gps` | 3 | Vehicle patrol tracker near Fort Irwin (NTC). Walks a 6-waypoint loop at ~8 m/s with realistic HDOP and satellite counts. |
| `accelerometer` | 4 | 9-DOF IMU mounted on a patrol vehicle. Gravity decomposition, terrain vibration, slow yaw drift, and calibrated magnetometer values. |
| `weather` | — | Surface weather station (Mojave Desert). Hot, dry, strong diurnal swing. Sent as **raw JSON with no SWL header** — weather is the one exception. |

## Wire format

Every sensor except weather wraps its JSON payload in a 4-byte SWL frame before base64-encoding:

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
  "workspaceId": 39054,
  "ipv4": { "payload": "<base64>" }
}
```

Weather sends the raw JSON bytes as base64 with no SWL header — the receiver identifies it by the absence of the magic bytes.

## Build

```bash
# Build all 5 binaries to bin/
make

# Build a single sensor
make bin/seismograph

# Remove built binaries
make clean
```

Requires Go 1.22+. No external dependencies — stdlib only.

## Run

Each binary takes the same flags:

```
--url        string    Beam API URL (default "http://localhost:9091")
--interval   duration  Posting interval (default 5s)
--workspace  int       Workspace ID (default 39054)
--verbose    bool      Print full JSON payload before each send
```

### Examples

```bash
# Run seismograph against local Beam, posting every 2 seconds
./bin/seismograph --interval 2s

# Run GPS against a remote Beam
./bin/gps --url http://192.168.1.100:9091 --workspace 12345

# Show full payloads for debugging
./bin/weather --verbose

# Run everything at once
for s in bin/*; do $s & done
```

### Sample output

```
[seismograph] 2026-09-17T12:00:00Z station=SWL-NOR-001 pgv=3.14e-08 m/s intensity=1
[seismograph] 2026-09-17T12:00:05Z station=SWL-NOR-001 pgv=4.21e-07 m/s intensity=1
[seismograph] 2026-09-17T12:00:10Z station=SWL-NOR-001 pgv=2.18e-03 m/s intensity=4 EVENT=EQ-20260917-0001

[airquality] 2026-09-17T12:00:00Z sensor=SWL-AQ-001 pm25=9.3 aqi=38 co2=461 temp=23.4°C

[gps] 2026-09-17T12:00:00Z device=SWL-GPS-001 lat=35.268432 lon=-116.701234 speed=8.2 m/s course=312°

[accelerometer] 2026-09-17T12:00:00Z device=SWL-IMU-001 accel=(0.24,-0.13,9.82) m/s² roll=1.2° pitch=1.5° yaw=46.3°

[weather] 2026-09-17T12:00:00Z station=SWL-WX-001 temp=38.4°C humidity=12% wind=4.1 m/s@228° condition=Clear
```

## How Beam sees the data

Beam treats these payloads as opaque IPv4Datagrams and routes them to any registered webhook consumer. A receiver decodes the base64 payload, checks for the `SWL` magic bytes, reads the type byte, then JSON-unmarshals the remaining bytes into the appropriate struct. Weather payloads (no magic) are identified by the absence of the `SWL` prefix.
