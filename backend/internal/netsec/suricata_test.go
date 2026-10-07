package netsec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// suricataHost lays a Suricata out in a temporary directory and stands a
// recorded systemctl and suricata behind run. dir holds eve.json, the rules
// file and the defaults file; none of the real ones is read.
type suricataHost struct {
	t       *testing.T
	dir     string
	execs   string
	active  string
	version string
}

func newSuricataHost(t *testing.T) *suricataHost {
	t.Helper()
	h := &suricataHost{t: t, dir: t.TempDir(), active: "active\n", version: "This is Suricata version 7.0.8 RELEASE\n"}
	h.execs = testdata(t, "suricata-execstart-ids.txt")
	prevRun, prevHas := run, hasTool
	prevEve, prevDefault, prevRules := suricataEvePath, suricataDefaultFile, suricataRulesFile
	suricataEvePath = filepath.Join(h.dir, "eve.json")
	suricataDefaultFile = filepath.Join(h.dir, "default-suricata")
	suricataRulesFile = filepath.Join(h.dir, "suricata.rules")
	run = func(_ context.Context, name string, args ...string) (string, error) {
		switch call := name + " " + strings.Join(args, " "); call {
		case "systemctl is-active suricata":
			return h.active, nil
		case "suricata -V":
			return h.version, nil
		case "systemctl show -p ExecStart suricata":
			return h.execs, nil
		default:
			t.Errorf("unexpected command: %s", call)
			return "", fmt.Errorf("unexpected command: %s", call)
		}
	}
	hasTool = func(string) bool { return true }
	t.Cleanup(func() {
		run, hasTool = prevRun, prevHas
		suricataEvePath, suricataDefaultFile, suricataRulesFile = prevEve, prevDefault, prevRules
	})
	return h
}

func (h *suricataHost) file(name, content string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, name), []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// allowAll is the log roots' check admitting every path.
func allowAll(path string) (string, error) { return path, nil }

