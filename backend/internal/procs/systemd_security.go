package procs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// SecurityCheck is one of the sandboxing settings `systemd-analyze security`
// weighs: Set is whether the unit has it (null where it does not apply, such
// as SupplementaryGroups= for a service running as root), and Exposure what
// leaving it unset adds to the score (null when it adds nothing).
type SecurityCheck struct {
	Name        string   `json:"name"`
	Field       string   `json:"field"`
	Description string   `json:"description"`
	Set         *bool    `json:"set"`
	Exposure    *float64 `json:"exposure"`
}

// SecurityReport is systemd's own exposure assessment of a service: a score
// from 0 (locked down) to 10 (nothing restricted), its verdict, and the
// checks behind it, most exposing first. A unit that cannot be assessed —
// not a service, no systemd-analyze, a systemd too old to answer in JSON —
// is Available false with the reason, which is an answer and not a failure.
type SecurityReport struct {
	Available bool            `json:"available"`
	Score     *float64        `json:"score,omitempty"`
	Verdict   string          `json:"verdict,omitempty"`
	Checks    []SecurityCheck `json:"checks"`
	Reason    string          `json:"reason,omitempty"`
}

const securityTimeout = 15 * time.Second

// analyze runs systemd-analyze on the host: the assessment reads the unit as
// the host's manager has it loaded, and the image need not carry the tool.
var analyze = func(ctx context.Context, args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, securityTimeout)
	defer cancel()
	cmd := hostexec.CommandOnHost(ctx, "systemd-analyze", args...)
	// The same chroot detection run() works around, and no colour codes in
	// the text the overall line is read from.
	cmd.Env = append(os.Environ(), "SYSTEMD_IGNORE_CHROOT=1", "SYSTEMD_COLORS=0")
	return runPrepared(ctx, cmd, securityTimeout, "systemd-analyze", args...)
}

// Security assesses one service with `systemd-analyze security`: the checks
// from its JSON form, and the score and verdict from the last line of its
// text form, which the JSON does not carry.
func (s *Systemd) Security(ctx context.Context, name string) (*SecurityReport, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	unavailable := func(reason string) *SecurityReport {
		return &SecurityReport{Checks: []SecurityCheck{}, Reason: reason}
	}
	res, err := analyze(ctx, "security", "--json=short", "--no-pager", "--", name)
	if err != nil {
		return unavailable(securityUnavailable(res, err)), nil
	}
	checks, err := ParseSecurityChecks(res.Stdout)
	if err != nil {
		return unavailable("systemd-analyze's answer could not be read: " + err.Error()), nil
	}
	report := &SecurityReport{Available: true, Checks: checks}
	if text, err := analyze(ctx, "security", "--no-pager", "--", name); err == nil {
		if score, verdict, ok := ParseSecurityOverall(text.Stdout); ok {
			report.Score, report.Verdict = &score, verdict
		}
	}
	return report, nil
}

// securityUnavailable says in a sentence why a unit has no assessment.
func securityUnavailable(res *CommandResult, err error) string {
	stderr := ""
	if res != nil {
		stderr = strings.TrimSpace(res.Stderr)
	}
	switch {
	case errors.Is(err, ErrNotInstalled),
		// nsenter's own exit status when the host has no such program.
		res != nil && res.ExitCode == 127:
		return "systemd-analyze is not installed on this host."
	case strings.Contains(stderr, "not a service unit"):
		return "systemd assesses services only, and this unit is not one."
	case strings.Contains(stderr, "not found"):
		return "systemd has no unit by this name."
	case strings.Contains(stderr, "unrecognized option") && strings.Contains(stderr, "json"):
		return "This host's systemd is older than 250 and cannot report its assessment in a form the dashboard reads."
	case strings.Contains(err.Error(), "timed out"):
		return fmt.Sprintf("systemd-analyze did not answer within %s.", securityTimeout)
	}
	if line, _, _ := strings.Cut(stderr, "\n"); line != "" {
		if len(line) > 200 {
			line = line[:200] + "…"
		}
		return "systemd-analyze refused: " + line
	}
	if res != nil {
		return fmt.Sprintf("systemd-analyze exited %d.", res.ExitCode)
	}
	return err.Error()
}

// ParseSecurityChecks reads `systemd-analyze security --json=short`. Each
// item's exposure is a decimal string, or null where the check adds nothing;
// a number is accepted too. The checks come back most exposing first, those
// with no exposure last, and otherwise in systemd's order.
func ParseSecurityChecks(stdout string) ([]SecurityCheck, error) {
	var raw []struct {
		Set         *bool           `json:"set"`
		Name        string          `json:"name"`
		Field       string          `json:"json_field"`
		Description string          `json:"description"`
		Exposure    json.RawMessage `json:"exposure"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &raw); err != nil {
		return nil, err
	}
	checks := make([]SecurityCheck, 0, len(raw))
	for _, r := range raw {
		check := SecurityCheck{Name: r.Name, Field: r.Field, Description: r.Description, Set: r.Set}
		check.Exposure = parseExposure(r.Exposure)
		checks = append(checks, check)
	}
	sort.SliceStable(checks, func(i, j int) bool {
		a, b := checks[i].Exposure, checks[j].Exposure
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return *a > *b
	})
	return checks, nil
}

func parseExposure(raw json.RawMessage) *float64 {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		text = strings.TrimSpace(s)
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil
	}
	return &n
}

// overallLine is the text form's last line, `→ Overall exposure level for
// cron.service: 9.6 UNSAFE :-{` — with "->" for the arrow outside a UTF-8
// locale. A unit name may hold a colon, so the score is the last ": " that a
// number follows.
var overallLine = regexp.MustCompile(`Overall exposure level for .+: ([0-9]+(?:\.[0-9]+)?) ([A-Z]+)`)

// ParseSecurityOverall reads the score and verdict from the text form of
// `systemd-analyze security`.
func ParseSecurityOverall(stdout string) (float64, string, bool) {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := overallLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		score, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, "", false
		}
		return score, m[2], true
	}
	return 0, "", false
}
