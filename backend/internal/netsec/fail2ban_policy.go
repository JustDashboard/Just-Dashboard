package netsec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// A jail's three numbers say how a ban is earned; they say nothing about
// whether the ban stops anything. That is the actions' half — a jail whose
// only action mails a report bans nobody, and an iptables-multiport ban on
// port 22 leaves an sshd moved to 2222 wide open — and where the values in
// force came from, since fail2ban-client changes the running server and a
// restart loads whatever the files say. JailPolicy puts the three together
// in words, read from the running server and the dashboard's own drop-in.

// JailPolicy explains one jail.
type JailPolicy struct {
	Name string `json:"name"`
	// Rule is the policy as a sentence: "5 failures within 10 minutes earn a
	// 2-hour ban".
	Rule   string        `json:"rule"`
	Values []PolicyValue `json:"values"`
	// Watches is what the jail reads: log files, a journal match, or nothing.
	Watches JailWatch      `json:"watches"`
	Actions []ActionPolicy `json:"actions"`
	// Coverage compares the ports the actions drop with the ones the watched
	// service listens on, where the jail names a service this host can read.
	Coverage   *PortCoverage   `json:"coverage,omitempty"`
	IgnoreSelf bool            `json:"ignoreSelf"`
	IgnoreIP   []string        `json:"ignoreIp"`
	Findings   []PolicyFinding `json:"findings"`
	Error      string          `json:"error,omitempty"`
}

// PolicyValue is one parameter as the running server holds it and as the
// dashboard's drop-in will load it at the next start.
type PolicyValue struct {
	Key     string `json:"key"`
	Running string `json:"running"`
	// DropIn is the value in jail.d/99-just-dashboard.local, empty where the
	// drop-in does not set it (the distribution's files decide then).
	DropIn string `json:"dropIn,omitempty"`
	// Drift says the running value is not what the drop-in will load.
	Drift bool `json:"drift,omitempty"`
}

// JailWatch is the jail's input.
type JailWatch struct {
	Kind  string   `json:"kind"`
	Files []string `json:"files,omitempty"`
	Match string   `json:"match,omitempty"`
}

// ActionPolicy is one action and what it does with a ban.
type ActionPolicy struct {
	Name string `json:"name"`
	// Kind is firewall (a drop in the kernel), route (a blackhole route),
	// edge (a block at a remote service such as Cloudflare), report (mail,
	// abuse reports — nothing is blocked) or unknown.
	Kind     string   `json:"kind"`
	Enforces bool     `json:"enforces"`
	AllPorts bool     `json:"allPorts"`
	Ports    []string `json:"ports,omitempty"`
	Words    string   `json:"words"`
}

// PortCoverage is the check that a ban lands on the port the service uses.
type PortCoverage struct {
	Service   string   `json:"service"`
	Listening []string `json:"listening"`
	Covered   []string `json:"covered"`
	Uncovered []string `json:"uncovered"`
}

// PolicyFinding is one thing about the policy worth acting on.
type PolicyFinding struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// JailPolicy reads one jail's policy from the running server. sshdPorts are
// the ports sshd actually listens on, for the sshd jail's coverage check.
func (s *Service) JailPolicy(ctx context.Context, jail string, sshdPorts []string) (*JailPolicy, error) {
	if !jailNameRe.MatchString(jail) {
		return nil, fmt.Errorf("invalid jail name %q", jail)
	}
	get := func(args ...string) (string, bool) {
		out, err := run(ctx, "fail2ban-client", append([]string{"get", jail}, args...)...)
		if err != nil || strings.Contains(out, "NOK:") {
			return "", false
		}
		return strings.TrimSpace(out), true
	}
	in := policyInput{Name: jail, SSHDPorts: sshdPorts, Props: map[string]map[string]string{}}
	var ok bool
	if in.BanTime, ok = get("bantime"); !ok {
		return &JailPolicy{Name: jail, Values: []PolicyValue{}, Actions: []ActionPolicy{}, IgnoreIP: []string{}, Findings: []PolicyFinding{},
			Error: "fail2ban did not answer for this jail"}, nil
	}
	in.FindTime, _ = get("findtime")
	in.MaxRetry, _ = get("maxretry")
	ignore, _ := get("ignoreip")
	in.IgnoreIP = parseClientList(ignore)
	self, _ := get("ignoreself")
	in.IgnoreSelf = strings.EqualFold(self, "true")
	logs, _ := get("logpath")
	in.LogPaths = treeBranches(logs)
	match, _ := get("journalmatch")
	in.JournalMatch = journalMatchOf(match)
	actions, _ := get("actions")
	in.Actions = parseActionList(actions)
	for _, action := range in.Actions {
		if !jailNameRe.MatchString(action) {
			continue
		}
		props := map[string]string{}
		for _, prop := range []string{"port", "type"} {
			if v, ok := get("action", action, prop); ok {
				props[prop] = v
			}
		}
		in.Props[action] = props
	}
	in.DropIn = readJailDropIn(jailOverridePath(), jail)
	p := ExplainJailPolicy(in)
	return &p, nil
}

