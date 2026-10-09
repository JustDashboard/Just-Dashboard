package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// CrowdSec is the other half of intrusion prevention. fail2ban reads one
// host's logs and bans on what it sees there; CrowdSec shares what its
// community has seen elsewhere, and its bouncers (a firewall, nginx, Traefik)
// are what turn a decision into a dropped connection. The decisions are the
// part worth a page: who is banned, why, for how long, and who enforces it.
//
// Everything here goes through cscli's JSON output, because the local API it
// talks to needs credentials the dashboard has no business holding: cscli
// already has them, on the host.

// ErrCrowdSecMissing is a ban or a release asked of a host without cscli. The
// routes answer it as an absent tool rather than as a bad request.
var ErrCrowdSecMissing = errors.New("cscli is not installed on this host")

// hasTool reports whether a program exists on the host. A variable so tests do
// not need the program.
var hasTool = hostexec.AvailableOnHost

// crowdsecNow dates a decision whose end cscli reports only as a duration.
var crowdsecNow = time.Now

// CrowdSecDecision is one ban (or captcha) in force, flattened out of the
// alert that caused it.
type CrowdSecDecision struct {
	ID       int64  `json:"id"`
	Origin   string `json:"origin"`
	Scenario string `json:"scenario"`
	// Scope is Ip, Range, Country or As; Value is what it names.
	Scope string `json:"scope"`
	Value string `json:"value"`
	// Type is ban or captcha.
	Type string `json:"type"`
	// Duration is what remains, as cscli prints it (3h59m50s). Until is when
	// it ends, from cscli where it says and worked out from the duration
	// where it does not.
	Duration string `json:"duration"`
	Until    string `json:"until,omitempty"`
	AlertID  int64  `json:"alertId"`
	Country  string `json:"country,omitempty"`
	AS       string `json:"as,omitempty"`
}

