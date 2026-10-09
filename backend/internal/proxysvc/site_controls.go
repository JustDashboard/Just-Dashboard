package proxysvc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A site's application-layer controls as one reading — its service policy —
// and their measured effect.
//
// The form writes request limits, caching and the HTTP versions as nginx
// directives, and a saved file says what was asked for, not what a visitor
// gets: a cache that never stores the application's answers (it sets a
// cookie, or says private), a limit whose zone counts the CDN's address, an
// http2 switch on a port another server block owns. The reading below is
// what the network page and the site page share; the measurement sends a
// bounded set of requests to this nginx on loopback, for one of its own
// sites, and says what each control did.

// Control states.
const (
	ControlVerified      = "verified"
	ControlNotEffective  = "not-effective"
	ControlNotMeasured   = "not-measured"
	ControlNotConfigured = "not-configured"
)

// PolicyControl is one control as the site's file sets it.
type PolicyControl struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Configured is whether the site asks for it; Setting says how.
	Configured bool   `json:"configured"`
	Setting    string `json:"setting,omitempty"`
	// Support is the running build's: "built-in", "module" (built with the
	// module it needs), "missing" (built without it) or "unknown".
	Support string `json:"support"`
	// Paths are locations that set their own value.
	Paths []string `json:"paths,omitempty"`
}

// ServicePolicy is a site's controls.
type ServicePolicy struct {
	Site     string          `json:"site"`
	File     string          `json:"file"`
	Engine   string          `json:"engine,omitempty"`
	Controls []PolicyControl `json:"controls"`
}

// siteBlocks are a site file's directives, read in http context.
func (s *Service) siteBlocks(site string) (file string, tree []Directive, err error) {
	logs, err := s.SiteLogsFor(site)
	if err != nil {
		return "", nil, err
	}
	content, err := os.ReadFile(logs.File)
	if err != nil {
		return "", nil, err
	}
	tree, err = ParseNginxFile(logs.File, string(content), []string{"http"})
	return logs.File, tree, err
}

// SitePolicy reads a site's controls from its file, and whether the build
// nginx runs has what each needs.
func (s *Service) SitePolicy(ctx context.Context, site string) (*ServicePolicy, error) {
	file, tree, err := s.siteBlocks(site)
	if err != nil {
		return nil, err
	}
	policy := &ServicePolicy{Site: site, File: file}
	build, buildErr := s.nginxBuildInfo(ctx)
	if buildErr == nil {
		policy.Engine = build.version
	}
	support := func(module string, standard bool) string {
		switch {
		case buildErr != nil:
			return "unknown"
		case standard && build.without[module]:
			return "missing"
		case standard:
			return "built-in"
		case build.modules[module] != "":
			return "module"
		}
		return "missing"
	}
	zones := zoneRates(tree)
	if len(zones) == 0 {
		if files, err := s.EffectiveConfig(ctx); err == nil {
			if all, err := NginxTree(files); err == nil {
				zones = zoneRates(all)
			}
		}
	}
	rate := PolicyControl{ID: "rate-limit", Title: "Request limit", Support: support("http_limit_req_module", true)}
	conn := PolicyControl{ID: "conn-limit", Title: "Connection limit", Support: support("http_limit_conn_module", true)}
	cache := PolicyControl{ID: "proxy-cache", Title: "Response cache", Support: support("http_proxy_module", true)}
	static := PolicyControl{ID: "static-cache", Title: "Browser caching of assets", Support: "built-in"}
	h2 := PolicyControl{ID: "http2", Title: "HTTP/2", Support: support("http_v2_module", false)}
	h3 := PolicyControl{ID: "http3", Title: "HTTP/3", Support: support("http_v3_module", false)}
	for _, server := range tree {
		if server.Name != "server" {
			continue
		}
		for _, d := range server.Block {
			switch d.Name {
			case "limit_req":
				rate.Configured, rate.Setting = true, limitReqSetting(d, zones)
			case "limit_conn":
				if len(d.Args) > 1 {
					conn.Configured, conn.Setting = true, strings.Join(d.Args[1:], " ")+" at once per key"
				}
			case "proxy_cache":
				if len(d.Args) == 1 && d.Args[0] != "off" {
					cache.Configured, cache.Setting = true, "zone "+d.Args[0]
				}
			case "expires":
				if len(d.Args) > 0 && d.Args[0] != "off" {
					static.Configured, static.Setting = true, "expires "+strings.Join(d.Args, " ")
				}
			case "http2":
				if len(d.Args) == 1 && d.Args[0] == "on" {
					h2.Configured, h2.Setting = true, "http2 on"
				}
			case "http3":
				if len(d.Args) == 1 && d.Args[0] == "on" {
					h3.Configured, h3.Setting = true, "http3 on"
				}
			case "listen":
				if len(d.Args) == 0 {
					continue
				}
				for _, a := range d.Args[1:] {
					switch a {
					case "http2":
						h2.Configured, h2.Setting = true, "listen "+strings.Join(d.Args, " ")
					case "quic":
						h3.Configured, h3.Setting = true, "listen "+strings.Join(d.Args, " ")
					}
				}
			case "location":
				for _, inner := range d.Block {
					switch inner.Name {
					case "limit_req":
						rate.Paths = append(rate.Paths, strings.Join(d.Args, " ")+": "+limitReqSetting(inner, zones))
					case "proxy_cache":
						cache.Paths = append(cache.Paths, strings.Join(d.Args, " ")+": proxy_cache "+strings.Join(inner.Args, " "))
					}
				}
			}
		}
	}
	policy.Controls = []PolicyControl{rate, conn, cache, static, h2, h3}
	return policy, nil
}

