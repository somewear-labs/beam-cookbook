#!/bin/bash
# Sends one seismograph reading to beam — identical wire format to the Go binary.
# Wire format: SWL\x05 + protobuf SeismicData, base64-encoded, posted as IPv4Datagram.
#
# Usage:
#   ./seismograph-curl.sh                          # ambient noise (intensity=1)
#   ./seismograph-curl.sh http://tank:9091         # different beam host
#   ./seismograph-curl.sh http://localhost:9091 76854 vehicle  # simulated vehicle event

BEAM_URL="${1:-http://localhost:9091}"
WORKSPACE_ID="${2:-76854}"
MODE="${3:-ambient}"   # ambient | vehicle

PAYLOAD=$(python3 - "$MODE" <<'PYEOF'
import struct, base64, time, sys, math, random

mode = sys.argv[1] if len(sys.argv) > 1 else "ambient"

# --- protobuf helpers ---
def varint(v):
    buf = []
    while v > 127:
        buf.append((v & 0x7F) | 0x80)
        v >>= 7
    buf.append(v)
    return bytes(buf)

def field_varint(n, v):
    return varint((n << 3) | 0) + varint(v)

def field_string(n, s):
    b = s.encode()
    return varint((n << 3) | 2) + varint(len(b)) + b

def field_float(n, f):
    return varint((n << 3) | 5) + struct.pack('<f', f)

def field_packed_floats(n, fs):
    data = b''.join(struct.pack('<f', f) for f in fs)
    return varint((n << 3) | 2) + varint(len(data)) + data

# --- waveform generation (mirrors seismograph/main.go) ---
SAMPLE_RATE = 100
BURST_LEN   = 10
NOISE_FLOOR = 1e-9

def ambient_noise(n=BURST_LEN):
    out = []
    for i in range(n):
        t = i / SAMPLE_RATE
        micro = NOISE_FLOOR * 3 * math.sin(2 * math.pi * 0.15 * t)
        out.append(micro + random.gauss(0, NOISE_FLOOR * 1.5))
    return out

def vehicle_waveform(n=BURST_LEN):
    amp         = 2e-5 + random.random() * 3e-4
    ground_hz   = 1.5  + random.random() * 2.5
    tread_hz    = 12.0 + random.random() * 10.0
    engine_hz   = 18.0 + random.random() * 12.0
    dt          = 1.0 / SAMPLE_RATE
    gp = random.random() * 2 * math.pi
    tp = random.random() * 2 * math.pi
    ep = random.random() * 2 * math.pi
    z, north, east = [], [], []
    for _ in range(n):
        gr    = amp * 0.45 * math.sin(gp)
        tl    = amp * math.sin(tp)
        tread = math.tanh(tl / (amp * 0.65)) * amp * 0.65
        eng   = amp * 0.28 * math.sin(ep)
        z.append(tread * 0.95 + gr * 0.75 + random.gauss(0, amp * 0.04))
        north.append(eng * 0.85 + gr * 0.55 + random.gauss(0, amp * 0.04))
        east.append(tread * 0.55 + eng * 0.45 + random.gauss(0, amp * 0.05))
        gp += 2 * math.pi * ground_hz * dt
        tp += 2 * math.pi * tread_hz  * dt
        ep += 2 * math.pi * engine_hz * dt
    return z, north, east, amp

def pgv_intensity(samples_z, samples_n, samples_e):
    pgv = max(abs(v) for ch in [samples_z, samples_n, samples_e] for v in ch)
    for threshold, mmi in [
        (1.2, 10), (0.50, 9), (0.20, 8), (0.05, 7),
        (0.01, 6), (0.005, 5), (0.002, 4), (0.001, 3), (0.0001, 2),
    ]:
        if pgv >= threshold:
            return pgv, mmi
    return pgv, 1

# --- build SeismicData fields ---
ts = int(time.time() * 1000)
event_id = ""

if mode == "vehicle":
    bhz, bhn, bhe, peak_amp = vehicle_waveform()
    amb = ambient_noise()
    bhz = [z + a     for z, a in zip(bhz, amb)]
    bhn = [n + a*0.5 for n, a in zip(bhn, amb)]
    bhe = [e + a*0.5 for e, a in zip(bhe, amb)]
    event_id = f"VEH-{time.strftime('%Y%m%d')}-0001"
else:
    bhz = ambient_noise()
    bhn = ambient_noise()
    bhe = ambient_noise()

pgv, intensity = pgv_intensity(bhz, bhn, bhe)

msg  = field_varint(1, ts)
msg += field_string(2, 'RCOE')
msg += field_string(3, 'CI')
msg += field_packed_floats(4, bhz)
msg += field_packed_floats(5, bhn)
msg += field_packed_floats(6, bhe)
msg += field_varint(7, SAMPLE_RATE)
msg += field_float(8, pgv)
msg += field_varint(9, intensity)
if event_id:
    msg += field_string(10, event_id)

# SWL header: bytes S W L \x05 (sensor type 5)
frame = b'SWL\x05' + msg
print(base64.b64encode(frame).decode())
PYEOF
)

curl -s -X POST "$BEAM_URL/api/package/ipv4/async" \
  -H 'Content-Type: application/json' \
  -d "{\"workspaceId\": $WORKSPACE_ID, \"ipv4\": {\"payload\": \"$PAYLOAD\"}}"
