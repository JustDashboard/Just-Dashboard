package proxysvc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// RequestTest is one request the operator sends to this machine's nginx as a
// visitor to one of its sites would, without asking DNS where that site is:
// the name goes in SNI and Host, the connection goes to loopback.
type RequestTest struct {
	URL     string          `json:"url"`
	Method  string          `json:"method"`
	Headers []RequestHeader `json:"headers"`
	// ConnectTo is the loopback address dialled, "127.0.0.1" (the default)
	// or "::1". Nothing else: the tester reaches this nginx and no one else.
	ConnectTo string `json:"connectTo,omitempty"`
}

type RequestHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RequestResult is the chain the request took: the first hop is the request
// asked for, and each later one a redirect followed on this server.
type RequestResult struct {
	ConnectTo string       `json:"connectTo"`
	Hops      []RequestHop `json:"hops"`
	// Stopped says why a redirect was not followed: "limit" (five were),
	// "loop" (a URL came round again) or "elsewhere" (the Location names no
	// site on this server, so following it would leave loopback).
	Stopped string `json:"stopped,omitempty"`
}

type RequestHop struct {
	URL    string `json:"url"`
	Method string `json:"method"`
	// Site is the nginx site whose server_name takes the host on this port.
	Site     string          `json:"site"`
	Status   int             `json:"status,omitempty"`
	Proto    string          `json:"proto,omitempty"`
	Headers  []RequestHeader `json:"headers"`
	Location string          `json:"location,omitempty"`
	// Body is at most the first 64 KiB, empty when it is not text.
	Body      string         `json:"body"`
	BodyBytes int            `json:"bodyBytes"`
	Truncated bool           `json:"truncated,omitempty"`
	Binary    bool           `json:"binary,omitempty"`
	Timings   RequestTimings `json:"timings"`
	TLS       *RequestTLS    `json:"tls,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// RequestTimings are milliseconds: the TCP connect and the handshake on their
// own, first byte and total from the start of the hop.
type RequestTimings struct {
	Connect   float64 `json:"connect"`
	TLS       float64 `json:"tls,omitempty"`
	FirstByte float64 `json:"firstByte,omitempty"`
	Total     float64 `json:"total"`
}

type RequestTLS struct {
	Version     string       `json:"version"`
	CipherSuite string       `json:"cipherSuite"`
	ALPN        string       `json:"alpn,omitempty"`
	Certificate *Certificate `json:"certificate,omitempty"`
	// Origin compares the certificate that answered with the file the
	// site's ssl_certificate names.
	Origin *Origin `json:"origin,omitempty"`
}

const (
	requestBodyLimit  = 64 << 10
	requestHeaderMax  = 32
	requestValueLimit = 8 << 10
)

var (
	requestMethods = []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"}
	headerNameRe   = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	// The URL decides Host, and the tester sends no body, so these would only
	// make the request say something other than what it is.
	reservedHeaders = []string{"host", "content-length", "transfer-encoding", "connection"}
)

// requestTarget is a URL checked against the sites: which one takes its
// host on its port, and the port.
type requestTarget struct {
	url  *url.URL
	site string
	port int
}

// localTarget accepts a URL only when an enabled nginx site takes its host
// on its port. That is what keeps the tester from being a way to have this
// server fetch anything: the dial goes to loopback on that port regardless,
// and the name must be one nginx here would route.
func localTarget(raw string, vhosts []VHost) (requestTarget, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return requestTarget{}, fmt.Errorf("%q is not a URL", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return requestTarget{}, fmt.Errorf("the URL must start with http:// or https://")
	}
	if u.User != nil {
		return requestTarget{}, fmt.Errorf("the URL must not carry a user or password; send an Authorization header instead")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return requestTarget{}, fmt.Errorf("the URL has no host")
	}
	if net.ParseIP(host) != nil {
		return requestTarget{}, fmt.Errorf("%s is an address; name one of this server's sites so nginx can route it", host)
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return requestTarget{}, fmt.Errorf("%q is not a port", p)
		}
		port = n
	}
	candidates := []VHost{}
	for _, v := range vhosts {
		if v.Kind == KindNginx && v.Enabled && listensOn(v.Listen, port) {
			candidates = append(candidates, v)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	site, _ := serverFor(candidates, host)
	if site == nil {
		return requestTarget{}, fmt.Errorf("no enabled nginx site on this server takes %s on port %d", host, port)
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return requestTarget{url: u, site: site.Name, port: port}, nil
}

func checkRequestHeaders(headers []RequestHeader) error {
	if len(headers) > requestHeaderMax {
		return fmt.Errorf("at most %d headers", requestHeaderMax)
	}
	for _, h := range headers {
		if !headerNameRe.MatchString(h.Name) {
			return fmt.Errorf("%q is not a header name", h.Name)
		}
		if slices.Contains(reservedHeaders, strings.ToLower(h.Name)) {
			return fmt.Errorf("%s is set by the request itself", h.Name)
		}
		if len(h.Value) > requestValueLimit || strings.ContainsAny(h.Value, "\r\n\x00") {
			return fmt.Errorf("the value of %s must be one line of at most 8 KiB", h.Name)
		}
	}
	return nil
}

// TestRequest sends the request to nginx on loopback and follows at most
// five redirects, each checked as the first was. A refused target is the
// error; a request that was sent and failed is a hop with an Error.
func TestRequest(ctx context.Context, req RequestTest, vhosts []VHost, certs []Certificate) (*RequestResult, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !slices.Contains(requestMethods, method) {
		return nil, fmt.Errorf("method must be one of %s", strings.Join(requestMethods, ", "))
	}
	connectTo := req.ConnectTo
	if connectTo == "" {
		connectTo = "127.0.0.1"
	}
	if connectTo != "127.0.0.1" && connectTo != "::1" {
		return nil, fmt.Errorf("the request can connect only to 127.0.0.1 or ::1")
	}
	if err := checkRequestHeaders(req.Headers); err != nil {
		return nil, err
	}
	target, err := localTarget(req.URL, vhosts)
	if err != nil {
		return nil, err
	}

	result := &RequestResult{ConnectTo: connectTo, Hops: []RequestHop{}}
	seen := map[string]bool{}
	headers := req.Headers
	for {
		seen[target.url.String()] = true
		hop := sendHop(ctx, connectTo, method, target, headers, vhosts, certs)
		result.Hops = append(result.Hops, hop)
		if hop.Location == "" || hop.Status < 300 || hop.Status > 399 {
			return result, nil
		}
		if len(result.Hops) > maxRedirectHops {
			result.Stopped = "limit"
			return result, nil
		}
		next, err := target.url.Parse(hop.Location)
		if err != nil {
			result.Stopped = "elsewhere"
			return result, nil
		}
		nextTarget, err := localTarget(next.String(), vhosts)
		if err != nil {
			result.Stopped = "elsewhere"
			return result, nil
		}
		if seen[nextTarget.url.String()] {
			result.Stopped = "loop"
			return result, nil
		}
		// A browser turns 301/302/303 into GET; 307 and 308 keep the method.
		if hop.Status != http.StatusTemporaryRedirect && hop.Status != http.StatusPermanentRedirect && method != http.MethodHead {
			method = http.MethodGet
		}
		// Credentials meant for one host are not handed to another, as curl
		// does when it follows a redirect.
		if nextTarget.url.Hostname() != target.url.Hostname() {
			headers = slices.DeleteFunc(slices.Clone(headers), func(h RequestHeader) bool {
				name := strings.ToLower(h.Name)
				return name == "authorization" || name == "cookie"
			})
		}
		target = nextTarget
	}
}

// hopClock records the trace's moments; the dial and handshake callbacks
// run on the transport's goroutines.
type hopClock struct {
	mu                                                sync.Mutex
	connStart, connDone, tlsStart, tlsDone, firstByte time.Time
}

func (c *hopClock) set(field *time.Time) {
	c.mu.Lock()
	*field = time.Now()
	c.mu.Unlock()
}

func sendHop(ctx context.Context, connectTo, method string, target requestTarget, headers []RequestHeader, vhosts []VHost, certs []Certificate) RequestHop {
	hop := RequestHop{URL: target.url.String(), Method: method, Site: target.site, Headers: []RequestHeader{}}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	port := strconv.Itoa(target.port)
	transport := &http.Transport{
		// The URL's host is only what nginx is told; every connection goes
		// to loopback on the URL's port, whatever DNS says about the name.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort(connectTo, port))
		},
		// The certificate is what is being looked at, so an untrusted one is
		// reported rather than refused.
		TLSClientConfig:     &tls.Config{ServerName: target.url.Hostname(), InsecureSkipVerify: true},
		ForceAttemptHTTP2:   true,
		DisableKeepAlives:   true,
		DisableCompression:  true,
		TLSHandshakeTimeout: 8 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:       15 * time.Second,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	clock := &hopClock{}
	trace := &httptrace.ClientTrace{
		ConnectStart:         func(string, string) { clock.set(&clock.connStart) },
		ConnectDone:          func(string, string, error) { clock.set(&clock.connDone) },
		TLSHandshakeStart:    func() { clock.set(&clock.tlsStart) },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { clock.set(&clock.tlsDone) },
		GotFirstResponseByte: func() { clock.set(&clock.firstByte) },
	}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, hop.URL, nil)
	if err != nil {
		hop.Error = err.Error()
		return hop
	}
	for _, h := range headers {
		request.Header.Add(h.Name, h.Value)
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "Just-Dashboard request tester")
	}

	start := time.Now()
	response, err := client.Do(request)
	if err == nil {
		defer response.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(response.Body, requestBodyLimit+1))
		if len(body) > requestBodyLimit {
			body, hop.Truncated = body[:requestBodyLimit], true
		}
		hop.BodyBytes = len(body)
		if utf8.Valid(body) && !slices.Contains(body, 0) {
			hop.Body = string(body)
		} else {
			hop.Binary = true
		}
		if readErr != nil {
			hop.Error = "the body was cut short: " + requestError(readErr)
		}
	}
	end := time.Now()

	clock.mu.Lock()
	hop.Timings = RequestTimings{
		Connect:   millis(clock.connStart, clock.connDone),
		TLS:       millis(clock.tlsStart, clock.tlsDone),
		FirstByte: millis(start, clock.firstByte),
		Total:     millis(start, end),
	}
	clock.mu.Unlock()

	if err != nil {
		hop.Error = requestError(err)
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			hop.Error = "no answer in time: " + hop.Error
		}
		return hop
	}
	hop.Status = response.StatusCode
	hop.Proto = response.Proto
	hop.Location = response.Header.Get("Location")
	names := make([]string, 0, len(response.Header))
	for name := range response.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range response.Header[name] {
			hop.Headers = append(hop.Headers, RequestHeader{Name: name, Value: value})
		}
	}
	if state := response.TLS; state != nil {
		hop.TLS = &RequestTLS{
			Version:     tls.VersionName(state.Version),
			CipherSuite: tls.CipherSuiteName(state.CipherSuite),
			ALPN:        state.NegotiatedProtocol,
		}
		if len(state.PeerCertificates) > 0 {
			host := target.url.Hostname()
			leaf := summarise(state.PeerCertificates[0], host, "")
			leaf.Source = "live"
			roots, _ := x509.SystemCertPool()
			if _, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
				DNSName:       host,
				Roots:         roots,
				Intermediates: intermediates(state.PeerCertificates),
			}); err != nil {
				leaf.Error = err.Error()
			}
			hop.TLS.Certificate = leaf
			hop.TLS.Origin = TraceOrigin(vhosts, certs, host, target.port, leaf)
		}
	}
	return hop
}

func millis(from, to time.Time) float64 {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return 0
	}
	return float64(to.Sub(from).Microseconds()) / 1000
}
