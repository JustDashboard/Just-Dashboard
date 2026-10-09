package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// What a web endpoint serves, how long each step took, whether its transport
// would be trusted, and which hardening headers apply to what it serves.
// Transport trust and the HTTP answer are separate readings: a 200 over a
// certificate a browser rejects is a finding, not a pass.

type httpHop struct {
	URL       string
	Host      string
	Status    string
	Code      int
	Proto     string
	Location  string
	Remote    string
	Reused    bool
	DNS       time.Duration
	Connect   time.Duration
	TLS       time.Duration
	FirstByte time.Duration
	Total     time.Duration
	TLSState  *tls.ConnectionState
	TrustErr  error
}

type httpReport struct {
	Hops     []httpHop
	Header   http.Header
	Err      error
	ErrStage string
	ErrURL   string
	Elapsed  time.Duration
}

// httpTransport is replaced in tests to reach loopback fixtures by name.
var httpTransport = func() *http.Transport {
	return &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		DisableKeepAlives: true,
		Proxy:             nil,
	}
}

// trustRoots is the pool a presented chain is verified against; nil is the
// system pool.
var trustRoots *x509.CertPool

func verifyChain(host string, state *tls.ConnectionState) ([][]*x509.Certificate, error) {
	if state == nil || len(state.PeerCertificates) == 0 {
		return nil, errors.New("the server presented no certificate")
	}
	roots := trustRoots
	if roots == nil {
		roots, _ = x509.SystemCertPool()
	}
	inter := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	return state.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: strings.Trim(host, "[]"), Roots: roots, Intermediates: inter})
}

// runHTTPChain follows up to ten redirects by hand, timing each request's
// stages with httptrace.
func runHTTPChain(ctx context.Context, target string, port int) *httpReport {
	scheme := "https"
	if port == 80 {
		scheme = "http"
	}
	report := &httpReport{}
	client := &http.Client{
		Timeout:       15 * time.Second,
		Transport:     httpTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	began := time.Now()
	defer func() { report.Elapsed = time.Since(began) }()
	current := targetURL(scheme, target, port)
	for hop := 0; hop < 10; hop++ {
		var h httpHop
		h.URL = current
		var dnsStart, connStart, tlsStart time.Time
		var dnsDone, connDone, tlsDone, gotConn bool
		start := time.Now()
		trace := &httptrace.ClientTrace{
			DNSStart: func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
			DNSDone: func(httptrace.DNSDoneInfo) {
				h.DNS, dnsDone = time.Since(dnsStart), true
			},
			ConnectStart: func(string, string) { connStart = time.Now() },
			ConnectDone: func(_, addr string, err error) {
				if err == nil {
					h.Connect, connDone, h.Remote = time.Since(connStart), true, addr
				}
			},
			TLSHandshakeStart: func() { tlsStart = time.Now() },
			TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
				if err == nil {
					h.TLS, tlsDone = time.Since(tlsStart), true
				}
			},
			GotConn: func(info httptrace.GotConnInfo) {
				gotConn, h.Reused = true, info.Reused
				if info.Conn != nil && h.Remote == "" {
					h.Remote = info.Conn.RemoteAddr().String()
				}
			},
			GotFirstResponseByte: func() { h.FirstByte = time.Since(start) },
		}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, current, nil)
		if err != nil {
			report.Err, report.ErrStage, report.ErrURL = err, "request", current
			return report
		}
		h.Host = req.URL.Hostname()
		req.Header.Set("User-Agent", "Just-Dashboard/probe")
		resp, err := client.Do(req)
		h.Total = time.Since(start)
		if err != nil {
			report.Err, report.ErrURL = err, current
			_, literal := netipParse(h.Host)
			switch {
			case !literal && !dnsDone && !connDone:
				report.ErrStage = "dns"
			case !connDone && !gotConn:
				report.ErrStage = "connect"
			case req.URL.Scheme == "https" && !tlsDone:
				report.ErrStage = "tls"
			default:
				report.ErrStage = "response"
			}
			report.Hops = append(report.Hops, h)
			return report
		}
		h.Status, h.Code, h.Proto = resp.Status, resp.StatusCode, resp.Proto
		if resp.TLS != nil {
			state := *resp.TLS
			h.TLSState = &state
			_, h.TrustErr = verifyChain(h.Host, &state)
		}
		loc := resp.Header.Get("Location")
		if isRedirect(resp.StatusCode) && loc != "" {
			next, perr := resp.Request.URL.Parse(loc)
			resp.Body.Close()
			if perr != nil {
				report.Hops = append(report.Hops, h)
				report.Err, report.ErrStage, report.ErrURL = perr, "redirect", current
				return report
			}
			h.Location = next.String()
			report.Hops = append(report.Hops, h)
			current = next.String()
			continue
		}
		report.Header = resp.Header
		resp.Body.Close()
		report.Hops = append(report.Hops, h)
		return report
	}
	report.Err, report.ErrStage = errors.New("too many redirects"), "redirect"
	return report
}

func netipParse(host string) (string, bool) {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return host, ip != nil
}

func ms(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.Round(time.Millisecond / 10).String()
}

