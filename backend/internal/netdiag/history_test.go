package netdiag

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

func pingResult(loss, avg float64, verdict string) *netsec.ProbeResult {
	return &netsec.ProbeResult{Tool: "ping", Target: "192.0.2.9", OK: verdict != netsec.ProbeUnknown, Verdict: verdict, Duration: "3s",
		Summary: "summary", Metrics: []netsec.ProbeMetric{{Key: "loss", Label: "Packet loss", Value: loss, Unit: "%"}, {Key: "rtt_avg", Label: "Average round trip", Value: avg, Unit: "ms"}},
		Tables: []netsec.ProbeTable{{ID: "replies", Title: "Echo replies", Columns: []string{"Sequence", "TTL", "Round trip (ms)"}, Rows: [][]string{{"1", "57", "10.1"}}}},
		Facts:  []netsec.ProbeFact{{Label: "Destination address", Value: "192.0.2.9", Basis: netsec.BasisObserved}}}
}

func TestStructuredVerdictsDecideStatusAndOutcome(t *testing.T) {
	results := []*netsec.ProbeResult{
		pingResult(100, 0, netsec.ProbeUnknown),
		pingResult(25, 11, netsec.ProbeFindings),
		pingResult(0, 10, netsec.ProbeOK),
		{Tool: "ping", Target: "192.0.2.9", Verdict: netsec.ProbeFailed, Error: "connection refused"},
	}
	var next atomic.Int32
	s, _ := diagnosticService(t, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		return results[next.Add(1)-1], nil
	})
	for _, want := range []struct{ status, outcome string }{{"completed", "completed_with_unknowns"}, {"completed", "completed_with_findings"}, {"completed", "completed"}, {"failed", "refused"}} {
		run, err := s.Create(t.Context(), "ping", netsec.ProbeRequest{Tool: "ping", Target: "192.0.2.9"}, "operator")
		if err != nil {
			t.Fatal(err)
		}
		got := awaitRun(t, s, run.ID)
		if got.Status != want.status || got.Outcome != want.outcome || got.OutcomeSource == "" {
			t.Fatalf("%+v, want %+v", got, want)
		}
	}
}

func TestHistoryAndComparisonUseStructuredEvidence(t *testing.T) {
	results := []*netsec.ProbeResult{pingResult(0, 10, netsec.ProbeOK), pingResult(25, 14, netsec.ProbeFindings), pingResult(0, 9, netsec.ProbeOK)}
	results[1].Tables[0].Rows[0][2] = "99.9"
	results[1].Facts = append(results[1].Facts, netsec.ProbeFact{Label: "New fact", Value: "x"})
	var next atomic.Int32
	s, _ := diagnosticService(t, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		return results[next.Add(1)-1], nil
	})
	var ids []string
	for range results[:2] {
		run, err := s.Create(t.Context(), "ping watch", netsec.ProbeRequest{Tool: "ping", Target: "192.0.2.9"}, "operator")
		if err != nil {
			t.Fatal(err)
		}
		awaitRun(t, s, run.ID)
		ids = append(ids, run.ID)
		time.Sleep(2 * time.Millisecond)
	}
	other, _ := s.Create(t.Context(), "other target", netsec.ProbeRequest{Tool: "ping", Target: "192.0.2.10"}, "operator")
	awaitRun(t, s, other.ID)

	h, err := s.History(t.Context(), ids[1])
	if err != nil || len(h.Points) != 2 || h.Points[0].ID != ids[0] || h.Points[1].Verdict != netsec.ProbeFindings || h.Points[1].Metrics[0].Value != 25 {
		t.Fatalf("history = %+v %v", h, err)
	}
	c, err := s.Compare(t.Context(), ids[0], ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Metrics) != 2 || *c.Metrics[0].Before != 0 || *c.Metrics[0].After != 25 || c.Metrics[1].Key != "rtt_avg" {
		t.Fatalf("metric changes = %+v", c.Metrics)
	}
	added := strings.Join(c.Records.Added, "\n")
	if !strings.Contains(added, "verdict: findings") || !strings.Contains(added, "fact New fact: x") || strings.Contains(added, "99.9") {
		t.Fatalf("structured differences = %v (timing columns must not count)", c.Records.Added)
	}
	if _, err := s.History(t.Context(), "missing"); err == nil {
		t.Fatal("history of a missing run")
	}
}

func TestAdoptSavesTheServersOwnResultWithoutRunning(t *testing.T) {
	var calls atomic.Int32
	s, _ := diagnosticService(t, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		calls.Add(1)
		return nil, nil
	})
	result := pingResult(100, 0, netsec.ProbeUnknown)
	result.ResultID = "should-not-be-saved"
	started := time.Now().Add(-4 * time.Second)
	run, err := s.Adopt(t.Context(), "site audit report", netsec.ProbeRequest{Tool: "ping", Target: "192.0.2.9"}, result, "operator", started, started.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Get(t.Context(), run.ID)
	if err != nil || calls.Load() != 0 || saved.Status != "completed" || saved.Outcome != "completed_with_unknowns" || saved.Result.ResultID != "" || !saved.HasResult || saved.JobID != "" {
		t.Fatalf("saved = %+v %v calls=%d", saved, err, calls.Load())
	}
	if !strings.Contains(strings.Join(saved.Scope.Limitations, " "), "sent no new traffic") || saved.Stages[1].Outcome != "completed_with_unknowns" {
		t.Fatalf("scope = %+v", saved)
	}
	if _, err := s.Adopt(t.Context(), "", netsec.ProbeRequest{Tool: "ping", Target: "192.0.2.9"}, result, "operator", started, started); err == nil {
		t.Fatal("adopted without a name")
	}
}

