package netsec

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The tools an operator opens a terminal for, on the page where the question
// arose.
//
// "Is this domain pointing at me yet", "can this box reach that host", "what
// is between us" — three questions that come up constantly while setting up a
// proxy or debugging a firewall rule, and every panel in this class makes you
// leave and find a shell. They are read-only, they take a single argument, and
// the argument is validated to be a hostname or an address before anything is
// run with it.

// ProbeResult is one diagnostic run.
type ProbeResult struct {
	Tool   string `json:"tool"`
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	// Output is the tool's own text, kept verbatim: an operator who knows
	// what traceroute output looks like should see traceroute output.
	Output string `json:"output"`
	// Records is the structured answer where there is one — the addresses a
	// name resolves to, so the UI can act on them rather than only show them.
	Records []string `json:"records,omitempty"`
	// Duration is how long it took, which for a port check is most of the
	// answer: refused is instant, filtered hangs until the timeout.
	Duration string `json:"duration"`
	Error    string `json:"error,omitempty"`
	// Verdict refines OK where a bare success/failure would mislead: an
	// unanswered ping is "unknown", not a service that is down. Empty keeps
	// the older reading of OK.
	Verdict string `json:"verdict,omitempty"`
	// Summary is the one sentence the verdict is read with.
	Summary     string         `json:"summary,omitempty"`
	Facts       []ProbeFact    `json:"facts,omitempty"`
	Stages      []ProbeStage   `json:"stages,omitempty"`
	Tables      []ProbeTable   `json:"tables,omitempty"`
	Findings    []ProbeFinding `json:"findings,omitempty"`
	Links       []ProbeLink    `json:"links,omitempty"`
	Metrics     []ProbeMetric  `json:"metrics,omitempty"`
	Limitations []string       `json:"limitations,omitempty"`
	// ResultID names a quick result the server holds briefly so it can be
	// saved without sending the probe again. It is never part of a saved run.
	ResultID string `json:"resultId,omitempty"`
}

// hostRe accepts a hostname or an IPv4 literal. Deliberately strict: this
// value becomes an argv element of a command that runs on the host, and while
// argv is never a shell string, a tool that accepts option-looking arguments
// would still be steerable by a target beginning with a dash.
var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)

// ValidTarget reports whether a probe target is a plain hostname or IP.
func ValidTarget(target string) bool {
	target = strings.TrimSpace(target)
	// An absolute DNS name ends in the root dot; keeping it prevents search
	// domains from changing the operator's diagnostic target.
	target = strings.TrimSuffix(target, ".")
	if target == "" || len(target) > 253 {
		return false
	}
	if net.ParseIP(target) != nil {
		return true
	}
	return hostRe.MatchString(target)
}

// dnsTypes is the closed set of record types offered.
var dnsTypes = map[string]bool{"A": true, "AAAA": true, "MX": true, "TXT": true, "NS": true, "CNAME": true, "PTR": true}

// Ping measures reachability from this host.
func (s *Service) Ping(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	res := &ProbeResult{Tool: "ping", Target: target}
	// -n keeps ping from doing a reverse lookup per hop, which on a host with
	// a slow resolver is most of the elapsed time and none of the answer.
	// -w bounds the whole run so a black hole cannot hold the request open.
	out, elapsed, err := diagnosticRun(ctx, 20*time.Second, "ping", "-n", "-c", "4", "-W", "2", "-w", "12", target)
	res.Output, res.Duration = out, elapsed
	if err != nil {
		res.Error = err.Error()
	}
	interpretPing(res, parsePing(out), target, err)
	return res, nil
}

