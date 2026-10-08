// Package netdiag retains bounded, named observations made by the existing
// network probes. It schedules through jobs; it is not a second monitor.
package netdiag

import (
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

var (
	ErrNotFound     = errors.New("diagnostic run not found")
	ErrUnavailable  = errors.New("durable diagnostics are unavailable")
	ErrBusy         = errors.New("four network diagnostics are already running")
	ErrNotRunning   = errors.New("this diagnostic is not running")
	ErrRunning      = errors.New("stop the diagnostic before deleting its record")
	ErrIncompatible = errors.New("compare completed runs with the same tool and normalised request")
	ErrInvalid      = errors.New("invalid diagnostic request")
)

const (
	JobPrefix        = "network.diagnostic."
	MaxRunning       = 4
	MaxOutputBytes   = 64 * 1024
	MaxRecordBytes   = 1024
	MaxRecords       = 128
	MaxExportBytes   = 512 * 1024
	MaxArtifactBytes = 128 * 1024
	RunTimeout       = 90 * time.Second
)

type Scope struct {
	Vantage       string   `json:"vantage"`
	Source        string   `json:"source,omitempty"`
	SourceAddress string   `json:"sourceAddress,omitempty"`
	Address       string   `json:"address,omitempty"`
	Mark          string   `json:"mark,omitempty"`
	Target        string   `json:"target,omitempty"`
	Family        string   `json:"family"`
	Protocol      string   `json:"protocol,omitempty"`
	Port          int      `json:"port,omitempty"`
	Interface     string   `json:"interface,omitempty"`
	Limitations   []string `json:"limitations"`
}

type Stage struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Outcome   string     `json:"outcome,omitempty"`
}

type Run struct {
	ID                   string              `json:"id"`
	Kind                 string              `json:"kind,omitempty"`
	InvestigationRequest *netpath.Request    `json:"investigationRequest,omitempty"`
	Investigation        *netpath.Result     `json:"investigation,omitempty"`
	Name                 string              `json:"name"`
	Request              netsec.ProbeRequest `json:"request"`
	Scope                Scope               `json:"scope"`
	Status               string              `json:"status"`
	Outcome              string              `json:"outcome,omitempty"`
	OutcomeSource        string              `json:"outcomeSource,omitempty"`
	CreatedAt            time.Time           `json:"createdAt"`
	StartedAt            *time.Time          `json:"startedAt,omitempty"`
	EndedAt              *time.Time          `json:"endedAt,omitempty"`
	UpdatedAt            time.Time           `json:"updatedAt"`
	CreatedBy            string              `json:"createdBy"`
	RerunOf              string              `json:"rerunOf,omitempty"`
	JobID                string              `json:"jobId,omitempty"`
	Stages               []Stage             `json:"stages"`
	Result               *netsec.ProbeResult `json:"result,omitempty"`
	HasResult            bool                `json:"hasResult"`
	ResultTruncated      bool                `json:"resultTruncated"`
	Error                string              `json:"error,omitempty"`
}

type Retention struct {
	MaxRuns     int `json:"maxRuns"`
	MaxAgeHours int `json:"maxAgeHours"`
}

var DefaultRetention = Retention{MaxRuns: 100, MaxAgeHours: 7 * 24}

func (p Retention) Validate() error {
	if p.MaxRuns < 1 || p.MaxRuns > 256 || p.MaxAgeHours < 1 || p.MaxAgeHours > 90*24 {
		return errors.New("retain 1 to 256 finished runs for 1 to 2160 hours")
	}
	return nil
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("use a name of 1 to 100 UTF-8 bytes without control characters")
	}
	return name, nil
}

func scopeFor(req netsec.ProbeRequest) Scope {
	s := Scope{Vantage: "dashboard_host", Target: req.Target, Family: "resolved_at_execution", Port: req.Port,
		Limitations: []string{"This is an observation from this host, not proof of inbound or provider-policy reachability.", "The existing tool determines its resolver, route and source; this record does not model every policy layer."}}
	if a, err := netip.ParseAddr(req.Target); err == nil {
		s.Family = "inet"
		if a.Unmap().Is6() {
			s.Family = "inet6"
		}
	}
	switch req.Tool {
	case "listeners", "egress", "neigh", "capabilities":
		s.Family = "not_applicable"
	case "capture":
		s.Family, s.Interface, s.Protocol = "not_applicable", req.Target, req.Option
		s.Limitations = append(s.Limitations, "Packet summaries may include sensitive decoded fields; no PCAP or payload dump is retained.")
	case "wol":
		s.Family, s.Interface, s.Protocol = "not_applicable", req.Option, "ethernet"
	case "ping", "traceroute", "mtu":
		s.Protocol = "tool_selected"
	case "port", "scan", "banner", "ssh":
		s.Protocol = "tcp"
	case "http", "tls", "starttls", "tlssurvey", "httpsec", "siteaudit":
		s.Protocol = "tcp"
	case "dns", "dnsauth", "dnsbl", "mx":
		s.Protocol = "resolver_selected"
	}
	return s
}

func terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "interrupted"
}

func clip(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	text = text[:limit]
	for !utf8.ValidString(text) && len(text) > 0 {
		text = text[:len(text)-1]
	}
	return text, true
}

func boundedResult(result *netsec.ProbeResult) (*netsec.ProbeResult, bool) {
	if result == nil {
		return nil, false
	}
	copy := *result
	var trimmed, next bool
	copy.Output, trimmed = clip(copy.Output, MaxOutputBytes)
	copy.Error, next = clip(copy.Error, 2048)
	trimmed = trimmed || next
	copy.Tool, _ = clip(copy.Tool, 64)
	copy.Target, next = clip(copy.Target, 1024)
	trimmed = trimmed || next
	copy.Duration, _ = clip(copy.Duration, 64)
	copy.Records = make([]string, 0, min(len(result.Records), MaxRecords))
	for i, record := range result.Records {
		if i == MaxRecords {
			trimmed = true
			break
		}
		record, next = clip(record, MaxRecordBytes)
		trimmed = trimmed || next
		copy.Records = append(copy.Records, record)
	}
	for {
		encoded, _ := json.Marshal(copy)
		if len(encoded) <= MaxArtifactBytes {
			break
		}
		trimmed = true
		if len(copy.Output) > 0 {
			copy.Output, _ = clip(copy.Output, len(copy.Output)/2)
		} else {
			copy.Records = copy.Records[:len(copy.Records)/2]
		}
	}
	return &copy, trimmed
}