func TestStructuredEvidenceIsBoundedAndCopied(t *testing.T) {
	huge := &netsec.ProbeResult{Tool: "listeners", Target: "this host", OK: true, ResultID: "abc", Summary: strings.Repeat("s", 4000)}
	for i := 0; i < 400; i++ {
		huge.Facts = append(huge.Facts, netsec.ProbeFact{Label: "fact", Value: strings.Repeat("v", 2000)})
		huge.Findings = append(huge.Findings, netsec.ProbeFinding{ID: "f", Title: strings.Repeat("t", 2000)})
		huge.Links = append(huge.Links, netsec.ProbeLink{Label: "l", Href: "/x"})
	}
	table := netsec.ProbeTable{ID: "t", Title: "rows", Columns: make([]string, 20)}
	for i := 0; i < 2000; i++ {
		table.Rows = append(table.Rows, make([]string, 20))
		table.RowLinks = append(table.RowLinks, "/proxy/ports")
		for j := range table.Rows[i] {
			table.Rows[i][j] = strings.Repeat("c", 300)
		}
	}
	for i := 0; i < 20; i++ {
		huge.Tables = append(huge.Tables, table)
	}
	bounded, truncated := boundedResult(huge)
	encoded, _ := json.Marshal(bounded)
	if !truncated || len(encoded) > MaxArtifactBytes || bounded.ResultID != "" || len(bounded.Facts) > maxFacts || len(bounded.Tables) > maxTables || len(bounded.Links) > maxLinks {
		t.Fatalf("bound failed: %d bytes, truncated=%v facts=%d tables=%d", len(encoded), truncated, len(bounded.Facts), len(bounded.Tables))
	}
	for _, tbl := range bounded.Tables {
		if len(tbl.Columns) > maxTableCols || len(tbl.RowLinks) > len(tbl.Rows) {
			t.Fatalf("table not bounded: cols=%d rows=%d links=%d", len(tbl.Columns), len(tbl.Rows), len(tbl.RowLinks))
		}
	}
	if len(huge.Tables[0].Rows) != 2000 {
		t.Fatal("bounding mutated the source result")
	}
}

// Content that JSON escaping inflates sixfold must still shrink below the
// artifact bound rather than spin under the service lock.
func TestBoundingAlwaysTerminatesOnEscapeHeavyEvidence(t *testing.T) {
	heavy := &netsec.ProbeResult{Tool: "dns", Target: strings.Repeat("<", 4000), Summary: strings.Repeat("&", 4000), Error: strings.Repeat(">", 4000)}
	for i := 0; i < 8; i++ {
		row := make([]string, 12)
		for j := range row {
			row[j] = strings.Repeat("<", 1024)
		}
		heavy.Tables = append(heavy.Tables, netsec.ProbeTable{ID: "t", Title: "x", Columns: make([]string, 12), Rows: [][]string{row}})
	}
	for i := 0; i < 32; i++ {
		heavy.Metrics = append(heavy.Metrics, netsec.ProbeMetric{Key: strings.Repeat("<", 1024), Label: strings.Repeat(">", 1024)})
		heavy.Links = append(heavy.Links, netsec.ProbeLink{Label: strings.Repeat("&", 1024), Href: strings.Repeat("<", 1024)})
	}
	done := make(chan *netsec.ProbeResult, 1)
	go func() { bounded, _ := boundedResult(heavy); done <- bounded }()
	select {
	case bounded := <-done:
		encoded, _ := json.Marshal(bounded)
		if len(encoded) > MaxArtifactBytes {
			t.Fatalf("bounded result is %d bytes", len(encoded))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bounding did not terminate")
	}
}

func TestAnsweredFollowsTheVerdict(t *testing.T) {
	for _, tc := range []struct {
		result *netsec.ProbeResult
		want   bool
	}{
		{nil, false},
		{&netsec.ProbeResult{OK: false, Verdict: netsec.ProbeFindings}, true},
		{&netsec.ProbeResult{OK: false, Verdict: netsec.ProbeUnknown}, true},
		{&netsec.ProbeResult{OK: true, Verdict: netsec.ProbeFailed}, false},
		{&netsec.ProbeResult{OK: true}, true},
		{&netsec.ProbeResult{OK: false}, false},
	} {
		if got := answered(tc.result); got != tc.want {
			t.Errorf("%+v => %v", tc.result, got)
		}
	}
}