// zoneRates are the limit_req zones a tree declares, by name: rate and key.
func zoneRates(tree []Directive) map[string]string {
	out := map[string]string{}
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for _, d := range ds {
			if d.Name == "limit_req_zone" {
				name, rate := "", ""
				for _, a := range d.Args {
					if v, ok := strings.CutPrefix(a, "zone="); ok {
						name, _, _ = strings.Cut(v, ":")
					}
					if v, ok := strings.CutPrefix(a, "rate="); ok {
						rate = v
					}
				}
				if name != "" {
					out[name] = rate
				}
			}
			walk(d.Block)
		}
	}
	walk(tree)
	return out
}

// limitReqSetting reads a limit_req line as its zone's rate and burst.
func limitReqSetting(d Directive, zones map[string]string) string {
	zone, burst, nodelay := "", "0", false
	for _, a := range d.Args {
		switch {
		case strings.HasPrefix(a, "zone="):
			zone = strings.TrimPrefix(a, "zone=")
		case strings.HasPrefix(a, "burst="):
			burst = strings.TrimPrefix(a, "burst=")
		case a == "nodelay":
			nodelay = true
		}
	}
	out := zones[zone]
	if out == "" {
		out = "zone " + zone
	}
	out += ", burst " + burst
	if nodelay {
		out += ", no delay"
	}
	return out
}

// ControlCheck is what measuring one control found.
type ControlCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	// Evidence is what was seen, one line a request or a handshake.
	Evidence []string `json:"evidence,omitempty"`
}

// ControlsVerification is one measurement of a site's controls.
type ControlsVerification struct {
	Site      string         `json:"site"`
	URL       string         `json:"url"`
	CheckedAt time.Time      `json:"checkedAt"`
	Requests  int            `json:"requests"`
	Policy    *ServicePolicy `json:"policy"`
	Checks    []ControlCheck `json:"checks"`
}

// VerifyOptions are what a measurement may touch: the path the requests ask
// for ("/" when empty) and an asset path for the browser cache check.
type VerifyOptions struct {
	Path  string `json:"path"`
	Asset string `json:"asset"`
}

// maxLimitRequests bounds the burst sent to trip a request limit.
const maxLimitRequests = 40

// ErrVerifyPath is a path the measurement will not ask for.
var ErrVerifyPath = errors.New("invalid path")

