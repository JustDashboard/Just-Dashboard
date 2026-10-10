package netsec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Installing Suricata leaves it watching whatever interface the package
// guessed, with no rules until somebody runs suricata-update, and stopped on
// Debian (RUN=no). The page walks the rest of the way: which interface it
// captures and whether that capture is seeing packets, when the rules were
// last fetched and fetching them again, starting it, and — read only — what
// an inline (IPS) deployment's queue rules do when Suricata is not there to
// answer, which is the one property that decides whether IPS can cut the
// host off.

var (
	suricataYAML    = "/etc/suricata/suricata.yaml"
	suricataSources = "/var/lib/suricata/update/sources"
)

// SuricataCapture is the newest stats event: whether the capture sees
// packets at all.
type SuricataCapture struct {
	At             string `json:"at"`
	Uptime         int64  `json:"uptimeSeconds"`
	KernelPackets  uint64 `json:"kernelPackets"`
	KernelDrops    uint64 `json:"kernelDrops"`
	DecoderPackets uint64 `json:"decoderPackets"`
}

// SuricataInterfaces is where af-packet capture is configured and what the
// host offers instead.
type SuricataInterfaces struct {
	// Configured are suricata.yaml's af-packet interfaces, without the
	// "default" template entry.
	Configured []string `json:"configured"`
	// Candidates are the host's interfaces that are up and not loopback.
	Candidates []string `json:"candidates"`
	// Editable says the interface can be changed from here; Reason says why
	// not where it cannot.
	Editable bool   `json:"editable"`
	Reason   string `json:"reason,omitempty"`
}

// SuricataRules is the rule set's provenance.
type SuricataRules struct {
	File      string   `json:"file"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
	Sources   []string `json:"sources"`
	// Updater says suricata-update is installed.
	Updater bool `json:"updater"`
}

// NFQueueRule is one rule sending packets to a userspace queue.
type NFQueueRule struct {
	Source string `json:"source"`
	Chain  string `json:"chain"`
	Queue  string `json:"queue"`
	// Bypass is fail-open: with nothing reading the queue the kernel
	// accepts the packet instead of dropping it.
	Bypass bool   `json:"bypass"`
	Rule   string `json:"rule"`
}

// SuricataInline is the inline deployment's evidence. It is read, never
// written: changing a queue rule changes what reaches every port of the host,
// the dashboard's included.
type SuricataInline struct {
	Queues []NFQueueRule `json:"queues"`
	// FailOpen is every queue rule bypassing when Suricata is not reading.
	FailOpen bool   `json:"failOpen"`
	Words    string `json:"words"`
	Error    string `json:"error,omitempty"`
}

// suricataSetup fills the setup evidence onto a view.
func (s *Service) suricataSetup(ctx context.Context, v *SuricataView, eve []byte) {
	v.Capture = latestCapture(eve)
	v.Interfaces = s.suricataInterfaces(ctx, v.Mode)
	v.Rules = suricataRules()
	v.Inline = readInline(ctx)
}

func (s *Service) suricataInterfaces(ctx context.Context, mode string) SuricataInterfaces {
	in := SuricataInterfaces{Configured: []string{}, Candidates: hostCandidates()}
	if b, err := os.ReadFile(suricataYAML); err == nil {
		in.Configured = afPacketInterfaces(string(b))
	} else {
		in.Reason = suricataYAML + " could not be read"
		return in
	}
	switch {
	case mode == "ips":
		in.Reason = "Inline mode reads a netfilter queue, not an interface."
	case explicitCaptureInterface(ctx):
		in.Reason = "The service names its interface on its own command line, which overrides suricata.yaml."
	case len(in.Configured) == 0:
		in.Reason = "suricata.yaml has no af-packet interface to change."
	default:
		in.Editable = true
	}
	return in
}

func hostCandidates() []string {
	out := []string{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 {
			out = append(out, i.Name)
		}
	}
	sort.Strings(out)
	return out
}

var afPacketItemRe = regexp.MustCompile(`^(\s*-\s*interface:\s*)(\S+)(.*)$`)

// afPacketInterfaces lists the interfaces under suricata.yaml's top-level
// af-packet key, in order, leaving out the "default" template.
func afPacketInterfaces(yaml string) []string {
	out := []string{}
	inSection := false
	for _, line := range strings.Split(yaml, "\n") {
		if topLevelKey(line) {
			inSection = strings.HasPrefix(line, "af-packet:")
			continue
		}
		if !inSection {
			continue
		}
		if m := afPacketItemRe.FindStringSubmatch(line); m != nil && m[2] != "default" {
			out = append(out, strings.Trim(m[2], `"'`))
		}
	}
	return out
}

