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
	out, elapsed, err := runProbe(ctx, 20*time.Second, "ping", "-n", "-c", "4", "-W", "2", "-w", "12", target)
	res.Output, res.Duration = out, elapsed
	res.OK = err == nil
	if err != nil {
		res.Error = err.Error()
	}
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
	switch {
	case hostexec.AvailableOnHost("traceroute"):
		args := []string{"-n", "-w", "2", "-q", "1", "-m", "20"}
		// traceroute defaults to IPv4 even when given an IPv6 literal.
		if ip := net.ParseIP(target); ip != nil && ip.To4() == nil {
			args = append(args, "-6")
		}
		out, elapsed, err = runProbe(ctx, 60*time.Second, "traceroute", append(args, target)...)
	case hostexec.AvailableOnHost("tracepath"):
		out, elapsed, err = runProbe(ctx, 60*time.Second, "tracepath", "-n", "-m", "20", target)
	default:
		res.Error = "neither traceroute nor tracepath is installed on this host"
		return res, nil
	}
	res.Output, res.Duration, res.OK = out, elapsed, err == nil
	if err != nil {
		res.Error = err.Error()
	}
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

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	res := &ProbeResult{Tool: "dns", Target: target, Records: []string{}}
	var err error
	switch recordType {
	case "A", "AAAA":
		var addrs []net.IP
		addrs, err = net.DefaultResolver.LookupIP(ctx, familyFor(recordType), target)
		for _, a := range addrs {
			res.Records = append(res.Records, a.String())
		}
	case "MX":
		var mx []*net.MX
		mx, err = net.DefaultResolver.LookupMX(ctx, target)
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
		res.Records, err = net.DefaultResolver.LookupTXT(ctx, target)
	case "NS":
		var ns []*net.NS
		ns, err = net.DefaultResolver.LookupNS(ctx, target)
		for _, n := range ns {
			res.Records = append(res.Records, strings.TrimSuffix(n.Host, "."))
		}
	case "CNAME":
		var cname string
		cname, err = net.DefaultResolver.LookupCNAME(ctx, target)
		if cname != "" {
			res.Records = append(res.Records, strings.TrimSuffix(cname, "."))
		}
	case "PTR":
		var names []string
		names, err = net.DefaultResolver.LookupAddr(ctx, target)
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
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "port", Target: net.JoinHostPort(target, strconv.Itoa(port))}
	start := time.Now()
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(port)))
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		res.Error = err.Error()
		// Refused and timed out mean different things — one is a host saying
		// no, the other is a firewall saying nothing — and the distinction is
		// the reason to run the check at all.
		res.Output = describeDialError(err)
		return res, nil
	}
	conn.Close()
	res.OK = true
	res.Output = "Connected in " + res.Duration + "."
	if preset, ok := PresetFor(strconv.Itoa(port), "tcp"); ok {
		res.Output += " Port " + strconv.Itoa(port) + " is normally " + preset.Name + "."
	}
	return res, nil
}

func describeDialError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "refused"):
		return "Connection refused: something answered and said no. The host is reachable and nothing is listening on that port."
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
	err = cmd.Run()
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
