package netsec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Suricata watches the traffic itself rather than a log: rules matched against
// packets, written as alerts to eve.json. As an IDS it reports; as an IPS it
// sits in an nfqueue and drops. The page shows which, what it has been
// matching lately, and whether the rules loaded at all — a Suricata with an
// empty ruleset is running and detecting nothing.

// Where Suricata keeps things on a Debian-family host, and the options each
// knob has. Variables so tests point them at their own files.
var (
	suricataEvePath     = "/var/log/suricata/eve.json"
	suricataDefaultFile = "/etc/default/suricata"
	suricataRulesFile   = "/var/lib/suricata/rules/suricata.rules"
)

// eveTail is how much of the end of eve.json is read. Two megabytes is some
// thousands of alerts on a busy host and a few seconds' work to parse; the
// file itself is gigabytes.
const eveTail = 2 << 20

// maxSuricataAlerts is how many alerts are returned, newest first.
const maxSuricataAlerts = 100

// SuricataAlert is one rule match. The packet payload eve.json may carry is
// left behind on purpose: it is somebody's traffic.
type SuricataAlert struct {
	Time        string `json:"time"`
	SrcIP       string `json:"srcIp"`
	SrcPort     int    `json:"srcPort,omitempty"`
	DestIP      string `json:"destIp"`
	DestPort    int    `json:"destPort,omitempty"`
	Proto       string `json:"proto,omitempty"`
	AppProto    string `json:"appProto,omitempty"`
	Signature   string `json:"signature"`
	SignatureID int    `json:"signatureId"`
	Category    string `json:"category,omitempty"`
	// Severity is Suricata's: 1 is the most severe.
	Severity int `json:"severity"`
	// Action is allowed, or blocked where Suricata is dropping.
	Action string `json:"action,omitempty"`
}

// SuricataSeverity counts alerts at one severity.
type SuricataSeverity struct {
	Severity int    `json:"severity"`
	Label    string `json:"label"`
	Count    int    `json:"count"`
}

// SuricataSignature counts one rule's alerts.
type SuricataSignature struct {
	Signature   string `json:"signature"`
	SignatureID int    `json:"signatureId"`
	Category    string `json:"category,omitempty"`
	Severity    int    `json:"severity"`
	Count       int    `json:"count"`
}

// SuricataView is what the page draws.
type SuricataView struct {
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Version   string `json:"version,omitempty"`
	// Mode is ips where Suricata sits in an nfqueue, ids otherwise; ModeSource
	// says where that was read from, because it is a reading of the
	// configuration and not something Suricata reports about itself.
	Mode       string `json:"mode"`
	ModeSource string `json:"modeSource,omitempty"`
	// Alerts are the newest first; Scanned is how many alerts the tail held,
	// which is what the counts below are over.
	Alerts        []SuricataAlert     `json:"alerts"`
	Scanned       int                 `json:"scanned"`
	BySeverity    []SuricataSeverity  `json:"bySeverity"`
	TopSignatures []SuricataSignature `json:"topSignatures"`
	// RulesLoaded is the number of enabled rules in the rule file, absent where
	// the file could not be read.
	RulesLoaded *int   `json:"rulesLoaded,omitempty"`
	LogPath     string `json:"logPath"`
	// LogRefused is why eve.json was not read: it is outside the log roots the
	// dashboard may read. LogError is any other reason it was not.
	LogRefused string `json:"logRefused,omitempty"`
	LogError   string `json:"logError,omitempty"`

	// The setup the page walks an operator through after installing: what
	// the newest stats event says the capture sees (nil where eve.json holds
	// none or could not be read), where it captures, where the rules came
	// from, and the queue rules an inline deployment depends on.
	Capture    *SuricataCapture   `json:"capture,omitempty"`
	Interfaces SuricataInterfaces `json:"interfaces"`
	Rules      SuricataRules      `json:"rules"`
	Inline     SuricataInline     `json:"inline"`
}

var versionRe = regexp.MustCompile(`(?i)version\s+(\S+)`)