// policyInput is everything ExplainJailPolicy reads, gathered by the caller.
type policyInput struct {
	Name              string
	BanTime, FindTime string
	MaxRetry          string
	IgnoreIP          []string
	IgnoreSelf        bool
	LogPaths          []string
	JournalMatch      string
	Actions           []string
	Props             map[string]map[string]string
	DropIn            map[string]string
	SSHDPorts         []string
}

// ExplainJailPolicy turns the readings into the explanation. Pure, so every
// sentence it can produce is pinned by a test.
func ExplainJailPolicy(in policyInput) JailPolicy {
	p := JailPolicy{Name: in.Name, IgnoreSelf: in.IgnoreSelf, IgnoreIP: in.IgnoreIP, Values: []PolicyValue{}, Actions: []ActionPolicy{}, Findings: []PolicyFinding{}}
	if p.IgnoreIP == nil {
		p.IgnoreIP = []string{}
	}
	ban, find, retry := atoi(in.BanTime), atoi(in.FindTime), atoi(in.MaxRetry)
	switch {
	case ban < 0:
		p.Rule = fmt.Sprintf("%s within %s earn a ban that never expires", failures(retry), secondsWords(find))
	default:
		p.Rule = fmt.Sprintf("%s within %s earn a %s ban", failures(retry), secondsWords(find), lengthWords(ban))
	}
	for _, v := range []struct{ key, running string }{{"bantime", in.BanTime}, {"findtime", in.FindTime}, {"maxretry", in.MaxRetry}} {
		pv := PolicyValue{Key: v.key, Running: v.running, DropIn: in.DropIn[v.key]}
		if pv.DropIn != "" && !sameJailValue(pv.Running, pv.DropIn) {
			pv.Drift = true
			p.Findings = append(p.Findings, PolicyFinding{Level: "warning",
				Text: fmt.Sprintf("%s is %s in the running server and %s in the drop-in, so a restart changes it.", v.key, v.running, pv.DropIn)})
		}
		p.Values = append(p.Values, pv)
	}

	switch {
	case len(in.LogPaths) > 0:
		p.Watches = JailWatch{Kind: "files", Files: in.LogPaths}
	case in.JournalMatch != "":
		p.Watches = JailWatch{Kind: "journal", Match: in.JournalMatch}
	default:
		p.Watches = JailWatch{Kind: "none"}
		p.Findings = append(p.Findings, PolicyFinding{Level: "warning", Text: "The jail watches no log file and no journal match, so it can never count a failure."})
	}

	enforcing := 0
	var portSets [][]string
	allPorts := false
	for _, name := range in.Actions {
		a := classifyAction(name, in.Props[name])
		if a.Enforces {
			enforcing++
			if a.AllPorts {
				allPorts = true
			} else {
				portSets = append(portSets, a.Ports)
			}
		}
		p.Actions = append(p.Actions, a)
	}
	if enforcing == 0 {
		p.Findings = append(p.Findings, PolicyFinding{Level: "critical",
			Text: "No action blocks traffic: a ban here is recorded, and the address can go on connecting."})
	}

	if in.Name == "sshd" && len(in.SSHDPorts) > 0 && enforcing > 0 {
		cov := &PortCoverage{Service: "sshd", Listening: in.SSHDPorts, Covered: []string{}, Uncovered: []string{}}
		for _, port := range in.SSHDPorts {
			if allPorts || anyPortSetCovers(portSets, port) {
				cov.Covered = append(cov.Covered, port)
			} else {
				cov.Uncovered = append(cov.Uncovered, port)
			}
		}
		p.Coverage = cov
		if len(cov.Uncovered) > 0 {
			p.Findings = append(p.Findings, PolicyFinding{Level: "critical",
				Text: fmt.Sprintf("sshd listens on %s, and the ban drops only %s: a banned address keeps reaching sshd.",
					strings.Join(cov.Uncovered, ", "), strings.Join(flatten(portSets), ", "))})
		}
	}
	return p
}

