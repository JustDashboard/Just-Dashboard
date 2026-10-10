package proxysvc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// UpstreamState is what a check of one upstream address found.
type UpstreamState string

const (
	UpstreamUp UpstreamState = "up"
	// UpstreamRefused is a port nothing listens on, or a socket nothing
	// serves: nginx answers every request routed there with a 502.
	UpstreamRefused UpstreamState = "refused"
	// UpstreamTimeout is an address that did not answer a connect in time,
	// which nginx turns into a 504 after proxy_connect_timeout.
	UpstreamTimeout UpstreamState = "timeout"
	// UpstreamUnresolvable is a host name that does not resolve here.
	UpstreamUnresolvable UpstreamState = "unresolvable"
	// UpstreamMissing is a unix socket path with nothing at it.
	UpstreamMissing UpstreamState = "missing"
	UpstreamError   UpstreamState = "error"
	// UpstreamDynamic is a destination built from variables at request
	// time, so there is no one address to check.
	UpstreamDynamic UpstreamState = "dynamic"
)

// UpstreamTarget is one destination nginx forwards to: a proxy_pass (or
// grpc_pass, fastcgi_pass, uwsgi_pass, scgi_pass) as it resolves, one entry
// per server of a named upstream block.
type UpstreamTarget struct {
	// Site is the server block's first server_name, or for a stream its
	// first listen; the file name when it has neither.
	Site string `json:"site"`
	// Kind is "http" or "stream".
	Kind      string `json:"kind"`
	Location  string `json:"location,omitempty"`
	Directive string `json:"directive"`
	// Upstream names the upstream block the address came from, if any.
	Upstream string `json:"upstream,omitempty"`
	// Address is host:port, unix:/path, or for a dynamic target the
	// directive's argument as written.
	Address string `json:"address"`
	File    string `json:"file"`
	Line    int    `json:"line"`

	State UpstreamState `json:"state"`
	// Ms is how long the connect took, for a target that is up.
	Ms int64 `json:"ms,omitempty"`
	// Status is the answer to HEAD / for an http or https target; zero when
	// it did not answer one or is not spoken to over HTTP.
	Status int `json:"status,omitempty"`
	// Owner is the process listening on a local TCP port.
	Owner  string `json:"owner,omitempty"`
	Detail string `json:"detail,omitempty"`

	scheme string
}

var upstreamPasses = map[string]bool{
	"proxy_pass": true, "grpc_pass": true, "fastcgi_pass": true, "uwsgi_pass": true, "scgi_pass": true,
}

type upstreamServer struct{ address string }

// UpstreamTargets reads every forwarding destination from a configuration
// tree. A name that matches an upstream block of the same context (http and
// stream keep separate ones) expands to each of its servers, unix sockets
// included; a server marked down is left out, since nginx sends it nothing.
func UpstreamTargets(tree []Directive) []UpstreamTarget {
	blocks := map[string]map[string][]upstreamServer{"http": {}, "stream": {}}
	var collect func([]Directive)
	collect = func(ds []Directive) {
		for _, d := range ds {
			if d.Name == "upstream" && d.Block != nil && len(d.Args) == 1 {
				kind := upstreamKind(d.Context)
				if blocks[kind] == nil {
					continue
				}
				var servers []upstreamServer
				for _, sd := range d.Block {
					if sd.Name != "server" || len(sd.Args) == 0 || containsArg(sd.Args[1:], "down") {
						continue
					}
					servers = append(servers, upstreamServer{address: sd.Args[0]})
				}
				blocks[kind][d.Args[0]] = servers
				continue
			}
			collect(d.Block)
		}
	}
	collect(tree)

	out := []UpstreamTarget{}
	var walk func(ds []Directive, site, location string)
	walk = func(ds []Directive, site, location string) {
		for _, d := range ds {
			switch {
			case d.Name == "server" && d.Block != nil:
				kind := upstreamKind(d.Context)
				if kind == "" || len(d.Context) == 0 || d.Context[len(d.Context)-1] != kind {
					continue
				}
				walk(d.Block, serverLabel(d, kind), "")
			case d.Name == "location" && d.Block != nil:
				walk(d.Block, site, strings.Join(d.Args, " "))
			case upstreamPasses[d.Name] && d.Block == nil && len(d.Args) >= 1 && site != "":
				kind := upstreamKind(d.Context)
				base := UpstreamTarget{
					Site: site, Kind: kind, Location: location, Directive: d.Name,
					File: d.File, Line: d.Line,
				}
				out = append(out, expandPass(base, d.Args[0], blocks[kind])...)
			case d.Block != nil:
				walk(d.Block, site, location)
			}
		}
	}
	walk(tree, "", "")
	return out
}