// Suricata reads the service's state and its recent alerts. allow is the log
// roots' check: eve.json is read only through it, so the file is subject to
// the same limits as every other log the dashboard shows. It returns the path
// to read, which may be the same path with its links resolved.
func (s *Service) Suricata(ctx context.Context, allow func(path string) (string, error)) (*SuricataView, error) {
	v := &SuricataView{
		Alerts: []SuricataAlert{}, BySeverity: []SuricataSeverity{}, TopSignatures: []SuricataSignature{},
		LogPath: suricataEvePath,
	}
	if !hasTool("suricata") {
		return v, nil
	}
	v.Installed = true
	if out, _ := run(ctx, "systemctl", "is-active", "suricata"); strings.TrimSpace(out) == "active" {
		v.Active = true
	}
	if out, err := run(ctx, "suricata", "-V"); err == nil {
		if m := versionRe.FindStringSubmatch(out); m != nil {
			v.Version = m[1]
		}
	}
	v.Mode, v.ModeSource = suricataMode(ctx)
	if n, ok := countRules(); ok {
		v.RulesLoaded = &n
	}

	if allow == nil {
		v.LogRefused = "no log roots are configured"
		s.suricataSetup(ctx, v, nil)
		return v, nil
	}
	path, err := allow(suricataEvePath)
	if err != nil {
		v.LogRefused = err.Error()
		s.suricataSetup(ctx, v, nil)
		return v, nil
	}
	data, err := tailFile(path, eveTail)
	if err != nil {
		if os.IsNotExist(err) {
			v.LogError = "Suricata has not written " + suricataEvePath + " yet"
		} else {
			v.LogError = err.Error()
		}
		s.suricataSetup(ctx, v, nil)
		return v, nil
	}
	s.suricataSetup(ctx, v, data)
	alerts := parseEveAlerts(data)
	v.Scanned = len(alerts)
	v.BySeverity, v.TopSignatures = summariseAlerts(alerts)
	// The file is in time order, so the newest are at its end.
	for i := len(alerts) - 1; i >= 0 && len(v.Alerts) < maxSuricataAlerts; i-- {
		v.Alerts = append(v.Alerts, alerts[i])
	}
	return v, nil
}

// suricataMode says whether Suricata is dropping or only watching.
//
// The service's own command line decides first: it is what runs. Debian's
// package ships /etc/default/suricata with LISTENMODE=nfqueue beside a unit
// that starts `suricata --af-packet` and never reads that file, so trusting
// the file first called every stock install an IPS. The file decides only
// where the command line does not say — an init script, or a unit that
// expands the file's variables. suricata.yaml is not consulted: its nfq
// section is in every default file and says nothing about whether it is used.
func suricataMode(ctx context.Context) (mode, source string) {
	if out, err := run(ctx, "systemctl", "show", "-p", "ExecStart", "suricata"); err == nil {
		argv := execArgv(out)
		if argv == "" {
			argv = out
		}
		if !strings.Contains(argv, "$") {
			lower := strings.ToLower(argv)
			for _, f := range strings.Fields(argv) {
				if f == "-q" || (strings.HasPrefix(f, "-q") && len(f) > 2 && f[2] >= '0' && f[2] <= '9') {
					return "ips", "the service's command line"
				}
			}
			if strings.Contains(lower, "nfqueue") || strings.Contains(lower, "--nfq") {
				return "ips", "the service's command line"
			}
			if strings.Contains(lower, "--af-packet") || strings.Contains(lower, "--pcap") || containsField(argv, "-i") {
				return "ids", "the service's command line"
			}
		}
	}
	if b, err := os.ReadFile(suricataDefaultFile); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			key, value, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(key) != "LISTENMODE" {
				continue
			}
			value = strings.ToLower(strings.Trim(strings.TrimSpace(value), `"'`))
			switch value {
			case "nfqueue":
				return "ips", "LISTENMODE in " + suricataDefaultFile
			case "af-packet", "pcap", "pfring", "netmap":
				return "ids", "LISTENMODE in " + suricataDefaultFile
			}
		}
	}
	return "ids", "default"
}

func containsField(s, field string) bool {
	for _, f := range strings.Fields(s) {
		if f == field {
			return true
		}
	}
	return false
}

