package proxysvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// What a domain is actually serving, graded.
//
// The certificate list on this page reads files from disk, which answers a
// question nobody has: what matters is what the internet gets when it asks.
// Those differ constantly — a certificate renewed and never reloaded, a proxy
// still offering TLS 1.0 because the config was copied from a 2015 blog post,
// a redirect to HTTPS that quietly stopped working. SSL Labs answers this and
// takes two minutes and a public hostname; every panel in this class leaves
// you to go there.
//
// The grade is deliberately coarse and its reasoning is attached to every
// finding, because a letter with no working is a number to optimise rather
// than a thing to fix.

type TLSScan struct {
	Domain    string    `json:"domain"`
	Port      int       `json:"port"`
	CheckedAt time.Time `json:"checkedAt"`
	Reachable bool      `json:"reachable"`
	Error     string    `json:"error,omitempty"`
	// Failure is how far a scan that never completed a handshake got, and
	// why it stopped there.
	Failure *ScanFailure `json:"failure,omitempty"`

	// Grade is A+ down to F, and Summary is the one sentence behind it.
	Grade   string `json:"grade"`
	Summary string `json:"summary"`

	Negotiated  string `json:"negotiated,omitempty"`
	CipherSuite string `json:"cipherSuite,omitempty"`
	// LegacyOnly reports that the server refused the handshake a current
	// client makes and took one offering every version and cipher suite this
	// library has; Negotiated and CipherSuite are what it took then.
	LegacyOnly bool `json:"legacyOnly"`
	// Protocols reports each version the server was asked for. "unknown" is a
	// real answer: the probe got nothing from the server to stand behind —
	// this client would not ask, the connection failed, or the server's answer
	// could mean either — and saying so beats reporting a version as absent
	// because it could not be tested.
	Protocols []ProtocolResult `json:"protocols"`

	Certificate *Certificate `json:"certificate,omitempty"`
	Chain       []ChainLink  `json:"chain"`
	// ChainComplete reports whether the server sent its intermediates. A
	// missing intermediate works in every desktop browser (they cache them)
	// and fails on exactly the clients nobody tests with.
	ChainComplete bool   `json:"chainComplete"`
	Trusted       bool   `json:"trusted"`
	TrustError    string `json:"trustError,omitempty"`
	NameMatches   bool   `json:"nameMatches"`

	KeyType      string `json:"keyType,omitempty"`
	KeyBits      int    `json:"keyBits,omitempty"`
	SignatureAlg string `json:"signatureAlgorithm,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	Serial       string `json:"serial,omitempty"`
	OCSPStapled  bool   `json:"ocspStapled"`
	// OCSPServers are the responders the leaf names. Without one there is
	// nothing to staple — Let's Encrypt stopped naming one in 2025 — so a
	// missing staple is only worth a word when this is not empty.
	OCSPServers []string `json:"ocspServers,omitempty"`
	// CRLURLs are the leaf's CRL distribution points.
	CRLURLs []string `json:"crlUrls,omitempty"`
	// SPKIPin is the base64 SHA-256 of the leaf's public key, the pin curl's
	// --pinnedpubkey and a mobile app's pinning take. It survives a renewal
	// that keeps the key, where the fingerprint does not.
	SPKIPin string `json:"spkiPin,omitempty"`
	// LifetimeHours is the leaf's whole term, and RenewalWindowHours the end
	// of it in which renewal is due (see renewalWindow). Hours, because a
	// short-lived certificate's 160 is no whole number of days.
	LifetimeHours      int `json:"lifetimeHours,omitempty"`
	RenewalWindowHours int `json:"renewalWindowHours,omitempty"`

	HTTP *HTTPScan `json:"http,omitempty"`
	// Preload is the domain against the HSTS preload list's rules, for a
	// name on port 443 whose HTTPS answered.
	Preload  *PreloadCheck `json:"preload,omitempty"`
	Findings []ScanFinding `json:"findings"`
}

type ProtocolResult struct {
	Name string `json:"name"`
	// Status is "offered", "refused" or "unknown".
	Status string `json:"status"`
	// Detail is what the server said, or why nothing it said was heard.
	Detail string `json:"detail,omitempty"`
}

// ChainLink is one certificate as the server presented it.
type ChainLink struct {
	Subject    string    `json:"subject"`
	Issuer     string    `json:"issuer"`
	NotAfter   time.Time `json:"notAfter"`
	IsCA       bool      `json:"isCa"`
	KeyType    string    `json:"keyType,omitempty"`
	KeyBits    int       `json:"keyBits,omitempty"`
	SelfIssued bool      `json:"selfIssued"`
}

// HTTPScan is what the site says about itself over HTTP.
type HTTPScan struct {
	// Service is "http" when the HTTPS request got an HTTP answer, "other"
	// when the service is known not to be a website — its port is
	// registered to another protocol, or it answered in one — and "unknown"
	// when the request got no answer that says either.
	Service string `json:"service"`
	// ServiceName is the protocol the port is registered to, when that is
	// why no request was sent.
	ServiceName string `json:"serviceName,omitempty"`
	// Banner is the first line a service that is not HTTP answered with.
	Banner     string `json:"banner,omitempty"`
	StatusCode int    `json:"statusCode"`
	Server     string `json:"server,omitempty"`
	// Location is where the HTTPS answer redirects, when it does.
	Location string `json:"location,omitempty"`
	// HTTPSError is why the HTTPS request got no response, when the service
	// said nothing to show it is not a website: it may be a quiet service or
	// a failing site. Nothing else here was measured then — no header can be
	// missing from a response that never came.
	HTTPSError string `json:"httpsError,omitempty"`
	// PlainRedirects reports whether http:// ends up at https://, however
	// many hops it takes. A site with a perfect certificate that still
	// answers on port 80 is one bookmark away from being read in plain text.
	PlainRedirects bool `json:"plainRedirects"`
	// PlainStatus and PlainLocation are the first hop's answer.
	PlainStatus   int    `json:"plainStatus,omitempty"`
	PlainLocation string `json:"plainLocation,omitempty"`
	// PlainError is why port 80 did not answer at all, and PlainErrorKind
	// which of "refused", "timeout", "dns" or "other" that was.
	PlainError     string `json:"plainError,omitempty"`
	PlainErrorKind string `json:"plainErrorKind,omitempty"`
	// RedirectChain is every plain-HTTP request made, in order, and
	// RedirectVerdict where it ended: "same-host" or "other-host" when it
	// reached HTTPS on the host it started from or another one, "stays-http"
	// at an answer that is not a redirect, "loop", "too-many" hops,
	// "dead-end" at a hop that did not answer or pointed nowhere usable, and
	// "internal" at a hop into this machine's network, which is not
	// followed. It is empty when port 80 did not answer.
	RedirectChain   []RedirectHop `json:"redirectChain"`
	RedirectVerdict string        `json:"redirectVerdict,omitempty"`
	HSTS            *HSTS         `json:"hsts,omitempty"`
	Headers         []HeaderCheck `json:"headers"`
}

// RedirectHop is one plain-HTTP request and its answer: a status and where it
// pointed, or the error when the hop did not answer. Internal is a hop that
// was not requested at all, because it would have reached this machine or
// its private network (see followPlainHTTP).
type RedirectHop struct {
	URL      string `json:"url"`
	Status   int    `json:"status,omitempty"`
	Location string `json:"location,omitempty"`
	Error    string `json:"error,omitempty"`
	Internal bool   `json:"internal,omitempty"`
}

// HSTS is the parsed Strict-Transport-Security header.
type HSTS struct {
	MaxAge            int    `json:"maxAge"`
	IncludeSubDomains bool   `json:"includeSubDomains"`
	Preload           bool   `json:"preload"`
	Raw               string `json:"raw"`
}

// HeaderCheck is one security header, present or not, with what it is for.
type HeaderCheck struct {
	Name    string `json:"name"`
	Value   string `json:"value,omitempty"`
	Present bool   `json:"present"`
	// Level is how much its absence matters: "important" or "optional".
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

// ScanFinding mirrors the shape used by the health and Docker verdicts: what
// was measured, what it means, what to do.
type ScanFinding struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Advice string `json:"advice,omitempty"`
}

// hstsStrongMaxAge is six months, the max-age A+ asks for here, as SSL Labs'
// does: the point at which HSTS is doing the job it exists for. The preload
// list asks for more (preloadMaxAge).
const hstsStrongMaxAge = 15552000

// ScanTLS runs the whole examination: a handshake, a version probe, the chain,
// and an HTTP request for the headers.
func ScanTLS(ctx context.Context, domain string, port int) *TLSScan {
	if port == 0 {
		port = 443
	}
	scan := &TLSScan{
		Domain: domain, Port: port, CheckedAt: time.Now().UTC(),
		Protocols: []ProtocolResult{}, Chain: []ChainLink{}, Findings: []ScanFinding{},
	}
	addr := net.JoinHostPort(domain, strconv.Itoa(port))

	conn, legacy, err := handshake(ctx, addr, domain)
	if err != nil {
		unanswered(ctx, scan, err)
		return scan
	}
	state := conn.ConnectionState()
	conn.Close()

	scan.Reachable = true
	scan.LegacyOnly = legacy
	scan.Negotiated = tls.VersionName(state.Version)
	scan.CipherSuite = tls.CipherSuiteName(state.CipherSuite)
	scan.OCSPStapled = len(state.OCSPResponse) > 0
	describeChain(scan, state.PeerCertificates, domain)

	// The preload list is for names served on 443. Its www rule is a
	// handshake of its own, so it runs beside the probes rather than after
	// them.
	preloadable := port == 443 && net.ParseIP(domain) == nil
	var www chan PreloadRule
	if preloadable && registrableDomain(domain) == domain {
		www = make(chan PreloadRule, 1)
		go func() { www <- checkWWW(ctx, domain, "443", net.DefaultResolver.LookupIPAddr) }()
	}

	scan.Protocols = probeProtocols(ctx, addr, domain)
	if name, registered := implicitTLSServices[port]; registered {
		scan.HTTP = &HTTPScan{Service: "other", ServiceName: name, Headers: []HeaderCheck{}, RedirectChain: []RedirectHop{}}
	} else {
		// The HTTPS request offers what the handshake got an answer to. The
		// plain half goes to port 80 whatever the TLS port is: a redirect on a
		// non-standard port tells nobody anything, because no browser goes
		// there.
		offer := tlsOffer("", 0, 0)
		if legacy {
			offer = tlsOffer("", oldestVersion, newestVersion)
		}
		scan.HTTP = scanHTTP(ctx, "https://"+addr+"/", "http://"+urlHost(domain)+"/", offer)
	}
	if preloadable && scan.HTTP.Service == "http" {
		var rule *PreloadRule
		if www != nil {
			r := <-www
			rule = &r
		}
		scan.Preload = preloadCheck(domain, scan, rule)
	}

	grade(scan)
	return scan
}

// implicitTLSServices are the ports registered to a protocol that speaks TLS
// from its first byte and is not HTTP. No web request is sent to them: the
// answer would be the service's own greeting, and HSTS, security headers and
// a port-80 redirect say nothing about a mail or directory server.
var implicitTLSServices = map[int]string{
	465:  "SMTP submission",
	563:  "NNTP",
	636:  "LDAP",
	853:  "DNS over TLS",
	990:  "FTP",
	992:  "Telnet",
	993:  "IMAP",
	994:  "IRC",
	995:  "POP3",
	3269: "LDAP global catalog",
	5061: "SIP",
	5223: "XMPP",
	5349: "TURN",
	5671: "AMQP",
	6697: "IRC",
	8883: "MQTT",
}

// handshake is the scan's own, offering what a current client does. A server
// that takes only what such a client leaves out — RSA key exchange, 3DES, TLS
// 1.0 — answers that with an alert, and was reported as nothing answering at
// all. So an alert is followed by an offer of every version and cipher suite
// this library has, and when that is taken the current offer is made once
// more: legacy is true only when it is refused again, so a passing fault is
// not reported as the server's policy.
func handshake(ctx context.Context, addr, serverName string) (conn *tls.Conn, legacy bool, err error) {
	conn, err = dialTLS(ctx, addr, serverName, 0, 0)
	if _, alerted := remoteAlert(err); !alerted {
		return conn, false, err
	}
	old, oldErr := dialTLS(ctx, addr, serverName, oldestVersion, newestVersion)
	if oldErr != nil {
		return nil, false, err
	}
	if again, againErr := dialTLS(ctx, addr, serverName, 0, 0); againErr == nil {
		old.Close()
		return again, false, nil
	}
	return old, true, nil
}

// oldestVersion and newestVersion bound the offer of everything this library
// has.
const oldestVersion, newestVersion = tls.VersionTLS10, tls.VersionTLS13

// remoteAlert is the alert a server answered a handshake with, in its words.
func remoteAlert(err error) (string, bool) {
	var op *net.OpError
	if err == nil || !errors.As(err, &op) || op.Op != "remote error" {
		return "", false
	}
	return strings.TrimPrefix(op.Err.Error(), "tls: "), true
}

// CheckEndpoint is CheckDomain for the watch list, which checks many
// endpoints under one budget: CheckDomain's dial ignores its context, so a
// list of silent endpoints held the request for eight seconds a batch however
// short the budget. This handshake ends when ctx does, and then returns ctx's
// error and no certificate — the budget ran out, which says nothing about the
// endpoint. Every other failure is the endpoint's answer, on the certificate.
func CheckEndpoint(ctx context.Context, domain string, port int) (*Certificate, error) {
	failed := func(reason string) *Certificate {
		return &Certificate{Name: domain, Domains: []string{domain}, Source: "live", UsedBy: []string{}, Error: reason}
	}
	conn, err := dialTLS(ctx, net.JoinHostPort(domain, strconv.Itoa(port)), domain, 0, 0)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isTimeout(err) {
			return failed("No TLS handshake within 10 seconds."), nil
		}
		return failed(err.Error()), nil
	}
	state := conn.ConnectionState()
	conn.Close()
	if len(state.PeerCertificates) == 0 {
		return failed("The handshake completed without a certificate."), nil
	}
	leaf := state.PeerCertificates[0]
	cert := summarise(leaf, domain, "")
	cert.Source = "live"
	// Trust is checked apart from reading the certificate, so the list can
	// say "valid but untrusted" rather than conflating the two.
	roots, _ := x509.SystemCertPool()
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: domain, Roots: roots, Intermediates: intermediates(state.PeerCertificates),
	}); err != nil {
		cert.Error = err.Error()
	}
	return cert, nil
}

// dialTLS handshakes with addr, offering tlsOffer's versions and suites. The
// connection and the handshake are two steps so a handshake that fails says
// where it was connected (handshakeError).
func dialTLS(ctx context.Context, addr, serverName string, minVer, maxVer uint16) (*tls.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, tlsOffer(serverName, minVer, maxVer))
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, &handshakeError{addr: raw.RemoteAddr().String(), err: err}
	}
	return conn, nil
}

// tlsOffer is what a handshake of this check offers. With versions set it
// offers every cipher suite this library implements (probeSuites), so the
// question it asks is only the version; unset it offers what a current client
// does, since that handshake is what the report calls negotiated.
func tlsOffer(serverName string, minVer, maxVer uint16) *tls.Config {
	config := &tls.Config{
		ServerName: serverName,
		// The certificate is being examined, not trusted. A failed
		// verification is the finding, not a reason to stop.
		InsecureSkipVerify: true,
		MinVersion:         minVer,
		MaxVersion:         maxVer,
	}
	if minVer != 0 {
		config.CipherSuites = probeSuites
	}
	return config
}

// probeSuites is every cipher suite this library implements, the insecure
// ones included (TLS 1.3's are not configurable, and Go skips them here). Go
// leaves RSA key exchange, 3DES and RC4 out of what it offers by default, so
// a server still taking TLS 1.0 with only AES128-SHA answered the default
// offer with a handshake failure and was reported as refusing a version it
// accepts.
var probeSuites = func() []uint16 {
	ids := []uint16{}
	for _, suite := range append(tls.CipherSuites(), tls.InsecureCipherSuites()...) {
		ids = append(ids, suite.ID)
	}
	return ids
}()

// probedVersions are asked for one at a time, pinned to exactly one version so
// the server's answer is unambiguous.
var probedVersions = []struct {
	name    string
	version uint16
}{
	{"TLS 1.0", tls.VersionTLS10},
	{"TLS 1.1", tls.VersionTLS11},
	{"TLS 1.2", tls.VersionTLS12},
	{"TLS 1.3", tls.VersionTLS13},
}

// probeProtocols asks for each version on a connection of its own.
//
// Concurrently, because they are independent and a host that black-holes
// refused versions would otherwise cost one dial timeout each — four of them
// in series is most of the request's budget spent proving nothing.
func probeProtocols(ctx context.Context, addr, serverName string) []ProtocolResult {
	out := make([]ProtocolResult, len(probedVersions))
	var wg sync.WaitGroup
	for i, v := range probedVersions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := dialTLS(ctx, addr, serverName, v.version, v.version)
			if err == nil {
				conn.Close()
				out[i] = ProtocolResult{Name: v.name, Status: "offered"}
				return
			}
			status, detail := protocolAnswer(err)
			out[i] = ProtocolResult{Name: v.name, Status: status, Detail: detail}
		}()
	}
	wg.Wait()
	return out
}

// protocolAnswer reads a handshake pinned to one version that failed.
//
// Only the server's own answer is a refusal: an alert, a different version
// picked instead, or the connection closed on the ClientHello. Anything that
// may be the network or this client stays "unknown" — calling it refused is
// exactly the false reassurance the probe exists to avoid. The server's
// protocol_version alert reads "protocol version not supported", which is why
// the alert is recognised by its type and not its words: matching the words
// once filed every correct refusal of TLS 1.0 as this client's own.
//
// Two alerts are the exception. OpenSSL refuses a version with
// protocol_version and answers handshake_failure (or insufficient_security)
// when the version is fine and no cipher is shared. The probe offers every
// suite Go has, which leaves a server that takes the version only with one Go
// lacks (finite-field DHE, Camellia), or a stack such as Java 8 that refuses
// versions with handshake_failure. The alert cannot say which, so it is
// "unknown" with both readings, never "refused".
func protocolAnswer(err error) (status, detail string) {
	var op *net.OpError
	switch {
	case strings.Contains(err.Error(), "no supported versions satisfy"):
		return "unknown", "This dashboard's TLS library will not ask for it, so the server was never asked."
	case errors.As(err, &op) && op.Op == "remote error":
		alert := strings.TrimPrefix(op.Err.Error(), "tls: ")
		if alert == "handshake failure" || alert == "insufficient security" {
			return "unknown", "The server answered: " + alert + ". It refuses this version, or accepts it only with a cipher this probe cannot offer."
		}
		return "refused", "The server answered: " + alert + "."
	case errors.As(err, &op) && op.Op == "dial":
		return "unknown", "This probe could not connect: " + op.Err.Error() + "."
	case strings.Contains(err.Error(), "server selected unsupported protocol version"):
		return "refused", "The server answered with a different version."
	case errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET):
		return "refused", "The server closed the connection instead of answering."
	case isTimeout(err):
		return "unknown", "The server did not answer in time."
	}
	return "unknown", err.Error()
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

func describeChain(scan *TLSScan, chain []*x509.Certificate, domain string) {
	if len(chain) == 0 {
		return
	}
	leaf := chain[0]
	scan.Certificate = summarise(leaf, domain, "")
	scan.Certificate.Source = "live"
	scan.KeyType, scan.KeyBits = keyInfo(leaf)
	scan.SignatureAlg = leaf.SignatureAlgorithm.String()
	sum := sha256.Sum256(leaf.Raw)
	scan.Fingerprint = colonHex(sum[:])
	// Hex, as openssl, browsers and crt.sh print it; the decimal big.Int
	// String gives matches nothing an operator can compare it with.
	scan.Serial = colonHex(leaf.SerialNumber.Bytes())
	scan.OCSPServers = leaf.OCSPServer
	scan.CRLURLs = leaf.CRLDistributionPoints
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	scan.SPKIPin = base64.StdEncoding.EncodeToString(pin[:])
	scan.LifetimeHours = wholeHours(leaf.NotAfter.Sub(leaf.NotBefore))
	scan.RenewalWindowHours = wholeHours(renewalWindow(leaf.NotBefore, leaf.NotAfter))

	for _, c := range chain {
		keyType, keyBits := keyInfo(c)
		scan.Chain = append(scan.Chain, ChainLink{
			Subject:  nameOf(c.Subject.CommonName, c.Subject.String()),
			Issuer:   nameOf(c.Issuer.CommonName, c.Issuer.String()),
			NotAfter: c.NotAfter.UTC(), IsCA: c.IsCA,
			KeyType: keyType, KeyBits: keyBits,
			SelfIssued: c.Issuer.String() == c.Subject.String(),
		})
	}
	// A chain of one is only complete if that one is self-signed; otherwise
	// the intermediate is missing and the server is relying on the client
	// having seen it before.
	scan.ChainComplete = len(chain) > 1 || scan.Chain[0].SelfIssued

	if err := leaf.VerifyHostname(domain); err == nil {
		scan.NameMatches = true
	}
	roots, _ := x509.SystemCertPool()
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: domain, Roots: roots, Intermediates: intermediates(chain),
	}); err != nil {
		scan.TrustError = err.Error()
	} else {
		scan.Trusted = true
	}
}

func nameOf(common, full string) string {
	if common != "" {
		return common
	}
	return full
}

func keyInfo(c *x509.Certificate) (string, int) {
	switch pub := c.PublicKey.(type) {
	case *rsa.PublicKey:
		return "RSA", pub.N.BitLen()
	case *ecdsa.PublicKey:
		return "ECDSA", pub.Curve.Params().BitSize
	case ed25519.PublicKey:
		return "Ed25519", 256
	}
	return c.PublicKeyAlgorithm.String(), 0
}

func colonHex(b []byte) string {
	s := hex.EncodeToString(b)
	var out strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			out.WriteByte(':')
		}
		out.WriteString(strings.ToUpper(s[i : i+2]))
	}
	return out.String()
}

// securityHeaders is the set worth reporting, with why. Kept short: a report
// listing twenty headers trains people to ignore the report.
var securityHeaders = []struct {
	name   string
	level  string
	detail string
}{
	{"Content-Security-Policy", "optional",
		"Restricts where scripts and styles may come from. The strongest defence against a script injected into your pages, and the fiddliest to get right."},
	{"X-Content-Type-Options", "important",
		"With nosniff, the browser stops guessing at content types — which is how an uploaded file becomes executable script."},
	{"X-Frame-Options", "important",
		"Stops another site embedding yours in a frame and collecting the clicks."},
	{"Referrer-Policy", "optional",
		"Controls how much of your URLs is leaked to the sites you link to."},
	{"Permissions-Policy", "optional",
		"Turns off browser features the page does not use: camera, microphone, geolocation."},
}

const scanUserAgent = "Just-Dashboard TLS check"

// scanHTTP fetches httpsURL for the headers, offering what offer does, then
// follows plainURL's redirects to see whether a plain-HTTP visitor reaches
// HTTPS.
func scanHTTP(ctx context.Context, httpsURL, plainURL string, offer *tls.Config) *HTTPScan {
	out := &HTTPScan{Headers: []HeaderCheck{}, RedirectChain: []RedirectHop{}}
	// The connection keeps the first bytes the service sent, because they
	// are what says a service is not a website: net/http's error quotes only
	// a fragment of a first line that is not HTTP, and says nothing of one
	// that arrived before the request was written (a server that greets on
	// connect, as SMTP and IMAP do). The transport dials on a goroutine of
	// its own, which may outlive a request that gave up.
	var heard atomic.Pointer[firstBytes]
	client := scanClient(&http.Transport{
		DisableKeepAlives: true,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: offer}
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			recorded := &firstBytes{Conn: conn}
			heard.Store(recorded)
			return recorded, nil
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpsURL, nil)
	if err != nil {
		out.Service, out.HTTPSError = "unknown", err.Error()
		return out
	}
	req.Header.Set("User-Agent", scanUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		// A mail server on a port of its own, or a site that is failing.
		// Either way whatever answers port 80 is some other service, so its
		// redirect is not this one's to be graded on.
		out.HTTPSError = requestError(err)
		out.Service = "unknown"
		if recorded := heard.Load(); recorded != nil {
			// It answered, in another protocol: that is no failure to
			// report, and the greeting says what it is.
			if line := recorded.firstLine(); line != "" && !strings.HasPrefix(line, "HTTP/") {
				out.Service, out.Banner, out.HTTPSError = "other", line, ""
			}
		}
		return out
	}
	resp.Body.Close()
	out.Service = "http"
	out.StatusCode = resp.StatusCode
	out.Server = resp.Header.Get("Server")
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		out.Location = resp.Header.Get("Location")
	}
	out.HSTS = parseHSTS(resp.Header.Get("Strict-Transport-Security"))
	for _, h := range securityHeaders {
		value := resp.Header.Get(h.name)
		out.Headers = append(out.Headers, HeaderCheck{
			Name: h.name, Value: value, Present: value != "",
			Level: h.level, Detail: h.detail,
		})
	}

	chain, verdict, err := followPlainHTTP(ctx, plainURL)
	if err != nil {
		out.PlainError = requestError(err)
		out.PlainErrorKind = netErrorKind(err)
		return out
	}
	out.RedirectChain = chain
	out.RedirectVerdict = verdict
	out.PlainRedirects = verdict == "same-host" || verdict == "other-host"
	out.PlainStatus, out.PlainLocation = chain[0].Status, chain[0].Location
	return out
}

// firstBytes is a connection that keeps the first bytes read from it.
type firstBytes struct {
	net.Conn
	mu   sync.Mutex
	head []byte
}

func (c *firstBytes) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	if room := 256 - len(c.head); room > 0 {
		c.head = append(c.head, p[:min(n, room)]...)
	}
	c.mu.Unlock()
	return n, err
}

// firstLine is the service's first line, as far as it can be shown: an IMAP
// server's "* OK [CAPABILITY IMAP4rev1] Dovecot ready." is proof it is not a
// website, and says what it is.
func (c *firstBytes) firstLine() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	line, _, _ := strings.Cut(string(c.head), "\n")
	line = strings.TrimSpace(printable([]byte(strings.TrimRight(line, "\r"))))
	if len(line) > 120 {
		line = line[:120] + "…"
	}
	return line
}

// scanClient makes one request per call and follows nothing. Redirects are
// the subject of the test: a client that follows them reports the headers of
// wherever it ended up, which may be an entirely different host, so
// followPlainHTTP walks the plain ones by hand.
func scanClient(transport *http.Transport) *http.Client {
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     transport,
	}
}

// maxRedirectHops bounds the plain-HTTP chain: http://x → http://www.x →
// https://www.x is two, and five is room for anything a working site does
// while a hostile one cannot hold the scan.
const maxRedirectHops = 5

// errInternalHop is a redirect hop that was not requested because it would
// have reached this machine or its private network.
var errInternalHop = errors.New("not requested: an address on this machine or its private network")

// followPlainHTTP requests start and follows its redirects by hand until one
// points at https://, an answer is not a redirect, a URL comes round again or
// the hops run out, and says which of those ended it (HTTPScan's
// RedirectVerdict). The error is the first request's, when nothing answered
// at all; a later hop that fails is recorded on the chain instead.
//
// The first request goes where the administrator asked. Every later one goes
// where the remote site's Location header says, which is that site's choice,
// so it may reach the address the first request reached or a public one and
// nothing else: otherwise any site scanned could have this server send GETs,
// with paths of its choosing, to the services on loopback and the private
// network behind it, and read their redirects back in the report. The check
// is on the address actually dialled, after DNS, so a name that resolves
// inward is caught too. Such a hop is recorded as Internal, unrequested.
func followPlainHTTP(ctx context.Context, start string) ([]RedirectHop, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	current, err := url.Parse(start)
	if err != nil {
		return nil, "", err
	}
	origin := current.Hostname()
	// The hops run one after another, so first is written by the first
	// request's dial before any later one reads it.
	var first string
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	guarded := &net.Dialer{Timeout: 8 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		if hopAllowed(first, address) {
			return nil
		}
		return errInternalHop
	}}
	client := scanClient(&http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if first != "" {
				return guarded.DialContext(ctx, network, addr)
			}
			conn, err := dialer.DialContext(ctx, network, addr)
			if err == nil {
				first = conn.RemoteAddr().String()
			}
			return conn, err
		},
	})

	chain := []RedirectHop{}
	seen := map[string]bool{}
	for len(chain) < maxRedirectHops {
		seen[current.String()] = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", scanUserAgent)
		resp, err := client.Do(req)
		if err != nil {
			if len(chain) == 0 {
				return nil, "", err
			}
			if errors.Is(err, errInternalHop) {
				return append(chain, RedirectHop{URL: current.String(), Internal: true}), "internal", nil
			}
			return append(chain, RedirectHop{URL: current.String(), Error: requestError(err)}), "dead-end", nil
		}
		resp.Body.Close()
		hop := RedirectHop{URL: current.String(), Status: resp.StatusCode, Location: resp.Header.Get("Location")}
		chain = append(chain, hop)
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || hop.Location == "" {
			return chain, "stays-http", nil
		}
		next, err := current.Parse(hop.Location)
		switch {
		case err != nil:
			return chain, "dead-end", nil
		case next.Scheme == "https" && strings.EqualFold(next.Hostname(), origin):
			return chain, "same-host", nil
		case next.Scheme == "https":
			return chain, "other-host", nil
		case next.Scheme != "http":
			return chain, "dead-end", nil
		case seen[next.String()]:
			return chain, "loop", nil
		}
		current = next
	}
	return chain, "too-many", nil
}

// hopAllowed says whether a redirect hop may dial address (ip:port, as
// resolved): the one the first request reached, or any public one.
func hopAllowed(first, address string) bool {
	if address == first {
		return true
	}
	host, _, err := net.SplitHostPort(address)
	return err == nil && IsPublicAddress(net.ParseIP(host))
}

// requestError drops the `Get "url": ` net/http puts in front of every
// failure — the URL is already on the page beside it — and says in words
// what a bare "EOF" means.
func requestError(err error) string {
	if errors.Is(err, io.EOF) {
		return "the server closed the connection without an HTTP response"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// netErrorKind names why a request got no answer, so "refused" is said only
// of a connection that was refused.
func netErrorKind(err error) string {
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case isTimeout(err):
		return "timeout"
	}
	return "other"
}

// urlHost is a host as a URL writes it, with an IPv6 address in brackets.
func urlHost(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

// parseHSTS reads the header's directives. max-age is the only one that does
// anything on its own, and a max-age of zero is an instruction to forget the
// policy — which is not the same as not sending the header, and is worth
// reporting as itself.
func parseHSTS(raw string) *HSTS {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	h := &HSTS{Raw: raw, MaxAge: -1}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(strings.ToLower(part))
		switch {
		case strings.HasPrefix(part, "max-age"):
			_, value, _ := strings.Cut(part, "=")
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				h.MaxAge = n
			}
		case part == "includesubdomains":
			h.IncludeSubDomains = true
		case part == "preload":
			h.Preload = true
		}
	}
	return h
}

// grade turns the scan into a letter and the findings that justify it.
//
// Kept as a pure function of the scan so the rules can be tested without a
// network, and so that "why did this get a B" has one place to look.
func grade(scan *TLSScan) {
	findings := scan.Findings
	worst := gradeA

	demote := func(to int, f ScanFinding) {
		findings = append(findings, f)
		if to > worst {
			worst = to
		}
	}

	if !scan.Reachable {
		scan.Grade, scan.Summary = "F", "Nothing answered a TLS handshake."
		scan.Findings = findings
		return
	}
	if scan.LegacyOnly {
		demote(gradeF, ScanFinding{ID: "tls.legacy-only", Level: "critical",
			Title:  "The handshake a current client makes is refused",
			Detail: "The server refused the versions and cipher suites a current client offers, and took " + scan.Negotiated + " with " + scan.CipherSuite + " when older ones were offered as well.",
			Advice: "Clients that no longer offer those — this dashboard's own TLS library among them — cannot connect. In nginx set ssl_protocols TLSv1.2 TLSv1.3 and an ssl_ciphers list with ECDHE suites, such as Mozilla's intermediate profile."})
	}
	cert := scan.Certificate
	switch {
	case cert == nil:
		demote(gradeF, ScanFinding{ID: "tls.no-cert", Level: "critical",
			Title: "No certificate was presented", Detail: "The handshake completed without one."})
	case cert.Expired:
		demote(gradeF, ScanFinding{ID: "tls.expired", Level: "critical",
			Title:  "The certificate has expired",
			Detail: fmt.Sprintf("It expired on %s.", cert.NotAfter.Format("2 January 2006")),
			Advice: "Every browser is refusing this site now. Renew it, then find out why the renewal did not run on its own."})
	case cert.Expiring:
		// Due is the Certificates page's "expiring": inside the renewal
		// window. Overdue is half of that window gone with the certificate
		// still served, which no working renewal leaves it at.
		left := time.Until(cert.NotAfter)
		window := renewalWindow(cert.NotBefore, cert.NotAfter)
		term := termShare(window, cert.NotAfter.Sub(cert.NotBefore))
		if left <= window/2 {
			demote(gradeB, ScanFinding{ID: "tls.expiring", Level: "warning",
				Title:  "The certificate expires in " + timeLeft(left),
				Detail: "Renewal is due in " + term + ", and half of that has passed with this certificate still served.",
				Advice: "Either renewal is failing, or it renewed and the proxy was never reloaded: a renewed certificate is served only after a reload. Check the renewal log, renew by hand if it failed, and reload."})
		} else {
			findings = append(findings, ScanFinding{ID: "tls.renewal-due", Level: "notice",
				Title:  "The certificate is due for renewal",
				Detail: "It expires in " + timeLeft(left) + ", inside " + term + ", when automatic renewal replaces it.",
				Advice: "Nothing to do yet if renewal is automatic. If this certificate is still served when half of that time has gone, renewal is not working."})
		}
	}
	if !scan.NameMatches && cert != nil {
		demote(gradeF, ScanFinding{ID: "tls.name-mismatch", Level: "critical",
			Title:  "The certificate is for a different name",
			Detail: "It covers " + strings.Join(cert.Domains, ", ") + ".",
			Advice: "Reissue it including " + scan.Domain + ", or point that name at the host that has a certificate for it."})
	}
	if !scan.Trusted {
		level, id := gradeF, "tls.untrusted"
		advice := "Browsers will show a warning page. Use a certificate from a public authority — certbot issues one free."
		if cert != nil && cert.SelfSigned {
			advice = "A self-signed certificate is fine for something only you reach, and a full-page warning for anybody else."
		}
		demote(level, ScanFinding{ID: id, Level: "critical",
			Title: "The chain is not trusted", Detail: scan.TrustError, Advice: advice})
	}
	if !scan.ChainComplete {
		demote(gradeB, ScanFinding{ID: "tls.incomplete-chain", Level: "warning",
			Title:  "The server did not send its intermediate certificate",
			Detail: "Only the leaf was presented.",
			Advice: "Point the proxy at fullchain.pem rather than cert.pem. Desktop browsers paper over this from cache; phones, curl and payment gateways do not."})
	}

	for _, p := range scan.Protocols {
		if p.Status != "offered" {
			continue
		}
		switch p.Name {
		case "TLS 1.0", "TLS 1.1":
			demote(gradeC, ScanFinding{ID: "tls.old-protocol." + p.Name, Level: "warning",
				Title:  p.Name + " is still offered",
				Detail: "Deprecated since 2021 and disabled in every current browser.",
				Advice: "Set ssl_protocols to TLSv1.2 TLSv1.3. Nothing that can reach this site today needs the older ones."})
		}
	}
	if protocolStatus(scan.Protocols, "TLS 1.3") == "refused" {
		demote(gradeB, ScanFinding{ID: "tls.no-13", Level: "notice",
			Title:  "TLS 1.3 is not offered",
			Detail: "The server negotiated " + scan.Negotiated + " at best.",
			Advice: "Add TLSv1.3 to ssl_protocols. It is faster and removes a whole category of downgrade problem."})
	}
	if scan.KeyType == "RSA" && scan.KeyBits > 0 && scan.KeyBits < 2048 {
		demote(gradeF, ScanFinding{ID: "tls.weak-key", Level: "critical",
			Title:  fmt.Sprintf("The key is only %d bits", scan.KeyBits),
			Detail: "Below the 2048-bit minimum every authority has enforced for a decade.",
			Advice: "Reissue with a 2048-bit RSA key or, better, an ECDSA P-256 one."})
	}
	if !scan.OCSPStapled && len(scan.OCSPServers) > 0 {
		findings = append(findings, ScanFinding{ID: "tls.no-ocsp", Level: "notice",
			Title:  "No OCSP response is stapled",
			Detail: "The certificate names an OCSP responder (" + strings.Join(scan.OCSPServers, ", ") + "), and the server is not attaching its answer.",
			Advice: "Optional: browsers work without it. In nginx it is ssl_stapling on and ssl_stapling_verify on, with a resolver set so nginx can reach the responder."})
	}

	// Only an HTTP answer is graded on HTTP: a service known to be something
	// else has nothing to fix there, and a request that got no answer says
	// nothing about headers it never received.
	if http := scan.HTTP; http != nil && http.Service == "unknown" {
		findings = append(findings, ScanFinding{ID: "http.https-error", Level: "notice",
			Title:  "HTTPS did not answer an HTTP request",
			Detail: http.HTTPSError,
			Advice: "Expected of a service that is not a website, such as a mail server. For a website, the proxy or the application behind it is failing. HSTS, the security headers and the plain-HTTP redirect need an HTTP answer, so they were not checked."})
	} else if http != nil && http.Service == "http" {
		if http.HSTS == nil {
			// A notice rather than a demotion: HSTS is what separates A from
			// A+, and letterFor reads the header itself for that.
			findings = append(findings, ScanFinding{ID: "tls.no-hsts", Level: "notice",
				Title:  "HSTS is not set",
				Detail: "No Strict-Transport-Security header.",
				Advice: "Add it with a max-age of six months. Without it, the first request a visitor makes is over plain HTTP and can be intercepted before the redirect."})
		} else if http.HSTS.MaxAge < hstsStrongMaxAge {
			findings = append(findings, ScanFinding{ID: "tls.weak-hsts", Level: "notice",
				Title:  "HSTS is set but short",
				Detail: fmt.Sprintf("max-age is %d seconds.", http.HSTS.MaxAge),
				Advice: "Six months (15552000) is what A+ asks for, and the preload list asks for a year (31536000)."})
		}
		chain := http.RedirectChain
		switch {
		case http.PlainRedirects || http.PlainError != "" || len(chain) == 0:
		case chain[len(chain)-1].Internal:
			// Where the chain ends is not known, so it is not graded: the
			// hop may be a private site's own next address, or a public site
			// sending its visitors somewhere they cannot reach.
			findings = append(findings, ScanFinding{ID: "http.redirect-internal", Level: "notice",
				Title:  "The plain-HTTP redirect was not followed",
				Detail: describePlainChain(chain),
				Advice: "Whether plain HTTP reaches HTTPS was not checked. For a public site, point the redirect at its public name."})
		default:
			demote(gradeB, ScanFinding{ID: "tls.no-redirect", Level: "warning",
				Title:  "Plain HTTP does not redirect to HTTPS",
				Detail: describePlainChain(chain),
				Advice: "Redirect port 80 to HTTPS permanently. A certificate protects nobody who arrives on the unencrypted port."})
		}
		for _, h := range http.Headers {
			if h.Present || h.Level != "important" {
				continue
			}
			findings = append(findings, ScanFinding{ID: "http.header." + h.Name, Level: "notice",
				Title: h.Name + " is not set", Detail: h.Detail})
		}
	}

	SortFindings(findings)
	scan.Findings = findings
	scan.Grade, scan.Summary = letterFor(worst, scan)
}

// describePlainChain says where a plain-HTTP chain that never reached HTTPS
// stopped, since "answered 301" of a redirect to http://www is no reason.
func describePlainChain(chain []RedirectHop) string {
	first, last := chain[0], chain[len(chain)-1]
	switch {
	case last.Internal:
		return fmt.Sprintf("%s redirects to %s, an address on this machine or its private network, where a remote site's redirect is not followed.", first.URL, last.URL)
	case last.Error != "":
		return fmt.Sprintf("%s redirects to %s, which did not answer.", first.URL, last.URL)
	case last.Status < 300 || last.Status >= 400 || last.Location == "":
		if len(chain) == 1 {
			return fmt.Sprintf("%s answered %d.", first.URL, first.Status)
		}
		return fmt.Sprintf("%s redirects to %s, which answered %d.", first.URL, last.URL, last.Status)
	}
	if base, err := url.Parse(last.URL); err == nil {
		if next, err := base.Parse(last.Location); err == nil {
			for _, hop := range chain {
				if hop.URL == next.String() {
					return fmt.Sprintf("The redirects from %s loop back to %s.", first.URL, hop.URL)
				}
			}
		}
	}
	return fmt.Sprintf("The redirects from %s stop at %s without reaching HTTPS.", first.URL, last.Location)
}

// Grade levels, worst-highest so a demotion is a max().
const (
	gradeA = iota
	gradeB
	gradeC
	gradeF
)

func letterFor(worst int, scan *TLSScan) (string, string) {
	if scan.LegacyOnly {
		return "F", "The handshake a current client makes is refused; only an older offer is taken."
	}
	switch worst {
	case gradeF:
		return "F", "Browsers will refuse or warn about this site."
	case gradeC:
		return "C", "Valid, but still offering protocol versions that were retired years ago."
	case gradeB:
		return "B", "Working correctly, with something worth fixing."
	}
	// A+ is A with the two things that make a correct configuration a
	// complete one: a long HSTS policy and no way in over plain HTTP.
	if scan.HTTP != nil && scan.HTTP.PlainRedirects &&
		scan.HTTP.HSTS != nil && scan.HTTP.HSTS.MaxAge >= hstsStrongMaxAge {
		return "A+", "Trusted, current, and HTTP-only visitors are pushed to HTTPS and kept there."
	}
	return "A", "Trusted certificate on a current protocol."
}

func protocolStatus(list []ProtocolResult, name string) string {
	for _, p := range list {
		if p.Name == name {
			return p.Status
		}
	}
	return "unknown"
}

// SortFindings orders a scan's findings worst-first, which is how they are
// rendered and is worth doing once here rather than in the client.
func SortFindings(findings []ScanFinding) {
	rank := map[string]int{"critical": 3, "warning": 2, "notice": 1}
	sort.SliceStable(findings, func(i, j int) bool {
		return rank[findings[i].Level] > rank[findings[j].Level]
	})
}