// VerifySiteControls measures a site's controls against this nginx on
// loopback, naming the site in SNI and Host as the request tester does.
func (s *Service) VerifySiteControls(ctx context.Context, site string, opts VerifyOptions, vhosts []VHost) (*ControlsVerification, error) {
	for _, p := range []*string{&opts.Path, &opts.Asset} {
		if *p == "" {
			continue
		}
		if !strings.HasPrefix(*p, "/") || strings.ContainsAny(*p, " \t\r\n#") || len(*p) > 512 {
			return nil, fmt.Errorf("%w: a path starts with / and holds no spaces", ErrVerifyPath)
		}
	}
	if opts.Path == "" {
		opts.Path = "/"
	}
	policy, err := s.SitePolicy(ctx, site)
	if err != nil {
		return nil, err
	}
	var vhost *VHost
	for i := range vhosts {
		if vhosts[i].Name == site && vhosts[i].Kind == KindNginx {
			vhost = &vhosts[i]
		}
	}
	if vhost == nil || !vhost.Enabled {
		return nil, fmt.Errorf("%s is not an enabled nginx site", site)
	}
	scheme, port, host := siteEndpoint(*vhost)
	if host == "" {
		return nil, fmt.Errorf("%s names no exact server_name to send in Host", site)
	}
	target, err := localTarget(fmt.Sprintf("%s://%s:%d%s", scheme, host, port, opts.Path), vhosts)
	if err != nil {
		return nil, err
	}
	if target.site != site {
		// Another enabled site wins the name on the port, and its controls
		// would be measured under this site's policy.
		return nil, fmt.Errorf("nginx answers %s on port %d from %s, not %s, so this site's controls cannot be measured there", host, port, target.site, site)
	}
	out := &ControlsVerification{Site: site, URL: target.url.String(), CheckedAt: time.Now().UTC(), Policy: policy}
	m := &measurer{target: target, scheme: scheme, port: port, host: host, out: out}
	defer func() { out.Requests = int(m.requests.Load()) }()
	// The request limit is measured last: every request the other checks
	// send counts against it, and a burst sent first would leave them
	// answered 429.
	checks := map[string]ControlCheck{}
	for _, id := range []string{"http2", "http3", "static-cache", "proxy-cache", "conn-limit", "rate-limit"} {
		var control PolicyControl
		for _, c := range policy.Controls {
			if c.ID == id {
				control = c
			}
		}
		var check ControlCheck
		switch control.ID {
		case "http2":
			check = m.http2(ctx, control)
		case "http3":
			check = m.http3(ctx, control)
		case "proxy-cache":
			check = m.cache(ctx, control)
		case "static-cache":
			check = m.static(ctx, control, opts.Asset)
		case "rate-limit":
			check = m.limit(ctx, control, s, site)
		case "conn-limit":
			check = ControlCheck{State: ControlNotConfigured, Detail: "The site sets no connection limit."}
			if control.Configured {
				check = ControlCheck{State: ControlNotMeasured, Detail: "limit_conn counts requests nginx is still answering, which a short request does not hold open; it is not measured, so its effect is unknown."}
			}
		}
		check.ID, check.Title = control.ID, control.Title
		if control.Configured && control.Support == "missing" && check.State != ControlVerified {
			check.Detail = "This nginx is built without the module it needs. " + check.Detail
		}
		checks[id] = check
	}
	for _, c := range policy.Controls {
		out.Checks = append(out.Checks, checks[c.ID])
	}
	return out, nil
}

// siteEndpoint is where a site is reached on loopback: its first TLS port,
// else its first plain one, and its first exact name.
func siteEndpoint(v VHost) (scheme string, port int, host string) {
	scheme, port = "http", 0
	for _, l := range v.Listen {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		b, ok := listenBind(fields, true)
		if !ok || b.udp {
			continue
		}
		tls := strings.Contains(" "+l+" ", " ssl ")
		if tls && scheme != "https" {
			scheme, port = "https", b.port
		} else if port == 0 {
			port = b.port
		}
	}
	if port == 0 {
		port = 80
	}
	for _, n := range v.ServerNames {
		if n != "_" && !strings.ContainsAny(n, "*~") {
			host = strings.ToLower(n)
			break
		}
	}
	return scheme, port, host
}

type measurer struct {
	target requestTarget
	scheme string
	port   int
	host   string
	out    *ControlsVerification
	// requests counts what was sent; the limit check sends at once.
	requests atomic.Int64
}

