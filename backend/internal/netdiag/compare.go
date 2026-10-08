package netdiag

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

const MaxDiffLines = 128

type ValueChange struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type Difference struct {
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Unchanged int      `json:"unchanged"`
	Truncated bool     `json:"truncated"`
}

type Comparison struct {
	BeforeID        string              `json:"beforeId"`
	AfterID         string              `json:"afterId"`
	Request         netsec.ProbeRequest `json:"request"`
	Status          ValueChange         `json:"status"`
	Outcome         ValueChange         `json:"outcome"`
	Duration        ValueChange         `json:"duration"`
	DurationDeltaMS *float64            `json:"durationDeltaMs,omitempty"`
	Records         Difference          `json:"records"`
	Output          Difference          `json:"output"`
	Partial         bool                `json:"partial"`
	Limitations     []string            `json:"limitations"`
}

func (s *Service) Export(ctx context.Context, id string) ([]byte, error) {
	run, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(struct {
		Version int `json:"version"`
		Run     Run `json:"run"`
	}{Version: 1, Run: run})
	if err != nil {
		return nil, err
	}
	if len(data) > MaxExportBytes {
		return nil, fmt.Errorf("diagnostic export exceeded its bound")
	}
	return data, nil
}

func (s *Service) Compare(ctx context.Context, beforeID, afterID string) (Comparison, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Comparison{}, err
	}
	if err := s.prune(ctx); err != nil {
		return Comparison{}, err
	}
	a, err := s.store.Get(ctx, beforeID)
	if err != nil {
		return Comparison{}, err
	}
	b, err := s.store.Get(ctx, afterID)
	if err != nil {
		return Comparison{}, err
	}
	if a.Request != b.Request || !terminal(a.Status) || !terminal(b.Status) || a.Result == nil || b.Result == nil {
		return Comparison{}, ErrIncompatible
	}
	c := Comparison{BeforeID: a.ID, AfterID: b.ID, Request: a.Request,
		Status: ValueChange{a.Status, b.Status}, Outcome: ValueChange{a.Outcome, b.Outcome},
		Duration:    ValueChange{a.Result.Duration, b.Result.Duration},
		Records:     difference(a.Result.Records, b.Result.Records),
		Output:      difference(strings.Split(a.Result.Output, "\n"), strings.Split(b.Result.Output, "\n")),
		Partial:     a.ResultTruncated || b.ResultTruncated,
		Limitations: []string{"Record and output differences are sets of retained lines; order and duplicate counts are ignored.", "Tool output may contain changing timestamps or counters. A difference does not establish its cause or every intervening policy layer."}}
	if x, err := time.ParseDuration(a.Result.Duration); err == nil {
		if y, err := time.ParseDuration(b.Result.Duration); err == nil {
			delta := float64(y-x) / float64(time.Millisecond)
			c.DurationDeltaMS = &delta
		}
	}
	c.Partial = c.Partial || c.Records.Truncated || c.Output.Truncated
	return c, nil
}

func difference(before, after []string) Difference {
	a, b := map[string]bool{}, map[string]bool{}
	for _, line := range before {
		if line != "" {
			a[line] = true
		}
	}
	for _, line := range after {
		if line != "" {
			b[line] = true
		}
	}
	d := Difference{Added: []string{}, Removed: []string{}}
	for line := range b {
		if a[line] {
			d.Unchanged++
		} else {
			d.Added = append(d.Added, line)
		}
	}
	for line := range a {
		if !b[line] {
			d.Removed = append(d.Removed, line)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	for _, lines := range []*[]string{&d.Added, &d.Removed} {
		if len(*lines) > MaxDiffLines {
			*lines = (*lines)[:MaxDiffLines]
			d.Truncated = true
		}
		for i, line := range *lines {
			var clipped bool
			(*lines)[i], clipped = clip(line, MaxRecordBytes)
			d.Truncated = d.Truncated || clipped
		}
	}
	return d
}
