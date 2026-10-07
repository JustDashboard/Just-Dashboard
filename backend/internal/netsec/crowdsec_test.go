package netsec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cscliHost stands a recorded cscli behind run. Replies are looked up by the
// command line's prefix, every command is kept in order, and one that nothing
// answers fails the test.
type cscliHost struct {
	t       *testing.T
	calls   []string
	replies map[string]string
	errs    map[string]error
}

func newCscliHost(t *testing.T, installed bool) *cscliHost {
	t.Helper()
	h := &cscliHost{t: t, replies: map[string]string{}, errs: map[string]error{}}
	prevRun, prevHas := run, hasTool
	run = func(_ context.Context, name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		h.calls = append(h.calls, call)
		for prefix, out := range h.replies {
			if strings.HasPrefix(call, prefix) {
				return out, h.errs[prefix]
			}
		}
		h.t.Errorf("unexpected command: %s", call)
		return "", fmt.Errorf("unexpected command: %s", call)
	}
	hasTool = func(name string) bool { return installed }
	t.Cleanup(func() { run, hasTool = prevRun, prevHas })
	return h
}

func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCrowdSecReadsDecisionsAlertsAndBouncers(t *testing.T) {
	h := newCscliHost(t, true)
	h.replies["systemctl is-active crowdsec"] = "active\n"
	h.replies["cscli decisions list -o json"] = testdata(t, "crowdsec-decisions.json")
	h.replies["cscli alerts list -o json --limit 50"] = testdata(t, "crowdsec-alerts.json")
	h.replies["cscli bouncers list -o json"] = testdata(t, "crowdsec-bouncers.json")
	prev := crowdsecNow
	crowdsecNow = func() time.Time { return time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { crowdsecNow = prev })

	v, err := New().CrowdSec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Installed || !v.Active || v.Error != "" {
		t.Fatalf("view = %+v", v)
	}

	// Three decisions flattened out of two alerts, newest first.
	if len(v.Decisions) != 3 || v.Decisions[0].ID != 4417 || v.Decisions[1].ID != 4391 || v.Decisions[2].ID != 4390 {
		t.Fatalf("decisions = %+v", v.Decisions)
	}
	d := v.Decisions[0]
	if d.Value != "203.0.113.45" || d.Scope != "Ip" || d.Type != "ban" || d.Origin != "crowdsec" || d.Scenario != "crowdsecurity/ssh-bf" ||
		d.Duration != "3h51m4.5s" || d.Until != "2026-10-07T12:05:36Z" || d.AlertID != 912 || d.Country != "NL" ||
		d.AS != "EXAMPLE-NET Example Hosting Ltd" {
		t.Fatalf("decision = %+v", d)
	}
	// A range, with no end given: worked out from what remains.
	r := v.Decisions[2]
	if r.Value != "198.51.100.0/24" || r.Scope != "Range" || r.Until != "2026-10-14T08:59:12Z" || r.AlertID != 905 {
		t.Fatalf("range = %+v", r)
	}
	// A decision with no scenario of its own takes its alert's, and a captcha is not a ban.
	if c := v.Decisions[1]; c.Type != "captcha" || c.Value != "2001:db8::bad" || c.Origin != "CAPI" {
		t.Fatalf("captcha = %+v", c)
	}

	if len(v.Alerts) != 3 || v.Alerts[0].ID != 912 || v.Alerts[1].ID != 905 || v.Alerts[2].ID != 903 {
		t.Fatalf("alerts = %+v", v.Alerts)
	}
	if a := v.Alerts[0]; a.Source.IP != "203.0.113.45" || a.Source.Country != "NL" || a.EventsCount != 6 || a.Decisions != 1 || a.CreatedAt != "2026-10-07T08:14:31Z" {
		t.Fatalf("alert = %+v", a)
	}
	if v.Alerts[2].Decisions != 0 {
		t.Fatalf("an alert with null decisions: %+v", v.Alerts[2])
	}

	if len(v.Bouncers) != 2 || v.Bouncers[0].Name != "caddy-bouncer" || v.Bouncers[0].LastPull != "" ||
		v.Bouncers[1].Name != "cs-firewall-bouncer" || !v.Bouncers[1].Valid || v.Bouncers[1].Version != "v0.0.31" || v.Bouncers[1].LastPull == "" {
		t.Fatalf("bouncers = %+v", v.Bouncers)
	}
}

