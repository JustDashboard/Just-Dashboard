package netx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The resolver chain of this host: what /etc/resolv.conf hands programs,
// what systemd-resolved does with it, what answers on port 53, and the one
// file the dashboard writes to change the upstreams.
//
// DNS settings and /etc/hosts are not part of the network spec. They are
// files of their own that the host reads natively (resolved.conf.d and
// hosts), so changing them does not go through commit: a boot unit restoring
// them would be a second copy of what systemd already restores. They keep the
// discipline commit has, though — take the lock, write atomically, check the
// result, and put the previous file back if the check fails — because the
// failure is the same one: a resolver that answers nothing is a server that
// cannot reach anything by name, including the registry it would update from.

// resolvConfPath is the file programs read. A variable so tests read their own.
var resolvConfPath = "/etc/resolv.conf"

// listListeners is the socket walk the port-53 listing reuses rather than
// reading /proc/net a second time. A variable so tests stand a fake behind it.
var listListeners = proxysvc.ListListeners

// dnsNow stamps the drop-in's header.
var dnsNow = time.Now

const (
	resolvedUnit    = "systemd-resolved"
	resolvedStub    = "127.0.0.53"
	resolvedStubAlt = "127.0.0.54"
)

// ResolvConf is /etc/resolv.conf as programs on this host see it.
type ResolvConf struct {
	Path string `json:"path"`
	// Mode is stub (a link to systemd-resolved's stub: every program asks
	// 127.0.0.53), uplink (a link to the file holding the uplink servers
	// directly), static (a plain file), link (a link somewhere else) or
	// missing.
	Mode string `json:"mode"`
	// Target is where a link points.
	Target string `json:"target,omitempty"`
	// ManagedBy names the software that writes the file, read from its target
	// or its header comment, so the page can say who to change it through.
	ManagedBy   string   `json:"managedBy,omitempty"`
	Nameservers []string `json:"nameservers"`
	Search      []string `json:"search"`
	Options     []string `json:"options"`
}

// ResolvedScope is what resolvectl says about the global configuration or
// one link; the two print the same lines.
type ResolvedScope struct {
	// Protocols are the flags as printed: +DefaultRoute -LLMNR -mDNS …
	Protocols []string `json:"protocols"`
	// DNSOverTLS is yes, opportunistic or no.
	DNSOverTLS string `json:"dnsOverTLS"`
	// DNSSEC is the setting: yes, allow-downgrade or no. DNSSECSupported is
	// whether the servers in use can answer it.
	DNSSEC          string   `json:"dnssec"`
	DNSSECSupported bool     `json:"dnssecSupported"`
	CurrentServer   string   `json:"currentServer,omitempty"`
	Servers         []string `json:"servers"`
	Fallback        []string `json:"fallbackServers"`
	Domains         []string `json:"domains"`
}

// ResolvedGlobal is the Global section.
type ResolvedGlobal struct {
	ResolvedScope
	// ResolvConfMode is resolved's own reading of /etc/resolv.conf: stub,
	// uplink, static, foreign or missing.
	ResolvConfMode string `json:"resolvConfMode,omitempty"`
}

// ResolvedLink is one Link section.
type ResolvedLink struct {
	ResolvedScope
	Name   string   `json:"name"`
	Index  int      `json:"index"`
	Scopes []string `json:"scopes"`
	// DefaultRoute is whether names with no better match go to this link's
	// servers.
	DefaultRoute bool `json:"defaultRoute"`
}

// ResolvedStats is `resolvectl statistics`.
type ResolvedStats struct {
	CacheSize     int64 `json:"cacheSize"`
	CacheHits     int64 `json:"cacheHits"`
	CacheMisses   int64 `json:"cacheMisses"`
	Transactions  int64 `json:"transactions"`
	Current       int64 `json:"currentTransactions"`
	Timeouts      int64 `json:"timeouts"`
	Failures      int64 `json:"failures"`
	DNSSECSecure  int64 `json:"dnssecSecure"`
	DNSSECInsec   int64 `json:"dnssecInsecure"`
	DNSSECBogus   int64 `json:"dnssecBogus"`
	DNSSECIndeter int64 `json:"dnssecIndeterminate"`
	// HitPercent is hits over hits and misses; zero before the first lookup.
	HitPercent float64 `json:"hitPercent"`
}

// ResolvedView is systemd-resolved as this host runs it.
type ResolvedView struct {
	// Installed is whether resolvectl exists; Active whether the service runs.
	Installed bool           `json:"installed"`
	Active    bool           `json:"active"`
	Global    ResolvedGlobal `json:"global"`
	// Links are the links that carry anything: a server, a search or routing
	// domain, or a DNS scope. Docker's veths and bridges print a section
	// each with nothing in it, and listing forty of them hides the one that
	// matters. LinksTotal is how many resolved printed.
	Links      []ResolvedLink `json:"links"`
	LinksTotal int            `json:"linksTotal"`
	Statistics *ResolvedStats `json:"statistics,omitempty"`
	// StatisticsError is why there are none: resolvectl statistics needs the
	// privilege to ask resolved's varlink socket.
	StatisticsError string `json:"statisticsError,omitempty"`
	Error           string `json:"error,omitempty"`
}

// DNSListener is one socket answering on port 53.
type DNSListener struct {
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	Process  string `json:"process,omitempty"`
	PID      int32  `json:"pid,omitempty"`
	// Kind classifies the owner: resolved-stub, dnsmasq, unbound, named,
	// adguardhome, pihole, docker-proxy or other.
	Kind      string `json:"kind"`
	Container string `json:"container,omitempty"`
	// Loopback is whether only this machine can ask.
	Loopback bool `json:"loopback"`
}