func (r *httpReport) final() *httpHop {
	if r.Err != nil || len(r.Hops) == 0 {
		return nil
	}
	return &r.Hops[len(r.Hops)-1]
}

// chainText is the hop list the HTTP tools have always printed.
func (r *httpReport) chainText() string {
	var b strings.Builder
	for _, h := range r.Hops {
		if h.Status == "" {
			continue
		}
		fmt.Fprintf(&b, "GET %s\n%s\n", h.URL, h.Status)
		if h.Location != "" {
			fmt.Fprintf(&b, "  → %s\n", h.Location)
		}
	}
	return strings.TrimSpace(b.String())
}

var httpStageNames = [][2]string{{"dns", "DNS lookup"}, {"connect", "TCP connect"}, {"tls", "TLS handshake"}, {"response", "Request to first byte"}}

func describeHTTP(res *ProbeResult, r *httpReport) {
	table := ProbeTable{ID: "requests", Title: "Requests", Columns: []string{"Hop", "Request", "Status", "Address", "DNS", "Connect", "TLS", "First byte", "Total"}}
	for i, h := range r.Hops {
		status := h.Status
		if status == "" {
			status = "failed at " + r.ErrStage
		}
		table.Rows = append(table.Rows, []string{strconv.Itoa(i + 1), h.URL, status, h.Remote, ms(h.DNS), ms(h.Connect), ms(h.TLS), ms(h.FirstByte), ms(h.Total)})
	}
	if len(table.Rows) > 0 {
		table.Note = "Times are per request from this host; an empty DNS cell means a literal address or a reused answer."
		res.Tables = append(res.Tables, table)
	}
	if len(r.Hops) > 0 {
		first := r.Hops[0]
		_, literal := netipParse(first.Host)
		https := strings.HasPrefix(first.URL, "https:")
		for _, stage := range httpStageNames {
			st := ProbeStage{ID: stage[0], Label: stage[1], Status: StagePassed}
			switch stage[0] {
			case "dns":
				st.Duration = ms(first.DNS)
				if literal {
					st.Status, st.Detail = StageSkipped, "literal address"
				}
			case "connect":
				st.Duration, st.Detail = ms(first.Connect), first.Remote
			case "tls":
				st.Duration = ms(first.TLS)
				if !https {
					st.Status, st.Detail = StageSkipped, "plain HTTP"
				}
			case "response":
				st.Duration, st.Detail = ms(first.FirstByte), first.Status
			}
			if len(r.Hops) == 1 && r.Err != nil {
				failedAt := r.ErrStage
				if failedAt == "redirect" || failedAt == "request" {
					failedAt = "response"
				}
				switch {
				case stage[0] == failedAt:
					st.Status, st.Detail, st.Duration = StageFailed, shortDialError(r.Err), ""
				case stageAfter(stage[0], failedAt):
					st.Status, st.Detail, st.Duration = StageSkipped, "not reached", ""
				}
			}
			res.Stages = append(res.Stages, st)
		}
		if first.FirstByte > 0 {
			res.metric("first_byte", "First request to first byte", float64(first.FirstByte.Microseconds())/1000, "ms")
		}
	}
	res.metric("total", "Whole chain", float64(r.Elapsed.Microseconds())/1000, "ms")
	res.metric("redirects", "Redirects followed", float64(max(0, len(r.Hops)-1)), "")
}

func stageAfter(stage, failed string) bool {
	order := map[string]int{"dns": 0, "connect": 1, "tls": 2, "response": 3}
	return order[stage] > order[failed]
}

// transportTrust reads the last HTTPS hop's chain trust, independent of the
// HTTP status.
func transportTrust(r *httpReport) (string, bool, bool) {
	for i := len(r.Hops) - 1; i >= 0; i-- {
		h := r.Hops[i]
		if h.TLSState == nil {
			continue
		}
		if h.TrustErr != nil {
			return "not trusted for " + h.Host + ": " + h.TrustErr.Error(), false, true
		}
		return "trusted for " + h.Host + " by this host's certificate store", true, true
	}
	return "", false, false
}