func upstreamKind(context []string) string {
	for _, c := range context {
		if c == "http" || c == "stream" {
			return c
		}
	}
	return ""
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func serverLabel(d Directive, kind string) string {
	for _, sd := range d.Block {
		if kind == "http" && sd.Name == "server_name" {
			for _, n := range sd.Args {
				if n != "_" && n != "" {
					return n
				}
			}
		}
		if kind == "stream" && sd.Name == "listen" && len(sd.Args) > 0 {
			return sd.Args[0]
		}
	}
	return filepath.Base(d.File)
}

// expandPass turns one pass directive's argument into its addresses.
func expandPass(base UpstreamTarget, arg string, blocks map[string][]upstreamServer) []UpstreamTarget {
	if strings.Contains(arg, "$") {
		base.Address, base.State = arg, UpstreamDynamic
		return []UpstreamTarget{base}
	}
	scheme, rest := passScheme(base, arg)
	base.scheme = scheme
	if strings.HasPrefix(rest, "unix:") {
		base.Address = unixAddress(rest)
		return []UpstreamTarget{base}
	}
	host := rest
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if servers, ok := blocks[host]; ok {
		out := make([]UpstreamTarget, 0, len(servers))
		for _, sv := range servers {
			t := base
			t.Upstream = host
			if strings.HasPrefix(sv.address, "unix:") {
				t.Address = unixAddress(sv.address)
			} else {
				t.Address = withPort(sv.address, "80")
			}
			out = append(out, t)
		}
		return out
	}
	base.Address = withPort(host, defaultPort(scheme))
	return []UpstreamTarget{base}
}

// passScheme splits the scheme off a pass argument. A stream proxy_pass and
// fastcgi, scgi and a bare grpc_pass or uwsgi_pass carry none.
func passScheme(base UpstreamTarget, arg string) (string, string) {
	if i := strings.Index(arg, "://"); i > 0 {
		return strings.ToLower(arg[:i]), arg[i+3:]
	}
	switch {
	case base.Kind == "stream":
		return "tcp", arg
	case base.Directive == "grpc_pass":
		return "grpc", arg
	case base.Directive == "proxy_pass":
		return "http", arg
	}
	return strings.TrimSuffix(base.Directive, "_pass"), arg
}

// unixAddress keeps "unix:/path" and drops a URI nginx allows after a second
// colon, as in proxy_pass http://unix:/run/app.sock:/api/.
func unixAddress(rest string) string {
	path := strings.TrimPrefix(rest, "unix:")
	if i := strings.IndexByte(path, ':'); i >= 0 {
		path = path[:i]
	}
	return "unix:" + path
}

func defaultPort(scheme string) string {
	if scheme == "https" || scheme == "grpcs" {
		return "443"
	}
	return "80"
}

func withPort(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

// DialFunc opens a connection, as net.Dialer.DialContext does.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

const (
	upstreamConnectTimeout = 1500 * time.Millisecond
	upstreamAnswerTimeout  = 3 * time.Second
	upstreamConcurrency    = 8
)

// CheckUpstreams connects to every address once, however many sites share
// it, eight at a time, and asks an http or https one for HEAD /. A unix
// socket is connected to on the host, through hostexec.HostPath. Local TCP
// ports that answer are named after the process listening on them.
func CheckUpstreams(ctx context.Context, targets []UpstreamTarget, dial DialFunc) []UpstreamTarget {
	type key struct{ scheme, address string }
	results := map[key]*UpstreamTarget{}
	for _, t := range targets {
		if t.State == UpstreamDynamic {
			continue
		}
		k := key{t.scheme, t.Address}
		if results[k] == nil {
			r := t
			results[k] = &r
		}
	}

	sem := make(chan struct{}, upstreamConcurrency)
	var wg sync.WaitGroup
	for _, r := range results {
		wg.Add(1)
		sem <- struct{}{}
		go func(r *UpstreamTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			checkUpstream(ctx, r, dial)
		}(r)
	}
	wg.Wait()

	owners := localOwners(ctx)
	out := make([]UpstreamTarget, len(targets))
	for i, t := range targets {
		out[i] = t
		r := results[key{t.scheme, t.Address}]
		if r == nil {
			continue
		}
		out[i].State, out[i].Ms, out[i].Status, out[i].Detail = r.State, r.Ms, r.Status, r.Detail
		if r.State == UpstreamUp {
			out[i].Owner = owners.of(t.Address)
		}
	}
	return out
}

func checkUpstream(ctx context.Context, t *UpstreamTarget, dial DialFunc) {
	network, address := "tcp", t.Address
	if strings.HasPrefix(address, "unix:") {
		network, address = "unix", hostexec.HostPath(strings.TrimPrefix(address, "unix:"))
	}
	cctx, cancel := context.WithTimeout(ctx, upstreamConnectTimeout)
	defer cancel()
	start := time.Now()
	conn, err := dial(cctx, network, address)
	if err != nil {
		t.State, t.Detail = classifyDial(err), err.Error()
		return
	}
	t.State = UpstreamUp
	t.Ms = time.Since(start).Milliseconds()
	_ = conn.Close()

	if t.scheme == "http" || t.scheme == "https" {
		t.Status, err = headUpstream(ctx, t, network, address, dial)
		if err != nil {
			t.Detail = "connected, but no answer to HEAD /: " + err.Error()
		}
	}
}

// headUpstream asks HEAD / of an address that accepted a connection. The
// certificate is not verified: nginx does not verify its upstreams unless
// proxy_ssl_verify is on, and what is asked here is whether it answers.
func headUpstream(ctx context.Context, t *UpstreamTarget, network, address string, dial DialFunc) (int, error) {
	host := t.Address
	if network == "unix" {
		host = "localhost"
	}
	client := &http.Client{
		Timeout: upstreamAnswerTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dial(ctx, network, address)
			},
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, t.scheme+"://"+host+"/", nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func classifyDial(err error) UpstreamState {
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && !dnsErr.IsTimeout:
		return UpstreamUnresolvable
	case errors.Is(err, syscall.ECONNREFUSED):
		return UpstreamRefused
	case errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT):
		return UpstreamMissing
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return UpstreamTimeout
	}
	return UpstreamError
}

type listenerOwners struct {
	local map[string]bool
	ports map[string]map[string]string // port → address → process
}

// localOwners maps this host's listening TCP ports to their processes. The
// dashboard runs in the host's network namespace, so its interfaces and
// sockets are the server's.
func localOwners(ctx context.Context) listenerOwners {
	o := listenerOwners{local: map[string]bool{}, ports: map[string]map[string]string{}}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				o.local[ipnet.IP.String()] = true
			}
		}
	}
	listeners, err := ListListeners(ctx)
	if err != nil {
		return o
	}
	for _, l := range listeners {
		if l.Protocol != "tcp" || l.Process == "" {
			continue
		}
		port := strconv.Itoa(int(l.Port))
		if o.ports[port] == nil {
			o.ports[port] = map[string]string{}
		}
		o.ports[port][l.Address] = fmt.Sprintf("%s (pid %d)", l.Process, l.PID)
	}
	return o
}