func topLevelKey(line string) bool {
	return line != "" && line[0] != ' ' && line[0] != '\t' && line[0] != '#' && line[0] != '-' && strings.Contains(line, ":")
}

// setAfPacketInterface rewrites the first non-default af-packet interface,
// line for line, so every comment and every other setting of the file stays
// as the operator left it.
func setAfPacketInterface(yaml, iface string) (string, string, error) {
	lines := strings.Split(yaml, "\n")
	inSection := false
	for i, line := range lines {
		if topLevelKey(line) {
			inSection = strings.HasPrefix(line, "af-packet:")
			continue
		}
		if !inSection {
			continue
		}
		m := afPacketItemRe.FindStringSubmatch(line)
		if m == nil || m[2] == "default" {
			continue
		}
		lines[i] = m[1] + iface + m[3]
		return strings.Join(lines, "\n"), strings.Trim(m[2], `"'`), nil
	}
	return "", "", fmt.Errorf("suricata.yaml has no af-packet interface to change")
}

// explicitCaptureInterface reports a command line naming its own capture
// interface (--af-packet=eth0, -i eth0), which suricata.yaml cannot override.
func explicitCaptureInterface(ctx context.Context) bool {
	out, err := run(ctx, "systemctl", "show", "-p", "ExecStart", "suricata")
	if err != nil {
		return false
	}
	for _, f := range strings.Fields(execArgv(out)) {
		if strings.HasPrefix(f, "--af-packet=") || f == "-i" || strings.HasPrefix(f, "--pcap=") {
			return true
		}
	}
	return false
}

// execArgv is the argv of `systemctl show -p ExecStart`.
func execArgv(out string) string {
	_, after, ok := strings.Cut(out, "argv[]=")
	if !ok {
		return ""
	}
	argv, _, _ := strings.Cut(after, " ;")
	return argv
}

func suricataRules() SuricataRules {
	r := SuricataRules{File: suricataRulesFile, Sources: []string{}, Updater: hasTool("suricata-update")}
	if st, err := os.Stat(suricataRulesFile); err == nil {
		r.UpdatedAt = st.ModTime().UTC().Format(time.RFC3339)
	}
	entries, err := os.ReadDir(suricataSources)
	if err != nil {
		return r
	}
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".yaml") {
			r.Sources = append(r.Sources, strings.ReplaceAll(strings.TrimSuffix(name, ".yaml"), "-", "/"))
		}
	}
	sort.Strings(r.Sources)
	return r
}