// HTTPCheck asks a host what it serves: status, redirects, a few headers,
// each request's timing stages and, separately, whether its certificate would
// be trusted. The transport skips verification so an untrusted certificate
// still yields an answer about the response.
func (s *Service) HTTPCheck(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	scheme := "https"
	if port == 80 {
		scheme = "http"
	}
	res := &ProbeResult{Tool: "http", Target: targetURL(scheme, target, port)}
	r := runHTTPChain(ctx, target, port)
	res.Duration = r.Elapsed.Round(time.Millisecond).String()
	describeHTTP(res, r)
	var b strings.Builder
	b.WriteString(r.chainText())
	final := r.final()
	if final == nil {
		res.Error = r.Err.Error()
		res.Output = strings.TrimSpace(b.String() + "\n" + describeDialError(r.Err))
		res.Verdict = ProbeFailed
		res.Summary = "No HTTP response: the request failed at the " + strings.ToLower(stageLabel(r.ErrStage)) + " stage."
		res.fact("HTTP result", "no response ("+r.ErrStage+" failed)", BasisObserved)
		return res, nil
	}
	res.OK = final.Code < 400
	b.WriteString("\n\n")
	for _, h := range httpHeaders {
		if v := r.Header.Get(h); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", h, v)
		}
	}
	redirects := len(r.Hops) - 1
	result := final.Status
	if redirects > 0 {
		result += fmt.Sprintf(" after %d redirect(s)", redirects)
	}
	res.fact("HTTP result", result, BasisObserved)
	res.fact("Protocol", final.Proto, BasisObserved)
	trust, trusted, hasTLS := transportTrust(r)
	if hasTLS {
		res.fact("Transport trust", trust, BasisObserved)
		b.WriteString("\nThe HTTP answer above did not depend on the certificate; transport trust is " + trust + ". Use the TLS tool for the full chain.")
	} else {
		res.fact("Transport trust", "not applicable: no HTTPS request was made", BasisObserved)
	}
	for i := 0; i+1 < len(r.Hops); i++ {
		if strings.HasPrefix(r.Hops[i].URL, "https:") && strings.HasPrefix(r.Hops[i+1].URL, "http:") {
			res.finding("downgrade", "warning", "A redirect leaves HTTPS",
				r.Hops[i].URL+" redirects to "+r.Hops[i+1].URL+", so the next request travels unencrypted.", "Application or proxy")
		}
	}
	if hasTLS && !trusted {
		res.finding("untrusted", "warning", "The server answered, but a browser would reject its certificate",
			strings.TrimPrefix(trust, "not trusted for ")+". The HTTP status is real; visitors would see a certificate error first.", "TLS terminator")
	}
	switch {
	case final.Code >= 500:
		res.finding("server-error", "warning", "The final response is a server error", final.Status+" from "+final.URL+".", "Application")
	case final.Code >= 400:
		res.finding("client-error", "notice", "The final response is an error status", final.Status+" from "+final.URL+". It may be intended (authentication, no index page).", "Application")
	}
	res.metric("status", "Final status", float64(final.Code), "")
	res.Output = strings.TrimSpace(b.String())
	res.Summary = final.Status + " from " + final.URL
	if hasTLS {
		res.Summary += "; transport " + map[bool]string{true: "trusted", false: "not trusted"}[trusted]
	}
	res.Summary += "."
	res.Verdict = ProbeOK
	if len(res.Findings) > 0 {
		res.Verdict = ProbeFindings
	}
	return res, nil
}

func stageLabel(id string) string {
	for _, s := range httpStageNames {
		if s[0] == id {
			return s[1]
		}
	}
	return id
}

// Header applicability. A JSON API has little use for a framing policy and a
// static asset none; grading every response like a page trains people to
// ignore the grade.
const (
	applyRequired    = "required"
	applyRecommended = "recommended"
	applyOptional    = "optional"
	applyNA          = "not applicable"
)

func responseKind(header http.Header) (string, string) {
	ct := header.Get("Content-Type")
	media, _, err := mime.ParseMediaType(ct)
	if err != nil || ct == "" {
		return "unknown", "no readable Content-Type"
	}
	media = strings.ToLower(media)
	switch {
	case media == "text/html" || media == "application/xhtml+xml":
		return "page", "Content-Type " + media
	case media == "application/json" || strings.HasSuffix(media, "+json") || media == "application/xml" || media == "text/xml" ||
		strings.HasSuffix(media, "+xml") || strings.HasPrefix(media, "application/grpc") || media == "text/event-stream":
		return "api", "Content-Type " + media
	case strings.HasPrefix(media, "image/") || strings.HasPrefix(media, "font/") || strings.HasPrefix(media, "video/") ||
		strings.HasPrefix(media, "audio/") || media == "text/css" || media == "application/javascript" || media == "text/javascript" ||
		media == "application/wasm" || media == "application/octet-stream" || media == "application/pdf":
		return "asset", "Content-Type " + media
	}
	return "unknown", "Content-Type " + media
}

type headerRule struct {
	name, label string
	apply       map[string]string
}

var headerRules = []headerRule{
	{"Strict-Transport-Security", "HSTS", map[string]string{"page": applyRequired, "api": applyRequired, "asset": applyRequired, "unknown": applyRequired}},
	{"Content-Security-Policy", "Content-Security-Policy", map[string]string{"page": applyRecommended, "api": applyOptional, "asset": applyOptional, "unknown": applyRecommended}},
	{"X-Frame-Options", "Framing protection", map[string]string{"page": applyRecommended, "api": applyNA, "asset": applyNA, "unknown": applyRecommended}},
	{"X-Content-Type-Options", "X-Content-Type-Options", map[string]string{"page": applyRecommended, "api": applyRecommended, "asset": applyRecommended, "unknown": applyRecommended}},
	{"Referrer-Policy", "Referrer-Policy", map[string]string{"page": applyRecommended, "api": applyOptional, "asset": applyOptional, "unknown": applyRecommended}},
}