func (o listenerOwners) of(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if host == "localhost" {
		ip = net.IPv4(127, 0, 0, 1)
	}
	if ip == nil || !(ip.IsLoopback() || o.local[ip.String()]) {
		return ""
	}
	byAddr := o.ports[port]
	if owner := byAddr[ip.String()]; owner != "" {
		return owner
	}
	for _, wildcard := range []string{"0.0.0.0", "::", "*", ""} {
		if owner := byAddr[wildcard]; owner != "" {
			return owner
		}
	}
	return ""
}

// UpstreamReport is one check of every upstream nginx loads.
type UpstreamReport struct {
	CheckedAt time.Time        `json:"checkedAt"`
	Targets   []UpstreamTarget `json:"targets"`
	// Pools are the same destinations grouped as nginx spreads requests
	// across them, with what its error logs said of each server over the
	// last hour (Evidence says which logs and how far back).
	Pools    []UpstreamPool `json:"pools"`
	Evidence *PoolEvidence  `json:"evidence,omitempty"`
}

// passiveWindow is how far back nginx's own record of failed connections is
// read for each pool.
const passiveWindow = time.Hour

// upstreamTTL bounds how often the page's polling makes the dashboard dial
// out: every signed-in account can read the report, so a read serves the
// last check rather than starting one.
const upstreamTTL = 15 * time.Second