// With nothing banned cscli prints the word null, which is not an empty array.
func TestCrowdSecWithNothingInForce(t *testing.T) {
	h := newCscliHost(t, true)
	h.replies["systemctl is-active crowdsec"] = "inactive\n"
	h.errs["systemctl is-active crowdsec"] = errors.New("inactive")
	h.replies["cscli decisions list"] = "null\n"
	h.replies["cscli alerts list"] = "null"
	h.replies["cscli bouncers list"] = "null\n"

	v, err := New().CrowdSec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Installed || v.Active || v.Error != "" {
		t.Fatalf("view = %+v", v)
	}
	if v.Decisions == nil || v.Alerts == nil || v.Bouncers == nil || len(v.Decisions)+len(v.Alerts)+len(v.Bouncers) != 0 {
		t.Fatalf("lists must be empty arrays, not null: %+v", v)
	}
}

// cscli's warnings arrive on the stream run reads, ahead of the JSON.
func TestCrowdSecIgnoresWarningsBeforeTheJSON(t *testing.T) {
	h := newCscliHost(t, true)
	h.replies["systemctl is-active crowdsec"] = "active\n"
	h.replies["cscli decisions list"] = "level=warning msg=\"hub index is out of date\"\n[]\n"
	h.replies["cscli alerts list"] = "[]"
	h.replies["cscli bouncers list"] = ""
	v, _ := New().CrowdSec(t.Context())
	if v.Error != "" || len(v.Decisions) != 0 {
		t.Fatalf("view = %+v", v)
	}
}

func TestCrowdSecReportsAFailedCommandAndKeepsTheRest(t *testing.T) {
	h := newCscliHost(t, true)
	h.replies["systemctl is-active crowdsec"] = "active\n"
	h.replies["cscli decisions list"] = "level=fatal msg=\"unable to list decisions: API error: connection refused\"\n"
	h.errs["cscli decisions list"] = errors.New("cscli: unable to list decisions")
	h.replies["cscli alerts list"] = testdata(t, "crowdsec-alerts.json")
	h.replies["cscli bouncers list"] = "not json"
	v, err := New().CrowdSec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Alerts) != 3 || len(v.Decisions) != 0 || len(v.Bouncers) != 0 ||
		!strings.Contains(v.Error, "decisions: cscli: unable to list decisions") || !strings.Contains(v.Error, "bouncers: unreadable output") {
		t.Fatalf("view = %+v", v)
	}
}

func TestCrowdSecNotInstalled(t *testing.T) {
	h := newCscliHost(t, false)
	v, err := New().CrowdSec(t.Context())
	if err != nil || v.Installed || v.Decisions == nil {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("ran %v without the tool", h.calls)
	}
}

func TestAddDecisionRunsCscli(t *testing.T) {
	tests := []struct {
		name, value, duration, reason string
		want                          string
	}{
		{"an address", "203.0.113.9", "4h", "port scan", "cscli decisions add --ip 203.0.113.9 --duration 4h --reason port scan --type ban"},
		{"a range, masked", "198.51.100.77/24", "168h", "noisy network", "cscli decisions add --range 198.51.100.0/24 --duration 168h --reason noisy network --type ban"},
		{"v6", "2001:db8::bad", "24h", "", "cscli decisions add --ip 2001:db8::bad --duration 24h --reason manual ban from Just Dashboard --type ban"},
		{"v6 range", "2001:db8:abcd::/48", "30m", "x", "cscli decisions add --range 2001:db8:abcd::/48 --duration 30m --reason x --type ban"},
		{"the longest", "203.0.113.9", "8760h", "x", "cscli decisions add --ip 203.0.113.9 --duration 8760h --reason x --type ban"},
		{"mapped v4", "::ffff:203.0.113.9", "1m", "x", "cscli decisions add --ip 203.0.113.9 --duration 1m --reason x --type ban"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCscliHost(t, true)
			h.replies["cscli decisions add"] = "Decision successfully added\n"
			out, err := New().AddDecision(t.Context(), tc.value, tc.duration, tc.reason, "192.0.2.77")
			if err != nil || !strings.Contains(out, "successfully") {
				t.Fatalf("out %q, err %v", out, err)
			}
			if len(h.calls) != 1 || h.calls[0] != tc.want {
				t.Fatalf("ran %q, want %q", h.calls, tc.want)
			}
		})
	}
}

