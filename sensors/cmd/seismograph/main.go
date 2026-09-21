// seismograph simulates a broadband seismometer near Ridgecrest, CA.
// Generates realistic vehicle-passage events (heavy machinery / tanks) with a
// multi-phase envelope: quiet → approach (build-up) → pass-by (peak) → recede (fade).
// Between events the station records low-amplitude ambient microseismic noise.
//
// Wire format: SWL header (type=5) + protobuf SeismicData
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"

	"google.golang.org/protobuf/proto"

	pb "somewear/sensors/proto"
	"somewear/sensors/shared"
)

const sensorType byte = 5

const (
	stationID  = "RCOE"
	netCode    = "CI"
	noiseFloor = 1e-9 // m/s ambient microseismic baseline
	sampleRate = 100  // Hz
	burstLen   = 10   // samples per burst
)

// vehiclePhase tracks where in the passage arc the current event is.
type vehiclePhase int

const (
	phaseApproach vehiclePhase = iota // amplitude building as vehicle closes
	phasePassBy                       // at closest point, maximum amplitude
	phaseRecede                       // amplitude falling as vehicle departs
)

// vehicleEvent holds all state for an ongoing vehicle passage.
// Phase accumulators (groundPhase, treadPhase, enginePhase) advance each burst
// so the waveform stays coherent across intervals instead of resetting.
type vehicleEvent struct {
	phase        vehiclePhase
	stepsLeft    int
	totalSteps   int
	peakAmp      float64 // PGV at closest approach, m/s
	groundRollHz float64 // low-frequency ground roll (1.5–4 Hz)
	treadHz      float64 // tread-impact repetition rate (12–22 Hz)
	engineHz     float64 // engine dominant harmonic (18–30 Hz)
	eventID      string
	groundPhase  float64
	treadPhase   float64
	enginePhase  float64
}

var (
	activeEvent    *vehicleEvent
	vehicleCounter int
)

func randn(mean, sigma float64) float64 {
	u1 := 1.0 - rand.Float64()
	u2 := 1.0 - rand.Float64()
	z := math.Sqrt(-2.0*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return mean + sigma*z
}

// ambientNoise generates low-level background microseismic samples.
func ambientNoise(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		t := float64(i) / sampleRate
		micro := noiseFloor * 3 * math.Sin(2*math.Pi*0.15*t)
		out[i] = micro + randn(0, noiseFloor*1.5)
	}
	return out
}

// vehicleWaveform synthesises the three-channel seismic signature of heavy machinery.
// The waveform has three components:
//   - ground roll: low-frequency body wave from vehicle mass (~2–4 Hz)
//   - tread impact: repetitive mid-frequency impulse from track/tyre contact (~12–22 Hz)
//   - engine harmonic: higher-frequency drivetrain coupling (~18–30 Hz)
//
// Phase accumulators on ev advance so consecutive bursts are phase-continuous.
func vehicleWaveform(ev *vehicleEvent, amp float64, n int) (z, north, east []float64) {
	z = make([]float64, n)
	north = make([]float64, n)
	east = make([]float64, n)

	dt := 1.0 / sampleRate
	gp := ev.groundPhase
	tp := ev.treadPhase
	ep := ev.enginePhase
	dg := 2 * math.Pi * ev.groundRollHz * dt
	dt2 := 2 * math.Pi * ev.treadHz * dt
	de := 2 * math.Pi * ev.engineHz * dt

	for i := range z {
		groundRoll := amp * 0.45 * math.Sin(gp)

		// Tread impact: soft-clip to give it an impulsive, non-sinusoidal shape
		treadLinear := amp * math.Sin(tp)
		tread := math.Tanh(treadLinear/(amp*0.65)) * amp * 0.65

		engine := amp * 0.28 * math.Sin(ep)

		// Z: vertical bounce from tread + ground roll
		z[i] = tread*0.95 + groundRoll*0.75 + randn(0, amp*0.04)
		// N: direction of travel — engine + ground roll
		north[i] = engine*0.85 + groundRoll*0.55 + randn(0, amp*0.04)
		// E: cross-track — tread slap + engine coupling
		east[i] = tread*0.55 + engine*0.45 + randn(0, amp*0.05)

		gp += dg
		tp += dt2
		ep += de
	}

	ev.groundPhase = gp
	ev.treadPhase = tp
	ev.enginePhase = ep
	return z, north, east
}

func newVehicleEvent() *vehicleEvent {
	vehicleCounter++
	approachSteps := 5 + rand.Intn(5) // 5–9 intervals of build-up (~25–45 s)
	return &vehicleEvent{
		phase:        phaseApproach,
		stepsLeft:    approachSteps,
		totalSteps:   approachSteps,
		peakAmp:      2e-5 + rand.Float64()*3e-4, // 2e-5 to 3.2e-4 m/s
		groundRollHz: 1.5 + rand.Float64()*2.5,
		treadHz:      12.0 + rand.Float64()*10.0,
		engineHz:     18.0 + rand.Float64()*12.0,
		eventID:      fmt.Sprintf("VEH-%s-%04d", time.Now().Format("20060102"), vehicleCounter),
		groundPhase:  rand.Float64() * 2 * math.Pi,
		treadPhase:   rand.Float64() * 2 * math.Pi,
		enginePhase:  rand.Float64() * 2 * math.Pi,
	}
}