func (m *measurer) client() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	port := strconv.Itoa(m.port)
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
			},
			TLSClientConfig:    &tls.Config{ServerName: m.host, InsecureSkipVerify: true},
			DisableKeepAlives:  true,
			DisableCompression: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get asks for a path on the site and answers the response's status and
// headers; the body is read and dropped.
func (m *measurer) get(ctx context.Context, client *http.Client, path string) (int, http.Header, error) {
	u := *m.target.url
	u.Path, u.RawQuery = path, ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("User-Agent", "Just-Dashboard control check")
	response, err := client.Do(request)
	m.requests.Add(1)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, response.Header, nil
}

func (m *measurer) http2(ctx context.Context, control PolicyControl) ControlCheck {
	if m.scheme != "https" {
		state := ControlNotConfigured
		if control.Configured {
			state = ControlNotMeasured
		}
		return ControlCheck{State: state, Detail: "The site has no TLS port; browsers speak HTTP/2 only over TLS, so there is nothing to negotiate."}
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(m.port)),
		&tls.Config{ServerName: m.host, InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		return ControlCheck{State: ControlNotMeasured, Detail: "The TLS handshake failed: " + err.Error() + "."}
	}
	negotiated := conn.ConnectionState().NegotiatedProtocol
	conn.Close()
	if negotiated == "" {
		negotiated = "none (HTTP/1.1)"
	}
	evidence := []string{"ALPN offered h2 and http/1.1; nginx chose " + negotiated}
	switch {
	case control.Configured && negotiated == "h2":
		return ControlCheck{State: ControlVerified, Detail: "nginx negotiated HTTP/2 with a browser's offer.", Evidence: evidence}
	case control.Configured:
		return ControlCheck{State: ControlNotEffective, Detail: "The site turns HTTP/2 on, and nginx did not negotiate it. nginx sets HTTP/2 per address and port, so another server block on the port, or a build without the module, decides.", Evidence: evidence}
	case negotiated == "h2":
		return ControlCheck{State: ControlNotConfigured, Detail: "The site does not turn HTTP/2 on, and nginx negotiates it anyway: another server block on the same address and port turns it on for every site there.", Evidence: evidence}
	}
	return ControlCheck{State: ControlNotConfigured, Detail: "The site does not turn HTTP/2 on, and nginx did not negotiate it.", Evidence: evidence}
}

func (m *measurer) http3(ctx context.Context, control PolicyControl) ControlCheck {
	if !control.Configured {
		return ControlCheck{State: ControlNotConfigured, Detail: "The site has no QUIC listen, so it does not offer HTTP/3."}
	}
	probe := probeQUIC(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(m.port)))
	evidence := []string{}
	if probe.Answered {
		evidence = append(evidence, "UDP "+strconv.Itoa(probe.Port)+" answered a QUIC packet with versions "+strings.Join(probe.Versions, ", "))
	} else {
		evidence = append(evidence, "UDP "+strconv.Itoa(probe.Port)+": "+probe.Detail)
	}
	altSvc := ""
	if m.scheme == "https" {
		if _, header, err := m.get(ctx, m.client(), "/"); err == nil {
			altSvc = header.Get("Alt-Svc")
		}
		if altSvc != "" {
			evidence = append(evidence, "Alt-Svc: "+altSvc)
		} else {
			evidence = append(evidence, "no Alt-Svc header on the HTTPS answer")
		}
	}
	switch {
	case probe.Answered && strings.Contains(altSvc, "h3"):
		return ControlCheck{State: ControlVerified, Detail: "nginx answers QUIC on the port and advertises HTTP/3 to browsers.", Evidence: evidence}
	case probe.Answered:
		return ControlCheck{State: ControlNotEffective, Detail: "nginx answers QUIC on the port, but the HTTPS answer advertises no h3 in Alt-Svc, so browsers will not switch to it.", Evidence: evidence}
	}
	return ControlCheck{State: ControlNotEffective, Detail: "Nothing answered QUIC on the port.", Evidence: evidence}
}

