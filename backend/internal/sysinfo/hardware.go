package sysinfo

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/sensors"
)

// FileHandles is the kernel's count of open file descriptions against the
// ceiling it will allocate, from /proc/sys/fs/file-nr.
//
// It is the resource a leaking service exhausts *before* memory: a process
// that opens a socket per request and forgets to close it can run a host out
// of handles while every utilisation chart reads idle, and the first symptom
// is "too many open files" in a log nobody is watching. One line from a
// pseudo filesystem, read every sample, is what it costs to see it coming.
type FileHandles struct {
	Open int `json:"open"`
	// Max is 0 when the kernel reports no meaningful ceiling — some container
	// runtimes hand out the 64-bit maximum, which is a ceiling in name only
	// and would make every percentage of it read as zero.
	Max int `json:"max"`
}

// Sensor is one temperature reading from hwmon or a thermal zone.
//
// High and Critical are the sensor's own thresholds where the driver reports
// them, and 0 where it does not. A VPS usually reports no sensors at all,
// which the UI must show as "none reported" rather than as a machine that is
// running cold.
type Sensor struct {
	Name     string  `json:"name"`
	TempC    float64 `json:"tempC"`
	High     float64 `json:"high"`
	Critical float64 `json:"critical"`
}

// fileNRPath is a variable so the tests can point it at a fixture.
var fileNRPath = "/proc/sys/fs/file-nr"

// Past this the "maximum" is the kernel saying it has no opinion, not a limit
// that any process on the machine could approach.
const fileHandleCeiling = 1 << 40

// ReadFileHandles parses "allocated free max". A missing or malformed file
// reads as zero rather than as an error: the feature is a reading, and a
// host that cannot give it should lose the figure, not the snapshot.
func ReadFileHandles() FileHandles {
	raw, err := os.ReadFile(fileNRPath)
	if err != nil {
		return FileHandles{}
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return FileHandles{}
	}
	open, _ := strconv.Atoi(fields[0])
	max, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || max <= 0 || max >= fileHandleCeiling {
		max = 0
	}
	return FileHandles{Open: open, Max: int(max)}
}

// ReadSensors lists every temperature the host exposes, hottest first.
//
// gopsutil reads hwmon and falls back to the legacy thermal zones, which is
// the whole of what a Raspberry Pi, a bare-metal box and a laptop each need.
// Readings at or below zero are dropped: a sensor that reports 0 °C is one
// the driver could not read, and it would sort as the coolest thing on the
// board while meaning nothing.
func ReadSensors(ctx context.Context) []Sensor {
	stats, err := sensors.TemperaturesWithContext(ctx)
	if err != nil && len(stats) == 0 {
		return nil
	}
	out := make([]Sensor, 0, len(stats))
	seen := map[string]bool{}
	for _, s := range stats {
		name := strings.TrimSpace(s.SensorKey)
		if name == "" || s.Temperature <= 0 || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, Sensor{
			Name:     name,
			TempC:    round1(s.Temperature),
			High:     round1(s.High),
			Critical: round1(s.Critical),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TempC > out[j].TempC })
	return out
}
