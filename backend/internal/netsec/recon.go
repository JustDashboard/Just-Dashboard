package netsec

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The recon half of the tools page: the questions that come up while pointing
// a browser, a firewall rule or a certificate at a host, and that otherwise
// send the operator to a shell.
//
// They obey the same rules as the diagnostics next to them — read-only, a
// single validated target, argv never a shell, and behind system.admin because
// they make the server send traffic to an address the caller chose. Three of
// the four need no subprocess for the reason Lookup does not shell to dig: the
// commonest tool on the page must not be the one that is not installed.

// maxProbeOutput bounds a tool whose output is not self-limiting. ping and
// traceroute stop themselves; whois does not, and some registry and netblock
// answers run to megabytes straight into a JSON body. 256 KB is dockerx's own
// per-line cap, for the same reason.
const maxProbeOutput = 256 * 1024

// httpHeaders is the curated set the HTTP check reports: what the server is,
// what it serves, and whether it set the headers that keep a browser honest.
// A full dump is noise; these are the lines an operator actually reads.
var httpHeaders = []string{
	"Server",
	"Content-Type",
	"Strict-Transport-Security",
	"Content-Security-Policy",
	"X-Frame-Options",
	"X-Content-Type-Options",
	"Referrer-Policy",
	"X-Powered-By",
}

func isRedirect(code int) bool {
	return code == http.StatusMovedPermanently || code == http.StatusFound ||
		code == http.StatusSeeOther || code == http.StatusTemporaryRedirect ||
		code == http.StatusPermanentRedirect
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	}
	return fmt.Sprintf("0x%04x", v)
}

func nameOrString(cn, full string) string {
	if cn != "" {
		return cn
	}
	return full
}

// PortScan opens a TCP connection to each of the catalogue's well-known
// service ports and reports which answer. It is the port check's neighbour:
// one asks about a port you name, this asks about the ones a single-server
// operator tends to run — and names each from the same catalogue the firewall
// form teaches from, so an open database reads with its warning attached.
//
// Only TCP ports are scanned: a connect scan cannot speak to a UDP service, so
// including DNS or WireGuard would report a working port as closed. The set is
// deliberately the catalogue and nothing wider — a full range scan is a
// different tool with a different blast radius, and the operator who wants one
// has a shell.
func (s *Service) PortScan(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	res := &ProbeResult{Tool: "scan", Target: target, Records: []string{}}
	ports := scanPorts()
	began := time.Now()
	addr, others, err := scanAddress(ctx, target)
	if err != nil {
		res.Duration = sinceMs(began)
		res.Error = err.Error()
		res.Output = describeDialError(err)
		res.Verdict, res.Summary = ProbeFailed, "The target did not resolve, so no port was tried."
		return res, nil
	}
	outcomes := scanPortsAt(ctx, addr, ports)
	res.Duration = sinceMs(began)
	interpretScan(res, addr, others, ports, outcomes)

	var b strings.Builder
	count := 0
	for i, sp := range ports {
		if outcomes[i].State != "open" {
			continue
		}
		count++
		line := fmt.Sprintf("%-6s open   %s", sp.Port, sp.Name)
		if sp.Danger != "" {
			line += "  ⚠ " + sp.Danger
		}
		b.WriteString(line + "\n")
	}
	if count == 0 {
		fmt.Fprintf(&b, "None of the %d common service ports answered on %s.", len(ports), addr)
	} else {
		fmt.Fprintf(&b, "\n%d of %d common ports open on %s.", count, len(ports), addr)
	}
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// scanPorts is the set PortScan probes: the catalogue's TCP ports, deduped.
// UDP entries are excluded because a connect scan cannot reach a UDP service,
// so scanning one would report a working port as closed — the dedupe then
// keeps 443 once rather than as both its TCP and HTTP/3 rows.
func scanPorts() []ServicePreset {
	seen := map[string]bool{}
	var ports []ServicePreset
	for _, sp := range ServiceCatalogue {
		if sp.Protocol != "tcp" || seen[sp.Port] {
			continue
		}
		seen[sp.Port] = true
		ports = append(ports, sp)
	}
	return ports
}
