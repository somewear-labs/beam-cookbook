// tws simulates a Tactical Weather Station (TWS) supporting ground and aviation
// operations at the National Training Center (NTC), Fort Irwin, CA.
//
// Wire format: raw JSON TacticalWeather — NO SWL header.
// The base64 payload encodes the raw JSON bytes directly.
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

// TacticalWeather is one report from the simulated tactical weather station.
type TacticalWeather struct {
	StationID       string  `json:"station_id"`
	Timestamp       string  `json:"timestamp"`            // RFC3339
	TemperatureC    float64 `json:"temperature_c"`
	DewPointC       float64 `json:"dew_point_c"`
	HumidityPct     float64 `json:"humidity_pct"`
	PressureHPa     float64 `json:"pressure_hpa"`
	AltimeterInHg   float64 `json:"altimeter_inhg"`       // for aviation
	WindSpeedKts    float64 `json:"wind_speed_kts"`
	WindDirDeg      float64 `json:"wind_direction_deg"`
	GustKts         float64 `json:"gust_kts"`             // 0 if no gust
	CrosswindKts    float64 `json:"crosswind_kts"`        // relative to primary runway/LZ
	VisibilityKM    float64 `json:"visibility_km"`
	CeilingFt       int     `json:"ceiling_ft"`           // -1 = unlimited
	Condition       string  `json:"condition"`            // Clear | Haze | Dust | Rain | Fog
	FlightCat       string  `json:"flight_category"`      // VFR | MVFR | IFR | LIFR
	DensityAltFt    int     `json:"density_altitude_ft"`
	DustStorm       bool    `json:"dust_storm_warning"`
	OperationalNote string  `json:"operational_note"`     // free-text advisory
}

const stationID = "TWS-BRAVO-001"

// NTC Fort Irwin primary LZ/airstrip heading for crosswind calculation
const lzHeadingDeg = 120.0

const (
	baseTempC    = 34.0
	tempSwingC   = 16.0
	basePressure = 981.0 // hPa — NTC is ~914 m MSL
	baseHumidity = 12.0
)