// CrowdSecSource is who an alert was about.
type CrowdSecSource struct {
	IP      string `json:"ip,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Value   string `json:"value,omitempty"`
	Country string `json:"country,omitempty"`
	ASName  string `json:"asName,omitempty"`
}

// CrowdSecAlert is something the engine decided was an attack.
type CrowdSecAlert struct {
	ID          int64          `json:"id"`
	Scenario    string         `json:"scenario"`
	Source      CrowdSecSource `json:"source"`
	EventsCount int            `json:"eventsCount"`
	CreatedAt   string         `json:"createdAt"`
	// Decisions is how many decisions the alert led to.
	Decisions int `json:"decisions"`
}

// CrowdSecBouncer is a component enforcing the decisions.
type CrowdSecBouncer struct {
	Name      string `json:"name"`
	IPAddress string `json:"ipAddress,omitempty"`
	Valid     bool   `json:"valid"`
	LastPull  string `json:"lastPull,omitempty"`
	Type      string `json:"type,omitempty"`
	Version   string `json:"version,omitempty"`
}

// CrowdSecView is what the page draws.
type CrowdSecView struct {
	Installed bool `json:"installed"`
	// Active is whether the crowdsec service runs.
	Active    bool               `json:"active"`
	Decisions []CrowdSecDecision `json:"decisions"`
	Alerts    []CrowdSecAlert    `json:"alerts"`
	Bouncers  []CrowdSecBouncer  `json:"bouncers"`
	// Enforcement is whether any bouncer is actually turning the decisions
	// into dropped traffic, judged from its pulls and the kernel's sets
	// rather than from the engine running.
	Enforcement *CrowdSecEnforcement `json:"enforcement,omitempty"`
	// Error is what cscli said when it could not answer; the lists it could
	// not fill are empty.
	Error string `json:"error,omitempty"`
}

// maxDecisions bounds the flattened list. A host under a scan accumulates
// thousands of community decisions, and the page is for the ones to act on.
const maxDecisions = 500

// CrowdSec reads the engine's decisions, recent alerts and bouncers.
func (s *Service) CrowdSec(ctx context.Context) (*CrowdSecView, error) {
	v := &CrowdSecView{Decisions: []CrowdSecDecision{}, Alerts: []CrowdSecAlert{}, Bouncers: []CrowdSecBouncer{}}
	if !hasTool("cscli") {
		return v, nil
	}
	v.Installed = true
	out, _ := run(ctx, "systemctl", "is-active", "crowdsec")
	v.Active = strings.TrimSpace(out) == "active"

	var problems []string
	note := func(what string, err error) { problems = append(problems, what+": "+err.Error()) }
	if out, err := run(ctx, "cscli", "decisions", "list", "-o", "json", "--limit", "500"); err != nil {
		note("decisions", err)
	} else if v.Decisions, err = parseCrowdSecDecisions(out, crowdsecNow()); err != nil {
		note("decisions", err)
		v.Decisions = []CrowdSecDecision{}
	}
	if out, err := run(ctx, "cscli", "alerts", "list", "-o", "json", "--limit", "50"); err != nil {
		note("alerts", err)
	} else if v.Alerts, err = parseCrowdSecAlerts(out); err != nil {
		note("alerts", err)
		v.Alerts = []CrowdSecAlert{}
	}
	bouncersRead := true
	if out, err := run(ctx, "cscli", "bouncers", "list", "-o", "json"); err != nil {
		note("bouncers", err)
		bouncersRead = false
	} else if v.Bouncers, err = parseCrowdSecBouncers(out); err != nil {
		note("bouncers", err)
		v.Bouncers = []CrowdSecBouncer{}
		bouncersRead = false
	}
	now := crowdsecNow()
	if bouncersRead {
		e := s.crowdsecEnforcement(ctx, v, now)
		v.Enforcement = &e
	} else {
		// An unreadable bouncer list is not an empty one: saying nothing
		// enforces the decisions would be as unfounded as saying something does.
		v.Enforcement = &CrowdSecEnforcement{
			State: EnforcementUnverified, Checked: now.UTC(), Fresh: bouncerFreshness.String(),
			Summary:  "The bouncer list could not be read, so whether anything enforces the decisions is unknown.",
			Bouncers: []BouncerEvidence{}, Enforced: []string{},
		}
	}
	v.Error = strings.Join(problems, "; ")
	return v, nil
}

// jsonFrom drops what cscli printed before its JSON. The command's standard
// error is read with its output, and a warning about a deprecated option or
// an unreachable hub is a line of text in front of the array.
func jsonFrom(out string) string {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") || t == "null" {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}

// cscliBody is the JSON in cscli's output, empty where the answer is none: the
// word null, or nothing at all. Text with no JSON in it is not an answer.
func cscliBody(out string) (string, error) {
	body := jsonFrom(out)
	if body == "" {
		if strings.TrimSpace(out) != "" {
			return "", fmt.Errorf("unreadable output: %s", firstLine(out))
		}
		return "", nil
	}
	if body == "null" {
		return "", nil
	}
	return body, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

type cscliAlert struct {
	ID          int64  `json:"id"`
	Scenario    string `json:"scenario"`
	EventsCount int    `json:"events_count"`
	CreatedAt   string `json:"created_at"`
	Source      struct {
		IP     string `json:"ip"`
		Scope  string `json:"scope"`
		Value  string `json:"value"`
		CN     string `json:"cn"`
		ASName string `json:"as_name"`
	} `json:"source"`
	Decisions []struct {
		ID       int64  `json:"id"`
		Origin   string `json:"origin"`
		Scenario string `json:"scenario"`
		Scope    string `json:"scope"`
		Value    string `json:"value"`
		Type     string `json:"type"`
		Duration string `json:"duration"`
		Until    string `json:"until"`
	} `json:"decisions"`
}

// parseCrowdSecDecisions flattens `cscli decisions list -o json`: an array of
// alerts, each holding the decisions it caused, or null when there are none.
func parseCrowdSecDecisions(out string, now time.Time) ([]CrowdSecDecision, error) {
	decisions := []CrowdSecDecision{}
	body, err := cscliBody(out)
	if err != nil || body == "" {
		return decisions, err
	}
	var alerts []cscliAlert
	if err := json.Unmarshal([]byte(body), &alerts); err != nil {
		return nil, fmt.Errorf("unreadable output: %w", err)
	}
	for _, a := range alerts {
		for _, d := range a.Decisions {
			dec := CrowdSecDecision{
				ID: d.ID, Origin: d.Origin, Scenario: firstNonEmpty(d.Scenario, a.Scenario),
				Scope: d.Scope, Value: d.Value, Type: d.Type, Duration: d.Duration, Until: d.Until,
				AlertID: a.ID, Country: a.Source.CN, AS: a.Source.ASName,
			}
			if dec.Until == "" {
				if left, err := time.ParseDuration(d.Duration); err == nil {
					dec.Until = now.Add(left).UTC().Format(time.RFC3339)
				}
			}
			decisions = append(decisions, dec)
		}
	}
	// Newest decision first: ids only grow.
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ID > decisions[j].ID })
	if len(decisions) > maxDecisions {
		decisions = decisions[:maxDecisions]
	}
	return decisions, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseCrowdSecAlerts(out string) ([]CrowdSecAlert, error) {
	alerts := []CrowdSecAlert{}
	body, err := cscliBody(out)
	if err != nil || body == "" {
		return alerts, err
	}
	var raw []cscliAlert
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("unreadable output: %w", err)
	}
	for _, a := range raw {
		alerts = append(alerts, CrowdSecAlert{
			ID: a.ID, Scenario: a.Scenario, EventsCount: a.EventsCount, CreatedAt: a.CreatedAt,
			Decisions: len(a.Decisions),
			Source: CrowdSecSource{
				// A range alert has no single address; its value is the range.
				IP: a.Source.IP, Scope: a.Source.Scope, Value: a.Source.Value,
				Country: a.Source.CN, ASName: a.Source.ASName,
			},
		})
	}
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].ID > alerts[j].ID })
	return alerts, nil
}

func parseCrowdSecBouncers(out string) ([]CrowdSecBouncer, error) {
	bouncers := []CrowdSecBouncer{}
	body, err := cscliBody(out)
	if err != nil || body == "" {
		return bouncers, err
	}
	var raw []struct {
		Name      string  `json:"name"`
		IPAddress string  `json:"ip_address"`
		Valid     bool    `json:"valid"`
		LastPull  *string `json:"last_pull"`
		Type      string  `json:"type"`
		Version   string  `json:"version"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("unreadable output: %w", err)
	}
	for _, b := range raw {
		out := CrowdSecBouncer{Name: b.Name, IPAddress: b.IPAddress, Valid: b.Valid, Type: b.Type, Version: b.Version}
		if b.LastPull != nil {
			out.LastPull = *b.LastPull
		}
		bouncers = append(bouncers, out)
	}
	sort.Slice(bouncers, func(i, j int) bool { return bouncers[i].Name < bouncers[j].Name })
	return bouncers, nil
}