var headerAdvice = map[string]map[string]string{
	"Strict-Transport-Security": {"": "Send Strict-Transport-Security with max-age of at least 15552000 from the TLS terminator so later visits cannot be downgraded."},
	"Content-Security-Policy": {
		"page":    "Add a Content-Security-Policy matched to the scripts and frames this application loads; roll it out in Report-Only mode first.",
		"unknown": "If this endpoint serves pages, add a Content-Security-Policy matched to what it loads.",
	},
	"X-Frame-Options":        {"": "Send X-Frame-Options: DENY (or SAMEORIGIN), or a CSP frame-ancestors directive, unless the page is meant to be embedded."},
	"X-Content-Type-Options": {"": "Send X-Content-Type-Options: nosniff so browsers honour the declared Content-Type."},
	"Referrer-Policy":        {"": "Send a Referrer-Policy such as strict-origin-when-cross-origin so full URLs do not leak to other sites."},
}

func advice(header, kind string) string {
	if a, ok := headerAdvice[header][kind]; ok {
		return a
	}
	return headerAdvice[header][""]
}

// gradeHeaders grades only what applies to the response kind, detected from
// Content-Type unless the operator chose a profile.
func gradeHeaders(res *ProbeResult, header http.Header, https bool, profile string) (pass, graded int, text string) {
	kind, basis := responseKind(header)
	switch profile {
	case "page", "api":
		res.fact("Response kind", profile+" (chosen; detected "+kind+" from "+basis+")", BasisConfigured)
		kind = profile
	default:
		res.fact("Response kind", kind+" (detected from "+basis+")", BasisInferred)
	}
	var b strings.Builder
	table := ProbeTable{ID: "headers", Title: "Security headers for this response", Columns: []string{"Header", "Applies", "Result", "Value"}}
	csp := header.Get("Content-Security-Policy")
	for _, rule := range headerRules {
		apply := rule.apply[kind]
		value := header.Get(rule.name)
		result := "set"
		switch rule.name {
		case "Strict-Transport-Security":
			if !https {
				apply, result = applyNA, "plain HTTP cannot set it"
				break
			}
			switch age := parseMaxAge(value); {
			case value == "":
				result = "missing"
			case age < 0:
				result = "malformed"
			case age < 15552000:
				result = fmt.Sprintf("set, short (max-age=%d)", age)
			}
		case "X-Frame-Options":
			if value == "" && cspContains(csp, "frame-ancestors") {
				value, result = "covered by CSP frame-ancestors", "set"
			} else if value == "" {
				result = "missing"
			}
		case "X-Content-Type-Options":
			if value == "" {
				result = "missing"
			} else if !strings.EqualFold(strings.TrimSpace(value), "nosniff") {
				result = "wrong value"
			}
		default:
			if value == "" {
				result = "missing"
			}
		}
		table.Rows = append(table.Rows, []string{rule.label, apply, result, value})
		weak := result == "missing" || result == "malformed" || result == "wrong value"
		switch apply {
		case applyRequired, applyRecommended:
			graded++
			if !weak {
				pass++
				fmt.Fprintf(&b, "PASS  %s: %s\n", rule.name, value)
				continue
			}
			level := "notice"
			word := "WARN"
			if apply == applyRequired {
				level, word = "warning", "FAIL"
			}
			fmt.Fprintf(&b, "%s  %s is %s (%s for a %s response)\n", word, rule.name, result, apply, kind)
			res.Records = append(res.Records, rule.name)
			res.Findings = append(res.Findings, ProbeFinding{ID: "header-" + strings.ToLower(rule.name), Level: level,
				Title: rule.label + " is " + result, Detail: fmt.Sprintf("%s for a %s response. %s", strings.ToUpper(apply[:1])+apply[1:], kind, advice(rule.name, kind)),
				Owner: "Application or the proxy in front of it"})
		case applyOptional:
			fmt.Fprintf(&b, "OPT   %s %s (optional for a %s response)\n", rule.name, result, kind)
		default:
			fmt.Fprintf(&b, "N/A   %s does not apply to a %s response\n", rule.name, kind)
		}
	}
	res.Tables = append(res.Tables, table)
	for _, name := range []string{"Server", "X-Powered-By"} {
		if v := header.Get(name); v != "" {
			res.fact(name+" header", v, BasisSelfReported)
			fmt.Fprintf(&b, "INFO  %s discloses %q.\n", name, v)
		}
	}
	return pass, graded, b.String()
}

// HTTPSecurity grades the response headers that keep a browser honest, for
// the kind of response actually served.
func (s *Service) HTTPSecurity(ctx context.Context, target string, port int, profile string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "httpsec", Target: targetURL("https", target, port), Records: []string{}}
	r := runHTTPChain(ctx, target, port)
	res.Duration = r.Elapsed.Round(time.Millisecond).String()
	final := r.final()
	if final == nil {
		res.Error = r.Err.Error()
		res.Output = strings.TrimSpace(r.chainText() + "\n" + describeDialError(r.Err))
		res.Verdict, res.Summary = ProbeFailed, "No HTTP response to grade."
		return res, nil
	}
	pass, graded, text := gradeHeaders(res, r.Header, strings.HasPrefix(final.URL, "https:"), profile)
	res.Output = strings.TrimSpace(r.chainText() + "\n\n" + text + fmt.Sprintf("\n%d of %d applicable headers hardened.", pass, graded))
	res.metric("hardened", "Applicable headers set", float64(pass), "")
	res.metric("applicable", "Headers that apply", float64(graded), "")
	res.OK = final.Code < 400
	if final.Code >= 400 {
		res.finding("error-response", "notice", "The graded response is an error page", final.Status+" from "+final.URL+"; the site's normal pages can send different headers.", "Application")
	}
	res.Summary = fmt.Sprintf("%d of %d headers that apply to this response are set.", pass, graded)
	res.Verdict = ProbeOK
	if len(res.Findings) > 0 {
		res.Verdict = ProbeFindings
	}
	res.Limitations = append(res.Limitations, "Only the final response of the chain is graded; other paths of the same site can send different headers.")
	return res, nil
}