func failures(n int) string {
	if n == 1 {
		return "1 failure"
	}
	return fmt.Sprintf("%d failures", n)
}

// secondsWords is a duration as fail2ban's own units would say it.
func secondsWords(n int) string {
	switch {
	case n <= 0:
		return "no time"
	case n%604800 == 0:
		return unitCount(n/604800, "week")
	case n%86400 == 0:
		return unitCount(n/86400, "day")
	case n%3600 == 0:
		return unitCount(n/3600, "hour")
	case n%60 == 0:
		return unitCount(n/60, "minute")
	}
	return unitCount(n, "second")
}

// lengthWords is a duration used as an adjective: "a 2-hour ban".
func lengthWords(n int) string {
	count, unit, _ := strings.Cut(secondsWords(n), " ")
	return count + "-" + strings.TrimSuffix(unit, "s")
}

func unitCount(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// sameJailValue compares a number fail2ban printed with one a file may spell
// as a duration ("1h", "10m", "1w").
func sameJailValue(running, file string) bool {
	if running == file {
		return true
	}
	r, rerr := strconv.Atoi(running)
	f, ferr := jailSeconds(file)
	return rerr == nil && ferr == nil && r == f
}

var jailDurationRe = regexp.MustCompile(`^(-?\d+)\s*(s|sec|m|min|h|hour|d|day|w|week|mo|month|y|year)?s?$`)

func jailSeconds(v string) (int, error) {
	m := jailDurationRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(v)))
	if m == nil {
		return 0, fmt.Errorf("not a duration: %q", v)
	}
	n, _ := strconv.Atoi(m[1])
	mult := map[string]int{"": 1, "s": 1, "sec": 1, "m": 60, "min": 60, "h": 3600, "hour": 3600, "d": 86400, "day": 86400,
		"w": 604800, "week": 604800, "mo": 2592000, "month": 2592000, "y": 31536000, "year": 31536000}[m[2]]
	return n * mult, nil
}

// classifyAction reads what a ban action does. fail2ban ships some ninety;
// they fall into a handful of kinds, and the name says which.
func classifyAction(name string, props map[string]string) ActionPolicy {
	lower := strings.ToLower(name)
	a := ActionPolicy{Name: name, Kind: "unknown"}
	switch {
	case hasPrefixAny(lower, "sendmail", "mail", "abuseipdb", "blocklist_de", "badips", "xarf", "complain", "mynetwatchman", "dummy", "smtp", "apprise", "matrix", "ntfy"):
		a.Kind = "report"
		a.Words = "reports the ban and blocks nothing"
		return a
	case strings.HasPrefix(lower, "cloudflare"):
		a.Kind, a.Enforces, a.AllPorts = "edge", true, false
		a.Words = "blocks the address at Cloudflare's edge, which covers only traffic proxied through Cloudflare"
		return a
	case lower == "route" || strings.HasPrefix(lower, "route-"):
		a.Kind, a.Enforces, a.AllPorts = "route", true, true
		a.Words = "installs a blackhole route, so nothing from the address reaches any port"
		return a
	case lower == "hostsdeny":
		a.Kind = "report"
		a.Words = "writes hosts.deny, which current OpenSSH and most services no longer read"
		return a
	case hasPrefixAny(lower, "iptables", "nftables", "firewallcmd", "ufw", "shorewall", "ipset", "ipfw", "bsd-ipfw", "pf", "npf", "apf", "csf", "nginx-block-map", "osx-"):
		a.Kind, a.Enforces = "firewall", true
	default:
		a.Words = "an action this page cannot classify; whether it blocks depends on what it runs"
		return a
	}
	port := strings.TrimSpace(props["port"])
	if strings.Contains(lower, "allports") || strings.EqualFold(props["type"], "allports") || port == "0:65535" || port == "1:65535" {
		a.AllPorts = true
		a.Words = "drops every port from a banned address in the firewall"
		return a
	}
	if port == "" {
		a.Words = "drops a banned address in the firewall; which ports could not be read"
		a.AllPorts = true
		return a
	}
	a.Ports = splitPorts(port)
	a.Words = "drops a banned address on " + strings.Join(a.Ports, ", ") + " in the firewall"
	return a
}

func hasPrefixAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func splitPorts(v string) []string {
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// servicePorts are the names fail2ban's jails use for ports, from
// /etc/services; a jail's port is usually one of these words.
var servicePorts = map[string]string{
	"ssh": "22", "http": "80", "https": "443", "smtp": "25", "submission": "587", "smtps": "465",
	"imap": "143", "imaps": "993", "imap2": "143", "pop3": "110", "pop3s": "995", "ftp": "21", "ftp-data": "20",
	"domain": "53", "mysql": "3306", "postgresql": "5432", "ldap": "389", "ldaps": "636", "rdp": "3389",
	"ms-wbt-server": "3389", "telnet": "23", "sieve": "4190", "xmpp-client": "5222", "asterisk": "5060",
}

// portCovers reports whether a jail's port entry ("ssh", "2222", "8000:8100")
// covers a numeric port.
func portCovers(entry, port string) bool {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if n, ok := servicePorts[entry]; ok {
		entry = n
	}
	want, err := strconv.Atoi(port)
	if err != nil {
		return entry == port
	}
	if lo, hi, ok := strings.Cut(entry, ":"); ok {
		l, lerr := strconv.Atoi(lo)
		h, herr := strconv.Atoi(hi)
		return lerr == nil && herr == nil && want >= l && want <= h
	}
	n, err := strconv.Atoi(entry)
	return err == nil && n == want
}

func anyPortSetCovers(sets [][]string, port string) bool {
	for _, set := range sets {
		for _, entry := range set {
			if portCovers(entry, port) {
				return true
			}
		}
	}
	return false
}

func flatten(sets [][]string) []string {
	out := []string{}
	for _, set := range sets {
		out = append(out, set...)
	}
	return out
}

// parseActionList reads `get <jail> actions`. Unlike the address lists, more
// than one action prints on one comma-separated line under the heading.
func parseActionList(out string) []string {
	actions := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, ":") {
			continue
		}
		if branch := treeBranches(line); len(branch) > 0 {
			line = branch[0]
		}
		for _, name := range strings.Split(line, ",") {
			if name = strings.TrimSpace(name); name != "" && !strings.Contains(name, " ") {
				actions = append(actions, name)
			}
		}
	}
	return actions
}

// journalMatchOf reads `get <jail> journalmatch`: the match under its
// heading, or nothing for "No journal match filter set".
func journalMatchOf(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 || !strings.HasSuffix(strings.TrimSpace(lines[0]), ":") {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[1:], " "))
}

// readJailDropIn reads the dashboard's own section for a jail out of its
// drop-in. It is not fail2ban's whole configuration — the distribution's
// jail.conf and the operator's jail.local come first — but it is the one
// file this dashboard writes, read last, so what it sets is what a restart
// loads.
func readJailDropIn(path, jail string) map[string]string {
	values := map[string]string{}
	if path == "" {
		return values
	}
	f, err := os.Open(path)
	if err != nil {
		return values
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = line == "["+jail+"]"
			continue
		}
		if !in || line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	return values
}
