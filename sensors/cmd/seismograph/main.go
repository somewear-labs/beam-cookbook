// ugs simulates an Unattended Ground Sensor (UGS) at the National Training
// Center (NTC), Fort Irwin, CA. UGS nodes detect seismic-acoustic signatures
// of vehicles, dismounted personnel, and explosions.
//
// Wire format: SWL header (type=1) + JSON UGSData
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"

	"somewear/sensors/shared"
)

const sensorType byte = 1

// UGSData is one transmission from a simulated UGS node.
// Each burst carries 10 seismic-acoustic samples at 100 sps.
type UGSData struct {
	Timestamp     int64     `json:"timestamp_ms"`
	NodeID        string    `json:"node_id"`
	Latitude      float64   `json:"latitude"`
	Longitude     float64   `json:"longitude"`
	AltitudeM     float64   `json:"altitude_m"`
	SeismicE      []float64 `json:"seismic_e"`           // East channel, m/s²
	SeismicN      []float64 `json:"seismic_n"`           // North channel, m/s²
	SeismicZ      []float64 `json:"seismic_z"`           // Vertical channel, m/s²
	SampleRateHz  int       `json:"sample_rate_hz"`      // always 100
	PeakAmplitude float64   `json:"peak_amplitude"`      // m/s²
	ThreatType    string    `json:"threat_type"`         // NONE | PERSONNEL | VEHICLE | EXPLOSION
	ConfidencePct int       `json:"confidence_pct"`      // 0–100
	BearingDeg    float64   `json:"bearing_deg"`         // estimated bearing to contact
	RangeM        float64   `json:"estimated_range_m"`   // estimated distance to contact
	EventID       string    `json:"event_id,omitempty"`  // set when a contact is detected
}

// NTC Goldstone area — typical OPFOR maneuver corridor
const (
	nodeID   = "UGS-ALPHA-001"
	nodeLat  = 35.2627
	nodeLon  = -116.6835
	nodeAlt  = 914.0 // metres above sea level

	noiseFloor = 2e-7 // m/s² ambient micro-seismic floor at NTC
)

var eventCounter int

