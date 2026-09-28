package proxysvc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// The deep scan: what the quick report leaves out because it takes forty
// connections rather than six.
//
// Every cipher suite each version accepts and how it rates, whose order picks
// one, the key exchange groups (post-quantum ones included) and the group a
// browser's offer settles on, HTTP/2 against the site form's switch, HTTP/3
// as advertised and as answered, session resumption, and the certificate a
// client gets when it names no site or one this server does not know. It
// runs on its own request, after the quick scan, so the report is on screen
// while it works.
//
// Every probe goes to the one address the first handshake reached: a name
// with several addresses would otherwise have its suites listed from one
// server and its groups from another.

// DeepScan is the deep scan's report.
type DeepScan struct {
	Domain    string    `json:"domain"`
	Port      int       `json:"port"`
	CheckedAt time.Time `json:"checkedAt"`
	Reachable bool      `json:"reachable"`
	Error     string    `json:"error,omitempty"`
	// Address is where every probe went, and Where whose address it is:
	// "here", "cloudflare", "elsewhere" or "unknown", as a failed scan says.
	Address string `json:"address,omitempty"`
	Where   string `json:"where,omitempty"`
	// Connections is how many the deep scan opened, which is what it cost
	// the server.
	Connections int `json:"connections"`

	Versions []VersionSuites `json:"versions"`
	// Groups are TLS 1.3's, each asked for alone. BrowserGroup is the group
	// a current browser's offer gets, in BrowserGroupVersion: TLS 1.3's, or
	// TLS 1.2's curve when there is no TLS 1.3. DHBits is the size of a DHE
	// suite's group.
	Groups              []GroupResult `json:"groups"`
	BrowserGroup        string        `json:"browserGroup,omitempty"`
	BrowserGroupVersion string        `json:"browserGroupVersion,omitempty"`
	DHBits              int           `json:"dhBits,omitempty"`

	ALPN       *ALPNResult        `json:"alpn,omitempty"`
	HTTP3      *HTTP3Result       `json:"http3,omitempty"`
	Resumption []ResumptionResult `json:"resumption"`
	SNI        []SNIProbe         `json:"sni"`
	Findings   []ScanFinding      `json:"findings"`
}

// DeepSite is the site on this server that serves the scanned name, with the
// site form's HTTP/2 switch as the form reads the file.
type DeepSite struct {
	Name  string `json:"name"`
	HTTP2 bool   `json:"http2"`
}

// ALPNResult is the application protocol a browser's offer got.
type ALPNResult struct {
	Offered []string `json:"offered"`
	// Negotiated is empty when the server chose none.
	Negotiated string `json:"negotiated"`
	// Site is the site on this server serving the name, when the
	// connection reached this server.
	Site *DeepSite `json:"site,omitempty"`
}

// HTTP3Result is HTTP/3 as the site advertises it in Alt-Svc, and whether
// QUIC answers where it points.
type HTTP3Result struct {
	// Answered is whether the HTTPS request got an HTTP answer; Error why
	// not.
	Answered bool   `json:"answered"`
	Error    string `json:"error,omitempty"`
	AltSvc   string `json:"altSvc,omitempty"`
	// Advertised is an h3 alternative in Alt-Svc; Host is set when it names
	// another host, which is not probed.
	Advertised bool       `json:"advertised"`
	Host       string     `json:"host,omitempty"`
	Port       int        `json:"port,omitempty"`
	QUIC       *QUICProbe `json:"quic,omitempty"`
}