// Traceroute shows the path. tracepath is the fallback because it needs no
// special privilege and is present on hosts that never installed traceroute.
func (s *Service) Traceroute(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	res := &ProbeResult{Tool: "traceroute", Target: target}
	var out, elapsed string
	var err error
	var report traceReport
	tool := "traceroute"
	switch {
	case diagnosticHas("traceroute"):
		args := []string{"-n", "-w", "2", "-q", "1", "-m", "20"}
		// traceroute defaults to IPv4 even when given an IPv6 literal.
		if ip := net.ParseIP(target); ip != nil && ip.To4() == nil {
			args = append(args, "-6")
		}
		out, elapsed, err = diagnosticRun(ctx, 60*time.Second, "traceroute", append(args, target)...)
		report = parseTraceroute(out)
	case diagnosticHas("tracepath"):
		tool = "tracepath"
		out, elapsed, err = diagnosticRun(ctx, 60*time.Second, "tracepath", "-n", "-m", "20", target)
		report = parseTracepath(out)
	default:
		res.Error = "neither traceroute nor tracepath is installed on this host"
		res.Verdict = ProbeFailed
		return res, nil
	}
	res.Output, res.Duration = out, elapsed
	if err != nil {
		res.Error = err.Error()
	}
	interpretTrace(res, report, tool, target, err)
	return res, nil
}

// Lookup resolves a name using this host's own resolver, which is the point:
// what the dashboard's machine sees is what the services on it will see.
//
// No subprocess. dig is not installed everywhere and its absence would make
// the commonest tool on the page the one that does not work.
func (s *Service) Lookup(ctx context.Context, target, recordType string) (*ProbeResult, error) {
	recordType = strings.ToUpper(strings.TrimSpace(recordType))
	if recordType == "" {
		recordType = "A"
	}
	if !dnsTypes[recordType] {
		return nil, fmt.Errorf("record type must be one of A, AAAA, MX, TXT, NS, CNAME or PTR")
	}
	if recordType == "PTR" {
		if net.ParseIP(target) == nil {
			return nil, fmt.Errorf("a PTR lookup takes an IP address")
		}
	} else if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	res := &ProbeResult{Tool: "dns", Target: target, Records: []string{}}
	var err error
	switch recordType {
	case "A", "AAAA":
		var addrs []net.IP
		addrs, err = lookupResolver.LookupIP(ctx, familyFor(recordType), target)
		for _, a := range addrs {
			res.Records = append(res.Records, a.String())
		}
	case "MX":
		var mx []*net.MX
		mx, err = lookupResolver.LookupMX(ctx, target)
		for _, m := range mx {
			host := strings.TrimSuffix(m.Host, ".")
			if host == "" {
				// A null MX — RFC 7505's "0 ." — is a real answer meaning the
				// domain accepts no mail at all. Rendered by trimming the dot
				// it becomes a preference and a blank, which reads as a broken
				// lookup rather than as a deliberate configuration.
				res.Records = append(res.Records, "0 . (null MX — this domain accepts no mail)")
				continue
			}
			res.Records = append(res.Records, fmt.Sprintf("%d %s", m.Pref, host))
		}
	case "TXT":
		res.Records, err = lookupResolver.LookupTXT(ctx, target)
	case "NS":
		var ns []*net.NS
		ns, err = lookupResolver.LookupNS(ctx, target)
		for _, n := range ns {
			res.Records = append(res.Records, strings.TrimSuffix(n.Host, "."))
		}
	case "CNAME":
		var cname string
		cname, err = lookupResolver.LookupCNAME(ctx, target)
		if cname != "" {
			res.Records = append(res.Records, strings.TrimSuffix(cname, "."))
		}
	case "PTR":
		var names []string
		names, err = lookupResolver.LookupAddr(ctx, target)
		for _, n := range names {
			res.Records = append(res.Records, strings.TrimSuffix(n, "."))
		}
	}
	sort.Strings(res.Records)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	res.OK = err == nil && len(res.Records) > 0
	if err != nil {
		res.Error = err.Error()
	} else if len(res.Records) == 0 {
		res.Error = "no " + recordType + " records"
	}
	res.Output = strings.Join(res.Records, "\n")
	addLookupProvenance(ctx, res, target, recordType)
	switch {
	case res.OK:
		res.Verdict = ProbeOK
		res.Summary = fmt.Sprintf("%d %s record(s) from this host's resolver.", len(res.Records), recordType)
	case err != nil && isDNSNotFound(err):
		res.Verdict = ProbeFailed
		res.Summary = "The name does not exist for this host's resolver (NXDOMAIN)."
	case err == nil:
		res.Verdict = ProbeFailed
		res.Summary = "The name exists but has no " + recordType + " records."
	default:
		res.Verdict = ProbeFailed
		res.Summary = "The resolver failed: " + err.Error()
	}
	if len(res.Findings) > 0 && res.OK {
		res.Verdict = ProbeFindings
	}
	return res, nil
}