func keyDescription(c *x509.Certificate) (string, bool) {
	switch k := c.PublicKey.(type) {
	case *rsa.PublicKey:
		bits := k.N.BitLen()
		return fmt.Sprintf("RSA %d", bits), bits < 2048
	case *ecdsa.PublicKey:
		return "ECDSA " + k.Curve.Params().Name, false
	case ed25519.PublicKey:
		return "Ed25519", false
	}
	return c.PublicKeyAlgorithm.String(), false
}

func certFingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

func certName(name, full string) string { return nameOrString(name, full) }

// describeTLSState reports a negotiated session and its presented chain:
// structured rows for each certificate, the trust verdict and expiry as
// separate readings, and fingerprints a later run is compared against.
func describeTLSState(res *ProbeResult, host string, state tls.ConnectionState) string {
	res.fact("Negotiated", tlsVersionName(state.Version)+", "+tls.CipherSuiteName(state.CipherSuite), BasisObserved)
	if state.NegotiatedProtocol != "" {
		res.fact("ALPN", state.NegotiatedProtocol, BasisObserved)
	}
	res.fact("OCSP response stapled", map[bool]string{true: "yes", false: "no"}[len(state.OCSPResponse) > 0], BasisObserved)
	if len(state.PeerCertificates) == 0 {
		res.Verdict, res.Summary = ProbeFailed, "The server presented no certificate."
		return "the server presented no certificate"
	}
	leaf := state.PeerCertificates[0]
	res.Records = append(res.Records, leaf.DNSNames...)
	chains, verr := verifyChain(host, &state)
	table := ProbeTable{ID: "chain", Title: "Presented certificate chain", Columns: []string{"Position", "Subject", "Issuer", "Valid from", "Valid until", "Key", "Signature", "SHA-256"}}
	weak := []string{}
	for i, c := range state.PeerCertificates {
		position := "leaf"
		if i > 0 {
			position = "intermediate " + strconv.Itoa(i)
			if c.IsCA && c.CheckSignatureFrom(c) == nil {
				position = "root (sent by server)"
			}
		}
		key, weakKey := keyDescription(c)
		if weakKey {
			weak = append(weak, position+" uses "+key)
		}
		if strings.Contains(strings.ToUpper(c.SignatureAlgorithm.String()), "SHA1") && i == 0 {
			weak = append(weak, "the leaf is signed with "+c.SignatureAlgorithm.String())
		}
		fp := certFingerprint(c)
		table.Rows = append(table.Rows, []string{position, certName(c.Subject.CommonName, c.Subject.String()), certName(c.Issuer.CommonName, c.Issuer.String()),
			c.NotBefore.UTC().Format("2006-01-02"), c.NotAfter.UTC().Format("2006-01-02"), key, c.SignatureAlgorithm.String(), fp})
		label := "leaf"
		if i > 0 {
			label = "chain " + strconv.Itoa(i)
		}
		res.Records = append(res.Records, label+" sha256 "+fp)
	}
	if verr == nil && len(chains) > 0 {
		chain := chains[0]
		root := chain[len(chain)-1]
		if len(chain) > len(state.PeerCertificates) || certFingerprint(root) != certFingerprint(state.PeerCertificates[len(state.PeerCertificates)-1]) {
			key, _ := keyDescription(root)
			table.Rows = append(table.Rows, []string{"root (this host's trust store)", certName(root.Subject.CommonName, root.Subject.String()), certName(root.Issuer.CommonName, root.Issuer.String()),
				root.NotBefore.UTC().Format("2006-01-02"), root.NotAfter.UTC().Format("2006-01-02"), key, root.SignatureAlgorithm.String(), certFingerprint(root)})
		}
	}
	res.Tables = append(res.Tables, table)
	res.metric("chain_length", "Certificates presented", float64(len(state.PeerCertificates)), "")
	res.Records = append(res.Records, "expires "+leaf.NotAfter.UTC().Format("2006-01-02"))

	var b strings.Builder
	fmt.Fprintf(&b, "Subject:  %s\n", nameOrString(leaf.Subject.CommonName, leaf.Subject.String()))
	fmt.Fprintf(&b, "Issuer:   %s\n", nameOrString(leaf.Issuer.CommonName, leaf.Issuer.String()))
	if len(leaf.DNSNames) > 0 {
		fmt.Fprintf(&b, "Names:    %s\n", strings.Join(leaf.DNSNames, ", "))
	}
	fmt.Fprintf(&b, "Valid:    %s → %s\n", leaf.NotBefore.UTC().Format("2006-01-02"), leaf.NotAfter.UTC().Format("2006-01-02"))
	now := time.Now()
	days := leaf.NotAfter.Sub(now).Hours() / 24
	res.metric("days_left", "Days until the leaf expires", float64(int(days)), "days")
	expiry := ""
	switch {
	case now.After(leaf.NotAfter):
		expiry = fmt.Sprintf("Expired %d days ago.", int(-days))
		res.finding("expired", "critical", "The certificate has expired", "It expired on "+leaf.NotAfter.UTC().Format("2006-01-02")+"; clients refuse it.", "Certificate issuer / TLS terminator")
	case now.Before(leaf.NotBefore):
		expiry = "Not valid yet."
		res.finding("not-yet-valid", "warning", "The certificate is not valid yet", "Its validity starts "+leaf.NotBefore.UTC().Format("2006-01-02")+"; check the clock of the issuing system and this host.", "Certificate issuer / TLS terminator")
	default:
		expiry = fmt.Sprintf("Expires in %d days.", int(days))
		switch {
		case days < 14:
			res.finding("expiring", "warning", fmt.Sprintf("The certificate expires in %d days", int(days)), "Automatic renewal normally replaces a certificate about 30 days before expiry; check that it is running.", "Certificate issuer / TLS terminator")
		case days < 30:
			res.finding("expiring-soon", "notice", fmt.Sprintf("The certificate expires in %d days", int(days)), "Within the window automatic renewal normally acts in.", "Certificate issuer / TLS terminator")
		}
	}
	b.WriteString(expiry + "\n")
	// The date, not the countdown: a fact that changes daily would make every
	// saved comparison differ. Days left is a metric.
	validity := "valid"
	switch {
	case now.After(leaf.NotAfter):
		validity = "expired"
	case now.Before(leaf.NotBefore):
		validity = "not valid yet"
	}
	res.fact("Expiry", leaf.NotAfter.UTC().Format("2006-01-02")+" ("+validity+")", BasisObserved)
	trust := ""
	if verr != nil {
		trust = fmt.Sprintf("Not trusted for %s: %v", host, verr)
		res.finding("untrusted", "warning", "The chain is not trusted for "+host, verr.Error(), "Certificate issuer / TLS terminator")
	} else {
		root := chains[0][len(chains[0])-1]
		trust = fmt.Sprintf("Trusted for %s (chains to %s).", host, nameOrString(root.Subject.CommonName, root.Subject.String()))
	}
	res.fact("Trust", trust, BasisObserved)
	b.WriteString(trust + "\n")
	for _, w := range weak {
		res.finding("weak-"+strconv.Itoa(len(res.Findings)), "warning", "Weak cryptography in the chain", w+".", "Certificate issuer")
	}
	res.Summary = strings.TrimSuffix(trust, ".") + "; " + strings.ToLower(expiry[:1]) + expiry[1:]
	res.Verdict = ProbeOK
	if len(res.Findings) > 0 {
		res.Verdict = ProbeFindings
	}
	return strings.TrimSpace(b.String())
}