// ResumptionResult is a second handshake offering what the first left
// behind.
type ResumptionResult struct {
	Version string `json:"version"`
	// Status is "resumed", "not-resumed", "no-ticket" or "unknown".
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// SNIProbe is a handshake that names no site ("none") or one no server has
// ("unknown").
type SNIProbe struct {
	Kind string `json:"kind"`
	// Sent is the name sent, empty for none.
	Sent string `json:"sent,omitempty"`
	// Status is "refused", "certificate" or "unknown".
	Status      string   `json:"status"`
	Detail      string   `json:"detail,omitempty"`
	Subject     string   `json:"subject,omitempty"`
	Issuer      string   `json:"issuer,omitempty"`
	Names       []string `json:"names,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	// SameAsNamed says it is the certificate the scanned name gets.
	SameAsNamed bool `json:"sameAsNamed"`
}

// deepBudget bounds the whole deep scan. Its probes run eight at a time and
// each gives up after five seconds, so a server that answers takes a few
// seconds and one that goes quiet half-way cannot hold the request.
const deepBudget = 45 * time.Second

// DeepScanTLS runs the deep scan of domain:port. site is the site on this
// server that serves the name, if any; it is compared only when the scan's
// connection reached this server.
func DeepScanTLS(ctx context.Context, domain string, port int, site *DeepSite) *DeepScan {
	ctx, cancel := context.WithTimeout(ctx, deepBudget)
	defer cancel()
	d := &DeepScan{
		Domain: domain, Port: port, CheckedAt: time.Now().UTC(),
		Versions: []VersionSuites{}, Groups: []GroupResult{},
		Resumption: []ResumptionResult{}, SNI: []SNIProbe{}, Findings: []ScanFinding{},
	}
	var dials atomic.Int32
	serverName := ""
	if net.ParseIP(domain) == nil {
		serverName = domain
	}

	// The first handshake is a browser's: it finds the address, the
	// protocol and the certificate every other probe is compared with, and
	// leaves the session the resumption probe offers back.
	cache := &ticketCache{ClientSessionCache: tls.NewLRUClientSessionCache(4)}
	named, address, err := dialDeep(ctx, net.JoinHostPort(domain, strconv.Itoa(port)), serverName, cache, 0, &dials)
	if address == "" {
		d.Error = err.Error()
		d.Connections = int(dials.Load())
		return d
	}
	d.Reachable = true
	d.Address = address
	d.Where = whereConnected(address, nil, localAddresses(), hostAddresses())
	var namedState *tls.ConnectionState
	if named != nil {
		state := named.ConnectionState()
		namedState = &state
		collectTickets(named, state, domain, port)
		named.Close()
		d.ALPN = &ALPNResult{Offered: browserProtocols, Negotiated: state.NegotiatedProtocol}
		if site != nil && d.Where == "here" {
			d.ALPN.Site = site
		}
	}

	prober, err := newDeepProber(address, serverName)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	var wg sync.WaitGroup
	versions := make([]VersionSuites, len(deepVersions))
	for i, v := range deepVersions {
		wg.Go(func() { versions[i] = prober.listSuites(ctx, v.name, v.version) })
	}
	var resumption []ResumptionResult
	var sni []SNIProbe
	var http3 *HTTP3Result
	wg.Go(func() { resumption = resumeProbes(ctx, address, serverName, cache, namedState, &dials) })
	wg.Go(func() { sni = sniProbes(ctx, address, serverName, namedState, &dials) })
	_, registered := implicitTLSServices[port]
	if !registered {
		wg.Go(func() { http3 = altSvcProbe(ctx, address, domain, port, &dials) })
	}
	wg.Wait()
	d.Versions, d.Resumption, d.SNI = versions, resumption, sni

	// What the versions accepted decides which groups are worth asking
	// about, and the Alt-Svc answer where QUIC is asked.
	accepted := func(name string) *VersionSuites {
		for i := range d.Versions {
			if d.Versions[i].Name == name && d.Versions[i].Status == "accepted" {
				return &d.Versions[i]
			}
		}
		return nil
	}
	var groupsErr error
	if accepted("TLS 1.3") != nil {
		wg.Go(func() {
			var groups []GroupResult
			groups, d.BrowserGroup, groupsErr = prober.listGroups(ctx)
			if groups != nil {
				d.Groups = groups
			}
			if d.BrowserGroup != "" {
				d.BrowserGroupVersion = "TLS 1.3"
			}
		})
	} else if v := accepted("TLS 1.2"); v != nil {
		// Without TLS 1.3, the curve a browser's TLS 1.2 offer gets is the
		// key exchange visitors have. Curves are not listed one by one here:
		// in TLS 1.2 the offered curves also bound an ECDSA certificate's, so
		// offering one at a time refuses the certificate, not the curve.
		if ecdhe := suiteIDs(v.Suites, "ECDHE"); len(ecdhe) > 0 {
			wg.Go(func() {
				if d.BrowserGroup = prober.tls12Curve(ctx, ecdhe); d.BrowserGroup != "" {
					d.BrowserGroupVersion = "TLS 1.2"
				}
			})
		}
	}
	for _, v := range deepVersions[1:] {
		if list := accepted(v.name); list != nil {
			if dhe := suiteIDs(list.Suites, "DHE"); len(dhe) > 0 {
				wg.Go(func() { d.DHBits = prober.dhSize(ctx, v.version, dhe) })
				break
			}
		}
	}
	if http3 != nil {
		d.HTTP3 = http3
		if target := quicTarget(http3, port); target != 0 {
			wg.Go(func() {
				host, _, _ := net.SplitHostPort(address)
				dials.Add(1)
				http3.QUIC = probeQUIC(ctx, net.JoinHostPort(host, strconv.Itoa(target)))
			})
		}
	}
	wg.Wait()
	if groupsErr != nil && d.Error == "" {
		d.Error = groupsErr.Error()
	}
	d.Connections = int(dials.Load() + prober.sent.Load())
	d.Findings = deepFindings(d)
	return d
}

// browserProtocols is the ALPN list a browser sends.
var browserProtocols = []string{"h2", "http/1.1"}

func suiteIDs(suites []SuiteResult, kex string) []uint16 {
	ids := []uint16{}
	for _, s := range suites {
		if s.Kex == kex {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// ticketCache is a session cache that says whether the server left anything
// in it: a TLS 1.3 server that sends no ticket cannot be resumed with, and a
// TLS 1.2 one may still resume by session ID, which this client cannot offer.
type ticketCache struct {
	tls.ClientSessionCache
	stored atomic.Bool
}

func (c *ticketCache) Put(key string, cs *tls.ClientSessionState) {
	if cs != nil {
		c.stored.Store(true)
	}
	c.ClientSessionCache.Put(key, cs)
}

// dialDeep makes a browser's handshake with addr: ALPN, the session cache
// and, when maxVersion is set, a cap. A server that refuses that offer is
// asked again with every suite Go has, as the quick scan's handshake does.
// The address is returned whenever a connection opened, even if the
// handshake then failed.
func dialDeep(ctx context.Context, addr, serverName string, cache tls.ClientSessionCache, maxVersion uint16, dials *atomic.Int32) (*tls.Conn, string, error) {
	offer := func(legacy bool) *tls.Config {
		config := &tls.Config{
			ServerName: serverName, InsecureSkipVerify: true, NextProtos: browserProtocols,
			ClientSessionCache: cache, MaxVersion: maxVersion,
		}
		if legacy {
			config.MinVersion, config.CipherSuites = oldestVersion, probeSuites
		}
		return config
	}
	conn, address, err := dialConfig(ctx, addr, offer(false), dials)
	if _, alerted := remoteAlert(err); alerted {
		if legacy, legacyAddress, legacyErr := dialConfig(ctx, addr, offer(true), dials); legacyErr == nil {
			return legacy, legacyAddress, nil
		}
	}
	return conn, address, err
}

func dialConfig(ctx context.Context, addr string, config *tls.Config, dials *atomic.Int32) (*tls.Conn, string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	dials.Add(1)
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, "", err
	}
	conn := tls.Client(raw, config)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, raw.RemoteAddr().String(), err
	}
	return conn, raw.RemoteAddr().String(), nil
}

// collectTickets reads past a TLS 1.3 handshake long enough for the tickets
// that follow it to arrive; this client only takes them in while reading. A
// website is asked for its headers so the read ends with an answer rather
// than a timeout.
func collectTickets(conn *tls.Conn, state tls.ConnectionState, domain string, port int) {
	if state.Version != tls.VersionTLS13 {
		return
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	if state.NegotiatedProtocol == "http/1.1" {
		host := urlHost(domain)
		if port != 443 {
			host += ":" + strconv.Itoa(port)
		}
		fmt.Fprintf(conn, "HEAD / HTTP/1.1\r\nHost: %s\r\nUser-Agent: %s\r\nConnection: close\r\n\r\n", host, scanUserAgent)
	}
	conn.Read(make([]byte, 1))
}

// resumeProbes offers the first handshake's session back, and, when that
// handshake was TLS 1.3, makes a TLS 1.2 pair of its own.
func resumeProbes(ctx context.Context, addr, serverName string, cache *ticketCache, first *tls.ConnectionState, dials *atomic.Int32) []ResumptionResult {
	out := []ResumptionResult{}
	if first == nil {
		return out
	}
	out = append(out, resume(ctx, addr, serverName, cache, first.Version, 0, dials))
	if first.Version == tls.VersionTLS13 {
		older := &ticketCache{ClientSessionCache: tls.NewLRUClientSessionCache(4)}
		conn, _, err := dialDeep(ctx, addr, serverName, older, tls.VersionTLS12, dials)
		if err == nil {
			version := conn.ConnectionState().Version
			conn.Close()
			if version == tls.VersionTLS12 {
				out = append(out, resume(ctx, addr, serverName, older, version, tls.VersionTLS12, dials))
			}
		}
	}
	return out
}

func resume(ctx context.Context, addr, serverName string, cache *ticketCache, version, maxVersion uint16, dials *atomic.Int32) ResumptionResult {
	out := ResumptionResult{Version: tls.VersionName(version), Status: "unknown"}
	if !cache.stored.Load() {
		out.Status = "no-ticket"
		if version == tls.VersionTLS13 {
			out.Detail = "The server sent no session ticket, so every connection is a full handshake."
		} else {
			out.Detail = "The server sent no session ticket. It may still resume by session ID, which this scan cannot offer."
		}
		return out
	}
	conn, _, err := dialDeep(ctx, addr, serverName, cache, maxVersion, dials)
	if err != nil {
		out.Detail = "The second handshake failed: " + err.Error() + "."
		return out
	}
	resumed := conn.ConnectionState().DidResume
	conn.Close()
	if resumed {
		out.Status, out.Detail = "resumed", "The second connection resumed the first one's session."
	} else {
		out.Status, out.Detail = "not-resumed", "The server issued a ticket and did not take it back."
	}
	return out
}

// sniProbes are a handshake naming no site, unless the target is an address
// and so already named none, and one naming a site nobody has.
func sniProbes(ctx context.Context, addr, serverName string, named *tls.ConnectionState, dials *atomic.Int32) []SNIProbe {
	fingerprint := ""
	if named != nil && len(named.PeerCertificates) > 0 {
		sum := sha256.Sum256(named.PeerCertificates[0].Raw)
		fingerprint = colonHex(sum[:])
	}
	unknown := make([]byte, 6)
	rand.Read(unknown)
	kinds := []SNIProbe{{Kind: "unknown", Sent: "jd-" + hex.EncodeToString(unknown) + ".invalid"}}
	if serverName != "" {
		kinds = append([]SNIProbe{{Kind: "none"}}, kinds...)
	}
	var wg sync.WaitGroup
	for i := range kinds {
		wg.Go(func() {
			probe := &kinds[i]
			conn, _, err := dialDeep(ctx, addr, probe.Sent, nil, 0, dials)
			switch {
			case err == nil:
			case errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET):
				probe.Status, probe.Detail = "refused", "The server closed the connection."
				return
			default:
				if alert, alerted := remoteAlert(err); alerted {
					probe.Status, probe.Detail = "refused", "The server answered: "+alert+"."
				} else {
					probe.Status, probe.Detail = "unknown", err.Error()
				}
				return
			}
			certs := conn.ConnectionState().PeerCertificates
			conn.Close()
			if len(certs) == 0 {
				probe.Status, probe.Detail = "unknown", "The handshake completed without a certificate."
				return
			}
			leaf := certs[0]
			sum := sha256.Sum256(leaf.Raw)
			probe.Status = "certificate"
			probe.Subject = nameOf(leaf.Subject.CommonName, leaf.Subject.String())
			probe.Issuer = nameOf(leaf.Issuer.CommonName, leaf.Issuer.String())
			probe.Names = append([]string{}, leaf.DNSNames...)
			for _, ip := range leaf.IPAddresses {
				probe.Names = append(probe.Names, ip.String())
			}
			probe.Fingerprint = colonHex(sum[:])
			probe.SameAsNamed = probe.Fingerprint == fingerprint
		})
	}
	wg.Wait()
	return kinds
}

// altSvcProbe asks the site for its headers over HTTP/1.1 and reads Alt-Svc.
func altSvcProbe(ctx context.Context, addr, domain string, port int, dials *atomic.Int32) *HTTP3Result {
	out := &HTTP3Result{}
	serverName := ""
	if net.ParseIP(domain) == nil {
		serverName = domain
	}
	client := scanClient(&http.Transport{
		DisableKeepAlives: true,
		// Every probe goes to the one address, whatever the name resolves
		// to by now.
		DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dials.Add(1)
			dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: probeTimeout}, Config: &tls.Config{
				ServerName: serverName, InsecureSkipVerify: true, NextProtos: []string{"http/1.1"},
			}}
			return dialer.DialContext(ctx, network, addr)
		},
	})
	target := "https://" + urlHost(domain)
	if port != 443 {
		target += ":" + strconv.Itoa(port)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"/", nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	req.Header.Set("User-Agent", scanUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		out.Error = requestError(err)
		return out
	}
	resp.Body.Close()
	out.Answered = true
	out.AltSvc = strings.Join(resp.Header.Values("Alt-Svc"), ", ")
	for _, alt := range parseAltSvc(out.AltSvc) {
		if alt.protocol == "h3" || strings.HasPrefix(alt.protocol, "h3-") {
			out.Advertised = true
			out.Port = alt.port
			if alt.host != "" && !strings.EqualFold(alt.host, domain) {
				out.Host = alt.host
			}
			break
		}
	}
	return out
}

// quicTarget is the UDP port to ask for QUIC: the one Alt-Svc advertises on
// this host, or the TCP port of a website that advertises none — to catch
// QUIC answering without a browser ever being told. An alternative on another
// host is that host's to answer and is not sent anything.
func quicTarget(h *HTTP3Result, port int) int {
	switch {
	case !h.Answered:
		return 0
	case h.Advertised && h.Host == "":
		return h.Port
	case h.Advertised:
		return 0
	}
	return port
}

type altService struct {
	protocol string
	host     string
	port     int
}

// parseAltSvc reads an Alt-Svc value (RFC 7838): comma-separated
// protocol="host:port" alternatives, each with parameters after semicolons.
// "clear" and anything unreadable give nothing.
func parseAltSvc(value string) []altService {
	out := []altService{}
	for _, part := range splitOutsideQuotes(value, ',') {
		alternative, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		protocol, authority, ok := strings.Cut(strings.TrimSpace(alternative), "=")
		if !ok {
			continue
		}
		authority = strings.Trim(strings.TrimSpace(authority), `"`)
		host, portText, err := net.SplitHostPort(authority)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		out = append(out, altService{protocol: strings.TrimSpace(protocol), host: host, port: port})
	}
	return out
}

