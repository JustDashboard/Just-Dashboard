package procs

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type WorkloadReport struct {
	CheckedAt time.Time `json:"checkedAt"`
	Sort      string    `json:"sort"`
	Processes []Process `json:"processes"`
	Total     int       `json:"total"`
	Silences  []string  `json:"silences"`
}

// Workloads takes two snapshots so attribution does not depend on another
// browser having warmed the sampler. All controls reuse the process identity
// and authorization of the process inventory; this method only reads.
func (t *Table) Workloads(ctx context.Context, order string) (*WorkloadReport, error) {
	if _, err := t.Snapshot(ctx); err != nil {
		return nil, err
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	rows, err := t.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	report := &WorkloadReport{CheckedAt: time.Now().UTC(), Sort: order, Total: len(rows), Processes: []Process{}, Silences: []string{}}
	if len(rows) > 4096 {
		report.Silences = append(report.Silences, "Only the first 4096 processes were inspected; this is partial attribution.")
		rows = rows[:4096]
	}
	missingCPU, missingMemory, missingHandles, missingIO := 0, 0, 0, 0
	for i := range rows {
		if !rows[i].CPUReady {
			missingCPU++
		}
		if !rows[i].MemoryReady {
			missingMemory++
		}
		if !rows[i].IOReady {
			missingIO++
		}
		if order != "handles" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := process.NewProcessWithContext(ctx, rows[i].PID)
		if err != nil {
			missingHandles++
			continue
		}
		created, err := p.CreateTimeWithContext(ctx)
		if err != nil || created != rows[i].CreateTime.UnixMilli() {
			missingHandles++
			continue
		}
		fds, err := p.NumFDsWithContext(ctx)
		if err != nil {
			missingHandles++
			continue
		}
		rows[i].FDs = fds
		rows[i].FDReady = true
	}
	if missingCPU > 0 {
		report.Silences = append(report.Silences, fmt.Sprintf("CPU intervals unavailable for %d processes (new, replaced or unreadable).", missingCPU))
	}
	if missingMemory > 0 {
		report.Silences = append(report.Silences, fmt.Sprintf("Memory and swap unavailable for %d processes.", missingMemory))
	}
	if missingHandles > 0 {
		report.Silences = append(report.Silences, fmt.Sprintf("Open-file counts unavailable for %d processes.", missingHandles))
	}
	if order == "io" && missingIO > 0 {
		report.Silences = append(report.Silences, fmt.Sprintf("Disk I/O intervals unavailable for %d processes.", missingIO))
	}
	sortWorkloads(rows, order)
	report.Processes = append(report.Processes, rows[:min(len(rows), 20)]...)
	return report, nil
}

func sortWorkloads(rows []Process, order string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch order {
		case "memory":
			if a.RSS != b.RSS {
				return a.RSS > b.RSS
			}
		case "swap":
			if a.Swap != b.Swap {
				return a.Swap > b.Swap
			}
		case "handles":
			if a.FDs != b.FDs {
				return a.FDs > b.FDs
			}
		case "io":
			if a.IOReadRate+a.IOWriteRate != b.IOReadRate+b.IOWriteRate {
				return a.IOReadRate+a.IOWriteRate > b.IOReadRate+b.IOWriteRate
			}
		default:
			if a.CPUPercent != b.CPUPercent {
				return a.CPUPercent > b.CPUPercent
			}
		}
		if a.RSS != b.RSS {
			return a.RSS > b.RSS
		}
		return a.PID < b.PID
	})
}
