// vimu simulates a Vehicle Inertial Measurement Unit (V-IMU) mounted on an
// M1A2 SEPv3 Abrams MBT at the National Training Center (NTC), Fort Irwin, CA.
//
// Wire format: SWL header (type=4) + JSON VehicleIMUData
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

const sensorType byte = 4

// VehicleIMUData is one burst from the V-IMU.
type VehicleIMUData struct {
	Timestamp   int64   `json:"timestamp_ms"`
	VehicleID   string  `json:"vehicle_id"`    // callsign / bumper number
	UnitID      string  `json:"unit_id"`       // e.g. "A/3-8 CAV"
	AccelX      float64 `json:"accel_x"`       // m/s² — longitudinal (positive forward)
	AccelY      float64 `json:"accel_y"`       // m/s² — lateral (positive right)
	AccelZ      float64 `json:"accel_z"`       // m/s² — vertical (positive up, ~9.81 static)
	GyroX       float64 `json:"gyro_x"`        // rad/s — roll rate
	GyroY       float64 `json:"gyro_y"`        // rad/s — pitch rate
	GyroZ       float64 `json:"gyro_z"`        // rad/s — yaw rate
	MagX        float64 `json:"mag_x"`         // µT
	MagY        float64 `json:"mag_y"`         // µT
	MagZ        float64 `json:"mag_z"`         // µT
	Roll        float64 `json:"roll_deg"`
	Pitch       float64 `json:"pitch_deg"`
	Heading     float64 `json:"heading_deg"`   // magnetic heading
	SpeedMPS    float64 `json:"speed_mps"`     // estimated ground speed
	GForcePeak  float64 `json:"g_force_peak"`  // peak G this interval
	MotionState string  `json:"motion_state"`  // STATIONARY | MOVING | MANEUVERING | FIRING
	TempC       float64 `json:"temperature_c"` // IMU housing temp
}

const (
	vehicleID = "A-31"       // Alpha Company, 3rd Platoon, 1st vehicle
	unitID    = "A/3-8 CAV"
)

var (
	roll    = 0.0
	pitch   = 1.0
	heading = 60.0  // initial heading NE
	speed   = 0.0   // m/s
	state   = "STATIONARY"
	stateTTL = 0    // intervals remaining in current state
)