func TestSuricataReadsStateAndAlerts(t *testing.T) {
	h := newSuricataHost(t)
	h.file("eve.json", testdata(t, "suricata-eve.json"))
	h.file("suricata.rules", testdata(t, "suricata-rules.txt"))
	h.file("default-suricata", testdata(t, "suricata-default-afpacket.txt"))

	var asked []string
	v, err := New().Suricata(t.Context(), func(path string) (string, error) {
		asked = append(asked, path)
		return path, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != suricataEvePath {
		t.Fatalf("eve.json was read through the roots' check: %v", asked)
	}
	if !v.Installed || !v.Active || v.Version != "7.0.8" || v.Mode != "ids" || v.LogRefused != "" || v.LogError != "" {
		t.Fatalf("view = %+v", v)
	}
	if v.RulesLoaded == nil || *v.RulesLoaded != 3 {
		t.Fatalf("rules = %v", v.RulesLoaded)
	}

	// Five alerts among the flow, dns and stats events, newest first.
	if v.Scanned != 5 || len(v.Alerts) != 5 {
		t.Fatalf("%d alerts scanned, %d listed", v.Scanned, len(v.Alerts))
	}
	newest := v.Alerts[0]
	if newest.Time != "2026-10-07T08:06:00Z" || newest.SrcIP != "203.0.113.9" || newest.SrcPort != 51530 ||
		newest.DestIP != "198.51.100.4" || newest.DestPort != 22 || newest.Proto != "TCP" || newest.AppProto != "ssh" ||
		newest.Signature != "ET SCAN Potential SSH Scan" || newest.SignatureID != 2001219 || newest.Severity != 2 || newest.Action != "allowed" {
		t.Fatalf("newest = %+v", newest)
	}
	oldest := v.Alerts[4]
	if oldest.Time != "2026-10-07T08:01:10.5Z" {
		t.Fatalf("oldest = %+v", oldest)
	}
	var order []int
	for _, a := range v.Alerts {
		order = append(order, a.SignatureID)
	}
	// The file holds, oldest first: ssh, ssh, ja3 (blocked), http, ssh.
	if fmt.Sprint(order) != "[2001219 2100498 2024897 2001219 2001219]" {
		t.Fatalf("order = %v", order)
	}
	if v.Alerts[2].Action != "blocked" {
		t.Fatalf("an IPS drop is reported as %q", v.Alerts[2].Action)
	}

	if fmt.Sprint(v.BySeverity) != "[{1 high 1} {2 medium 3} {3 low 1}]" {
		t.Fatalf("by severity = %v", v.BySeverity)
	}
	if len(v.TopSignatures) != 3 || v.TopSignatures[0].SignatureID != 2001219 || v.TopSignatures[0].Count != 3 ||
		v.TopSignatures[0].Category != "Attempted Information Leak" {
		t.Fatalf("top = %+v", v.TopSignatures)
	}
}

// The payload eve.json carries is somebody's traffic; it does not reach the page.
func TestSuricataAlertsCarryNoPayload(t *testing.T) {
	alerts := parseEveAlerts([]byte(testdata(t, "suricata-eve.json")))
	for _, a := range alerts {
		if strings.Contains(fmt.Sprintf("%+v", a), "secret") {
			t.Fatalf("payload leaked into %+v", a)
		}
	}
}

func TestSuricataStopsAtTheNewestHundred(t *testing.T) {
	h := newSuricataHost(t)
	var b strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&b, `{"timestamp":"2026-10-07T08:00:00.000000+0000","event_type":"alert","src_ip":"203.0.113.9","dest_ip":"198.51.100.4","proto":"TCP","alert":{"signature_id":%d,"signature":"rule %d","severity":3}}`+"\n", i, i)
	}
	h.file("eve.json", b.String())
	v, _ := New().Suricata(t.Context(), allowAll)
	if v.Scanned != 250 || len(v.Alerts) != 100 || v.Alerts[0].SignatureID != 250 || v.Alerts[99].SignatureID != 151 {
		t.Fatalf("scanned %d, listed %d, first %d", v.Scanned, len(v.Alerts), v.Alerts[0].SignatureID)
	}
	if len(v.TopSignatures) != 10 {
		t.Fatalf("top signatures = %d", len(v.TopSignatures))
	}
}

func TestSuricataReadsOnlyTheTailOfAHugeLog(t *testing.T) {
	h := newSuricataHost(t)
	// More than the tail's size of early, distinct alerts, then three recent
	// ones. Only the end of the file may be read, and the line the cut lands
	// in the middle of is dropped.
	line := func(id int) string {
		return fmt.Sprintf(`{"timestamp":"2026-10-07T08:00:00.000000+0000","event_type":"alert","src_ip":"203.0.113.9","dest_ip":"198.51.100.4","proto":"TCP","alert":{"signature_id":%d,"signature":"old rule %d","severity":3},"padding":"%s"}`+"\n", id, id, strings.Repeat("x", 900))
	}
	var b strings.Builder
	for i := 1; b.Len() < eveTail+(256<<10); i++ {
		b.WriteString(line(i))
	}
	total := strings.Count(b.String(), "\n")
	for i := 1; i <= 3; i++ {
		b.WriteString(line(900000 + i))
	}
	h.file("eve.json", b.String())

	v, _ := New().Suricata(t.Context(), allowAll)
	if v.LogError != "" || len(v.Alerts) != 100 || v.Alerts[0].SignatureID != 900003 || v.Alerts[2].SignatureID != 900001 {
		t.Fatalf("view = %+v", v)
	}
	if v.Scanned >= total || v.Scanned < 1000 {
		t.Fatalf("scanned %d of %d: the tail is not the whole file", v.Scanned, total+3)
	}
}