// DNSPort is one published port of a container.
type DNSPort struct {
	IP      string `json:"ip,omitempty"`
	Private uint16 `json:"private"`
	Public  uint16 `json:"public,omitempty"`
	Type    string `json:"type"`
}

// DNSContainer is what the API layer knows of a container, enough to tell
// whether it is an ad-blocking resolver. This package does not depend on how
// containers are discovered.
type DNSContainer struct {
	Name  string
	Image string
	State string
	Ports []DNSPort
}

// Adblock is an ad-blocking resolver running on this host.
type Adblock struct {
	// Kind is adguardhome or pihole; Name is how it is called on the page.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// RunsAs is container or process.
	RunsAs    string `json:"runsAs"`
	Container string `json:"container,omitempty"`
	Image     string `json:"image,omitempty"`
	// Answering is whether it holds port 53 on this host.
	Answering bool `json:"answering"`
	// WebPort is the port its admin page is published on, zero where none is.
	WebPort int `json:"webPort,omitempty"`
}

// ManagedDNS is the drop-in the dashboard writes.
type ManagedDNS struct {
	Path       string   `json:"path"`
	Exists     bool     `json:"exists"`
	Servers    []string `json:"servers"`
	Fallback   []string `json:"fallback"`
	Domains    []string `json:"domains"`
	DNSSEC     string   `json:"dnssec"`
	DNSOverTLS string   `json:"dnsOverTLS"`
	Cache      string   `json:"cache"`
}

// DNSPreset is a public resolver the form offers as one choice.
type DNSPreset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Product names the logo the page draws.
	Product string   `json:"product"`
	Servers []string `json:"servers"`
	// TLSName is the name its certificate carries, which DNS over TLS
	// validates the server against. TLSServers are Servers written the way
	// resolved reads them, address#name.
	TLSName       string   `json:"tlsName"`
	TLSServers    []string `json:"tlsServers"`
	BlocksAds     bool     `json:"blocksAds"`
	BlocksMalware bool     `json:"blocksMalware"`
}

// DNSView is everything the DNS page draws.
type DNSView struct {
	ResolvConf ResolvConf    `json:"resolvConf"`
	Resolved   ResolvedView  `json:"resolved"`
	Listeners  []DNSListener `json:"listeners"`
	Adblock    []Adblock     `json:"adblock"`
	Managed    ManagedDNS    `json:"managed"`
	Presets    []DNSPreset   `json:"presets"`
}

// DNSPresets are served from here so the form and the documentation agree on
// which addresses a name stands for.
func DNSPresets() []DNSPreset {
	mk := func(id, name, product, tls string, ads, malware bool, servers ...string) DNSPreset {
		p := DNSPreset{ID: id, Name: name, Product: product, Servers: servers, TLSName: tls,
			BlocksAds: ads, BlocksMalware: malware, TLSServers: make([]string, len(servers))}
		for i, s := range servers {
			p.TLSServers[i] = s + "#" + tls
		}
		return p
	}
	return []DNSPreset{
		mk("cloudflare", "Cloudflare", "cloudflare", "cloudflare-dns.com", false, false,
			"1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"),
		mk("cloudflare-malware", "Cloudflare, malware blocking", "cloudflare", "security.cloudflare-dns.com", false, true,
			"1.1.1.2", "1.0.0.2", "2606:4700:4700::1112", "2606:4700:4700::1002"),
		mk("quad9", "Quad9", "quad9", "dns.quad9.net", false, true,
			"9.9.9.9", "149.112.112.112", "2620:fe::fe", "2620:fe::9"),
		mk("google", "Google", "google", "dns.google", false, false,
			"8.8.8.8", "8.8.4.4", "2001:4860:4860::8888", "2001:4860:4860::8844"),
		mk("adguard", "AdGuard DNS, ad blocking", "adguard", "dns.adguard-dns.com", true, true,
			"94.140.14.14", "94.140.15.15", "2a10:50c0::ad1:ff", "2a10:50c0::ad2:ff"),
		mk("mullvad-adblock", "Mullvad, ad blocking", "mullvad", "adblock.dns.mullvad.net", true, true,
			"194.242.2.3", "2a07:e340::3"),
	}
}

// DNS reads the resolver chain. containers is what the API layer knows of the
// running containers, for spotting an ad-blocking resolver in one; nil on a
// host without Docker.
func (s *Service) DNS(ctx context.Context, containers []DNSContainer) (*DNSView, error) {
	return s.readDNS(ctx, containers, true), nil
}

// readDNS is DNS with the statistics call optional, because the Overview's
// summary has no use for it.
func (s *Service) readDNS(ctx context.Context, containers []DNSContainer, stats bool) *DNSView {
	v := &DNSView{
		ResolvConf: readResolvConf(resolvConfPath),
		Presets:    DNSPresets(),
		Managed:    readManagedDNS(s.paths.Resolved),
		Listeners:  []DNSListener{},
		Adblock:    []Adblock{},
	}
	v.Resolved = s.readResolved(ctx, stats)

	var all []proxysvc.Listener
	if l, err := listListeners(ctx); err == nil {
		all = l
	}
	v.Listeners = dnsListeners(all)
	v.Adblock = detectAdblock(containers, all, v.Listeners)
	return v
}

// resolvedActive is whether systemd-resolved is running here: the tool to
// talk to it exists and the unit says so.
func resolvedActive(ctx context.Context) bool {
	if !has("resolvectl") || !has("systemctl") {
		return false
	}
	// is-active exits non-zero for everything but "active" and prints the
	// state either way, so the error carries no more than the text does.
	out, _ := run(ctx, "systemctl", "is-active", resolvedUnit)
	return strings.TrimSpace(out) == "active"
}