func familyFor(recordType string) string {
	if recordType == "AAAA" {
		return "ip6"
	}
	return "ip4"
}

// PortCheck opens a TCP connection from this host.
//
// The direction is worth being precise about, and the UI says so: this proves
// the *server* can reach a destination. It is not a check of whether the
// internet can reach the server, which cannot be answered from inside it.
func (s *Service) PortCheck(ctx context.Context, target string, port int) (*ProbeResult, error) {
	return s.PortCheckFromSource(ctx, target, port, "")
}

func (s *Service) PortCheckFromSource(ctx context.Context, target string, port int, source string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "port", Target: net.JoinHostPort(target, strconv.Itoa(port))}
	start := time.Now()
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	if source != "" {
		ip := net.ParseIP(source)
		if ip == nil || net.ParseIP(target) == nil || (ip.To4() == nil) != (net.ParseIP(target).To4() == nil) {
			return nil, fmt.Errorf("the probe source and literal target must have the same family")
		}
		dialer.LocalAddr = &net.TCPAddr{IP: ip}
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(port)))
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	res.Limitations = append(res.Limitations, "A TCP handshake from this host shows neither what the internet can reach nor that the service behind the port works.")
	if err != nil {
		res.Error = err.Error()
		// Refusal and silence provide different evidence, but neither
		// identifies which device or policy caused the failure.
		res.Output = describeDialError(err)
		state, sentence := dialFailure(err)
		if addr := dialedAddress(err); addr != "" {
			res.fact("Attempted address", addr, BasisObserved)
		}
		res.fact("Failure", state, BasisInferred)
		res.Verdict, res.Summary = ProbeFailed, sentence
		return res, nil
	}
	res.fact("Connected address", conn.RemoteAddr().String(), BasisObserved)
	res.fact("Local source", conn.LocalAddr().String(), BasisObserved)
	conn.Close()
	res.OK = true
	res.Output = "Connected in " + res.Duration + "."
	if preset, ok := PresetFor(strconv.Itoa(port), "tcp"); ok {
		res.Output += " Port " + strconv.Itoa(port) + " is normally " + preset.Name + "."
	}
	res.Verdict, res.Summary = ProbeOK, "TCP connected from this host in "+res.Duration+"."
	return res, nil
}

func describeDialError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "refused"):
		return "Connection refused: the connection was actively rejected. A closed port or a firewall rejection can cause this."
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "Timed out with no reply. A firewall dropping packets, an offline host or a broken route can all cause this."
	case strings.Contains(msg, "no such host"):
		return "The name did not resolve."
	}
	return msg
}

func runProbe(ctx context.Context, limit time.Duration, name string, args ...string) (out string, elapsed string, err error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	start := time.Now()
	cmd := hostexec.CommandOnHost(ctx, name, args...)
	var raw probeOutput
	cmd.Stdout, cmd.Stderr = &raw, &raw
	// nsenter may fork a child holding the output pipes. Stop the whole
	// process group so a timed-out capture cannot survive its request.
	_, err = hostexec.RunGroup(ctx, cmd, 200*time.Millisecond)
	return strings.TrimSpace(raw.String()), time.Since(start).Round(time.Millisecond).String(), err
}

// A remote registry or a busy capture must not allocate its entire output
// before the response is truncated. Continue draining once the cap is met.
type probeOutput struct {
	// A named field prevents io.Copy using bytes.Buffer.ReadFrom and
	// bypassing the cap enforced by Write.
	buffer    bytes.Buffer
	truncated bool
}

func (b *probeOutput) Len() int { return b.buffer.Len() }

func (b *probeOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxProbeOutput - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *probeOutput) String() string {
	if b.truncated {
		return b.buffer.String() + "\n… (truncated)"
	}
	return b.buffer.String()
}