func randn(mean, sigma float64) float64 {
	u1 := 1.0 - rand.Float64()
	u2 := 1.0 - rand.Float64()
	z := math.Sqrt(-2.0*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return mean + sigma*z
}

// ambientNoise generates 10 background samples — desert micro-seismic + wind.
func ambientNoise() []float64 {
	out := make([]float64, 10)
	for i := range out {
		micro := noiseFloor * (1.0 + rand.Float64()*4.0) * math.Sin(2*math.Pi*0.05*float64(i)/100.0)
		hf := randn(0, noiseFloor*0.5)
		out[i] = micro + hf
	}
	return out
}

// vehicleSignature generates a seismic signature typical of a tracked vehicle
// (M1 Abrams / BMP): rhythmic 15–25 Hz tread pattern with strong ground coupling.
func vehicleSignature(amp float64) []float64 {
	out := make([]float64, 10)
	for i := range out {
		t := float64(i) / 100.0
		// Tread fundamental ~18 Hz + harmonic at 36 Hz
		tread := amp * 0.85 * math.Sin(2*math.Pi*18*t)
		tread += amp * 0.35 * math.Sin(2*math.Pi*36*t)
		// Engine vibration envelope
		eng := amp * 0.4 * math.Sin(2*math.Pi*9*t) * math.Exp(-t*2)
		noise := randn(0, noiseFloor*2)
		out[i] = tread + eng + noise
	}
	return out
}

// personnelSignature generates a low-amplitude, irregular signature of a
// dismounted squad: footfall ~1.5–2.5 Hz with spread variance.
func personnelSignature(amp float64) []float64 {
	out := make([]float64, 10)
	for i := range out {
		t := float64(i) / 100.0
		step := amp * math.Sin(2*math.Pi*(1.8+randn(0, 0.2))*t)
		// Inter-person interference
		step += amp * 0.3 * math.Sin(2*math.Pi*(2.1+randn(0, 0.15))*t+0.6)
		noise := randn(0, noiseFloor*3)
		out[i] = step + noise
	}
	return out
}

// explosionSignature generates an impulsive signature: sharp P-wave onset
// followed by Rayleigh surface-wave coda.
func explosionSignature(amp float64) []float64 {
	out := make([]float64, 10)
	for i := range out {
		t := float64(i) / 100.0
		// Impulsive onset
		impEnv := math.Exp(-t * 40)
		imp := amp * impEnv * math.Sin(2*math.Pi*30*t)
		// Slower Rayleigh coda
		codaDelay := 0.02
		codaT := t - codaDelay
		coda := 0.0
		if codaT > 0 {
			coda = amp * 0.6 * math.Exp(-codaT*8) * math.Sin(2*math.Pi*8*codaT)
		}
		noise := randn(0, noiseFloor)
		out[i] = imp + coda + noise
	}
	return out
}

func peakAmp(e, n, z []float64) float64 {
	peak := 0.0
	for i := range e {
		for _, v := range []float64{math.Abs(e[i]), math.Abs(n[i]), math.Abs(z[i])} {
			if v > peak {
				peak = v
			}
		}
	}
	return peak
}

type contactParams struct {
	threatType string
	amp        float64
	confidence int
}

func randomContact() contactParams {
	r := rand.Float64()
	switch {
	case r < 0.50:
		// Vehicle (most common threat at NTC)
		amp := 0.002 + rand.Float64()*0.018 // 2–20 mm/s²
		return contactParams{"VEHICLE", amp, 75 + rand.Intn(25)}
	case r < 0.80:
		// Dismounted personnel
		amp := 0.0002 + rand.Float64()*0.0008 // 0.2–1.0 mm/s²
		return contactParams{"PERSONNEL", amp, 55 + rand.Intn(35)}
	default:
		// Explosion
		amp := 0.05 + rand.Float64()*0.45 // 50–500 mm/s²
		return contactParams{"EXPLOSION", amp, 90 + rand.Intn(10)}
	}
}

func generateReading() UGSData {
	now := time.Now()

	var (
		seisE, seisN, seisZ []float64
		threat               string = "NONE"
		confidence           int    = 0
		bearing              float64
		rangeM               float64
		eventID              string
	)

	// 5% chance of a contact each interval
	if rand.Float64() < 0.05 {
		eventCounter++
		c := randomContact()
		threat = c.threatType
		confidence = c.confidence
		bearing = rand.Float64() * 360
		// Range inversely proportional to amplitude proxy
		rangeM = 800 / (1 + c.amp*50)
		if rangeM < 50 {
			rangeM = 50
		}
		eventID = fmt.Sprintf("TGT-%s-%04d", now.Format("20060102"), eventCounter)

		// Horizontal channels carry most energy for surface targets
		seisE = vehicleSignature(c.amp * 0.8)
		seisN = vehicleSignature(c.amp * 0.8)
		seisZ = vehicleSignature(c.amp * 0.4)

		switch c.threatType {
		case "PERSONNEL":
			seisE = personnelSignature(c.amp * 0.7)
			seisN = personnelSignature(c.amp * 0.7)
			seisZ = personnelSignature(c.amp * 0.3)
		case "EXPLOSION":
			seisE = explosionSignature(c.amp * 0.9)
			seisN = explosionSignature(c.amp * 0.85)
			seisZ = explosionSignature(c.amp * 0.7)
		}
	} else {
		seisE = ambientNoise()
		seisN = ambientNoise()
		seisZ = ambientNoise()
	}

	peak := peakAmp(seisE, seisN, seisZ)

	return UGSData{
		Timestamp:     now.UnixMilli(),
		NodeID:        nodeID,
		Latitude:      nodeLat,
		Longitude:     nodeLon,
		AltitudeM:     nodeAlt,
		SeismicE:      seisE,
		SeismicN:      seisN,
		SeismicZ:      seisZ,
		SampleRateHz:  100,
		PeakAmplitude: peak,
		ThreatType:    threat,
		ConfidencePct: confidence,
		BearingDeg:    bearing,
		RangeM:        rangeM,
		EventID:       eventID,
	}
}

func run(beamURL string, workspaceID int, interval time.Duration, verbose bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	fmt.Printf("[ugs] node=%s lat=%.4f lon=%.4f interval=%s\n",
		nodeID, nodeLat, nodeLon, interval)

	for range ticker.C {
		d := generateReading()

		raw, err := json.Marshal(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ugs] marshal error: %v\n", err)
			continue
		}
		if verbose {
			fmt.Printf("[ugs] payload: %s\n", raw)
		}

		b64, err := shared.WrapSensorPayload(sensorType, raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ugs] wrap error: %v\n", err)
			continue
		}

		if err := shared.SendIPv4(beamURL, workspaceID, b64); err != nil {
			fmt.Fprintf(os.Stderr, "[ugs] send error: %v\n", err)
			continue
		}

		contactNote := ""
		if d.EventID != "" {
			contactNote = fmt.Sprintf(" %s bearing=%.0f° range=%.0fm conf=%d%%",
				d.ThreatType, d.BearingDeg, d.RangeM, d.ConfidencePct)
		}
		fmt.Printf("[ugs] %s node=%s peak=%.2e m/s² threat=%s%s\n",
			now().UTC().Format(time.RFC3339),
			d.NodeID, d.PeakAmplitude, d.ThreatType, contactNote)
	}
}

func now() time.Time { return time.Now() }

func main() {
	url := flag.String("url", shared.DefaultBeamURL, "Beam API URL")
	interval := flag.Duration("interval", 5*time.Second, "Posting interval")
	workspace := flag.Int("workspace", shared.DefaultWorkspaceID, "Workspace ID")
	verbose := flag.Bool("verbose", false, "Print each payload before sending")
	flag.Parse()

	run(*url, *workspace, *interval, *verbose)
}