func (s *Service) readResolved(ctx context.Context, stats bool) ResolvedView {
	v := ResolvedView{Installed: has("resolvectl"), Links: []ResolvedLink{}, Global: ResolvedGlobal{ResolvedScope: emptyScope()}}
	if !v.Installed {
		return v
	}
	v.Active = resolvedActive(ctx)
	if !v.Active {
		return v
	}
	out, err := run(ctx, "resolvectl", "status", "--no-pager")
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.Global, v.Links, v.LinksTotal = parseResolvedStatus(out)
	if !stats {
		return v
	}
	out, err = run(ctx, "resolvectl", "statistics")
	if err != nil {
		v.StatisticsError = err.Error()
		return v
	}
	if st := parseResolvedStats(out); st != nil {
		v.Statistics = st
	} else {
		v.StatisticsError = "resolvectl statistics printed nothing this page could read"
	}
	return v
}

// readResolvConf describes the file programs resolve through.
func readResolvConf(path string) ResolvConf {
	rc := ResolvConf{Path: path, Mode: "missing", Nameservers: []string{}, Search: []string{}, Options: []string{}}
	st, err := os.Lstat(path)
	if err != nil {
		return rc
	}
	rc.Mode = "static"
	if st.Mode()&fs.ModeSymlink != 0 {
		rc.Target, _ = os.Readlink(path)
		rc.Mode = "link"
		// Compared on the tail because the link is relative
		// (../run/systemd/resolve/stub-resolv.conf) and what it resolves to
		// depends on where this process sees the host's files.
		switch {
		case strings.HasSuffix(rc.Target, "systemd/resolve/stub-resolv.conf"):
			rc.Mode, rc.ManagedBy = "stub", "systemd-resolved"
		case strings.HasSuffix(rc.Target, "systemd/resolve/resolv.conf"):
			rc.Mode, rc.ManagedBy = "uplink", "systemd-resolved"
		case strings.Contains(rc.Target, "resolvconf"):
			rc.ManagedBy = "resolvconf"
		case strings.Contains(rc.Target, "NetworkManager"):
			rc.ManagedBy = "NetworkManager"
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return rc
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	header := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line[0] == '#' || line[0] == ';' {
			if header && rc.ManagedBy == "" {
				rc.ManagedBy = resolvConfWriter(line)
			}
			continue
		}
		header = false
		key, rest, _ := strings.Cut(line, " ")
		fields := strings.Fields(rest)
		switch key {
		case "nameserver":
			if len(fields) > 0 {
				rc.Nameservers = append(rc.Nameservers, fields[0])
			}
		case "search", "domain":
			rc.Search = fields
		case "options":
			rc.Options = append(rc.Options, fields...)
		}
	}
	return rc
}

// resolvConfWriter names the software a header comment admits to.
func resolvConfWriter(comment string) string {
	c := strings.ToLower(comment)
	for _, w := range []struct{ needle, name string }{
		{"networkmanager", "NetworkManager"},
		{"systemd-resolved", "systemd-resolved"},
		{"resolvconf", "resolvconf"},
		{"openresolv", "resolvconf"},
		{"dhcpcd", "dhcpcd"},
		{"dhclient", "dhclient"},
		{"tailscale", "Tailscale"},
	} {
		if strings.Contains(c, w.needle) {
			return w.name
		}
	}
	return ""
}

// resolvedLabels are the labels resolvectl status prints, lower-cased. A line
// is a labelled one only if its text before the first colon is one of these:
// an IPv6 address on a continuation line has colons of its own, and reading
// it as "fd7a" would drop the servers it belongs to.
var resolvedLabels = map[string]string{
	"protocols":            "protocols",
	"resolv.conf mode":     "mode",
	"current scopes":       "scopes",
	"current dns server":   "current",
	"dns servers":          "servers",
	"fallback dns servers": "fallback",
	"dns domain":           "domains",
	"default route":        "defaultroute",
	"defaultroute setting": "defaultroute",
	"dnsovertls setting":   "dot",
	"dnssec setting":       "dnssec",
	"dnssec supported":     "dnssecsupported",
	"dnssec nta":           "nta",
	"llmnr setting":        "llmnr",
	"multicastdns setting": "mdns",
}

// parseResolvedStatus reads `resolvectl status`. Two shapes are in the wild: a
// "Protocols:" line of +/- flags (systemd 248 and later) and one "… setting:"
// line each (before that). Both are read, and the one that is absent leaves
// the other's answer.
func parseResolvedStatus(out string) (ResolvedGlobal, []ResolvedLink, int) {
	var (
		global ResolvedGlobal
		links  []ResolvedLink
		total  int
		scope  *ResolvedScope
		link   *ResolvedLink
		key    string
	)
	global.ResolvedScope = emptyScope()
	flush := func() {
		if link == nil {
			return
		}
		total++
		if len(link.Servers) > 0 || len(link.Domains) > 0 || link.CurrentServer != "" || containsString(link.Scopes, "DNS") {
			links = append(links, *link)
		}
		link = nil
	}
	scope = &global.ResolvedScope
	for _, raw := range strings.Split(out, "\n") {
		raw = strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if isResolvedHeader(raw) {
			// A section header: Global, or "Link 2 (eth0)".
			flush()
			key = ""
			if strings.HasPrefix(raw, "Link ") {
				link = &ResolvedLink{ResolvedScope: emptyScope(), Scopes: []string{}}
				fmt.Sscanf(strings.TrimPrefix(raw, "Link "), "%d", &link.Index)
				if open, end := strings.Index(raw, "("), strings.LastIndex(raw, ")"); open >= 0 && end > open {
					link.Name = raw[open+1 : end]
				}
				scope = &link.ResolvedScope
			} else {
				scope = &global.ResolvedScope
			}
			continue
		}
		label, value, ok := strings.Cut(raw, ":")
		name, known := resolvedLabels[strings.ToLower(strings.TrimSpace(label))]
		if !ok || !known {
			// A continuation of the previous label's list.
			appendResolved(scope, link, key, strings.Fields(raw))
			continue
		}
		key = name
		value = strings.TrimSpace(value)
		switch name {
		case "mode":
			global.ResolvConfMode = value
		case "protocols":
			scope.Protocols = strings.Fields(value)
			readProtocols(scope, link, scope.Protocols)
		case "scopes":
			if link != nil && value != "none" {
				link.Scopes = strings.Fields(value)
			}
		case "current":
			scope.CurrentServer = value
		case "defaultroute":
			if link != nil {
				link.DefaultRoute = value == "yes"
			}
		case "dot":
			scope.DNSOverTLS = value
		case "dnssec":
			scope.DNSSEC = value
		case "dnssecsupported":
			scope.DNSSECSupported = value == "yes"
		case "servers", "fallback", "domains":
			appendResolved(scope, link, name, strings.Fields(value))
		}
	}
	flush()
	if links == nil {
		links = []ResolvedLink{}
	}
	return global, links, total
}

// emptyScope is a scope whose lists are empty rather than null, which is what
// the page iterates over.
func emptyScope() ResolvedScope {
	return ResolvedScope{Protocols: []string{}, Servers: []string{}, Fallback: []string{}, Domains: []string{}}
}

// isResolvedHeader tells a section header from a label line. Column zero is
// not enough: the longest label in a section (Current DNS Server) is printed
// flush left, and reading it as a header would end the section it is in.
func isResolvedHeader(line string) bool {
	if line == "Global" {
		return true
	}
	return strings.HasPrefix(line, "Link ") && strings.Contains(line, "(") && strings.HasSuffix(line, ")")
}

// readProtocols turns the flag line into the settings it states.
func readProtocols(scope *ResolvedScope, link *ResolvedLink, flags []string) {
	for _, f := range flags {
		switch {
		case f == "+DNSOverTLS":
			scope.DNSOverTLS = "yes"
		case f == "-DNSOverTLS":
			scope.DNSOverTLS = "no"
		case strings.HasPrefix(f, "DNSOverTLS="):
			scope.DNSOverTLS = strings.TrimPrefix(f, "DNSOverTLS=")
		case strings.HasPrefix(f, "DNSSEC="):
			mode, supported, _ := strings.Cut(strings.TrimPrefix(f, "DNSSEC="), "/")
			scope.DNSSEC, scope.DNSSECSupported = mode, supported == "supported"
		case f == "+DefaultRoute" && link != nil:
			link.DefaultRoute = true
		}
	}
}

func appendResolved(scope *ResolvedScope, link *ResolvedLink, key string, values []string) {
	switch key {
	case "servers":
		scope.Servers = append(scope.Servers, values...)
	case "fallback":
		scope.Fallback = append(scope.Fallback, values...)
	case "domains":
		scope.Domains = append(scope.Domains, values...)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// parseResolvedStats reads `resolvectl statistics`, which prints a heading and
// then "label: number" lines. Nil when there is no number in it at all.
func parseResolvedStats(out string) *ResolvedStats {
	st := &ResolvedStats{}
	found := false
	for _, line := range strings.Split(out, "\n") {
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		found = true
		switch strings.TrimSpace(label) {
		case "Current Transactions":
			st.Current = n
		case "Total Transactions":
			st.Transactions = n
		case "Current Cache Size":
			st.CacheSize = n
		case "Cache Hits":
			st.CacheHits = n
		case "Cache Misses":
			st.CacheMisses = n
		case "Total Timeouts":
			st.Timeouts = n
		case "Total Failure Responses":
			st.Failures = n
		case "Secure":
			st.DNSSECSecure = n
		case "Insecure":
			st.DNSSECInsec = n
		case "Bogus":
			st.DNSSECBogus = n
		case "Indeterminate":
			st.DNSSECIndeter = n
		}
	}
	if !found {
		return nil
	}
	if total := st.CacheHits + st.CacheMisses; total > 0 {
		st.HitPercent = float64(int(float64(st.CacheHits)/float64(total)*1000+0.5)) / 10
	}
	return st
}

// dnsListeners keeps the sockets on port 53 and says whose they are.
func dnsListeners(all []proxysvc.Listener) []DNSListener {
	out := []DNSListener{}
	for _, l := range all {
		if l.Port != 53 {
			continue
		}
		d := DNSListener{
			Address: l.Address, Protocol: l.Protocol, Process: l.Process, PID: l.PID,
			Loopback: l.Scope == proxysvc.ScopeLoopback,
		}
		if l.Container != nil {
			d.Container = l.Container.Name
		}
		d.Kind = dnsKind(l.Process, l.Address)
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Address != out[j].Address {
			return out[i].Address < out[j].Address
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

// dnsKind classifies the owner of a port-53 socket by the kernel's name for
// the process, which is 15 characters at most: systemd-resolved is
// "systemd-resolve" there.
func dnsKind(process, address string) string {
	p := strings.ToLower(process)
	switch {
	case strings.HasPrefix(p, "systemd-resolve"), address == resolvedStub, address == resolvedStubAlt:
		return "resolved-stub"
	case p == "dnsmasq":
		return "dnsmasq"
	case p == "unbound":
		return "unbound"
	case p == "named" || p == "bind9":
		return "named"
	case p == "adguardhome":
		return "adguardhome"
	case p == "pihole-ftl":
		return "pihole"
	case p == "docker-proxy":
		return "docker-proxy"
	}
	return "other"
}

// adblockKind matches a container image to an ad-blocking resolver. The tag
// and the registry are dropped first: ghcr.io/pi-hole/pihole:2024.07.0 is the
// same program as pihole/pihole:latest.
func adblockKind(image string) string {
	img := strings.ToLower(image)
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		img = img[:i]
	}
	if i := strings.Index(img, "@"); i >= 0 {
		img = img[:i]
	}
	switch {
	case strings.HasSuffix(img, "adguard/adguardhome"), img == "adguardhome", strings.HasSuffix(img, "/adguardhome"):
		return "adguardhome"
	case strings.HasSuffix(img, "pihole/pihole"), strings.HasSuffix(img, "pi-hole/pihole"), img == "pihole", strings.HasSuffix(img, "/pihole"):
		return "pihole"
	}
	return ""
}

var adblockNames = map[string]string{"adguardhome": "AdGuard Home", "pihole": "Pi-hole"}

// webPortPreference is which container port of a resolver is its admin page
// when it publishes several: AdGuard's setup page on 3000, then the web ports
// Pi-hole and a reverse-proxied AdGuard use.
var webPortPreference = []uint16{3000, 80, 8080, 443, 8443}

// notWebPorts are the other things a resolver publishes: DNS over TLS, DNS
// over QUIC and AdGuard's DNS-over-HTTPS listener.
var notWebPorts = map[uint16]bool{53: true, 784: true, 853: true, 5443: true, 8853: true}

// pickWebPort chooses the admin page among a resolver's TCP ports and returns
// the port it is reachable on, which for a container is the published one.
func pickWebPort(ports []DNSPort) int {
	for _, want := range webPortPreference {
		for _, p := range ports {
			if p.Private == want && p.Public != 0 {
				return int(p.Public)
			}
		}
	}
	best := 0
	for _, p := range ports {
		if p.Public == 0 || notWebPorts[p.Private] {
			continue
		}
		if best == 0 || int(p.Public) < best {
			best = int(p.Public)
		}
	}
	return best
}

// detectAdblock finds AdGuard Home and Pi-hole, as containers (by image) and
// as processes (by the kernel's name for them).
func detectAdblock(containers []DNSContainer, all []proxysvc.Listener, dns []DNSListener) []Adblock {
	out := []Adblock{}
	holds53 := func(match func(DNSListener) bool) bool {
		for _, l := range dns {
			if match(l) {
				return true
			}
		}
		return false
	}
	for _, c := range containers {
		kind := adblockKind(c.Image)
		if kind == "" || c.State != "running" {
			continue
		}
		a := Adblock{Kind: kind, Name: adblockNames[kind], RunsAs: "container", Container: c.Name, Image: c.Image}
		var web []DNSPort
		for _, p := range c.Ports {
			if p.Public == 0 {
				continue
			}
			if p.Private == 53 {
				a.Answering = true
			} else if p.Type == "tcp" || p.Type == "" {
				web = append(web, p)
			}
		}
		// A resolver on the host's own network answers without publishing.
		a.Answering = a.Answering || holds53(func(l DNSListener) bool { return l.Container == c.Name })
		a.WebPort = pickWebPort(web)
		out = append(out, a)
	}

	// As a process: one entry per program however many sockets it holds.
	seen := map[string]bool{}
	for _, l := range all {
		kind := ""
		switch strings.ToLower(l.Process) {
		case "adguardhome":
			kind = "adguardhome"
		case "pihole-ftl":
			kind = "pihole"
		}
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		a := Adblock{Kind: kind, Name: adblockNames[kind], RunsAs: "process"}
		a.Answering = holds53(func(d DNSListener) bool { return d.Kind == kind })
		var web []DNSPort
		for _, o := range all {
			if o.PID == l.PID && o.PID > 0 && o.Protocol == "tcp" && o.Port != 53 {
				web = append(web, DNSPort{Private: uint16(o.Port), Public: uint16(o.Port), Type: "tcp"})
			}
		}
		a.WebPort = pickWebPort(web)
		out = append(out, a)
	}
	return out
}

// readManagedDNS parses the drop-in the dashboard writes. The format is
// resolved.conf's: ini sections, space-separated lists, repeated keys adding
// to the list, and an empty assignment resetting it.
func readManagedDNS(path string) ManagedDNS {
	m := ManagedDNS{Path: path, Servers: []string{}, Fallback: []string{}, Domains: []string{}}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	m.Exists = true
	inResolve := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			inResolve = strings.EqualFold(line, "[Resolve]")
			continue
		}
		if !inResolve {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		fields := strings.Fields(value)
		switch strings.TrimSpace(key) {
		case "DNS":
			m.Servers = appendOrReset(m.Servers, fields)
		case "FallbackDNS":
			m.Fallback = appendOrReset(m.Fallback, fields)
		case "Domains":
			m.Domains = appendOrReset(m.Domains, fields)
		case "DNSSEC":
			m.DNSSEC = strings.TrimSpace(value)
		case "DNSOverTLS":
			m.DNSOverTLS = strings.TrimSpace(value)
		case "Cache":
			m.Cache = strings.TrimSpace(value)
		}
	}
	return m
}

func appendOrReset(list, fields []string) []string {
	if len(fields) == 0 {
		return []string{}
	}
	return append(list, fields...)
}

// DNSSummary is the Overview's one line about the resolver.
type DNSSummary struct {
	// Resolver names what answers programs' lookups: systemd-resolved, or the
	// program on port 53, or "resolv.conf" when nothing local does.
	Resolver string `json:"resolver"`
	// Mode is /etc/resolv.conf's.
	Mode      string   `json:"mode"`
	Upstreams []string `json:"upstreams"`
	// DNSOverTLS and DNSSEC are the global settings, "no" where resolved is
	// not what answers.
	DNSOverTLS string `json:"dnsOverTLS"`
	DNSSEC     string `json:"dnssec"`
	// Adblock is whether an ad-blocking resolver runs here; Custom whether
	// the dashboard's drop-in is in force.
	Adblock bool `json:"adblock"`
	Custom  bool `json:"custom"`
}

// DNSSummary reads the resolver chain without its statistics. It walks the
// listening sockets like DNS does, so a caller polling it should cache it.
func (s *Service) DNSSummary(ctx context.Context, containers []DNSContainer) DNSSummary {
	v := s.readDNS(ctx, containers, false)
	sum := DNSSummary{Mode: v.ResolvConf.Mode, DNSOverTLS: "no", DNSSEC: "no", Upstreams: []string{},
		Adblock: len(v.Adblock) > 0, Custom: v.Managed.Exists}
	if v.Resolved.Active {
		sum.Resolver = "systemd-resolved"
		sum.DNSOverTLS = orDefault(v.Resolved.Global.DNSOverTLS, "no")
		sum.DNSSEC = orDefault(v.Resolved.Global.DNSSEC, "no")
		sum.Upstreams = append(sum.Upstreams, v.Resolved.Global.Servers...)
		if len(sum.Upstreams) == 0 {
			// With nothing global, the servers in use are the links' that
			// carry the default route.
			for _, l := range v.Resolved.Links {
				if l.DefaultRoute {
					sum.Upstreams = append(sum.Upstreams, l.Servers...)
				}
			}
		}
		return sum
	}
	for _, l := range v.Listeners {
		if !l.Loopback || l.Kind == "docker-proxy" {
			continue
		}
		sum.Resolver = l.Process
		break
	}
	if sum.Resolver == "" {
		sum.Resolver = "resolv.conf"
	}
	sum.Upstreams = append(sum.Upstreams, v.ResolvConf.Nameservers...)
	return sum
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// DNSSettings is a change to the drop-in. An empty string or list leaves that
// setting at resolved's own default, which is not the same as writing the
// default: it keeps the file saying only what was chosen.
type DNSSettings struct {
	Servers  []string `json:"servers"`
	Fallback []string `json:"fallback"`
	Domains  []string `json:"domains"`
	// DNSSEC is no, allow-downgrade or yes.
	DNSSEC string `json:"dnssec"`
	// DNSOverTLS is no, opportunistic or yes.
	DNSOverTLS string `json:"dnsOverTLS"`
	// Cache is yes, no or no-negative.
	Cache string `json:"cache"`
}

// DNSApplied is what a change did.
type DNSApplied struct {
	// Verified is whether a name resolved through the stub afterwards; Via is
	// the name that did and Millis how long it took.
	Verified bool    `json:"verified"`
	Via      string  `json:"via,omitempty"`
	Millis   float64 `json:"millis,omitempty"`
	// Warning is a fact the change cannot fix, such as programs not using the
	// resolver the change is for.
	Warning string      `json:"warning,omitempty"`
	Managed *ManagedDNS `json:"managed"`
}

// UpstreamError is a change refused because the servers it names did not
// answer. The previous settings are back in force, and the sentence says so.
type UpstreamError struct{ Reason string }

func (e *UpstreamError) Error() string { return e.Reason }

const (
	maxDNSServers = 8
	maxDNSDomains = 16
)

// verifyNames are resolved after a change. Two, from unrelated operators, so
// one of them being down does not roll back a good change.
var verifyNames = []string{"cloudflare.com", "example.com"}

// SetDNS writes the drop-in and has systemd-resolved read it.
//
// The order is the proxy editor's: validate, remember the previous file,
// write, restart, check that a name still resolves, and put the previous file
// back if any step fails. A resolver pointed at servers that do not answer is
// a host that cannot resolve anything — the update server, the registry, the
// certificate authority — and no amount of UI helps once the dashboard cannot
// resolve its own way out.
func (s *Service) SetDNS(ctx context.Context, req DNSSettings, who string) (*DNSApplied, error) {
	req, err := cleanDNSSettings(req)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rc, err := s.requireResolved(ctx)
	if err != nil {
		return nil, err
	}

	prev, existed := readFileBytes(s.paths.Resolved)
	if err := writeFileAtomic(s.paths.Resolved, []byte(renderResolved(req, who)), 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", s.paths.Resolved, err)
	}
	if _, err := run(ctx, "systemctl", "restart", resolvedUnit); err != nil {
		s.restoreResolved(ctx, prev, existed)
		return nil, fmt.Errorf("systemd-resolved did not restart with the new settings, so the previous ones were put back: %w", err)
	}
	via, took, err := verifyResolution(ctx)
	if err != nil {
		s.restoreResolved(ctx, prev, existed)
		return nil, &UpstreamError{Reason: "The new upstreams did not answer: " + err.Error() +
			". The previous settings were put back, and names resolve as they did."}
	}
	m := readManagedDNS(s.paths.Resolved)
	out := &DNSApplied{Verified: true, Via: via, Millis: took, Managed: &m}
	out.Warning = resolvConfWarning(rc)
	return out, nil
}

// ResetDNS removes the drop-in, which returns the host to the servers its
// network configuration hands out. Nothing is rolled back when the default
// does not answer: that is the host's own resolver configuration, not a
// change made here, and the result says whether it resolved.
func (s *Service) ResetDNS(ctx context.Context) (*DNSApplied, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rc, err := s.requireResolved(ctx)
	if err != nil {
		return nil, err
	}
	prev, existed := readFileBytes(s.paths.Resolved)
	out := &DNSApplied{Managed: ptrTo(readManagedDNS(s.paths.Resolved)), Warning: resolvConfWarning(rc)}
	if !existed {
		return out, nil
	}
	if err := os.Remove(s.paths.Resolved); err != nil {
		return nil, fmt.Errorf("removing %s: %w", s.paths.Resolved, err)
	}
	if _, err := run(ctx, "systemctl", "restart", resolvedUnit); err != nil {
		s.restoreResolved(ctx, prev, existed)
		return nil, fmt.Errorf("systemd-resolved did not restart without the dashboard's settings, so they were put back: %w", err)
	}
	out.Managed = ptrTo(readManagedDNS(s.paths.Resolved))
	if via, took, err := verifyResolution(ctx); err == nil {
		out.Verified, out.Via, out.Millis = true, via, took
	}
	return out, nil
}

func ptrTo[T any](v T) *T { return &v }

// requireResolved refuses a change on a host where systemd-resolved is not the
// resolver: a drop-in for a service that is not running would be a file that
// says the change was made and a resolver that never read it.
func (s *Service) requireResolved(ctx context.Context) (ResolvConf, error) {
	rc := readResolvConf(resolvConfPath)
	if resolvedActive(ctx) {
		return rc, nil
	}
	by := rc.ManagedBy
	switch {
	case by == "" && rc.Mode == "static":
		by = "a plain file"
	case by == "" && rc.Mode == "missing":
		by = "nothing: the file does not exist"
	case by == "":
		by = "something other than systemd-resolved"
	}
	return rc, &ReadOnlyError{Reason: "systemd-resolved is not running here; /etc/resolv.conf is managed by " + by +
		", so the upstreams are changed there rather than on this page"}
}

// resolvConfWarning notes a host where resolved runs but programs do not ask it.
func resolvConfWarning(rc ResolvConf) string {
	if rc.Mode == "stub" || rc.Mode == "uplink" {
		return ""
	}
	return "systemd-resolved is running, but /etc/resolv.conf does not point at it, so programs on this host keep using " +
		strings.Join(rc.Nameservers, ", ") + " until it does."
}

// restoreResolved puts the previous drop-in back (or removes the one that
// was never there) and restarts resolved once more. A failure here has
// nowhere left to go but the log.
func (s *Service) restoreResolved(ctx context.Context, prev []byte, existed bool) {
	if existed {
		if err := writeFileAtomic(s.paths.Resolved, prev, 0o644); err != nil {
			s.log.Error("restoring the resolver drop-in", "path", s.paths.Resolved, "err", err)
		}
	} else if err := os.Remove(s.paths.Resolved); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Error("removing the resolver drop-in", "path", s.paths.Resolved, "err", err)
	}
	if _, err := run(ctx, "systemctl", "restart", resolvedUnit); err != nil {
		s.log.Error("restarting systemd-resolved after a rollback", "err", err)
	}
}

func readFileBytes(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	return b, err == nil
}

// verifyResolution resolves a well-known name through the stub. It succeeds
// when either name does, and reports which and how fast.
func verifyResolution(ctx context.Context) (string, float64, error) {
	var last error
	for _, name := range verifyNames {
		start := time.Now()
		answers, err := lookupVia(ctx, resolvedStub, name, "A")
		if err == nil && len(answers) > 0 {
			return name, millis(time.Since(start)), nil
		}
		if err == nil {
			err = fmt.Errorf("%s resolved to nothing", name)
		}
		last = err
	}
	return "", 0, fmt.Errorf("%s could not be resolved through the stub (%v)", verifyNames[0], last)
}

func millis(d time.Duration) float64 {
	return float64(d.Microseconds()/100) / 10
}

// cleanDNSSettings validates a request and returns it in the form it is written.
func cleanDNSSettings(req DNSSettings) (DNSSettings, error) {
	var err error
	if req.Servers, err = cleanDNSServers(req.Servers, "upstream"); err != nil {
		return req, err
	}
	if req.Fallback, err = cleanDNSServers(req.Fallback, "fallback"); err != nil {
		return req, err
	}
	if len(req.Domains) > maxDNSDomains {
		return req, fmt.Errorf("at most %d search or routing domains", maxDNSDomains)
	}
	seen := map[string]bool{}
	domains := []string{}
	for _, d := range req.Domains {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		if err := validDomainEntry(d); err != nil {
			return req, err
		}
		seen[d] = true
		domains = append(domains, d)
	}
	req.Domains = domains

	if req.DNSSEC, err = oneOf(req.DNSSEC, "DNSSEC", "no", "allow-downgrade", "yes"); err != nil {
		return req, err
	}
	if req.DNSOverTLS, err = oneOf(req.DNSOverTLS, "DNS over TLS", "no", "opportunistic", "yes"); err != nil {
		return req, err
	}
	if req.Cache, err = oneOf(req.Cache, "the cache setting", "yes", "no", "no-negative"); err != nil {
		return req, err
	}
	if req.DNSOverTLS == "yes" {
		if len(req.Servers) == 0 {
			return req, fmt.Errorf("DNS over TLS set to yes needs upstreams to talk to: without them, every server handed out by the network would be held to it, and most cannot speak it")
		}
		for _, list := range [][]string{req.Servers, req.Fallback} {
			for _, sv := range list {
				if !strings.Contains(sv, "#") {
					return req, fmt.Errorf("DNS over TLS set to yes needs a server name for every server, such as %s#cloudflare-dns.com; %s has none", sv, sv)
				}
			}
		}
	}
	if len(req.Servers) == 0 && len(req.Fallback) == 0 && len(req.Domains) == 0 &&
		req.DNSSEC == "" && req.DNSOverTLS == "" && req.Cache == "" {
		return req, fmt.Errorf("nothing to set; remove the dashboard's settings instead to go back to the host's own")
	}
	return req, nil
}

func oneOf(v, what string, allowed ...string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "", nil
	}
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s is %s", what, strings.Join(allowed, ", "))
}

// cleanDNSServers validates addresses with an optional #name, which is the
// server name DNS over TLS checks the certificate against.
func cleanDNSServers(in []string, what string) ([]string, error) {
	if len(in) > maxDNSServers {
		return nil, fmt.Errorf("at most %d %s servers", maxDNSServers, what)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		addr, name, hasName := strings.Cut(raw, "#")
		a, err := ParseAddr(addr)
		if err != nil {
			return nil, fmt.Errorf("%w (a server is an IP address, optionally followed by #servername)", err)
		}
		if a.IsUnspecified() || a.IsMulticast() {
			return nil, fmt.Errorf("%s cannot be a DNS server", a)
		}
		// The stub's own address as an upstream would have resolved ask
		// itself, and every lookup would wait for its timeout.
		if a.String() == resolvedStub || a.String() == resolvedStubAlt {
			return nil, fmt.Errorf("%s is systemd-resolved's own listener; as its upstream it would ask itself", a)
		}
		entry := a.String()
		if hasName {
			if err := validDNSName(name, false); err != nil {
				return nil, fmt.Errorf("the server name after # for %s: %w", a, err)
			}
			entry += "#" + strings.ToLower(strings.TrimSuffix(name, "."))
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, entry)
	}
	return out, nil
}

// validDomainEntry is a search domain, or a routing domain: ~. for every name
// or ~example.com for the names under one.
func validDomainEntry(d string) error {
	if d == "~." {
		return nil
	}
	name := strings.TrimPrefix(d, "~")
	if err := validDNSName(name, false); err != nil {
		return fmt.Errorf("domain %q: %w", d, err)
	}
	return nil
}

// renderResolved is the drop-in. Only what was chosen is written.
func renderResolved(req DNSSettings, who string) string {
	var b strings.Builder
	b.WriteString("# Generated by Just Dashboard")
	if who != "" {
		b.WriteString(" for " + who)
	}
	b.WriteString(" on " + dnsNow().UTC().Format("2006-01-02 15:04 UTC") + ".\n")
	b.WriteString("# systemd-resolved reads this after resolved.conf. Change it on the dashboard's\n# Network, DNS page; an edit here is overwritten by the next change there.\n")
	b.WriteString("[Resolve]\n")
	list := func(key string, v []string) {
		if len(v) > 0 {
			b.WriteString(key + "=" + strings.Join(v, " ") + "\n")
		}
	}
	list("DNS", req.Servers)
	list("FallbackDNS", req.Fallback)
	list("Domains", req.Domains)
	for _, kv := range [][2]string{{"DNSSEC", req.DNSSEC}, {"DNSOverTLS", req.DNSOverTLS}, {"Cache", req.Cache}} {
		if kv[1] != "" {
			b.WriteString(kv[0] + "=" + kv[1] + "\n")
		}
	}
	return b.String()
}

// serverIP is the address of a resolved server entry, which may carry a port,
// a zone and a #name: 1.1.1.1#cloudflare-dns.com, [::1]:5353, fe80::1%eth0.
func serverIP(entry string) (netip.Addr, bool) {
	entry, _, _ = strings.Cut(entry, "#")
	if ap, err := netip.ParseAddrPort(entry); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	entry = strings.Trim(entry, "[]")
	entry, _, _ = strings.Cut(entry, "%")
	a, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// hostFilePath maps a path onto the one this process can write. The dashboard
// runs in a container that mounts the host's /etc, but Docker mounts its own
// copy of hosts, hostname and resolv.conf over those three files, and a
// rename onto a mount point fails (or, worse, a write lands in Docker's copy
// and the host never sees it). The host's real file is under /host.
func hostFilePath(path string) string {
	if path != "/etc/hosts" {
		return path
	}
	if st, err := os.Stat(filepath.Join("/host", path)); err == nil && st.Mode().IsRegular() {
		return filepath.Join("/host", path)
	}
	return path
}

// validDNSName is a host or domain name in RFC 1123's letters-digits-hyphen
// form, at most 253 characters with labels of at most 63. Underscores are
// allowed only where a service label needs them (_sip._tcp). A trailing dot is
// accepted and ignored.
func validDNSName(name string, allowUnderscore bool) error {
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return fmt.Errorf("a name is required")
	}
	if len(name) > 253 {
		return fmt.Errorf("a name is at most 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return fmt.Errorf("%q has an empty label", name)
		}
		if len(label) > 63 {
			return fmt.Errorf("a label of %q is longer than 63 characters", name)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("a label of %q starts or ends with a hyphen", name)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			case c == '_' && allowUnderscore:
			case c >= 0x80:
				return fmt.Errorf("%q is not ASCII; use its punycode (xn--) form", name)
			default:
				return fmt.Errorf("%q may hold only letters, digits and hyphens", name)
			}
		}
	}
	return nil
}

// isLoopbackName is the part of /etc/hosts that must keep meaning this machine.
func isLoopbackName(name string) bool {
	switch strings.ToLower(name) {
	case "localhost", "localhost.localdomain", "ip6-localhost", "ip6-loopback":
		return true
	}
	return false
}