// A decision is a drop at the bouncer: banning the address the dashboard is
// used from ends the session, however the address is written.
func TestAddDecisionRefusesTheCaller(t *testing.T) {
	tests := []struct{ value, caller string }{
		{"192.0.2.77", "192.0.2.77"},
		{"192.0.2.0/24", "192.0.2.77"},
		{"192.0.2.0/16", "192.0.2.77"},
		{"2001:db8::77", "2001:db8::77"},
		{"2001:db8::/32", "2001:db8::77"},
		{"192.0.2.77", "::ffff:192.0.2.77"},
		{"::ffff:192.0.2.77", "192.0.2.77"},
		{" 192.0.2.77 ", "192.0.2.77"},
	}
	for _, tc := range tests {
		t.Run(tc.value+" from "+tc.caller, func(t *testing.T) {
			h := newCscliHost(t, true)
			_, err := New().AddDecision(t.Context(), tc.value, "4h", "x", tc.caller)
			if !errors.Is(err, ErrLockout) || !strings.Contains(err.Error(), "the address you are connected from") {
				t.Fatalf("error = %v", err)
			}
			if len(h.calls) != 0 {
				t.Fatalf("ran %v for a refused ban", h.calls)
			}
		})
	}
	// Somebody else's address in the same family is fine.
	h := newCscliHost(t, true)
	h.replies["cscli decisions add"] = "ok"
	if _, err := New().AddDecision(t.Context(), "192.0.2.78", "4h", "x", "192.0.2.77"); err != nil {
		t.Fatal(err)
	}
	if _, err := New().AddDecision(t.Context(), "198.51.100.0/24", "4h", "x", "192.0.2.77"); err != nil {
		t.Fatal(err)
	}
}

func TestAddDecisionValidates(t *testing.T) {
	tests := []struct {
		name, value, duration, reason string
	}{
		{"not an address", "example.com", "4h", "x"},
		{"command in the value", "203.0.113.9; reboot", "4h", "x"},
		{"flag as value", "--ip", "4h", "x"},
		{"bad prefix", "203.0.113.9/33", "4h", "x"},
		{"every address", "0.0.0.0/0", "4h", "x"},
		{"every v6 address", "::/0", "4h", "x"},
		{"loopback", "127.0.0.1", "4h", "x"},
		{"loopback range", "127.0.0.0/8", "4h", "x"},
		{"a range covering loopback", "0.0.0.0/1", "4h", "x"},
		{"unspecified", "0.0.0.0", "4h", "x"},
		{"zone", "fe80::1%eth0", "4h", "x"},
		{"v4 written as v6 range", "::ffff:203.0.113.0/120", "4h", "x"},
		{"no duration", "203.0.113.9", "", "x"},
		{"a word", "203.0.113.9", "forever", "x"},
		{"days are not Go durations", "203.0.113.9", "7d", "x"},
		{"too short", "203.0.113.9", "30s", "x"},
		{"too long", "203.0.113.9", "8761h", "x"},
		{"negative", "203.0.113.9", "-4h", "x"},
		{"newline in the reason", "203.0.113.9", "4h", "scan\n--type captcha"},
		{"tab in the reason", "203.0.113.9", "4h", "scan\tx"},
		{"long reason", "203.0.113.9", "4h", strings.Repeat("a", 129)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCscliHost(t, true)
			if _, err := New().AddDecision(t.Context(), tc.value, tc.duration, tc.reason, "192.0.2.77"); err == nil {
				t.Fatal("accepted")
			}
			if len(h.calls) != 0 {
				t.Fatalf("ran %v for an invalid request", h.calls)
			}
		})
	}
	h := newCscliHost(t, true)
	h.replies["cscli decisions add"] = "ok"
	if _, err := New().AddDecision(t.Context(), "203.0.113.9", "4h", strings.Repeat("é", 128), ""); err != nil {
		t.Fatalf("a reason of 128 characters: %v", err)
	}
	newCscliHost(t, false)
	if _, err := New().AddDecision(t.Context(), "203.0.113.9", "4h", "x", ""); !errors.Is(err, ErrCrowdSecMissing) {
		t.Fatalf("without cscli: %v", err)
	}
}

func TestDeleteDecision(t *testing.T) {
	h := newCscliHost(t, true)
	h.replies["cscli decisions delete"] = "1 decision(s) deleted\n"
	out, err := New().DeleteDecision(t.Context(), 4417)
	if err != nil || !strings.Contains(out, "deleted") {
		t.Fatalf("out %q, err %v", out, err)
	}
	if len(h.calls) != 1 || h.calls[0] != "cscli decisions delete --id 4417" {
		t.Fatalf("ran %q", h.calls)
	}
	for _, id := range []int{0, -3} {
		if _, err := New().DeleteDecision(t.Context(), id); err == nil {
			t.Fatalf("accepted id %d", id)
		}
	}
	if len(h.calls) != 1 {
		t.Fatalf("ran %v for an invalid id", h.calls)
	}
}