// latestCapture reads the newest stats event in the tail.
func latestCapture(eve []byte) *SuricataCapture {
	var newest *SuricataCapture
	sc := bufio.NewScanner(bytes.NewReader(eve))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"stats"`)) {
			continue
		}
		var e struct {
			Timestamp string `json:"timestamp"`
			EventType string `json:"event_type"`
			Stats     struct {
				Uptime  int64 `json:"uptime"`
				Capture struct {
					KernelPackets uint64 `json:"kernel_packets"`
					KernelDrops   uint64 `json:"kernel_drops"`
				} `json:"capture"`
				Decoder struct {
					Pkts uint64 `json:"pkts"`
				} `json:"decoder"`
			} `json:"stats"`
		}
		if json.Unmarshal(line, &e) != nil || e.EventType != "stats" {
			continue
		}
		newest = &SuricataCapture{At: eveTime(e.Timestamp), Uptime: e.Stats.Uptime, KernelPackets: e.Stats.Capture.KernelPackets,
			KernelDrops: e.Stats.Capture.KernelDrops, DecoderPackets: e.Stats.Decoder.Pkts}
	}
	return newest
}

// readInline reads every rule that sends packets to a queue.
func readInline(ctx context.Context) SuricataInline {
	in := SuricataInline{Queues: []NFQueueRule{}}
	var problems []string
	for _, tool := range []string{"iptables-save", "ip6tables-save"} {
		if !hasTool(tool) {
			continue
		}
		out, err := run(ctx, tool)
		if err != nil {
			problems = append(problems, tool+": "+firstLine(out+" "+err.Error()))
			continue
		}
		in.Queues = append(in.Queues, parseIptablesQueues(out, tool)...)
	}
	if hasTool("nft") {
		if out, err := run(ctx, "nft", "-j", "list", "ruleset"); err != nil {
			problems = append(problems, "nft: "+firstLine(out+" "+err.Error()))
		} else {
			in.Queues = append(in.Queues, parseNFTQueues(out)...)
		}
	}
	if len(problems) > 0 {
		in.Error = strings.Join(problems, "; ")
	}
	in.FailOpen = len(in.Queues) > 0
	for _, q := range in.Queues {
		in.FailOpen = in.FailOpen && q.Bypass
	}
	switch {
	case len(in.Queues) == 0:
		in.Words = "No rule sends packets to a queue, so nothing waits on Suricata: inline mode would inspect nothing, and its absence drops nothing."
	case in.FailOpen:
		in.Words = "Every queue rule bypasses when nothing reads the queue: if Suricata stops, traffic passes uninspected rather than stopping."
	default:
		in.Words = "A queue rule without bypass drops what it queues whenever Suricata is not reading — a stopped or restarting Suricata then cuts that traffic, the dashboard's included if it is queued."
	}
	return in
}

var queueNumRe = regexp.MustCompile(`--queue-(?:num|balance)\s+(\S+)`)

func parseIptablesQueues(out, tool string) []NFQueueRule {
	var rules []NFQueueRule
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "-A ") || !strings.Contains(line, "-j NFQUEUE") {
			continue
		}
		fields := strings.Fields(line)
		r := NFQueueRule{Source: tool, Chain: fields[1], Queue: "0", Bypass: strings.Contains(line, "--queue-bypass"), Rule: line}
		if m := queueNumRe.FindStringSubmatch(line); m != nil {
			r.Queue = m[1]
		}
		rules = append(rules, r)
	}
	return rules
}

func parseNFTQueues(out string) []NFQueueRule {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil {
		return nil
	}
	var rules []NFQueueRule
	for _, item := range doc.Nftables {
		raw, ok := item["rule"]
		if !ok {
			continue
		}
		var r struct {
			Family, Table, Chain string
			Expr                 []map[string]json.RawMessage `json:"expr"`
		}
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		for _, e := range r.Expr {
			q, ok := e["queue"]
			if !ok {
				continue
			}
			var queue struct {
				Num   json.RawMessage `json:"num"`
				Flags json.RawMessage `json:"flags"`
			}
			if json.Unmarshal(q, &queue) != nil {
				continue
			}
			rules = append(rules, NFQueueRule{Source: "nftables", Chain: r.Family + " " + r.Table + " " + r.Chain,
				Queue: nftQueueNum(queue.Num), Bypass: strings.Contains(string(queue.Flags), "bypass"),
				Rule: "queue " + nftQueueNum(queue.Num) + " " + strings.Trim(string(queue.Flags), `"[]`)})
		}
	}
	return rules
}

// nftQueueNum reads "num": 0 or "num": {"range": [1, 3]}.
func nftQueueNum(raw json.RawMessage) string {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return strconv.Itoa(n)
	}
	var r struct {
		Range []int `json:"range"`
	}
	if json.Unmarshal(raw, &r) == nil && len(r.Range) == 2 {
		return fmt.Sprintf("%d:%d", r.Range[0], r.Range[1])
	}
	if len(raw) == 0 {
		return "0"
	}
	return string(raw)
}

// Everything below changes the host: the capture interface, the rule set and
// the service's state. Each is a fixed argv, and each is checked by
// Suricata's own parser or service state before it is reported done.