func randn(mean, sigma float64) float64 {
	u1 := 1.0 - rand.Float64()
	u2 := 1.0 - rand.Float64()
	z := math.Sqrt(-2.0*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return mean + sigma*z
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// updateState transitions the vehicle through realistic tactical motion states.
func updateState() {
	if stateTTL > 0 {
		stateTTL--
		return
	}
	r := rand.Float64()
	switch state {
	case "STATIONARY":
		if r < 0.3 {
			state = "MOVING"
			stateTTL = 10 + rand.Intn(20)
		} else if r < 0.35 {
			state = "FIRING"
			stateTTL = 1
		}
	case "MOVING":
		if r < 0.15 {
			state = "STATIONARY"
			stateTTL = 5 + rand.Intn(15)
		} else if r < 0.25 {
			state = "MANEUVERING"
			stateTTL = 3 + rand.Intn(8)
		}
	case "MANEUVERING":
		if r < 0.4 {
			state = "MOVING"
			stateTTL = 5 + rand.Intn(10)
		}
	case "FIRING":
		state = "STATIONARY"
		stateTTL = 3
	}
}

func generateReading() VehicleIMUData {
	updateState()

	var targetSpeed float64
	var vibAmp, headingRate float64

	switch state {
	case "STATIONARY":
		targetSpeed = 0
		vibAmp = 0.1 // idle engine vibration
		headingRate = 0
	case "MOVING":
		targetSpeed = 8 + rand.Float64()*5 // 8–13 m/s (~18–29 mph)
		vibAmp = 1.2                         // road/terrain vibration
		headingRate = 0.3
	case "MANEUVERING":
		targetSpeed = 4 + rand.Float64()*4 // slower during turns
		vibAmp = 1.8
		headingRate = 2.5 // faster turn rate
	case "FIRING":
		targetSpeed = 0
		vibAmp = 8.0  // main gun impulse
		headingRate = 0
	}

	// Smooth speed transition
	speed += (targetSpeed - speed) * 0.3
	speed = clamp(speed+randn(0, 0.3), 0, 20)

	// Heading drift
	heading += randn(0, headingRate)
	heading = math.Mod(heading+360, 360)

	// Terrain-induced roll and pitch
	roll += randn(0, 0.4)
	roll = clamp(roll, -20, 20)
	pitch += randn(0, 0.3)
	pitch = clamp(pitch, -15, 15)

	rollR := roll * math.Pi / 180.0
	pitchR := pitch * math.Pi / 180.0

	g := 9.806
	// Longitudinal: forward accel = speed change + gravity on slope
	ax := randn(0, vibAmp) - g*math.Sin(pitchR)
	// Lateral: centripetal during turns + gravity on slope
	ay := randn(0, vibAmp*0.8) + g*math.Sin(rollR)*math.Cos(pitchR)
	// Vertical: gravity + vertical vibration
	az := g*math.Cos(rollR)*math.Cos(pitchR) + randn(0, vibAmp*0.5)

	if state == "FIRING" {
		// Main gun recoil: sharp spike on X axis
		ax += randn(-15, 5)
		az += randn(3, 2)
	}

	// Gyro
	gx := randn(0, 0.02+vibAmp*0.005)
	gy := randn(0, 0.02+vibAmp*0.005)
	gz := randn(0, 0.03+headingRate*0.02)

	// Magnetic field — NTC area (inclination ~59°, decl ~12°E)
	const (
		magNorth = 22.5
		magEast  = 5.0
		magDown  = 41.0
	)
	yawR := heading * math.Pi / 180.0
	mx := magNorth*math.Cos(yawR)*math.Cos(pitchR) + magEast*math.Sin(yawR)*math.Cos(pitchR) + magDown*math.Sin(pitchR) + randn(0, 0.8)
	my := -magNorth*math.Sin(yawR)*math.Cos(rollR) + magEast*math.Cos(yawR)*math.Cos(rollR) + magDown*math.Sin(rollR) + randn(0, 0.8)
	mz := magNorth*(math.Sin(yawR)*math.Sin(rollR)-math.Cos(yawR)*math.Sin(pitchR)*math.Cos(rollR)) +
		magEast*(-math.Cos(yawR)*math.Sin(rollR)-math.Sin(yawR)*math.Sin(pitchR)*math.Cos(rollR)) +
		magDown*math.Cos(pitchR)*math.Cos(rollR) + randn(0, 0.8)

	// Peak G
	totalA := math.Sqrt(ax*ax + ay*ay + az*az)
	gForcePeak := math.Round(totalA/g*100) / 100

	tempC := clamp(38.0+randn(0, 1.0), 20, 70)

	round2 := func(v float64) float64 { return math.Round(v*100) / 100 }

	return VehicleIMUData{
		Timestamp:   time.Now().UnixMilli(),
		VehicleID:   vehicleID,
		UnitID:      unitID,
		AccelX:      round2(ax),
		AccelY:      round2(ay),
		AccelZ:      round2(az),
		GyroX:       round2(gx),
		GyroY:       round2(gy),
		GyroZ:       round2(gz),
		MagX:        round2(mx),
		MagY:        round2(my),
		MagZ:        round2(mz),
		Roll:        math.Round(roll*10) / 10,
		Pitch:       math.Round(pitch*10) / 10,
		Heading:     math.Round(heading*10) / 10,
		SpeedMPS:    math.Round(speed*100) / 100,
		GForcePeak:  gForcePeak,
		MotionState: state,
		TempC:       math.Round(tempC*10) / 10,
	}
}

func run(beamURL string, workspaceID int, interval time.Duration, verbose bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	fmt.Printf("[vimu] vehicle=%s unit=%s interval=%s\n", vehicleID, unitID, interval)

	for range ticker.C {
		d := generateReading()

		raw, err := json.Marshal(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vimu] marshal error: %v\n", err)
			continue
		}
		if verbose {
			fmt.Printf("[vimu] payload: %s\n", raw)
		}

		b64, err := shared.WrapSensorPayload(sensorType, raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[vimu] wrap error: %v\n", err)
			continue
		}

		if err := shared.SendIPv4(beamURL, workspaceID, b64); err != nil {
			fmt.Fprintf(os.Stderr, "[vimu] send error: %v\n", err)
			continue
		}

		fmt.Printf("[vimu] %s vehicle=%s hdg=%.0f° speed=%.1fm/s state=%s g=%.2f\n",
			time.Now().UTC().Format(time.RFC3339),
			d.VehicleID, d.Heading, d.SpeedMPS, d.MotionState, d.GForcePeak)
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
