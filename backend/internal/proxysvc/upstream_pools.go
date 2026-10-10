package proxysvc

import (
	"context"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// How each route's traffic is spread, and what nginx itself saw of it.
//
// The upstream check says whether each address takes a connection now. A
// pool is more than its addresses: nginx picks among them by a method, sets
// one aside after it fails (max_fails within fail_timeout), and turns to a
// backup only when every primary is gone — and it writes each of those
// decisions to its error log as it makes them. A route with one address is a
// different thing again: whatever spreads its traffic beyond that address — a
// provider's load balancer, a floating address, a Kubernetes Service — is
// managed outside nginx, which sees one server whose failure is the route's.

// Balancing kinds.
const (
	// BalancingNative is an upstream block of several servers that nginx
	// itself chooses among.
	BalancingNative = "native"
	// BalancingNativeDNS is one server named by a host name that resolves to
	// several addresses: nginx resolves it when it loads and balances across
	// every address as if each were written out.
	BalancingNativeDNS = "native-dns"
	// BalancingSingle is one address. Any spreading happens past it, managed
	// by something nginx cannot see.
	BalancingSingle = "single"
)

// Pool verdicts.
const (
	PoolServing  = "serving"
	PoolDegraded = "degraded"
	PoolOnBackup = "on-backup"
	PoolDown     = "down"
	PoolUnknown  = "unknown"
)

// UpstreamPool is the servers one route, or several, forwards to, how nginx
// spreads requests across them, and what it saw them do.
type UpstreamPool struct {
	// Name is the upstream block's name; empty for a single endpoint.
	Name string `json:"name,omitempty"`
	Kind string `json:"kind"`
	// Sites are the routes forwarding here: a site's first server name, a
	// stream's first listen.
	Sites []string `json:"sites"`
	// Files are the site files forwarding here, as the pages know them.
	Files []string `json:"files"`
	// Method is nginx's: round-robin, least_conn, ip_hash, hash <key>,
	// random; empty for a single endpoint.
	Method    string `json:"method,omitempty"`
	Keepalive int    `json:"keepalive,omitempty"`
	Balancing string `json:"balancing"`
	// Provider names the managed balancer a single endpoint's host name
	// suggests, from its suffix alone.
	Provider string       `json:"provider,omitempty"`
	Members  []PoolMember `json:"members"`
	Verdict  string       `json:"verdict"`
	// NoLive counts nginx's "no live upstreams": requests it had nowhere to
	// send because it had set every server aside.
	NoLive int `json:"noLive,omitempty"`
}

// PoolMember is one server of a pool.
type PoolMember struct {
	Address     string `json:"address"`
	Weight      int    `json:"weight,omitempty"`
	MaxFails    int    `json:"maxFails,omitempty"`
	FailTimeout string `json:"failTimeout,omitempty"`
	Backup      bool   `json:"backup,omitempty"`
	// Down is a server the configuration marks down: nginx sends it nothing
	// and it is not checked.
	Down bool `json:"down,omitempty"`
	// State, Ms, Status and Owner are the upstream check's, for a server
	// that is not down.
	State  UpstreamState `json:"state,omitempty"`
	Ms     int64         `json:"ms,omitempty"`
	Status int           `json:"status,omitempty"`
	Owner  string        `json:"owner,omitempty"`
	Detail string        `json:"detail,omitempty"`
	// Resolved are the addresses a host name resolves to from here now;
	// nginx resolved it when it last loaded, which may have differed.
	Resolved []string `json:"resolved,omitempty"`
	// Failures are what nginx's error log says it met at this server over
	// the window, by kind: refused, timeout, reset, closed, disabled (set
	// aside after max_fails), other.
	Failures    map[string]int `json:"failures,omitempty"`
	LastFailure *time.Time     `json:"lastFailure,omitempty"`
}

// PoolEvidence is what nginx's error logs were read for, and how far.
type PoolEvidence struct {
	Since time.Time `json:"since"`
	// Logs are the error logs read: the main one and every one a site
	// names, inside nginx's log directory.
	Logs []string `json:"logs"`
	// Complete is false when a log's byte bound cut into the window, so
	// every count is a floor.
	Complete bool   `json:"complete"`
	Note     string `json:"note,omitempty"`
}

// passiveFailures is the failures nginx logged, by member address, and
// "no live upstreams" by block name.
type passiveFailures struct {
	byAddress map[string]map[string]int
	last      map[string]time.Time
	noLive    map[string]int
}

// failureKind is what an error-log line says nginx met at an upstream, or ""
// for a line that is not about reaching one.
func failureKind(message string) string {
	switch {
	case strings.Contains(message, "no live upstreams"):
		return "no-live"
	case strings.Contains(message, "upstream server temporarily disabled"):
		return "disabled"
	case strings.Contains(message, "Connection refused"):
		return "refused"
	case strings.Contains(message, "timed out"):
		return "timeout"
	case strings.Contains(message, "Connection reset"):
		return "reset"
	case strings.Contains(message, "prematurely closed"):
		return "closed"
	}
	return "other"
}

// readPassive reads what nginx logged about its upstreams since a moment.
func readPassive(logs []string, since time.Time) (passiveFailures, bool) {
	out := passiveFailures{byAddress: map[string]map[string]int{}, last: map[string]time.Time{}, noLive: map[string]int{}}
	complete := true
	for _, path := range logs {
		lines, reachedStart, _, err := tailLines(path, errorTailBytes)
		if err != nil {
			complete = false
			continue
		}
		windowStarted := reachedStart
		for _, raw := range lines {
			line, ok := parseErrorLine(raw)
			if !ok {
				continue
			}
			if line.at.Before(since) {
				windowStarted = true
				continue
			}
			if line.upstream == "" {
				continue
			}
			kind := failureKind(line.message)
			if kind == "no-live" {
				out.noLive[line.upstream]++
				continue
			}
			if out.byAddress[line.upstream] == nil {
				out.byAddress[line.upstream] = map[string]int{}
			}
			out.byAddress[line.upstream][kind]++
			if line.at.After(out.last[line.upstream]) {
				out.last[line.upstream] = line.at
			}
		}
		complete = complete && windowStarted
	}
	return out, complete
}

// requestErrorLogs are the error logs nginx writes request failures to: every
// error_log the tree names (main, http, server), and nginx's default where
// none is named, each confined to nginx's log directory.
func requestErrorLogs(tree []Directive) []string {
	var paths []string
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for _, d := range ds {
			if d.Name == "error_log" && len(d.Args) > 0 {
				if path, err := confineLog(d.Args[0]); err == nil && !slices.Contains(paths, path) {
					paths = append(paths, path)
				}
			}
			walk(d.Block)
		}
	}
	walk(tree)
	if len(paths) == 0 {
		if path, err := confineLog(nginxLogRoot + "/error.log"); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}

// poolBlock is one upstream block as the configuration writes it.
type poolBlock struct {
	kind, name, method string
	keepalive          int
	members            []PoolMember
}

// upstreamBlocks reads every upstream block's method and servers, down ones
// included.
func upstreamBlocks(tree []Directive) map[string]*poolBlock {
	blocks := map[string]*poolBlock{}
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for _, d := range ds {
			if d.Name != "upstream" || d.Block == nil || len(d.Args) != 1 {
				walk(d.Block)
				continue
			}
			kind := upstreamKind(d.Context)
			if kind == "" {
				continue
			}
			b := &poolBlock{kind: kind, name: d.Args[0], method: "round-robin"}
			for _, inner := range d.Block {
				switch inner.Name {
				case "server":
					if len(inner.Args) > 0 {
						b.members = append(b.members, poolMember(inner.Args, kind))
					}
				case "least_conn", "ip_hash", "random", "least_time":
					b.method = strings.TrimSpace(inner.Name + " " + strings.Join(inner.Args, " "))
				case "hash":
					b.method = "hash " + strings.Join(inner.Args, " ")
				case "keepalive":
					if len(inner.Args) > 0 {
						b.keepalive, _ = strconv.Atoi(inner.Args[0])
					}
				}
			}
			blocks[kind+"\x00"+b.name] = b
		}
	}
	walk(tree)
	return blocks
}