type upstreamRun struct {
	done   chan struct{}
	report *UpstreamReport
	err    error
}

// UpstreamMonitor serves UpstreamReports, checking at most once per
// upstreamTTL, and readers that arrive during a check share it.
type UpstreamMonitor struct {
	svc     *Service
	dial    DialFunc
	resolve Resolver

	mu      sync.Mutex
	last    *UpstreamReport
	running *upstreamRun
}

func NewUpstreamMonitor(svc *Service) *UpstreamMonitor {
	return &UpstreamMonitor{svc: svc, dial: (&net.Dialer{}).DialContext, resolve: net.DefaultResolver.LookupHost}
}

// Report answers the last check while it is fresh, or runs one. fresh
// forces a check whatever the age of the last, for an operator's "check
// now"; the caller decides who may ask for that.
func (m *UpstreamMonitor) Report(ctx context.Context, fresh bool) (*UpstreamReport, error) {
	m.mu.Lock()
	if !fresh && m.last != nil && time.Since(m.last.CheckedAt) < upstreamTTL {
		last := m.last
		m.mu.Unlock()
		return last, nil
	}
	run := m.running
	if run == nil {
		run = &upstreamRun{done: make(chan struct{})}
		m.running = run
		go m.check(run)
	}
	m.mu.Unlock()

	select {
	case <-run.done:
		return run.report, run.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// check is detached from the request that started it, as the effective
// dump is, so one reader giving up does not fail the others.
func (m *UpstreamMonitor) check(run *upstreamRun) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Only nginx is read: a Caddy host has no report, and the page shows
	// nothing rather than a list that is always empty.
	var files []ConfigFile
	err := ErrNoProxy
	if hostexec.Available("nginx") {
		files, err = m.svc.EffectiveConfig(ctx)
	}
	if err == nil {
		var tree []Directive
		if tree, err = NginxTree(files); err == nil {
			targets := UpstreamTargets(tree)
			// nginx names a site by the sites-enabled link it read; the
			// pages know it by the file behind it.
			for i := range targets {
				if full, err := m.svc.ResolveConfigPath(targets[i].File); err == nil {
					targets[i].File = full
				}
			}
			checked := CheckUpstreams(ctx, targets, m.dial)
			now := time.Now()
			evidence := &PoolEvidence{Since: now.Add(-passiveWindow), Logs: requestErrorLogs(tree)}
			failures, complete := readPassive(evidence.Logs, evidence.Since)
			evidence.Complete = complete
			if len(evidence.Logs) == 0 {
				evidence.Note = "nginx writes its errors outside " + nginxLogRoot + ", which the dashboard does not read."
			}
			run.report = &UpstreamReport{CheckedAt: now, Targets: checked, Evidence: evidence,
				Pools: UpstreamPools(ctx, tree, checked, failures, m.resolve)}
		}
	}
	run.err = err

	m.mu.Lock()
	if run.report != nil {
		m.last = run.report
	}
	m.running = nil
	m.mu.Unlock()
	close(run.done)
}