func (m *measurer) cache(ctx context.Context, control PolicyControl) ControlCheck {
	if !control.Configured {
		return ControlCheck{State: ControlNotConfigured, Detail: "The site keeps no response cache."}
	}
	client := m.client()
	var statuses []string
	var last http.Header
	for range 2 {
		code, header, err := m.get(ctx, client, m.target.url.Path)
		if err != nil {
			return ControlCheck{State: ControlNotMeasured, Detail: "The request failed: " + err.Error() + "."}
		}
		status := header.Get("X-Cache-Status")
		if status == "" {
			return ControlCheck{State: ControlNotMeasured, Detail: "The site does not send X-Cache-Status, so whether nginx stored the answer cannot be read.", Evidence: []string{fmt.Sprintf("answered %d without X-Cache-Status", code)}}
		}
		statuses = append(statuses, fmt.Sprintf("%d %s", code, status))
		last = header
	}
	evidence := []string{"first request: " + statuses[0], "second request: " + statuses[1]}
	if strings.HasSuffix(statuses[1], "HIT") {
		return ControlCheck{State: ControlVerified, Detail: "nginx answered the second request from its cache.", Evidence: evidence}
	}
	reason := "nginx did not answer the second request from its cache."
	switch {
	case last.Get("Set-Cookie") != "":
		reason += " The application sets a cookie, and nginx does not store an answer that does."
	case strings.Contains(strings.ToLower(last.Get("Cache-Control")), "private"), strings.Contains(strings.ToLower(last.Get("Cache-Control")), "no-store"), strings.Contains(strings.ToLower(last.Get("Cache-Control")), "no-cache"):
		reason += " The application says Cache-Control: " + last.Get("Cache-Control") + ", which nginx obeys."
	case strings.HasSuffix(statuses[1], "BYPASS"):
		reason += " The request was sent past the cache by its bypass rules."
	default:
		reason += " Only 200, 301 and 302 answers are kept, for the time proxy_cache_valid gives them."
	}
	return ControlCheck{State: ControlNotEffective, Detail: reason, Evidence: evidence}
}

func (m *measurer) static(ctx context.Context, control PolicyControl, asset string) ControlCheck {
	if !control.Configured {
		return ControlCheck{State: ControlNotConfigured, Detail: "The site sets no browser caching for assets."}
	}
	if asset == "" {
		return ControlCheck{State: ControlNotMeasured, Detail: "Name an asset path, such as a .css or .js file, to see what browsers are told to keep."}
	}
	code, header, err := m.get(ctx, m.client(), asset)
	if err != nil {
		return ControlCheck{State: ControlNotMeasured, Detail: "The request failed: " + err.Error() + "."}
	}
	evidence := []string{fmt.Sprintf("%s answered %d, Cache-Control: %q, Expires: %q", asset, code, header.Get("Cache-Control"), header.Get("Expires"))}
	if strings.Contains(header.Get("Cache-Control"), "max-age=") && !strings.Contains(header.Get("Cache-Control"), "max-age=0") {
		return ControlCheck{State: ControlVerified, Detail: "Browsers are told to keep the asset.", Evidence: evidence}
	}
	return ControlCheck{State: ControlNotEffective, Detail: "The asset's answer tells browsers to keep nothing: its type may not be one the site's map lists, or the application set its own headers.", Evidence: evidence}
}