const (
	minDecisionDuration = time.Minute
	maxDecisionDuration = 8760 * time.Hour
	maxReasonLength     = 128
)

// AddDecision bans an address or a range for a while.
//
// callerIP is why this refuses what it does: a decision is a drop at the
// bouncer, and one covering the address the dashboard is being used from ends
// this session the way an inbound deny rule would. The same guard the
// firewall and fail2ban's ban carry, for the same reason.
func (s *Service) AddDecision(ctx context.Context, value, duration, reason, callerIP string) (string, error) {
	value = strings.TrimSpace(value)
	flag, target, covered, err := parseDecisionTarget(value)
	if err != nil {
		return "", err
	}
	if caller, err := netip.ParseAddr(strings.TrimSpace(callerIP)); err == nil && covered(caller.Unmap()) {
		return "", fmt.Errorf("%w: %s covers %s, the address you are connected from, and a ban drops its traffic",
			ErrLockout, value, caller.Unmap())
	}
	duration = strings.TrimSpace(duration)
	d, err := time.ParseDuration(duration)
	if err != nil || d < minDecisionDuration || d > maxDecisionDuration {
		return "", fmt.Errorf("a duration is a length of time such as 4h, 24h or 168h, from 1m to 8760h")
	}
	reason, err = cleanDecisionReason(reason)
	if err != nil {
		return "", err
	}
	if !hasTool("cscli") {
		return "", ErrCrowdSecMissing
	}
	return run(ctx, "cscli", "decisions", "add", flag, target, "--duration", duration, "--reason", reason, "--type", "ban")
}

// parseDecisionTarget reads an address or a range and says which cscli flag
// takes it and whether an address falls inside it.
func parseDecisionTarget(value string) (flag, target string, covers func(netip.Addr) bool, err error) {
	if strings.Contains(value, "/") {
		p, err := netip.ParsePrefix(value)
		if err != nil || p.Addr().Zone() != "" {
			return "", "", nil, fmt.Errorf("%q is not an address or a network in CIDR form", value)
		}
		if p.Addr().Is4In6() {
			return "", "", nil, fmt.Errorf("%q is an IPv4 network written as IPv6; write it as plain IPv4", value)
		}
		// A network is named by its first address, which is what cscli
		// stores and what the decision list will show.
		p = p.Masked()
		if p.Bits() == 0 {
			return "", "", nil, fmt.Errorf("%s is every address; a ban on it would refuse everybody", value)
		}
		if p.Contains(netip.MustParseAddr("127.0.0.1")) || p.Contains(netip.IPv6Loopback()) {
			return "", "", nil, fmt.Errorf("%s covers this machine's own loopback address", value)
		}
		return "--range", p.String(), p.Contains, nil
	}
	a, err := netip.ParseAddr(value)
	if err != nil || a.Zone() != "" {
		return "", "", nil, fmt.Errorf("%q is not an IP address or a network in CIDR form", value)
	}
	a = a.Unmap()
	if a.IsLoopback() || a.IsUnspecified() {
		return "", "", nil, fmt.Errorf("%s is this machine itself", value)
	}
	return "--ip", a.String(), func(c netip.Addr) bool { return c == a }, nil
}

// cleanDecisionReason keeps the reason to one line of printable text. It is
// stored by CrowdSec and shown on every page that lists decisions.
func cleanDecisionReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "manual ban from Just Dashboard", nil
	}
	if len([]rune(reason)) > maxReasonLength {
		return "", fmt.Errorf("a reason is at most %d characters", maxReasonLength)
	}
	for _, r := range reason {
		if !unicode.IsPrint(r) {
			return "", fmt.Errorf("a reason is one line of printable text")
		}
	}
	return reason, nil
}

// DeleteDecision lifts one decision. The id is what cscli printed, so a
// decision that has since expired or been removed is cscli's to report.
func (s *Service) DeleteDecision(ctx context.Context, id int) (string, error) {
	if id < 1 {
		return "", fmt.Errorf("a decision id is a positive number")
	}
	if !hasTool("cscli") {
		return "", ErrCrowdSecMissing
	}
	return run(ctx, "cscli", "decisions", "delete", "--id", strconv.Itoa(id))
}