func poolMember(args []string, kind string) PoolMember {
	m := PoolMember{Address: args[0]}
	if !strings.HasPrefix(m.Address, "unix:") {
		m.Address = withPort(m.Address, "80")
	}
	for _, arg := range args[1:] {
		key, value, _ := strings.Cut(arg, "=")
		switch key {
		case "weight":
			m.Weight, _ = strconv.Atoi(value)
		case "max_fails":
			m.MaxFails, _ = strconv.Atoi(value)
		case "fail_timeout":
			m.FailTimeout = value
		case "backup":
			m.Backup = true
		case "down":
			m.Down = true
		}
	}
	return m
}

// providerSuffixes are host-name endings of services that balance behind one
// name of their own. A suffix is a hint, never proof.
var providerSuffixes = []struct{ suffix, provider string }{
	{".elb.amazonaws.com", "AWS Elastic Load Balancing"},
	{".cloudfront.net", "Amazon CloudFront"},
	{".azurewebsites.net", "Azure App Service"},
	{".cloudapp.azure.com", "Azure"},
	{".trafficmanager.net", "Azure Traffic Manager"},
	{".run.app", "Google Cloud Run"},
	{".appspot.com", "Google App Engine"},
	{".herokuapp.com", "Heroku"},
	{".fly.dev", "Fly.io"},
	{".onrender.com", "Render"},
	{".vercel.app", "Vercel"},
	{".netlify.app", "Netlify"},
	{".workers.dev", "Cloudflare Workers"},
	{".pages.dev", "Cloudflare Pages"},
	{".svc.cluster.local", "a Kubernetes Service"},
}