var (
	windDir   = 230.0 // prevailing SW
	windSpeed = 4.0   // m/s internally; reported in knots
	dustEvent = false
	dustTimer = 0
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

func msToKts(ms float64) float64 { return ms * 1.944 }

// crosswind returns the crosswind component of windDir/windSpeed relative to lzHeading.
func crosswind(windDirDeg, windSpeedKts, lzDeg float64) float64 {
	diff := (windDirDeg - lzDeg) * math.Pi / 180.0
	return math.Abs(windSpeedKts * math.Sin(diff))
}

// flightCategory classifies visibility and ceiling per FAA/military standards.
func flightCategory(visKM float64, ceilingFt int) string {
	visSM := visKM * 0.621371
	switch {
	case (ceilingFt < 0 || ceilingFt >= 3000) && visSM >= 5:
		return "VFR"
	case (ceilingFt < 0 || ceilingFt >= 1000) && visSM >= 3:
		return "MVFR"
	case (ceilingFt < 0 || ceilingFt >= 500) && visSM >= 1:
		return "IFR"
	default:
		return "LIFR"
	}
}

// densityAltitude computes density altitude in feet using the standard formula.
func densityAltitude(pressureHPa, tempC float64) int {
	// Station elevation ~3000 ft at NTC
	const stationElevFt = 3000.0
	isaDev := tempC - (15.0 - 1.98*(stationElevFt/1000.0)) // ISA deviation at elevation
	return int(stationElevFt + 120*isaDev)
}

func operationalNote(cat string, dustStorm bool, gustKts float64) string {
	if dustStorm {
		return "BROWNOUT WARNING — rotor wing ops restricted"
	}
	if gustKts > 25 {
		return "HIGH GUST — sling load ops restricted"
	}
	switch cat {
	case "LIFR":
		return "LIFR — all aviation ops suspended"
	case "IFR":
		return "IFR — instrument rated crews only"
	case "MVFR":
		return "MVFR — marginal conditions, crew discretion"
	}
	return "Ops normal"
}

func generateReading() TacticalWeather {
	now := time.Now()
	hour := float64(now.Hour()) + float64(now.Minute())/60.0

	tempC := baseTempC + tempSwingC*math.Sin(2*math.Pi*(hour-6)/24.0-math.Pi/2)
	tempC += randn(0, 1.5)

	humidity := clamp(baseHumidity+8*math.Cos(2*math.Pi*(hour-6)/24.0)+randn(0, 2), 5, 80)
	dewPointC := tempC - ((100 - humidity) / 5.0) // Magnus approximation

	pressure := clamp(basePressure+randn(0, 1.2), 960, 1040)
	altimeterInHg := math.Round((pressure*0.02953)*100) / 100

	// Wind random walk
	windDir += randn(0, 6.0)
	windDir = math.Mod(windDir+360, 360)
	windSpeed += randn(0, 0.6)
	windSpeed = clamp(windSpeed, 0, 20)
	windSpeedKts := msToKts(windSpeed)

	// Gusts: 15% chance of gusty conditions
	var gustKts float64
	if rand.Float64() < 0.15 {
		gustKts = windSpeedKts + clamp(randn(8, 4), 3, 20)
	}

	// Dust storm: rare, lasts ~10 intervals
	if !dustEvent && rand.Float64() < 0.01 {
		dustEvent = true
		dustTimer = 10 + rand.Intn(10)
	}
	if dustEvent {
		dustTimer--
		if dustTimer <= 0 {
			dustEvent = false
		}
	}

	var visKM float64
	var cond string
	var ceilInt int

	if dustEvent {
		visKM = clamp(randn(0.8, 0.4), 0.1, 3.0)
		cond = "Dust"
		ceilInt = -1
	} else if windSpeed > 12 && humidity < 20 {
		visKM = clamp(randn(8, 3), 3, 20)
		cond = "Haze"
		ceilInt = -1
	} else {
		visKM = clamp(randn(25, 5), 5, 50)
		cond = "Clear"
		ceilInt = -1
	}

	ceilInt = -1 // NTC is predominantly clear/unlimited

	xwind := crosswind(windDir, windSpeedKts, lzHeadingDeg)
	flightCat := flightCategory(visKM, ceilInt)
	da := densityAltitude(pressure, tempC)
	note := operationalNote(flightCat, dustEvent, gustKts)

	round1 := func(v float64) float64 { return math.Round(v*10) / 10 }

	return TacticalWeather{
		StationID:       stationID,
		Timestamp:       now.UTC().Format(time.RFC3339),
		TemperatureC:    round1(tempC),
		DewPointC:       round1(dewPointC),
		HumidityPct:     round1(humidity),
		PressureHPa:     round1(pressure),
		AltimeterInHg:   altimeterInHg,
		WindSpeedKts:    round1(windSpeedKts),
		WindDirDeg:      round1(windDir),
		GustKts:         round1(gustKts),
		CrosswindKts:    round1(xwind),
		VisibilityKM:    round1(visKM),
		CeilingFt:       ceilInt,
		Condition:       cond,
		FlightCat:       flightCat,
		DensityAltFt:    da,
		DustStorm:       dustEvent,
		OperationalNote: note,
	}
}

func run(beamURL string, workspaceID int, interval time.Duration, verbose bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	fmt.Printf("[tws] station=%s interval=%s (raw JSON, no SWL header)\n", stationID, interval)

	for range ticker.C {
		d := generateReading()

		raw, err := json.Marshal(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[tws] marshal error: %v\n", err)
			continue
		}
		if verbose {
			fmt.Printf("[tws] payload: %s\n", raw)
		}

		b64 := shared.WrapRawPayload(raw)

		if err := shared.SendIPv4(beamURL, workspaceID, b64); err != nil {
			fmt.Fprintf(os.Stderr, "[tws] send error: %v\n", err)
			continue
		}

		dustWarn := ""
		if d.DustStorm {
			dustWarn = " DUST_STORM"
		}
		fmt.Printf("[tws] %s station=%s %.0f°@%.0fkts gust=%.0f vis=%.1fkm %s DA=%dft%s\n",
			time.Now().UTC().Format(time.RFC3339),
			d.StationID, d.WindDirDeg, d.WindSpeedKts, d.GustKts,
			d.VisibilityKM, d.FlightCat, d.DensityAltFt, dustWarn)
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
