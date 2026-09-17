// cbrn simulates a Joint Chemical Agent Detector (JCAD) / CBRN standoff
// detector node at a forward operating position.
//
// Wire format: SWL header (type=2) + JSON CBRNData
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

const sensorType byte = 2

// CBRNData is one reading from the simulated CBRN detector node.
type CBRNData struct {
	Timestamp      int64   `json:"timestamp_ms"`
	NodeID         string  `json:"node_id"`
	RadiationMRadH float64 `json:"radiation_mrad_hr"`   // background ~0.01–0.02; alert >10
	ChemAgent      string  `json:"chem_agent"`          // NONE | GA | GB | VX | HD | CG | AC
	ChemConcentPPB float64 `json:"chem_concent_ppb"`    // 0 when NONE
	BioIndicator   bool    `json:"bio_indicator"`       // positive aerosol trigger
	ToxIndustrial  bool    `json:"tox_industrial"`      // TIC detected above threshold
	ThreatLevel    string  `json:"threat_level"`        // GREEN | YELLOW | RED | BLACK
	ConfidencePct  int     `json:"confidence_pct"`
	WindDirDeg     float64 `json:"wind_dir_deg"`        // upwind bearing for source estimation
	TempC          float64 `json:"temperature_c"`
	HumidityPct    float64 `json:"humidity_pct"`
	AlarmActive    bool    `json:"alarm_active"`
	EventID        string  `json:"event_id,omitempty"`
}

// Chemical agent names and typical action levels (ppb)
var agents = []struct {
	code      string
	name      string
	actionPPB float64 // concentration that triggers RED
}{
	{"GA", "Tabun", 0.003},
	{"GB", "Sarin", 0.001},
	{"VX", "VX", 0.0001},
	{"HD", "Mustard", 1.0},
	{"CG", "Phosgene", 200.0},
	{"AC", "Hydrogen Cyanide", 2000.0},
}

const nodeID = "CBRN-DET-001"

var (
	eventCounter int
	windDir      = 270.0 // prevailing westerly
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

func threatLevel(radiation float64, chemAgent string, bio, tic bool, chemConc float64) (string, bool) {
	if chemAgent != "NONE" {
		// Find action level for this agent
		for _, a := range agents {
			if a.code == chemAgent && chemConc >= a.actionPPB*10 {
				return "BLACK", true
			}
			if a.code == chemAgent && chemConc >= a.actionPPB {
				return "RED", true
			}
		}
		return "YELLOW", true
	}
	if radiation > 50 {
		return "RED", true
	}
	if radiation > 10 || bio || tic {
		return "YELLOW", true
	}
	return "GREEN", false
}

func generateReading() CBRNData {
	now := time.Now()

	// Wind drift
	windDir += randn(0, 5)
	windDir = math.Mod(windDir+360, 360)

	// Background radiation: 0.01–0.025 mrad/hr with occasional spikes
	radiation := clamp(randn(0.015, 0.003), 0.005, 500)
	if rand.Float64() < 0.005 {
		// Rare radiation spike (fallout sim, nuclear detonation far off)
		radiation = 20 + rand.Float64()*200
	}

	// Ambient temperature and humidity (NTC desert)
	tempC := clamp(randn(34, 4), 10, 55)
	humidity := clamp(randn(15, 5), 5, 80)

	chemAgent := "NONE"
	chemConc := 0.0
	bio := false
	tic := false
	confidence := 0
	var eventID string

	// 3% chance of a chemical detection event
	if rand.Float64() < 0.03 {
		eventCounter++
		ag := agents[rand.Intn(len(agents))]
		chemAgent = ag.code
		// Concentration: 0.1–100× action level
		chemConc = ag.actionPPB * (0.1 + rand.Float64()*100)
		confidence = 60 + rand.Intn(40)
		eventID = fmt.Sprintf("CBRN-%s-%04d", now.Format("20060102"), eventCounter)
	} else if rand.Float64() < 0.01 {
		// Bio trigger
		bio = true
		confidence = 40 + rand.Intn(30) // lower confidence — bio needs lab confirmation
		eventCounter++
		eventID = fmt.Sprintf("CBRN-%s-%04d", now.Format("20060102"), eventCounter)
	} else if rand.Float64() < 0.01 {
		// TIC (toxic industrial chemical) — ammonia, chlorine, etc.
		tic = true
		confidence = 70 + rand.Intn(25)
		eventCounter++
		eventID = fmt.Sprintf("CBRN-%s-%04d", now.Format("20060102"), eventCounter)
	}

	level, alarm := threatLevel(radiation, chemAgent, bio, tic, chemConc)

	return CBRNData{
		Timestamp:      now.UnixMilli(),
		NodeID:         nodeID,
		RadiationMRadH: math.Round(radiation*1000) / 1000,
		ChemAgent:      chemAgent,
		ChemConcentPPB: math.Round(chemConc*1000) / 1000,
		BioIndicator:   bio,
		ToxIndustrial:  tic,
		ThreatLevel:    level,
		ConfidencePct:  confidence,
		WindDirDeg:     math.Round(windDir*10) / 10,
		TempC:          math.Round(tempC*10) / 10,
		HumidityPct:    math.Round(humidity*10) / 10,
		AlarmActive:    alarm,
		EventID:        eventID,
	}
}

func run(beamURL string, workspaceID int, interval time.Duration, verbose bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	fmt.Printf("[cbrn] node=%s interval=%s\n", nodeID, interval)

	for range ticker.C {
		d := generateReading()

		raw, err := json.Marshal(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[cbrn] marshal error: %v\n", err)
			continue
		}
		if verbose {
			fmt.Printf("[cbrn] payload: %s\n", raw)
		}

		b64, err := shared.WrapSensorPayload(sensorType, raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[cbrn] wrap error: %v\n", err)
			continue
		}

		if err := shared.SendIPv4(beamURL, workspaceID, b64); err != nil {
			fmt.Fprintf(os.Stderr, "[cbrn] send error: %v\n", err)
			continue
		}

		alarmStr := ""
		if d.AlarmActive {
			alarmStr = fmt.Sprintf(" *** ALARM agent=%s conc=%.4fppb conf=%d%%", d.ChemAgent, d.ChemConcentPPB, d.ConfidencePct)
		}
		fmt.Printf("[cbrn] %s node=%s rad=%.3f_mR/hr threat=%s%s\n",
			time.Now().UTC().Format(time.RFC3339),
			d.NodeID, d.RadiationMRadH, d.ThreatLevel, alarmStr)
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
