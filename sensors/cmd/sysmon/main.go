// sysmon collects host CPU, memory, and disk diagnostics and posts them to
// Beam on a configurable interval.
//
// Wire format: SWL header (type=7) + protobuf ComputerDiagnosticsData
// CPU utilisation is derived from two successive /proc/stat samples; the
// first sample is taken at startup so the first posted reading is accurate.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	pb "somewear/sensors/proto"
	"somewear/sensors/shared"
)

const sensorType byte = 7

// cpuStat holds the raw /proc/stat tick counters for one CPU line.
type cpuStat struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (s cpuStat) busy() uint64  { return s.user + s.nice + s.system + s.irq + s.softirq + s.steal }
func (s cpuStat) total() uint64 { return s.busy() + s.idle + s.iowait }

// usagePct returns utilisation percentage between two samples.
func usagePct(prev, cur cpuStat) float32 {
	dt := cur.total() - prev.total()
	db := cur.busy() - prev.busy()
	if dt == 0 {
		return 0
	}
	return float32(db) * 100.0 / float32(dt)
}

// readProcStat returns the overall cpu line followed by per-core lines.
func readProcStat() (overall cpuStat, cores []cpuStat, err error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		var s cpuStat
		vals := []*uint64{&s.user, &s.nice, &s.system, &s.idle, &s.iowait, &s.irq, &s.softirq, &s.steal}
		for i, p := range vals {
			if i+1 < len(fields) {
				*p, _ = strconv.ParseUint(fields[i+1], 10, 64)
			}
		}
		if fields[0] == "cpu" {
			overall = s
		} else {
			cores = append(cores, s)
		}
	}
	err = scanner.Err()
	return
}

// readLoadAvg returns the three load averages from /proc/loadavg.
func readLoadAvg() (avg1, avg5, avg15 float32) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		f, _ := strconv.ParseFloat(fields[0], 32)
		avg1 = float32(f)
		f, _ = strconv.ParseFloat(fields[1], 32)
		avg5 = float32(f)
		f, _ = strconv.ParseFloat(fields[2], 32)
		avg15 = float32(f)
	}
	return
}

// readMemInfo returns total and available memory in MB.
func readMemInfo() (totalMB, usedMB uint64, usedPct float32) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	var totalKB, availKB uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			totalKB = v
		case "MemAvailable:":
			availKB = v
		}
	}
	totalMB = totalKB / 1024
	usedKB := totalKB - availKB
	usedMB = usedKB / 1024
	if totalKB > 0 {
		usedPct = float32(usedKB) * 100.0 / float32(totalKB)
	}
	return
}

// readDisk returns total and used bytes for the root filesystem.
func readDisk() (totalGB, usedGB uint64, usedPct float32) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return
	}
	bs := uint64(st.Bsize)
	total := bs * st.Blocks
	avail := bs * st.Bavail
	used := total - avail
	totalGB = total / (1024 * 1024 * 1024)
	usedGB = used / (1024 * 1024 * 1024)
	if total > 0 {
		usedPct = float32(used) * 100.0 / float32(total)
	}
	return
}

// readUptime returns total uptime in seconds.
func readUptime() float32 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.Fields(string(data))[0], 32)
	return float32(f)
}

type prevSample struct {
	overall cpuStat
	cores   []cpuStat
}

func generateReading(prev *prevSample) (*pb.ComputerDiagnosticsData, *prevSample) {
	overall, cores, _ := readProcStat()
	cur := &prevSample{overall, cores}

	var cpuPct float32
	var perCore []float32
	if prev != nil {
		cpuPct = usagePct(prev.overall, overall)
		n := len(prev.cores)
		if len(cores) < n {
			n = len(cores)
		}
		for i := 0; i < n; i++ {
			perCore = append(perCore, usagePct(prev.cores[i], cores[i]))
		}
	}

	totalMB, usedMB, memPct := readMemInfo()
	totalGB, usedGB, diskPct := readDisk()
	avg1, avg5, avg15 := readLoadAvg()
	uptime := readUptime()

	hostname, _ := os.Hostname()

	d := &pb.ComputerDiagnosticsData{
		TimestampMs:   time.Now().UnixMilli(),
		NodeId:        hostname,
		CpuUsagePct:   cpuPct,
		PerCorePct:    perCore,
		LoadAvg_1M:    avg1,
		LoadAvg_5M:    avg5,
		LoadAvg_15M:   avg15,
		MemoryTotalMb: totalMB,
		MemoryUsedMb:  usedMB,
		MemoryUsedPct: memPct,
		DiskTotalGb:   totalGB,
		DiskUsedGb:    usedGB,
		DiskUsedPct:   diskPct,
		UptimeSeconds: uptime,
	}
	return d, cur
}

func run(beamURL string, workspaceID int, interval time.Duration, verbose bool) {
	hostname, _ := os.Hostname()
	fmt.Printf("[sysmon] node=%s interval=%s\n", hostname, interval)

	// Warm-up sample so the first tick has a meaningful CPU delta.
	warmupOverall, warmupCores, _ := readProcStat()
	prev := &prevSample{warmupOverall, warmupCores}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		d, cur := generateReading(prev)
		prev = cur

		raw, err := proto.Marshal(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[sysmon] marshal error: %v\n", err)
			continue
		}

		if verbose {
			fmt.Printf("[sysmon] payload: node=%s cpu=%.1f%% mem=%.1f%% disk=%.1f%% load=%.2f/%.2f/%.2f\n",
				d.NodeId, d.CpuUsagePct, d.MemoryUsedPct, d.DiskUsedPct,
				d.LoadAvg_1M, d.LoadAvg_5M, d.LoadAvg_15M)
		}

		b64, err := shared.WrapSensorPayload(sensorType, raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[sysmon] wrap error: %v\n", err)
			continue
		}

		if err := shared.SendIPv4(beamURL, workspaceID, b64); err != nil {
			fmt.Fprintf(os.Stderr, "[sysmon] send error: %v\n", err)
			continue
		}

		fmt.Printf("[sysmon] %s node=%s cpu=%.1f%% mem=%d/%dMB(%.1f%%) disk=%d/%dGB(%.1f%%) load=%.2f\n",
			time.Now().UTC().Format(time.RFC3339),
			d.NodeId, d.CpuUsagePct,
			d.MemoryUsedMb, d.MemoryTotalMb, d.MemoryUsedPct,
			d.DiskUsedGb, d.DiskTotalGb, d.DiskUsedPct,
			d.LoadAvg_1M)
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