// TLSCert inspects the certificate a TLS port actually presents. It dials
// without verification so an expired or self-signed certificate is reported
// rather than refused; the trust check then runs on its own.
func (s *Service) TLSCert(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	addr := net.JoinHostPort(target, strconv.Itoa(port))
	res := &ProbeResult{Tool: "tls", Target: addr, Records: []string{}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	began := time.Now()
	dialer := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, ServerName: target}}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	if err != nil {
		res.Error = err.Error()
		res.Output = describeDialError(err)
		res.Verdict, res.Summary = ProbeFailed, "No TLS session: "+shortDialError(err)
		return res, nil
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		res.Error = "the server presented no certificate"
		res.Verdict, res.Summary = ProbeFailed, "The server presented no certificate."
		return res, nil
	}
	res.OK = true
	text := describeTLSState(res, target, state)
	res.Output = tlsVersionName(state.Version) + ", " + tls.CipherSuiteName(state.CipherSuite) + "\n\n" + text
	res.link("Survey the protocol versions this port accepts", toolsLink("tlssurvey", target))
	res.link("Proxy TLS report", "/proxy/tls")
	return res, nil
}

type surveyRow struct {
	Version, Result, Cipher, Detail string
	Offered, Network                bool
}

// surveyDial is replaced in tests to reach loopback fixtures.
var surveyDial = func(ctx context.Context, address string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 6 * time.Second}
	return d.DialContext(ctx, "tcp", address)
}

// classifyHandshake tells a server's refusal of a version from everything
// else that can end a handshake.
func classifyHandshake(err error) (string, string) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "remote error: tls: protocol version not supported"):
		return "rejected by the server", "protocol_version alert"
	case strings.Contains(msg, "remote error: tls: handshake failure"), strings.Contains(msg, "remote error: tls: insufficient security level"):
		return "no common parameters", "the server alerted handshake_failure; it may support this version only with cipher suites this client does not offer"
	case strings.Contains(msg, "server selected unsupported protocol version"):
		return "rejected by the server", "the server answered with a different version"
	case strings.Contains(msg, "remote error:"):
		return "rejected by the server", strings.TrimSpace(msg[strings.Index(msg, "remote error:")+13:])
	case strings.Contains(msg, "first record does not look like a TLS handshake"):
		return "not TLS", "the port answered with something other than TLS"
	case errors.Is(err, net.ErrClosed), strings.Contains(msg, "EOF"), strings.Contains(msg, "connection reset"):
		return "closed during handshake", "the server closed the connection without an alert; usually a rejection, but unconfirmed"
	case isTimeout(err):
		return "timed out", "no handshake answer before the deadline"
	}
	return "handshake failed", shortDialError(err)
}