var suricataIfaceRe = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,15}$`)

// SuricataInterfacePlan is a validated interface change.
type SuricataInterfacePlan struct {
	Interface string
	Previous  string
	content   string
	original  []byte
	mode      os.FileMode
}

// PlanSuricataInterface validates an interface change. Nothing is written.
func (s *Service) PlanSuricataInterface(ctx context.Context, iface string) (*SuricataInterfacePlan, error) {
	iface = strings.TrimSpace(iface)
	if !suricataIfaceRe.MatchString(iface) {
		return nil, fmt.Errorf("%q is not an interface name", iface)
	}
	if !hasTool("suricata") {
		return nil, fmt.Errorf("suricata is not installed on this host")
	}
	mode, _ := suricataMode(ctx)
	setup := s.suricataInterfaces(ctx, mode)
	if !setup.Editable {
		return nil, fmt.Errorf("%s", setup.Reason)
	}
	found := false
	for _, c := range setup.Candidates {
		found = found || c == iface
	}
	if !found {
		return nil, fmt.Errorf("%s is not an interface that is up on this host", iface)
	}
	original, err := os.ReadFile(suricataYAML)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(suricataYAML)
	if err != nil {
		return nil, err
	}
	content, previous, err := setAfPacketInterface(string(original), iface)
	if err != nil {
		return nil, err
	}
	if previous == iface {
		return nil, fmt.Errorf("Suricata already captures on %s", iface)
	}
	return &SuricataInterfacePlan{Interface: iface, Previous: previous, content: content, original: original, mode: st.Mode().Perm()}, nil
}

// ApplySuricataInterface writes the change, tests it with `suricata -T`,
// puts the file back on failure, and restarts a running Suricata onto it —
// restoring the previous interface if it does not come back.
func (s *Service) ApplySuricataInterface(ctx context.Context, plan *SuricataInterfacePlan, out LineWriter) error {
	out.Status("Writing %s", suricataYAML)
	if err := writeFileKeepingMode(suricataYAML, []byte(plan.content), plan.mode); err != nil {
		return err
	}
	restore := func(why string) error {
		out.Status("%s — putting the previous file back", why)
		return writeFileKeepingMode(suricataYAML, plan.original, plan.mode)
	}
	out.Status("Testing the configuration with suricata -T")
	if text, err := runLong(ctx, 2*time.Minute, "suricata", "-T", "-c", suricataYAML); err != nil {
		lines(out, text)
		if rerr := restore("Rejected"); rerr != nil {
			return fmt.Errorf("suricata rejected the configuration and restoring it failed: %w", rerr)
		}
		return fmt.Errorf("suricata rejected the configuration; the previous interface %s was kept", plan.Previous)
	}
	active, _ := run(ctx, "systemctl", "is-active", "suricata")
	if strings.TrimSpace(active) != "active" {
		out.Status("Suricata is not running; it captures on %s when it starts", plan.Interface)
		return nil
	}
	out.Status("Restarting Suricata onto %s", plan.Interface)
	if text, err := runLong(ctx, 2*time.Minute, "systemctl", "restart", "suricata"); err != nil || !suricataActive(ctx) {
		lines(out, text)
		if rerr := restore("Suricata did not come back"); rerr != nil {
			return fmt.Errorf("suricata did not restart on %s and restoring the file failed: %w", plan.Interface, rerr)
		}
		if _, err := runLong(ctx, 2*time.Minute, "systemctl", "restart", "suricata"); err != nil || !suricataActive(ctx) {
			return fmt.Errorf("suricata did not restart on %s, and did not come back on %s either", plan.Interface, plan.Previous)
		}
		return fmt.Errorf("suricata did not restart on %s; it is back on %s", plan.Interface, plan.Previous)
	}
	out.Status("Suricata captures on %s", plan.Interface)
	return nil
}

// UpdateSuricataRules fetches the rule set with suricata-update — which tests
// it with Suricata's own parser before installing it — and has a running
// Suricata reload it.
func (s *Service) UpdateSuricataRules(ctx context.Context, out LineWriter) error {
	if !hasTool("suricata-update") {
		return fmt.Errorf("suricata-update is not installed on this host")
	}
	before, _ := countRules()
	out.Status("Fetching rules with suricata-update")
	text, err := runLong(ctx, 10*time.Minute, "suricata-update")
	lines(out, text)
	if err != nil {
		return fmt.Errorf("suricata-update failed; the rules in force are unchanged")
	}
	after, _ := countRules()
	out.Status("%d rules enabled (%+d)", after, after-before)
	if suricataActive(ctx) {
		out.Status("Reloading the rules into the running Suricata")
		if text, err := runLong(ctx, 2*time.Minute, "systemctl", "reload", "suricata"); err != nil {
			lines(out, text)
			return fmt.Errorf("the rules were updated but the running Suricata did not reload them")
		}
	}
	return nil
}

// StartSuricata enables and starts the service, and says whether it stayed up.
func (s *Service) StartSuricata(ctx context.Context, out LineWriter) error {
	if !hasTool("suricata") {
		return fmt.Errorf("suricata is not installed on this host")
	}
	out.Status("Enabling and starting suricata")
	if text, err := runLong(ctx, 2*time.Minute, "systemctl", "enable", "--now", "suricata"); err != nil {
		lines(out, text)
		return fmt.Errorf("suricata did not start")
	}
	if !suricataActive(ctx) {
		return fmt.Errorf("suricata started and then stopped; its own log says why")
	}
	out.Status("Suricata is running")
	return nil
}

func suricataActive(ctx context.Context) bool {
	out, _ := run(ctx, "systemctl", "is-active", "suricata")
	return strings.TrimSpace(out) == "active"
}

func lines(out LineWriter, text string) {
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if l != "" {
			out.Line("stdout", l)
		}
	}
}

// runLong runs a host command that may take minutes — a rule download, a
// service restart — with its own deadline. A variable so tests stand a
// transcript behind it.
var runLong = func(ctx context.Context, limit time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := hostexec.CommandOnHost(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// writeFileKeepingMode replaces a file through a temporary file beside it.
func writeFileKeepingMode(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jd-suricata-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