// mmIntensity converts PGV (m/s) to Modified Mercalli Intensity (Wald et al. 1999).
func mmIntensity(pgv float64) int {
	switch {
	case pgv >= 1.2:
		return 10
	case pgv >= 0.50:
		return 9
	case pgv >= 0.20:
		return 8
	case pgv >= 0.05:
		return 7
	case pgv >= 0.01:
		return 6
	case pgv >= 0.005:
		return 5
	case pgv >= 0.002:
		return 4
	case pgv >= 0.001:
		return 3
	case pgv >= 0.0001:
		return 2
	default:
		return 1
	}
}

// toFloat32s converts a []float64 slice to []float32 for protobuf repeated float fields.
func toFloat32s(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

func generateReading() (*pb.SeismicData, string) {
	now := time.Now()

	// 8% chance per quiet interval of a vehicle passage beginning
	if activeEvent == nil && rand.Float64() < 0.08 {
		activeEvent = newVehicleEvent()
	}

	var bhz, bhn, bhe []float64
	var eventID, phaseLabel string

	if activeEvent != nil {
		ev := activeEvent
		var amp float64

		switch ev.phase {
		case phaseApproach:
			// Linear ramp: starts at 5% of peak, climbs to ~90%
			progress := 1.0 - float64(ev.stepsLeft)/float64(ev.totalSteps)
			amp = ev.peakAmp * (0.05 + progress*0.85)
			phaseLabel = "approach"

		case phasePassBy:
			// Peak amplitude with slight variation
			amp = ev.peakAmp * (0.92 + rand.Float64()*0.10)
			phaseLabel = "pass-by"

		case phaseRecede:
			// Exponential-ish decay back toward quiet
			progress := float64(ev.stepsLeft) / float64(ev.totalSteps)
			amp = ev.peakAmp * (0.04 + progress*0.86)
			phaseLabel = "recede"
		}

		bhz, bhn, bhe = vehicleWaveform(ev, amp, burstLen)

		// Ambient noise rides underneath — negligible vs. vehicle amplitude
		amb := ambientNoise(burstLen)
		for i := range bhz {
			bhz[i] += amb[i]
			bhn[i] += amb[i] * 0.5
			bhe[i] += amb[i] * 0.5
		}

		eventID = ev.eventID

		// Advance state machine
		ev.stepsLeft--
		if ev.stepsLeft <= 0 {
			switch ev.phase {
			case phaseApproach:
				passSteps := 2 + rand.Intn(2) // 2–3 intervals at peak
				ev.phase = phasePassBy
				ev.stepsLeft = passSteps
				ev.totalSteps = passSteps
			case phasePassBy:
				recedeSteps := 5 + rand.Intn(5) // 5–9 intervals fading
				ev.phase = phaseRecede
				ev.stepsLeft = recedeSteps
				ev.totalSteps = recedeSteps
			case phaseRecede:
				activeEvent = nil
				phaseLabel = "complete"
			}
		}
	} else {
		bhz = ambientNoise(burstLen)
		bhn = ambientNoise(burstLen)
		bhe = ambientNoise(burstLen)
	}

	pgv := 0.0
	for i := range bhz {
		for _, v := range []float64{math.Abs(bhz[i]), math.Abs(bhn[i]), math.Abs(bhe[i])} {
			if v > pgv {
				pgv = v
			}
		}
	}

	d := &pb.SeismicData{
		TimestampMs:  now.UnixMilli(),
		StationId:    stationID,
		NetworkCode:  netCode,
		ChannelBhz:   toFloat32s(bhz),
		ChannelBhn:   toFloat32s(bhn),
		ChannelBhe:   toFloat32s(bhe),
		SampleRateHz: int32(sampleRate),
		PgvMs:        float32(pgv),
		Intensity:    int32(mmIntensity(pgv)),
		EventId:      eventID,
	}
	return d, phaseLabel
}

func run(beamURL string, workspaceID int, interval time.Duration, verbose bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	fmt.Printf("[seismograph] station=%s.%s interval=%s\n", netCode, stationID, interval)

	for range ticker.C {
		d, phaseLabel := generateReading()

		raw, err := proto.Marshal(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[seismograph] marshal error: %v\n", err)
			continue
		}
		if verbose {
			fmt.Printf("[seismograph] payload: station=%s.%s ts=%d pgv=%.2e intensity=%d event=%s\n",
				d.NetworkCode, d.StationId, d.TimestampMs, d.PgvMs, d.Intensity, d.EventId)
		}

		b64, err := shared.WrapSensorPayload(sensorType, raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[seismograph] wrap error: %v\n", err)
			continue
		}

		if err := shared.SendIPv4(beamURL, workspaceID, b64); err != nil {
			fmt.Fprintf(os.Stderr, "[seismograph] send error: %v\n", err)
			continue
		}

		eventNote := ""
		if d.EventId != "" {
			eventNote = fmt.Sprintf(" EVENT=%s phase=%s", d.EventId, phaseLabel)
		}
		fmt.Printf("[seismograph] %s station=%s.%s pgv=%.2e m/s intensity=%d%s\n",
			time.Now().UTC().Format(time.RFC3339),
			d.NetworkCode, d.StationId, d.PgvMs, d.Intensity, eventNote)
	}
}

func main() {
	url := flag.String("url", shared.DefaultBeamURL, "Beam API URL")
	interval := flag.Duration("interval", 5*time.Second, "Posting interval")
	workspace := flag.Int("workspace", shared.DefaultWorkspaceID, "Workspace ID")
	verbose := flag.Bool("verbose", false, "Print each payload before sending")
	flag.Parse()

	run(*url, *workspace, *interval, *verbose)
}