// TLSSurvey tries each protocol version on its own connection. A TCP failure
// is reported as a network failure, never as a version the server refused.
func (s *Service) TLSSurvey(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "tlssurvey", Target: target + ":" + strconv.Itoa(port), Records: []string{}}
	address := net.JoinHostPort(target, strconv.Itoa(port))
	versions := []uint16{tls.VersionTLS10, tls.VersionTLS11, tls.VersionTLS12, tls.VersionTLS13}
	began := time.Now()
	var rows []surveyRow
	for i, v := range versions {
		row := surveyRow{Version: tlsVersionName(v)}
		attempt, cancel := context.WithTimeout(ctx, 8*time.Second)
		raw, err := surveyDial(attempt, address)
		if err != nil {
			cancel()
			_, sentence := dialFailure(err)
			row.Result, row.Detail, row.Network = "network failure", sentence, true
			rows = append(rows, row)
			if i == 0 {
				// Every version shares this TCP path; repeating the wait
				// three more times adds nothing but time.
				for _, rest := range versions[1:] {
					rows = append(rows, surveyRow{Version: tlsVersionName(rest), Result: "not tested", Detail: "TCP to the port failed", Network: true})
				}
				break
			}
			continue
		}
		conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: target, MinVersion: v, MaxVersion: v})
		_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
		err = conn.HandshakeContext(attempt)
		cancel()
		if err != nil {
			row.Result, row.Detail = classifyHandshake(err)
		} else {
			state := conn.ConnectionState()
			row.Result, row.Cipher, row.Offered = "offered", tls.CipherSuiteName(state.CipherSuite), true
			res.Records = append(res.Records, row.Version)
		}
		conn.Close()
		rows = append(rows, row)
	}
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	table := ProbeTable{ID: "versions", Title: "Protocol versions", Columns: []string{"Version", "Result", "Cipher suite", "Detail"}}
	var b strings.Builder
	var offered, rejected []string
	network, notTLS := 0, 0
	for _, row := range rows {
		table.Rows = append(table.Rows, []string{row.Version, row.Result, row.Cipher, row.Detail})
		fmt.Fprintf(&b, "%-8s %-24s %s\n", row.Version, row.Result, nonEmptyOr(row.Cipher, row.Detail))
		switch {
		case row.Offered:
			offered = append(offered, row.Version)
		case row.Network:
			network++
		case row.Result == "not TLS":
			notTLS++
		default:
			rejected = append(rejected, row.Version)
		}
	}
	res.Tables = append(res.Tables, table)
	res.fact("Client", "Go crypto/tls; for TLS 1.0–1.2 it offers only the ECDHE and AES-GCM/ChaCha20/CBC suites it implements, so a server limited to other suites shows as no common parameters", BasisConfigured)
	res.link("Proxy TLS report for sites this dashboard serves", "/proxy/tls")
	res.link("Certificate and trust for this port", toolsLink("tls", target))
	res.metric("versions_offered", "Versions offered", float64(len(offered)), "")
	switch {
	case network == len(rows):
		res.OK, res.Verdict = false, ProbeFailed
		res.Error = rows[0].Detail
		res.Summary = "No version was tested: TCP to the port failed. " + rows[0].Detail
	case len(offered) == 0 && notTLS > 0:
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "The port does not speak TLS."
	case len(offered) == 0:
		res.OK, res.Verdict = true, ProbeUnknown
		res.Summary = "No version completed a handshake; read each version's reason above."
	default:
		res.OK, res.Verdict = true, ProbeOK
		res.Summary = "Offers " + strings.Join(offered, ", ")
		if len(rejected) > 0 {
			res.Summary += "; does not complete " + strings.Join(rejected, ", ")
		}
		res.Summary += "."
		for _, legacy := range []string{"TLS 1.0", "TLS 1.1"} {
			for _, v := range offered {
				if v == legacy {
					res.finding("legacy-"+strings.ReplaceAll(strings.ToLower(legacy), " ", ""), "warning", legacy+" is still accepted",
						legacy+" is deprecated (RFC 8996). Disable it where TLS terminates unless a specific old client depends on it.", "TLS terminator")
				}
			}
		}
		if len(res.Findings) > 0 {
			res.Verdict = ProbeFindings
		}
	}
	if network > 0 && network < len(rows) {
		res.Limitations = append(res.Limitations, "Some attempts failed at the TCP stage; those versions are unknown, not refused.")
	}
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// SiteAudit is the one-click "is this site up, securely": the HTTP chain, the
// certificate and the header grade, with every finding naming who acts on it.
func (s *Service) SiteAudit(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "siteaudit", Target: strings.TrimSpace(target), Records: []string{}}
	began := time.Now()
	var b strings.Builder
	parts := []struct {
		id, label string
		run       func() (*ProbeResult, error)
	}{
		{"http", "HTTP", func() (*ProbeResult, error) { return s.HTTPCheck(ctx, target, port) }},
		{"tls", "Certificate", func() (*ProbeResult, error) {
			if port == 80 {
				return nil, nil
			}
			return s.TLSCert(ctx, target, port)
		}},
		{"headers", "Headers", func() (*ProbeResult, error) { return s.HTTPSecurity(ctx, target, port, "auto") }},
	}
	ok := true
	for _, part := range parts {
		clock := res.begin()
		sub, err := part.run()
		if err != nil {
			return nil, err
		}
		if sub == nil {
			res.Stages = append(res.Stages, ProbeStage{ID: part.id, Label: part.label, Status: StageSkipped, Detail: "plain HTTP has no certificate"})
			continue
		}
		status := StagePassed
		switch sub.Verdict {
		case ProbeFindings, ProbeUnknown:
			status = StageWarning
		case ProbeFailed:
			status = StageFailed
		}
		if !sub.OK && sub.Verdict == "" {
			status = StageFailed
		}
		clock.done(part.id, part.label, status, sub.Summary)
		// A failed stage ends the audit's success, not an error status the
		// HTTP stage already reported as a finding (a login page's 401).
		if part.id != "headers" && status == StageFailed {
			ok = false
		}
		body := sub.Output
		if part.id == "headers" {
			// The chain repeats what HTTP already showed; the grade is new.
			if i := strings.Index(body, "\n\n"); i >= 0 {
				body = body[i+2:]
			}
		}
		fmt.Fprintf(&b, "── %s ──\n%s\n\n", part.label, body)
		for _, f := range sub.Facts {
			if f.Label == "HTTP result" || f.Label == "Transport trust" || f.Label == "Response kind" || f.Label == "Expiry" || f.Label == "Protocol" {
				res.Facts = append(res.Facts, f)
			}
		}
		for _, t := range sub.Tables {
			t.ID = part.id + "-" + t.ID
			res.Tables = append(res.Tables, t)
		}
		for _, m := range sub.Metrics {
			m.Key = part.id + "_" + m.Key
			res.Metrics = append(res.Metrics, m)
		}
		for _, f := range sub.Findings {
			f.ID = part.id + "-" + f.ID
			if f.Action == "" {
				f.Action = siteAction(part.id, f)
			}
			res.Findings = append(res.Findings, f)
		}
		if part.id == "tls" {
			res.Records = append(res.Records, sub.Records...)
		}
		if part.id == "headers" {
			for _, r := range sub.Records {
				res.Records = append(res.Records, "missing "+r)
			}
		}
		res.Limitations = append(res.Limitations, sub.Limitations...)
	}
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	res.OK = ok
	res.Output = strings.TrimSpace(b.String())
	failed := 0
	for _, st := range res.Stages {
		if st.Status == StageFailed {
			failed++
		}
	}
	switch {
	case !ok:
		res.Error = "one or more checks failed — read the stages above"
		res.Verdict = ProbeFailed
		res.Summary = fmt.Sprintf("%d of %d checks failed.", failed, len(res.Stages))
	case len(res.Findings) > 0:
		res.Verdict = ProbeFindings
		res.Summary = fmt.Sprintf("The site answers; %d finding(s) to act on.", len(res.Findings))
	default:
		res.Verdict = ProbeOK
		res.Summary = "The site answers over a trusted certificate with the headers that apply to it."
	}
	return res, nil
}