// eve.json is read only where the log roots say it may be.
func TestSuricataRefusedPath(t *testing.T) {
	h := newSuricataHost(t)
	h.file("eve.json", testdata(t, "suricata-eve.json"))
	v, err := New().Suricata(t.Context(), func(path string) (string, error) {
		return "", fmt.Errorf("path %q is outside the configured log roots", path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.LogRefused, "outside the configured log roots") || len(v.Alerts) != 0 || v.Scanned != 0 || !v.Active {
		t.Fatalf("view = %+v", v)
	}
	// The state that does not come from the log is still reported.
	if v.Version != "7.0.8" || v.Mode != "ids" {
		t.Fatalf("view = %+v", v)
	}
	v, _ = New().Suricata(t.Context(), nil)
	if v.LogRefused == "" {
		t.Fatal("no check at all must not mean no limit")
	}
}

func TestSuricataLogNotWrittenYet(t *testing.T) {
	newSuricataHost(t)
	v, _ := New().Suricata(t.Context(), allowAll)
	if !strings.Contains(v.LogError, "has not written") || v.LogRefused != "" || v.Alerts == nil {
		t.Fatalf("view = %+v", v)
	}
	if v.RulesLoaded != nil {
		t.Fatalf("a missing rule file read as %d rules", *v.RulesLoaded)
	}
}

func TestSuricataMode(t *testing.T) {
	tests := []struct {
		name         string
		defaults     string
		execStart    string
		wantMode     string
		wantContains string
	}{
		{"defaults say nfqueue", "suricata-default-nfqueue.txt", "suricata-execstart-ids.txt", "ips", "LISTENMODE"},
		{"defaults say af-packet", "suricata-default-afpacket.txt", "suricata-execstart-ips.txt", "ids", "LISTENMODE"},
		{"no defaults file, -q on the command line", "", "suricata-execstart-ips.txt", "ips", "command line"},
		{"no defaults file, af-packet on the command line", "", "suricata-execstart-ids.txt", "ids", "command line"},
		{"nothing says", "", "", "ids", "default"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newSuricataHost(t)
			if tc.defaults != "" {
				h.file("default-suricata", testdata(t, tc.defaults))
			}
			h.execs = ""
			if tc.execStart != "" {
				h.execs = testdata(t, tc.execStart)
			}
			mode, source := suricataMode(t.Context())
			if mode != tc.wantMode || !strings.Contains(source, tc.wantContains) {
				t.Fatalf("mode %q from %q", mode, source)
			}
		})
	}
	// A pidfile is not a queue.
	h := newSuricataHost(t)
	h.execs = "ExecStart={ argv[]=/usr/bin/suricata -c /etc/suricata/suricata.yaml --pidfile /run/q.pid ; }"
	if mode, _ := suricataMode(t.Context()); mode != "ids" {
		t.Fatalf("mode = %q", mode)
	}
}

func TestSuricataNotInstalledOrStopped(t *testing.T) {
	h := newSuricataHost(t)
	h.active = "inactive\n"
	v, _ := New().Suricata(t.Context(), allowAll)
	if !v.Installed || v.Active {
		t.Fatalf("view = %+v", v)
	}
	hasTool = func(string) bool { return false }
	called := false
	v, err := New().Suricata(t.Context(), func(p string) (string, error) { called = true; return p, nil })
	if err != nil || v.Installed || called || v.Alerts == nil {
		t.Fatalf("view = %+v, %v, %v", v, err, called)
	}
}

func TestEveTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-07T08:06:00.123456+0000": "2026-10-07T08:06:00.123456Z",
		"2026-10-07T10:06:00.000000+0200": "2026-10-07T08:06:00Z",
		"2026-10-07T08:06:00Z":            "2026-10-07T08:06:00Z",
		"yesterday":                       "yesterday",
	} {
		if got := eveTime(in); got != want {
			t.Errorf("eveTime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTailFileOnAMissingFile(t *testing.T) {
	if _, err := tailFile(filepath.Join(t.TempDir(), "nope"), 10); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v", err)
	}
}