func splitOutsideQuotes(s string, sep rune) []string {
	parts := []string{}
	quoted := false
	start := 0
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == sep && !quoted:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// deepFindings judges the deep scan. The quick scan's findings stand as they
// are; these are the ones only the deep scan can see, and the version ones
// it can see better — a version the quick scan could only call "unknown"
// the deep scan may find accepted, under the quick scan's own finding id.
func deepFindings(d *DeepScan) []ScanFinding {
	findings := []ScanFinding{}
	if !d.Reachable {
		return findings
	}
	add := func(f ScanFinding) { findings = append(findings, f) }
	here := d.Where == "here"
	elsewhere := func(advice string) string {
		if here || d.Where == "unknown" {
			return advice
		}
		return d.Address + " is not this server, so this is its configuration to change, not this one's. " + advice
	}
	status := map[string]*VersionSuites{}
	for i := range d.Versions {
		status[d.Versions[i].Name] = &d.Versions[i]
	}
	isAccepted := func(name string) bool { return status[name] != nil && status[name].Status == "accepted" }

	if isAccepted("SSL 3.0") {
		add(ScanFinding{ID: "tls.ssl3", Level: "critical",
			Title:  "SSL 3.0 is accepted",
			Detail: fmt.Sprintf("It took %d suites over SSL 3.0, which POODLE broke in 2014.", len(status["SSL 3.0"].Suites)),
			Advice: elsewhere("Set ssl_protocols TLSv1.2 TLSv1.3. Nothing still in use needs SSL 3.0.")})
	}
	for _, name := range []string{"TLS 1.0", "TLS 1.1"} {
		if isAccepted(name) {
			add(ScanFinding{ID: "tls.old-protocol." + name, Level: "warning",
				Title:  name + " is still offered",
				Detail: fmt.Sprintf("It took %d suites over %s, deprecated since 2021 and disabled in every current browser.", len(status[name].Suites), name),
				Advice: elsewhere("Set ssl_protocols to TLSv1.2 TLSv1.3. Nothing that can reach this site today needs the older ones.")})
		}
	}
	if v := status["TLS 1.3"]; v != nil && v.Status == "refused" && (isAccepted("TLS 1.2") || isAccepted("TLS 1.1") || isAccepted("TLS 1.0")) {
		add(ScanFinding{ID: "tls.no-13", Level: "notice",
			Title:  "TLS 1.3 is not offered",
			Detail: v.Detail,
			Advice: elsewhere("Add TLSv1.3 to ssl_protocols. It is faster and removes a whole category of downgrade problem.")})
	}

	insecure, weak := map[uint16]SuiteResult{}, map[uint16]SuiteResult{}
	forwardSecret := false
	var insecureOrder, weakOrder []uint16
	for _, v := range d.Versions {
		for _, s := range v.Suites {
			if s.ForwardSecrecy {
				forwardSecret = true
			}
			switch s.Rating {
			case "insecure":
				if _, seen := insecure[s.ID]; !seen {
					insecure[s.ID] = s
					insecureOrder = append(insecureOrder, s.ID)
				}
			case "weak":
				if _, seen := weak[s.ID]; !seen {
					weak[s.ID] = s
					weakOrder = append(weakOrder, s.ID)
				}
			}
		}
	}
	const profile = "ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384:DHE-RSA-CHACHA20-POLY1305;"
	if len(insecure) > 0 {
		add(ScanFinding{ID: "tls.cipher.insecure", Level: "critical",
			Title:  plural(len(insecure), "insecure cipher suite is", "insecure cipher suites are") + " accepted",
			Detail: describeSuites(insecureOrder, insecure),
			Advice: elsewhere("A client can be pushed onto them, and then the connection is readable or forgeable by whoever is in the middle. Replace ssl_ciphers with a list of forward-secret AEAD suites, such as Mozilla's intermediate profile: " + profile)})
	}
	if !forwardSecret && len(d.Versions) > 0 && anyAccepted(d.Versions) {
		add(ScanFinding{ID: "tls.cipher.no-forward-secrecy", Level: "warning",
			Title:  "No suite has forward secrecy",
			Detail: "Every suite accepted takes its session key from the certificate's key, so whoever gets that key later can read every connection recorded before.",
			Advice: elsewhere("Offer ECDHE suites, and TLS 1.3, whose suites all have forward secrecy: " + profile)})
	}
	if len(weak) > 0 {
		add(ScanFinding{ID: "tls.cipher.weak", Level: "notice",
			Title:  plural(len(weak), "weak cipher suite is", "weak cipher suites are") + " accepted",
			Detail: describeSuites(weakOrder, weak),
			Advice: elsewhere("Browsers pick a strong suite when there is one; the weak ones serve only old clients. Mozilla's intermediate profile drops them: " + profile)})
	}
	for _, v := range d.Versions {
		if v.Order != "client" || v.Name == "TLS 1.3" {
			continue
		}
		if slices.ContainsFunc(v.Suites, func(s SuiteResult) bool { return s.Rating != "strong" }) {
			add(ScanFinding{ID: "tls.cipher.client-order." + v.Name, Level: "notice",
				Title:  "The client chooses the " + v.Name + " suite",
				Detail: "The server takes the client's order, and accepts suites that are not strong, so a client that lists one of those first gets it.",
				Advice: elsewhere("Set ssl_prefer_server_ciphers on, or drop the weaker suites; with only strong suites left, the client's order is fine.")})
			break
		}
	}
	if d.DHBits > 0 && d.DHBits < 2048 {
		add(ScanFinding{ID: "tls.kex.weak-dh", Level: "warning",
			Title:  fmt.Sprintf("DHE uses a %d-bit group", d.DHBits),
			Detail: "Groups under 2048 bits are within reach of a well-funded attacker (Logjam), and a shared one breaks every server using it at once.",
			Advice: elsewhere("Point ssl_dhparam at a 2048-bit (or larger) file, or remove the DHE suites from ssl_ciphers.")})
	}

	if isAccepted("TLS 1.3") && len(d.Groups) > 0 {
		pq := slices.ContainsFunc(d.Groups, func(g GroupResult) bool { return g.PostQuantum && g.Status == "accepted" })
		pqKnown := !slices.ContainsFunc(d.Groups, func(g GroupResult) bool { return g.PostQuantum && g.Status == "unknown" })
		switch {
		case !pq && pqKnown:
			add(ScanFinding{ID: "tls.kex.no-pq", Level: "notice",
				Title:  "No post-quantum key exchange",
				Detail: "X25519MLKEM768, which current browsers offer first, was refused, as were the other hybrids. Traffic recorded today could be read once a large enough quantum computer exists.",
				Advice: elsewhere("nginx negotiates what the OpenSSL it was built with offers, and X25519MLKEM768 arrived in OpenSSL 3.5, on by default; `nginx -V` names the version. On 3.5 or later, an ssl_ecdh_curve set by hand must list X25519MLKEM768 first.")})
		case pq && d.BrowserGroup != "" && !isPostQuantum(d.BrowserGroup):
			add(ScanFinding{ID: "tls.kex.pq-not-preferred", Level: "notice",
				Title:  "Post-quantum key exchange is accepted but not chosen",
				Detail: "Offered X25519MLKEM768 and X25519 with a key share for each, as a browser does, the server chose " + d.BrowserGroup + ".",
				Advice: elsewhere("List X25519MLKEM768 first in ssl_ecdh_curve.")})
		}
	}

	if a := d.ALPN; a != nil {
		website := d.HTTP3 != nil && d.HTTP3.Answered
		h2 := a.Negotiated == "h2"
		switch {
		case a.Site != nil && a.Site.HTTP2 && !h2:
			add(ScanFinding{ID: "tls.alpn.h2-off", Level: "warning",
				Title:  "HTTP/2 is on in the site form and not offered",
				Detail: fmt.Sprintf("%s has HTTP/2 switched on, and offered h2 and http/1.1 the server chose %s.", a.Site.Name, alpnWord(a.Negotiated)),
				Advice: "nginx reads `http2 on;` from the server block the name selects, from 1.25.1. A change saved without a reload is not served yet; otherwise check which site answers this name."})
		case a.Site != nil && !a.Site.HTTP2 && h2:
			add(ScanFinding{ID: "tls.alpn.h2-unexpected", Level: "notice",
				Title:  "HTTP/2 is offered though the site form has it off",
				Detail: a.Site.Name + " has HTTP/2 switched off, and the server chose h2.",
				Advice: "Another site on the same address turns it on with the old `listen … http2` spelling, which applies to every site on that address and port."})
		case !h2 && website:
			advice := elsewhere("HTTP/2 serves a page's files over one connection. In nginx 1.25.1 or later it is `http2 on;` in the server block; the site form has a switch for it.")
			if a.Site != nil {
				advice = "HTTP/2 serves a page's files over one connection. Switch it on in " + a.Site.Name + "'s site form."
			}
			add(ScanFinding{ID: "tls.alpn.no-h2", Level: "notice",
				Title:  "HTTP/2 is not offered",
				Detail: fmt.Sprintf("Offered h2 and http/1.1, the server chose %s.", alpnWord(a.Negotiated)),
				Advice: advice})
		}
	}

	if h := d.HTTP3; h != nil && h.QUIC != nil {
		switch {
		case h.Advertised && !h.QUIC.Answered:
			add(ScanFinding{ID: "tls.h3.no-quic", Level: "warning",
				Title:  "HTTP/3 is advertised and QUIC does not answer",
				Detail: fmt.Sprintf("Alt-Svc offers h3 on UDP %d, and nothing answered a QUIC packet there. %s", h.QUIC.Port, h.QUIC.Detail),
				Advice: elsewhere(fmt.Sprintf("Browsers try it and fall back to TCP, a delay on the first visit. Open UDP %d in the firewall and at the provider, and check nginx has `listen %d quic reuseport;` — or stop sending the Alt-Svc header.", h.QUIC.Port, h.QUIC.Port))})
		case !h.Advertised && h.QUIC.Answered:
			add(ScanFinding{ID: "tls.h3.not-advertised", Level: "notice",
				Title:  "QUIC answers and HTTP/3 is not advertised",
				Detail: fmt.Sprintf("A QUIC server answers on UDP %d, and the site sends no Alt-Svc header, so no browser is told to use it.", h.QUIC.Port),
				Advice: elsewhere(fmt.Sprintf(`Add add_header Alt-Svc 'h3=":%d"; ma=86400' always; to the server block.`, h.QUIC.Port))})
		}
	}

	if len(d.Resumption) > 0 {
		first := d.Resumption[0]
		if first.Status == "not-resumed" || (first.Status == "no-ticket" && first.Version == "TLS 1.3") {
			add(ScanFinding{ID: "tls.resumption.none", Level: "notice",
				Title:  first.Version + " sessions are not resumed",
				Detail: first.Detail,
				Advice: elsewhere("A returning visitor pays for a full handshake every time. In nginx, ssl_session_cache shared:SSL:10m and ssl_session_timeout 1d; with ssl_session_tickets off, TLS 1.3 then resumes from that cache.")})
		}
	}

	var leaked []SNIProbe
	for _, p := range d.SNI {
		if p.Status == "certificate" {
			leaked = append(leaked, p)
		}
	}
	if len(leaked) > 0 {
		who := "A client that names no site"
		if len(leaked) == 1 && leaked[0].Kind == "unknown" {
			who = "A client that names a site this server does not have"
		} else if len(leaked) == 2 {
			who = "A client that names no site, or one this server does not have,"
		}
		names := leaked[0].Names
		if len(names) == 0 {
			names = []string{leaked[0].Subject}
		}
		host, _, _ := net.SplitHostPort(d.Address)
		advice := "Scanners connect to every address without a name, so the certificate's names are how they learn which sites live on " + host + ". A default server for the port that refuses those handshakes closes that, and the names a browser sends still reach their sites: server { listen " + strconv.Itoa(d.Port) + " ssl default_server; ssl_reject_handshake on; } (nginx 1.19.4 or later)."
		add(ScanFinding{ID: "tls.sni.default-certificate", Level: "notice",
			Title:  who + " gets a certificate",
			Detail: fmt.Sprintf("It gets %s, for %s.", certificateWord(leaked[0]), strings.Join(names, ", ")),
			Advice: elsewhere(advice)})
	}

	SortFindings(findings)
	return findings
}

func anyAccepted(versions []VersionSuites) bool {
	return slices.ContainsFunc(versions, func(v VersionSuites) bool { return v.Status == "accepted" })
}

func isPostQuantum(name string) bool {
	return slices.ContainsFunc(tls13Groups, func(g groupInfo) bool { return g.name == name && g.postQuantum })
}

func alpnWord(p string) string {
	if p == "" {
		return "neither"
	}
	return p
}

func certificateWord(p SNIProbe) string {
	if p.SameAsNamed {
		return "the certificate the scanned name gets"
	}
	return p.Subject + "'s certificate"
}

// describeSuites names the suites in the order they were found, with why each
// is not strong. Past eight the rest are counted; the suite list has them.
func describeSuites(order []uint16, suites map[uint16]SuiteResult) string {
	parts := []string{}
	for _, id := range order[:min(len(order), 8)] {
		s := suites[id]
		parts = append(parts, s.OpenSSL+" ("+strings.Join(s.Reasons, ", ")+")")
	}
	text := strings.Join(parts, "; ")
	if more := len(order) - len(parts); more > 0 {
		text += fmt.Sprintf("; and %d more", more)
	}
	return text + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