func siteAction(part string, f ProbeFinding) string {
	switch part {
	case "tls":
		return "Renew or replace the certificate where TLS terminates."
	case "headers":
		return "Add the header in the application or in the proxy site that fronts it."
	}
	if f.ID == "untrusted" {
		return "Install a certificate that chains to a public root for this name."
	}
	return "Check the application's response for this URL."
}

// AttributeSiteOwner names the proxy site that serves a target, so each
// finding's owner is the configuration that can fix it. An empty site means
// this dashboard's proxy does not serve the name.
func AttributeSiteOwner(res *ProbeResult, site string) {
	if res == nil || res.Tool != "siteaudit" {
		return
	}
	if site == "" {
		res.fact("Served by", "not a site in this dashboard's proxy; findings belong to whoever operates "+res.Target, BasisInferred)
		return
	}
	href := "/proxy/sites/" + url.PathEscape(site)
	res.fact("Served by", "proxy site "+site+" on this host", BasisConfigured)
	res.link("Open proxy site "+site, href)
	for i := range res.Findings {
		f := &res.Findings[i]
		switch {
		case strings.HasPrefix(f.ID, "tls-"):
			f.Owner, f.Href = "This dashboard's proxy certificate for "+site, "/proxy/certificates"
		case strings.HasPrefix(f.ID, "headers-"):
			f.Owner, f.Href = "Proxy site "+site+" (or the application behind it)", href
			f.Action = "Add the header to proxy site " + site + ", or have the application send it."
		case f.ID == "http-untrusted":
			f.Owner, f.Href = "This dashboard's proxy certificate for "+site, "/proxy/certificates"
		default:
			f.Owner, f.Href = "Application behind proxy site "+site, href
		}
	}
}