// countRules counts the enabled rules in the rule file: a line that is not
// blank and not a comment, since a disabled rule is commented out.
func countRules() (int, bool) {
	for _, base := range []string{"", "/host"} {
		f, err := os.Open(base + suricataRulesFile)
		if err != nil {
			continue
		}
		n := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				n++
			}
		}
		f.Close()
		return n, true
	}
	return 0, false
}

// tailFile reads the last n bytes of a file, from the start of a whole line.
func tailFile(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	start := st.Size() - n
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, n+1<<16))
	if err != nil {
		return nil, err
	}
	if start > 0 {
		// The read began in the middle of a line.
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	return data, nil
}

type eveEvent struct {
	Timestamp string `json:"timestamp"`
	EventType string `json:"event_type"`
	SrcIP     string `json:"src_ip"`
	SrcPort   int    `json:"src_port"`
	DestIP    string `json:"dest_ip"`
	DestPort  int    `json:"dest_port"`
	Proto     string `json:"proto"`
	AppProto  string `json:"app_proto"`
	Alert     *struct {
		Action      string `json:"action"`
		SignatureID int    `json:"signature_id"`
		Signature   string `json:"signature"`
		Category    string `json:"category"`
		Severity    int    `json:"severity"`
	} `json:"alert"`
}

// parseEveAlerts reads alert events out of eve.json's JSON lines, in file
// order. A line that is not JSON — the first of a tail, the last of a file
// being written — is skipped, as is every event that is not an alert.
func parseEveAlerts(data []byte) []SuricataAlert {
	var out []SuricataAlert
	sc := bufio.NewScanner(bytes.NewReader(data))
	// An alert carrying a packet and its decoded protocol can be tens of
	// kilobytes.
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		// Cheap rejection before a JSON parse: most of eve.json is flow, dns
		// and http events.
		if !bytes.Contains(line, []byte(`"alert"`)) {
			continue
		}
		var e eveEvent
		if err := json.Unmarshal(line, &e); err != nil || e.EventType != "alert" || e.Alert == nil {
			continue
		}
		out = append(out, SuricataAlert{
			Time: eveTime(e.Timestamp), SrcIP: e.SrcIP, SrcPort: e.SrcPort, DestIP: e.DestIP, DestPort: e.DestPort,
			Proto: e.Proto, AppProto: e.AppProto, Signature: e.Alert.Signature, SignatureID: e.Alert.SignatureID,
			Category: e.Alert.Category, Severity: e.Alert.Severity, Action: e.Alert.Action,
		})
	}
	return out
}

// eveTime turns Suricata's timestamp, 2024-05-01T12:00:00.123456+0000 (a zone
// offset with no colon, which is not RFC 3339), into one a browser parses.
func eveTime(s string) string {
	for _, layout := range []string{"2006-01-02T15:04:05.999999-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339Nano)
		}
	}
	return s
}

func summariseAlerts(alerts []SuricataAlert) ([]SuricataSeverity, []SuricataSignature) {
	bySev := map[int]int{}
	type sigKey struct {
		id  int
		sig string
	}
	sigs := map[sigKey]*SuricataSignature{}
	for _, a := range alerts {
		bySev[a.Severity]++
		k := sigKey{a.SignatureID, a.Signature}
		if sigs[k] == nil {
			sigs[k] = &SuricataSignature{Signature: a.Signature, SignatureID: a.SignatureID, Category: a.Category, Severity: a.Severity}
		}
		sigs[k].Count++
	}
	sev := []SuricataSeverity{}
	for s, n := range bySev {
		sev = append(sev, SuricataSeverity{Severity: s, Label: severityLabel(s), Count: n})
	}
	sort.Slice(sev, func(i, j int) bool { return sev[i].Severity < sev[j].Severity })
	top := []SuricataSignature{}
	for _, s := range sigs {
		top = append(top, *s)
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Count != top[j].Count {
			return top[i].Count > top[j].Count
		}
		return top[i].SignatureID < top[j].SignatureID
	})
	if len(top) > 10 {
		top = top[:10]
	}
	return sev, top
}

func severityLabel(s int) string {
	switch s {
	case 1:
		return "high"
	case 2:
		return "medium"
	case 3:
		return "low"
	}
	return "informational"
}
