package netsec

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The structured half of a probe result. Tool text stays verbatim in Output;
// these are the readings the page can act on, each saying what it rests on so
// an inference is never presented as a measurement.

// Verdicts. An empty Verdict keeps OK's reading.
const (
	ProbeOK       = "ok"
	ProbeFindings = "findings"
	ProbeUnknown  = "unknown"
	ProbeFailed   = "failed"
)

// Fact bases.
const (
	BasisObserved     = "observed"
	BasisConfigured   = "configured"
	BasisSelfReported = "self_reported"
	BasisInferred     = "inferred"
	BasisRegistry     = "registry"
	BasisUnknown      = "unknown"
)

// Stage statuses.
const (
	StagePassed  = "passed"
	StageWarning = "warning"
	StageFailed  = "failed"
	StageSkipped = "skipped"
	StageUnknown = "unknown"
)

// ProbeFact is one labelled reading and where it came from.
type ProbeFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Basis string `json:"basis,omitempty"`
}

// ProbeStage is one step of a multi-step check. A later stage after a failed
// one is skipped, never reported as passed.
type ProbeStage struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// ProbeTable is a structured listing: hops, chain certificates, ports.
// RowLinks, when present, holds one in-product destination per row.
type ProbeTable struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Columns  []string   `json:"columns"`
	Rows     [][]string `json:"rows"`
	RowLinks []string   `json:"rowLinks,omitempty"`
	Note     string     `json:"note,omitempty"`
}

// ProbeFinding is something to act on, with whoever can act on it.
type ProbeFinding struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Owner  string `json:"owner,omitempty"`
	Action string `json:"action,omitempty"`
	Href   string `json:"href,omitempty"`
}

// ProbeLink hands the reader to the page that owns the next question.
type ProbeLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// ProbeMetric is a number a later run of the same request can be compared
// with: loss, jitter, hop count, days to expiry.
type ProbeMetric struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

func (r *ProbeResult) fact(label, value, basis string) {
	r.Facts = append(r.Facts, ProbeFact{Label: label, Value: value, Basis: basis})
}

func (r *ProbeResult) metric(key, label string, value float64, unit string) {
	r.Metrics = append(r.Metrics, ProbeMetric{Key: key, Label: label, Value: value, Unit: unit})
}

func (r *ProbeResult) finding(id, level, title, detail, owner string) {
	r.Findings = append(r.Findings, ProbeFinding{ID: id, Level: level, Title: title, Detail: detail, Owner: owner})
}

func (r *ProbeResult) link(label, href string) {
	r.Links = append(r.Links, ProbeLink{Label: label, Href: href})
}

// stageClock records a stage's duration from the moment it starts.
type stageClock struct {
	result *ProbeResult
	start  time.Time
}

func (r *ProbeResult) begin() stageClock { return stageClock{result: r, start: time.Now()} }

func (c stageClock) done(id, label, status, detail string) {
	c.result.Stages = append(c.result.Stages, ProbeStage{ID: id, Label: label, Status: status, Detail: detail, Duration: sinceMs(c.start)})
}

// skipRemaining marks the stages a failure stopped as skipped, so the result
// names every step and which one ended the check.
func (r *ProbeResult) skipRemaining(stages [][2]string, reason string) {
	for _, stage := range stages {
		r.Stages = append(r.Stages, ProbeStage{ID: stage[0], Label: stage[1], Status: StageSkipped, Detail: reason})
	}
}

// toolsLink builds the in-product address that opens a tool with a target.
func toolsLink(tool, target string) string {
	return "/network/tools?tool=" + tool + "&target=" + url.QueryEscape(target)
}

func parseMillis(text string) (float64, bool) {
	text = strings.TrimSuffix(strings.TrimSpace(text), "ms")
	v, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	return v, err == nil
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