func (m *measurer) limit(ctx context.Context, control PolicyControl, s *Service, site string) ControlCheck {
	if !control.Configured {
		return ControlCheck{State: ControlNotConfigured, Detail: "The site sets no request limit."}
	}
	_, tree, err := s.siteBlocks(site)
	if err != nil {
		return ControlCheck{State: ControlNotMeasured, Detail: "The site's file could not be read: " + err.Error() + "."}
	}
	limit := readLimit(tree)
	if limit.zone == "" {
		return ControlCheck{State: ControlNotMeasured, Detail: "The limit's settings could not be read from the site's file."}
	}
	e := accessEval{tree: tree, source: netip.MustParseAddr("127.0.0.1")}
	if exempt, ok := e.geo(limitIdent(limit.zone) + "_limit_exempt"); ok && exempt == "1" {
		return ControlCheck{State: ControlNotMeasured, Detail: "127.0.0.1 is on the site's list of addresses no limit counts, so a request from here is never refused."}
	}
	perSecond, ok := ratePerSecond(zoneRates(tree)[limit.zone])
	if !ok {
		files, _ := s.EffectiveConfig(ctx)
		if all, err := NginxTree(files); err == nil {
			perSecond, ok = ratePerSecond(zoneRates(all)[limit.zone])
		}
	}
	if !ok {
		return ControlCheck{State: ControlNotMeasured, Detail: "The zone's rate could not be read."}
	}
	send := limit.burst + 2
	if send > maxLimitRequests {
		return ControlCheck{State: ControlNotMeasured, Detail: fmt.Sprintf("Tripping it takes %d requests at once, more than the %d a check sends.", send, maxLimitRequests)}
	}
	if !limit.nodelay && float64(limit.burst)/perSecond > 5 {
		return ControlCheck{State: ControlNotMeasured, Detail: "Without nodelay nginx holds the burst back at the zone's rate, which would keep the check waiting for more than five seconds."}
	}
	client := m.client()
	codes := map[int]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range send {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, err := m.get(ctx, client, m.target.url.Path)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				codes[0]++
				return
			}
			codes[code]++
		}()
	}
	wg.Wait()
	refused := codes[limit.status]
	evidence := []string{fmt.Sprintf("%d requests at once: %s", send, codeCounts(codes))}
	switch {
	case limit.dryRun && refused == 0:
		return ControlCheck{State: ControlVerified, Detail: "The limit runs as a dry run: nginx refused nothing and logs what it would have refused.", Evidence: evidence}
	case limit.dryRun:
		return ControlCheck{State: ControlNotEffective, Detail: fmt.Sprintf("The limit is a dry run, and nginx still answered %d.", limit.status), Evidence: evidence}
	case refused > 0:
		return ControlCheck{State: ControlVerified, Detail: fmt.Sprintf("nginx answered %d to the requests past the burst.", limit.status), Evidence: evidence}
	}
	return ControlCheck{State: ControlNotEffective, Detail: fmt.Sprintf("Every request past the burst was answered; none got %d. A key other than the address, or real_ip naming another address, may be what the zone counts.", limit.status), Evidence: evidence}
}

type siteLimit struct {
	zone    string
	burst   int
	nodelay bool
	dryRun  bool
	status  int
}

// readLimit is the server-level limit_req of a site's file.
func readLimit(tree []Directive) siteLimit {
	l := siteLimit{status: http.StatusServiceUnavailable}
	for _, server := range tree {
		if server.Name != "server" {
			continue
		}
		for _, d := range server.Block {
			switch d.Name {
			case "limit_req":
				for _, a := range d.Args {
					switch {
					case strings.HasPrefix(a, "zone="):
						l.zone = strings.TrimPrefix(a, "zone=")
					case strings.HasPrefix(a, "burst="):
						l.burst, _ = strconv.Atoi(strings.TrimPrefix(a, "burst="))
					case a == "nodelay":
						l.nodelay = true
					}
				}
			case "limit_req_status":
				if len(d.Args) == 1 {
					l.status, _ = strconv.Atoi(d.Args[0])
				}
			case "limit_req_dry_run":
				l.dryRun = len(d.Args) == 1 && d.Args[0] == "on"
			}
		}
	}
	return l
}

// ratePerSecond reads nginx's "10r/s" or "30r/m".
func ratePerSecond(rate string) (float64, bool) {
	n, unit, ok := strings.Cut(rate, "r/")
	value, err := strconv.ParseFloat(n, 64)
	if !ok || err != nil || value <= 0 {
		return 0, false
	}
	switch unit {
	case "s":
		return value, true
	case "m":
		return value / 60, true
	}
	return 0, false
}

// codeCounts says how many requests got each answer, the common ones in
// order and any other after them.
func codeCounts(codes map[int]int) string {
	var parts []string
	rest := maps.Clone(codes)
	for _, code := range []int{200, 204, 301, 302, 304, 401, 403, 404, 429, 500, 502, 503, 504} {
		if rest[code] > 0 {
			parts = append(parts, fmt.Sprintf("%d×%d", rest[code], code))
			delete(rest, code)
		}
	}
	for code, n := range rest {
		if code == 0 {
			parts = append(parts, fmt.Sprintf("%d failed", n))
			continue
		}
		parts = append(parts, fmt.Sprintf("%d×%d", n, code))
	}
	return strings.Join(parts, ", ")
}