func providerOf(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range providerSuffixes {
		if strings.HasSuffix(host, p.suffix) {
			return p.provider
		}
	}
	return ""
}

// Resolver looks a host name up, as net.Resolver.LookupHost does.
type Resolver func(ctx context.Context, host string) ([]string, error)

// UpstreamPools joins the configuration's upstream blocks and direct
// destinations with the check's states, host names resolved from here and
// what nginx logged about each server. checked is CheckUpstreams' answer for
// the same tree.
func UpstreamPools(ctx context.Context, tree []Directive, checked []UpstreamTarget, failures passiveFailures, resolve Resolver) []UpstreamPool {
	blocks := upstreamBlocks(tree)
	pools := map[string]*UpstreamPool{}
	var order []string
	for _, t := range checked {
		if t.State == UpstreamDynamic {
			continue
		}
		key := t.Kind + "\x00" + t.Upstream
		if t.Upstream == "" {
			key = t.Kind + "\x00\x00" + t.Address
		}
		p := pools[key]
		if p == nil {
			p = &UpstreamPool{Kind: t.Kind, Name: t.Upstream, Sites: []string{}, Files: []string{}}
			if b := blocks[t.Kind+"\x00"+t.Upstream]; t.Upstream != "" && b != nil {
				p.Method, p.Keepalive = b.method, b.keepalive
				p.Members = slices.Clone(b.members)
			} else {
				p.Members = []PoolMember{{Address: t.Address}}
			}
			pools[key] = p
			order = append(order, key)
		}
		if !slices.Contains(p.Sites, t.Site) {
			p.Sites = append(p.Sites, t.Site)
		}
		if t.File != "" && !slices.Contains(p.Files, t.File) {
			p.Files = append(p.Files, t.File)
		}
		for i := range p.Members {
			if p.Members[i].Address == t.Address && p.Members[i].State == "" {
				p.Members[i].State, p.Members[i].Ms, p.Members[i].Status = t.State, t.Ms, t.Status
				p.Members[i].Owner, p.Members[i].Detail = t.Owner, t.Detail
			}
		}
	}
	out := make([]UpstreamPool, 0, len(order))
	for _, key := range order {
		p := pools[key]
		for i := range p.Members {
			m := &p.Members[i]
			host, port, err := net.SplitHostPort(m.Address)
			if err == nil && net.ParseIP(host) == nil && resolve != nil {
				rctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
				if addrs, err := resolve(rctx, host); err == nil {
					sort.Strings(addrs)
					m.Resolved = addrs
				}
				cancel()
			}
			// nginx names the address it connected to, so a server written
			// as a name is found under each address the name resolves to.
			keys := []string{m.Address}
			for _, addr := range m.Resolved {
				keys = append(keys, net.JoinHostPort(addr, port))
			}
			for _, key := range keys {
				for kind, n := range failures.byAddress[key] {
					if m.Failures == nil {
						m.Failures = map[string]int{}
					}
					m.Failures[kind] += n
					if last := failures.last[key]; m.LastFailure == nil || last.After(*m.LastFailure) {
						m.LastFailure = &last
					}
				}
			}
		}
		p.NoLive = failures.noLive[p.Name]
		p.Balancing = balancing(p)
		if p.Balancing == BalancingSingle {
			p.Provider = providerOf(p.Members[0].Address)
		}
		p.Verdict = poolVerdict(p.Members)
		out = append(out, *p)
	}
	return out
}

// balancing says who spreads a route's requests: nginx among a block's
// servers or a name's addresses, or something past a single address.
func balancing(p *UpstreamPool) string {
	live := 0
	for _, m := range p.Members {
		if !m.Down {
			live++
		}
	}
	if p.Name != "" && live > 1 {
		return BalancingNative
	}
	for _, m := range p.Members {
		if !m.Down && len(m.Resolved) > 1 {
			return BalancingNativeDNS
		}
	}
	if p.Name != "" && live == 0 {
		return BalancingNative
	}
	return BalancingSingle
}

// poolVerdict is what a visitor meets: served by the primaries, by fewer of
// them, by a backup, or by nothing.
func poolVerdict(members []PoolMember) string {
	var primaries, primariesUp, backupsUp, known int
	for _, m := range members {
		if m.Down || m.State == "" {
			continue
		}
		known++
		up := m.State == UpstreamUp
		switch {
		case m.Backup && up:
			backupsUp++
		case !m.Backup:
			primaries++
			if up {
				primariesUp++
			}
		}
	}
	switch {
	case known == 0:
		return PoolUnknown
	case primaries > 0 && primariesUp == primaries:
		return PoolServing
	case primariesUp > 0:
		return PoolDegraded
	case backupsUp > 0:
		return PoolOnBackup
	}
	return PoolDown
}
