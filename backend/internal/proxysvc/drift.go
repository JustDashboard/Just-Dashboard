package proxysvc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DriftState is how the certificate nginx hands out for a server block
// compares with the file that block names.
type DriftState string

const (
	// DriftOK is the file's certificate, served.
	DriftOK DriftState = "ok"
	// DriftStale is another certificate for the same name: the file was
	// replaced, typically renewed, and nginx has not reloaded since.
	DriftStale DriftState = "stale"
	// DriftMismatch is a certificate that is not this block's at all: the
	// name fell through to another server block, usually the default one.
	DriftMismatch DriftState = "mismatch"
	// DriftUnreachable is a handshake that did not complete.
	DriftUnreachable DriftState = "unreachable"
	// DriftSkipped is a block the check could not ask, with the reason.
	DriftSkipped DriftState = "skipped"
)

// DriftSite is one TLS server block of an enabled nginx site, what
// the check asked it and what it found.
type DriftSite struct {
	Site string `json:"site"`
	Path string `json:"path"`
	Line int    `json:"line"`
	// ServerName is the name sent as SNI, and Address where it was sent.
	ServerName string     `json:"serverName,omitempty"`
	Address    string     `json:"address,omitempty"`
	CertPath   string     `json:"certPath,omitempty"`
	State      DriftState `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	// Served is the leaf the handshake returned and Disk the file's
	// certificate; both are summaries, never key material.
	Served *Certificate `json:"served,omitempty"`
	Disk   *Certificate `json:"disk,omitempty"`
	// ServedBy names the site whose own file holds the certificate that was
	// served, when it is another site's: where the name fell through to.
	ServedBy string `json:"servedBy,omitempty"`
}

// DriftReport is every checked block and when the check ran.
type DriftReport struct {
	CheckedAt time.Time   `json:"checkedAt"`
	Sites     []DriftSite `json:"sites"`
}

// handshakeFunc returns the leaf a TLS server presents to serverName at addr.
type handshakeFunc func(ctx context.Context, addr, serverName string) (*x509.Certificate, error)

const (
	driftDialTimeout = 4 * time.Second
	driftParallel    = 8
	// driftTTL keeps the check from handshaking with every site on every
	// poll: a certificate renewed and not reloaded stays that way for days,
	// so five minutes late is not late.
	driftTTL = 5 * time.Minute
	// driftBudget bounds one whole check, detached from the request that
	// started it so a closed tab does not cache a list of timeouts.
	driftBudget = 30 * time.Second
)

// driftHandshake dials over TCP and reads the leaf without trusting it: an expired
// or self-signed certificate is exactly what the check is there to report.
func driftHandshake(ctx context.Context, addr, serverName string) (*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, driftDialTimeout)
	defer cancel()
	dialer := &tls.Dialer{Config: &tls.Config{ServerName: serverName, InsecureSkipVerify: true}}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	peers := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return nil, fmt.Errorf("no certificate was presented")
	}
	return peers[0], nil
}

// DriftCheck caches the served-certificate check. A result is kept for
// driftTTL, and dropped earlier when a configuration change or reload has
// happened since it was taken, so the check after a reload is a fresh one.
type DriftCheck struct {
	svc *Service
	mu  sync.Mutex
	// report is the last result and gen the change generation it was
	// taken at.
	report *DriftReport
	gen    uint64
}

func NewDriftCheck(svc *Service) *DriftCheck { return &DriftCheck{svc: svc} }

// Report answers from the cache unless refresh is set or the cache is old.
// Callers queue on the lock, so concurrent polls share one check.
func (d *DriftCheck) Report(ctx context.Context, refresh bool) DriftReport {
	d.mu.Lock()
	defer d.mu.Unlock()
	gen := d.svc.effectiveGen()
	if !refresh && d.report != nil && d.gen == gen && time.Since(d.report.CheckedAt) < driftTTL {
		return *d.report
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), driftBudget)
	defer cancel()
	report := checkServedCertificates(ctx, d.svc.nginxDir, d.svc.nginxVHosts(), driftLocalAddresses(), driftHandshake)
	d.report, d.gen = &report, gen
	return report
}

func (s *Service) effectiveGen() uint64 {
	s.effective.mu.Lock()
	defer s.effective.mu.Unlock()
	return s.effective.gen
}

// driftLocalAddresses are the host's own interface addresses. The backend shares
// the host's network, so a listen bound to one of them is still a dial that
// never leaves the machine.
func driftLocalAddresses() map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if prefix, err := netip.ParsePrefix(a.String()); err == nil {
			out[prefix.Addr().Unmap()] = true
		}
	}
	return out
}

// driftTarget is one server block to ask.
type driftTarget struct {
	entry     DriftSite
	certPaths []string
}

// checkServedCertificates handshakes with every TLS server block of the
// enabled nginx sites, on the address it listens on, and compares the leaf
// with the file the block names.
func checkServedCertificates(ctx context.Context, nginxDir string, vhosts []VHost, local map[netip.Addr]bool, dial handshakeFunc) DriftReport {
	var targets []driftTarget
	for _, v := range vhosts {
		if v.Kind != KindNginx || !v.Enabled || !v.TLS {
			continue
		}
		targets = append(targets, siteTargets(v, nginxDir, local)...)
	}

	// Fingerprint to site, for every file the blocks name, so a mismatch can
	// say which block answered instead.
	disk := make([][]*Certificate, len(targets))
	owner := map[string]string{}
	for i, t := range targets {
		for _, p := range t.certPaths {
			c, err := readCertificate(p)
			if err != nil {
				if len(disk[i]) == 0 && targets[i].entry.Reason == "" {
					targets[i].entry.Reason = fmt.Sprintf("%s could not be read: %v", p, err)
				}
				continue
			}
			disk[i] = append(disk[i], c)
			if _, ok := owner[c.Fingerprint]; !ok {
				owner[c.Fingerprint] = t.entry.Site
			}
		}
	}

	out := make([]DriftSite, len(targets))
	sem := make(chan struct{}, driftParallel)
	var wg sync.WaitGroup
	for i := range targets {
		entry := targets[i].entry
		if entry.State == DriftSkipped {
			out[i] = entry
			continue
		}
		if len(disk[i]) == 0 {
			entry.State = DriftSkipped
			out[i] = entry
			continue
		}
		entry.Reason = ""
		entry.Disk = disk[i][0]
		wg.Add(1)
		go func(i int, entry DriftSite) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = compareServed(ctx, entry, disk[i], owner, dial)
		}(i, entry)
	}
	wg.Wait()
	return DriftReport{CheckedAt: time.Now().UTC(), Sites: out}
}

func compareServed(ctx context.Context, entry DriftSite, disk []*Certificate, owner map[string]string, dial handshakeFunc) DriftSite {
	leaf, err := dial(ctx, entry.Address, entry.ServerName)
	if err != nil {
		entry.State, entry.Reason = DriftUnreachable, err.Error()
		return entry
	}
	served := summarise(leaf, entry.ServerName, "")
	served.Source = "live"
	entry.Served = served
	for _, c := range disk {
		// A block may name an RSA and an ECDSA certificate; either one
		// being served is the file being served.
		if c.Fingerprint == served.Fingerprint {
			entry.State, entry.Disk = DriftOK, c
			return entry
		}
	}
	if site, ok := owner[served.Fingerprint]; ok && site != entry.Site {
		entry.State, entry.ServedBy = DriftMismatch, site
		entry.Reason = fmt.Sprintf("%s answered with the certificate %s names", entry.ServerName, site)
		return entry
	}
	if leaf.VerifyHostname(entry.ServerName) == nil {
		entry.State = DriftStale
		entry.Reason = "nginx is serving a certificate for this name other than the one in the file, which it reads only when it reloads"
		return entry
	}
	entry.State = DriftMismatch
	entry.Reason = fmt.Sprintf("the certificate served for %s does not cover that name", entry.ServerName)
	return entry
}

// siteTargets reads a site file into one target per TLS server block. A
// block the check cannot ask comes back skipped, saying why.
func siteTargets(v VHost, nginxDir string, local map[netip.Addr]bool) []driftTarget {
	skipped := func(line int, reason string) driftTarget {
		return driftTarget{entry: DriftSite{Site: v.Name, Path: v.Path, Line: line, State: DriftSkipped, Reason: reason}}
	}
	raw, err := os.ReadFile(v.Path)
	if err != nil {
		return []driftTarget{skipped(0, err.Error())}
	}
	directives, err := ParseNginxFile(v.Path, string(raw), []string{"http"})
	if err != nil {
		return []driftTarget{skipped(0, err.Error())}
	}
	var out []driftTarget
	for _, server := range directives {
		if server.Name != "server" {
			continue
		}
		var names, certs []string
		var listen []string
		for _, d := range server.Block {
			switch d.Name {
			case "server_name":
				names = append(names, d.Args...)
			case "ssl_certificate":
				if len(d.Args) > 0 {
					certs = append(certs, d.Args[0])
				}
			case "listen":
				if listen == nil && hasArg(d.Args, "ssl") {
					listen = d.Args
				}
			}
		}
		if listen == nil {
			if len(certs) > 0 {
				out = append(out, skipped(server.Line, "no listen in this server block says ssl"))
			}
			continue
		}
		if len(certs) == 0 {
			out = append(out, skipped(server.Line, "this server block names no certificate of its own"))
			continue
		}
		t := driftTarget{entry: DriftSite{Site: v.Name, Path: v.Path, Line: server.Line, CertPath: certs[0]}}
		fail := func(reason string) {
			t.entry.State, t.entry.Reason = DriftSkipped, reason
			out = append(out, t)
		}
		addr, why := loopbackAddress(listen[0], local)
		if why != "" {
			fail(why)
			continue
		}
		t.entry.Address = addr
		name := exactName(names)
		if name == "" {
			fail("no exact server_name to send as SNI")
			continue
		}
		t.entry.ServerName = name
		for _, p := range certs {
			if strings.Contains(p, "$") {
				t.certPaths = nil
				break
			}
			if !filepath.IsAbs(p) {
				// nginx resolves a relative path against its prefix, the
				// directory of its main configuration.
				p = filepath.Join(nginxDir, p)
			}
			t.certPaths = append(t.certPaths, p)
		}
		if t.certPaths == nil {
			fail("the certificate is chosen per request by a variable")
			continue
		}
		out = append(out, t)
	}
	return out
}

func hasArg(args []string, want string) bool {
	for _, a := range args[min(1, len(args)):] {
		if a == want {
			return true
		}
	}
	return false
}

// loopbackAddress is where to dial a listen's address from this host, or why
// it is not dialled. Only the host's own addresses are ever dialled.
func loopbackAddress(listen string, local map[netip.Addr]bool) (string, string) {
	if strings.HasPrefix(listen, "unix:") || strings.Contains(listen, "$") {
		return "", fmt.Sprintf("listens on %s, which is not a TCP port", listen)
	}
	host, port := "", listen
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		host, port = listen[:i], listen[i+1:]
	} else if _, err := strconv.Atoi(listen); err != nil {
		// A bare address listens on port 80.
		host, port = listen, "80"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Sprintf("cannot read a port from listen %s", listen)
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	switch host {
	case "", "*", "0.0.0.0":
		return net.JoinHostPort("127.0.0.1", port), ""
	case "::":
		return net.JoinHostPort("::1", port), ""
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", fmt.Sprintf("listens on the name %s; only addresses are dialled", host)
	}
	if !ip.Unmap().IsLoopback() && !local[ip.Unmap()] {
		return "", fmt.Sprintf("listens on %s, which is not an address of this host", host)
	}
	return net.JoinHostPort(ip.String(), port), ""
}

// exactName is the first server_name a client could send as SNI: not the
// catch-all "_", not a wildcard and not a regular expression. A leading dot
// also matches the bare domain.
func exactName(names []string) string {
	for _, n := range names {
		n = strings.TrimPrefix(n, ".")
		if n == "" || n == "_" || strings.HasPrefix(n, "~") || strings.ContainsAny(n, "*$") {
			continue
		}
		return strings.ToLower(n)
	}
	return ""
}
