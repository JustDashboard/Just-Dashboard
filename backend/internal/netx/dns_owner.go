package netx

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Who writes /etc/resolv.conf differs by distribution: systemd-resolved on
// Ubuntu and Fedora, NetworkManager writing the file itself on RHEL and many
// desktops, Debian's resolvconf or openresolv on Debian, Alpine and Arch,
// SUSE's netconfig, dhcpcd on Raspberry Pi OS, tailscaled when nothing else is
// there. The page used to treat systemd-resolved as the answer everywhere.
// The owner adapter reads the file's link, its header and the owners' own
// configuration, names the writer and what programs actually ask, and says
// where a change is made instead when this page's resolved drop-in would not
// reach them. It only reads: nothing here changes an owner's configuration.

// dnsHostRoot is where this process sees the host's root filesystem. The
// dashboard's container mounts / at /host, and Docker may lay its own copy of
// resolv.conf over the container's /etc; reading through /host sees the host's
// file and resolves an absolute link target against the host's /run rather
// than the container's. Empty when the process runs on the host itself. A
// variable so tests stage a host tree.
var dnsHostRoot = detectDNSHostRoot()

// dnsOwnerRoot is where the owners' own configuration is read —
// NetworkManager's, resolvconf's, netconfig's. It is the host root; tests stage
// it apart from the resolv.conf they point at.
var dnsOwnerRoot = dnsHostRoot

func detectDNSHostRoot() string {
	if st, err := os.Stat("/host/etc"); err == nil && st.IsDir() {
		return "/host"
	}
	return ""
}

// under maps a host path into this process's view of a host root.
func under(root, path string) string {
	if root == "" {
		return path
	}
	return filepath.Join(root, path)
}

// hostView maps a host path into this process's view of the host's root.
func hostView(path string) string { return under(dnsHostRoot, path) }

// maxHostConfigBytes bounds every configuration file the adapter reads.
const maxHostConfigBytes = 64 << 10

// resolveUnder follows symbolic links component by component inside a host
// root, so /etc/resolv.conf -> /run/NetworkManager/resolv.conf and a /var/run
// -> /run directory link are both read on the host's side.
func resolveUnder(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s is not an absolute path", path)
	}
	resolved := "/"
	rest := strings.Split(strings.Trim(filepath.Clean(path), "/"), "/")
	for hops := 0; len(rest) > 0; {
		part := rest[0]
		rest = rest[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, part)
		st, err := os.Lstat(under(root, next))
		if err != nil {
			return "", err
		}
		if st.Mode()&fs.ModeSymlink == 0 {
			resolved = next
			continue
		}
		if hops++; hops > 16 {
			return "", fmt.Errorf("%s has too many symbolic links", path)
		}
		target, err := os.Readlink(under(root, next))
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			resolved = "/"
		}
		rest = append(strings.Split(strings.Trim(target, "/"), "/"), rest...)
	}
	return resolved, nil
}

// readUnder reads one bounded regular file of a host root.
func readUnder(root, path string) ([]byte, error) {
	resolved, err := resolveUnder(root, path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(under(root, resolved))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxHostConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxHostConfigBytes {
		return nil, fmt.Errorf("%s is larger than %d KiB", path, maxHostConfigBytes>>10)
	}
	return data, nil
}

// readHostFile reads one bounded regular file of the host.
func readHostFile(path string) ([]byte, error) { return readUnder(dnsHostRoot, path) }

func ownerPathExists(path string, dir bool) bool {
	resolved, err := resolveUnder(dnsOwnerRoot, path)
	if err != nil {
		return false
	}
	st, err := os.Stat(under(dnsOwnerRoot, resolved))
	return err == nil && st.IsDir() == dir
}

// ResolverOwner is who decides what /etc/resolv.conf says and what programs
// on this host ask as a result.
type ResolverOwner struct {
	// ID is systemd-resolved, networkmanager, resolvconf, openresolv,
	// netconfig, dhcpcd, dhclient, tailscale, wsl, local-cache, static,
	// missing or unknown. Name is how the page says it.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Chain is how programs reach a resolver: stub (systemd-resolved's local
	// stub), uplink (resolved's upstream list written out for programs to ask
	// directly), local-cache (another resolver on loopback), direct (the
	// servers the file lists) or missing.
	Chain string `json:"chain"`
	// Resolver names what answers programs: systemd-resolved, dnsmasq, or the
	// servers themselves.
	Resolver string `json:"resolver"`
	// Confidence is confirmed (the file and the owner's running service or own
	// configuration agree), declared (only the file's link or header says so),
	// inferred (from the nameserver and a port-53 listener) or unknown.
	Confidence string `json:"confidence"`
	// DashboardWrites is whether the systemd-resolved drop-in this page writes
	// reaches the resolver programs ask.
	DashboardWrites bool `json:"dashboardWrites"`
	// Handoff is where a DNS change is made on this host instead of, or as
	// well as, this page. HandoffHref is a page of the dashboard that edits it.
	Handoff     string                  `json:"handoff"`
	HandoffHref string                  `json:"handoffHref,omitempty"`
	Evidence    []ResolverOwnerEvidence `json:"evidence"`
	// Conflicts are disagreements between what the sources say, each a sentence.
	Conflicts []string `json:"conflicts"`
}

// ResolverOwnerEvidence is one fact the verdict rests on, with where it came from.
type ResolverOwnerEvidence struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
}

// nmResolverConfig is the [main] dns= and rc-manager= NetworkManager reads,
// with the file each came from.
type nmResolverConfig struct {
	present         bool
	dns, rcManager  string
	dnsFrom, rcFrom string
	unreadable      []string
}

// nmConfigDirs are NetworkManager's conf.d directories in precedence order: a
// file in a later directory shadows one with the same name in an earlier one,
// and the snippets are then read in name order after NetworkManager.conf.
var nmConfigDirs = []string{"/usr/lib/NetworkManager/conf.d", "/run/NetworkManager/conf.d", "/etc/NetworkManager/conf.d"}

func readNetworkManagerDNS() nmResolverConfig {
	var cfg nmResolverConfig
	files := []string{}
	if ownerPathExists("/etc/NetworkManager/NetworkManager.conf", false) {
		files = append(files, "/etc/NetworkManager/NetworkManager.conf")
	}
	snippets := map[string]string{}
	for _, dir := range nmConfigDirs {
		resolved, err := resolveUnder(dnsOwnerRoot, dir)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(under(dnsOwnerRoot, resolved))
		if err != nil {
			continue
		}
		if len(entries) > 64 {
			cfg.unreadable = append(cfg.unreadable, dir+" holds more than 64 entries")
			entries = entries[:64]
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".conf") {
				snippets[e.Name()] = filepath.Join(dir, e.Name())
			}
		}
	}
	names := make([]string, 0, len(snippets))
	for name := range snippets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		files = append(files, snippets[name])
	}
	cfg.present = len(files) > 0 || ownerPathExists("/etc/NetworkManager", true)
	for _, path := range files {
		data, err := readUnder(dnsOwnerRoot, path)
		if err != nil {
			cfg.unreadable = append(cfg.unreadable, path)
			continue
		}
		section := ""
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
			if line[0] == '[' {
				section = strings.ToLower(strings.Trim(line, "[] "))
				continue
			}
			if section != "main" {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(key) {
			case "dns":
				cfg.dns, cfg.dnsFrom = strings.TrimSpace(value), path
			case "rc-manager":
				cfg.rcManager, cfg.rcFrom = strings.TrimSpace(value), path
			}
		}
	}
	return cfg
}

// netconfigPolicy is SUSE's NETCONFIG_DNS_POLICY, empty where there is none.
func netconfigPolicy() string {
	data, err := readUnder(dnsOwnerRoot, "/etc/sysconfig/network/config")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "NETCONFIG_DNS_POLICY="); ok {
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if value == "" {
				return "(empty)"
			}
			return value
		}
	}
	return ""
}

type resolverOwnerInputs struct {
	rc               ResolvConf
	resolvedActive   bool
	resolvedMode     string
	nm               nmResolverConfig
	nmActive         bool
	debianResolvconf bool
	openresolv       bool
	netconfig        string
	listeners        []DNSListener
}

// resolverOwner gathers the owners' evidence from the host and classifies it.
func resolverOwner(ctx context.Context, rc ResolvConf, rv ResolvedView, listeners []DNSListener) ResolverOwner {
	in := resolverOwnerInputs{
		rc: rc, resolvedActive: rv.Active, resolvedMode: rv.Global.ResolvConfMode, listeners: listeners,
		nm:               readNetworkManagerDNS(),
		debianResolvconf: ownerPathExists("/etc/resolvconf/resolv.conf.d", true) || strings.Contains(rc.Target, "/resolvconf/"),
		openresolv:       ownerPathExists("/etc/resolvconf.conf", false),
		netconfig:        netconfigPolicy(),
	}
	// Only a host that shows signs of NetworkManager is asked whether it runs.
	if (in.nm.present || rc.ManagedBy == "NetworkManager") && has("systemctl") {
		out, _ := run(ctx, "systemctl", "is-active", "NetworkManager")
		in.nmActive = strings.TrimSpace(out) == "active"
	}
	return classifyResolverOwner(in)
}

var resolverOwnerNames = map[string]string{
	"systemd-resolved": "systemd-resolved",
	"networkmanager":   "NetworkManager",
	"resolvconf":       "resolvconf (Debian)",
	"openresolv":       "openresolv",
	"netconfig":        "netconfig (SUSE)",
	"dhcpcd":           "dhcpcd",
	"dhclient":         "dhclient",
	"tailscale":        "Tailscale",
	"wsl":              "WSL",
	"local-cache":      "A local resolver",
	"static":           "A plain file",
	"missing":          "Nothing",
	"unknown":          "Unrecognised",
}

func classifyResolverOwner(in resolverOwnerInputs) ResolverOwner {
	rc := in.rc
	o := ResolverOwner{Evidence: []ResolverOwnerEvidence{}, Conflicts: []string{}}
	note := func(source, detail string) {
		o.Evidence = append(o.Evidence, ResolverOwnerEvidence{Source: source, Detail: detail})
	}
	conflict := func(text string) { o.Conflicts = append(o.Conflicts, text) }

	o.Chain, o.Resolver = resolverChain(rc, in.listeners)
	switch {
	case rc.Mode == "missing":
		note(rc.Path, "does not exist")
	case rc.Target != "":
		note(rc.Path+" link", "points at "+rc.Target)
	default:
		note(rc.Path, "is a plain file")
	}
	if len(rc.header) > 0 {
		note(rc.Path+" header", rc.header[0])
	}
	if rc.unreadable != "" {
		note(rc.Path, "cannot be read: "+rc.unreadable)
	}
	if len(rc.Nameservers) > 0 {
		note(rc.Path+" nameservers", strings.Join(rc.Nameservers, ", "))
	}
	if len(rc.Search) > 0 {
		note(rc.Path+" search", strings.Join(rc.Search, " "))
	}

	// Each writer signs its first comment line; a later line can quote another
	// writer's name (Tailscale's links to its resolvconf notes).
	headerWriter := ""
	for _, line := range rc.header {
		if headerWriter = resolvConfWriter(line); headerWriter != "" {
			break
		}
	}
	switch {
	case rc.Mode == "missing":
		o.ID = "missing"
	case rc.Mode == "stub" || rc.Mode == "uplink":
		o.ID = "systemd-resolved"
	case strings.Contains(rc.Target, "NetworkManager"):
		o.ID = "networkmanager"
	case strings.Contains(rc.Target, "/resolvconf/"):
		o.ID = "resolvconf"
	case strings.Contains(rc.Target, "netconfig"):
		o.ID = "netconfig"
	case rc.Target != "":
		o.ID = "unknown"
	case headerWriter == "NetworkManager":
		o.ID = "networkmanager"
	case headerWriter == "resolvconf":
		o.ID = "resolvconf"
		if in.openresolv && !in.debianResolvconf || strings.Contains(strings.ToLower(strings.Join(rc.header, " ")), "openresolv") {
			o.ID = "openresolv"
		}
	case headerWriter == "systemd-resolved":
		o.ID = "systemd-resolved"
		conflict("/etc/resolv.conf is a plain copy of systemd-resolved's file rather than a link to it, so it no longer follows resolved's changes.")
	case headerWriter == "netconfig":
		o.ID = "netconfig"
	case headerWriter == "dhcpcd":
		o.ID = "dhcpcd"
	case headerWriter == "dhclient":
		o.ID = "dhclient"
	case headerWriter == "Tailscale":
		o.ID = "tailscale"
	case headerWriter == "WSL":
		o.ID = "wsl"
	case o.Chain == "local-cache":
		o.ID = "local-cache"
	case in.netconfig != "" && in.netconfig != "(empty)":
		o.ID = "netconfig"
	default:
		o.ID = "static"
	}
	o.Name = resolverOwnerNames[o.ID]
	if o.ID == "local-cache" {
		o.Name = o.Resolver
	}

	// What the owners' own configuration and services say.
	if in.nm.present {
		dns, rcm := orDefault(in.nm.dns, "unset"), orDefault(in.nm.rcManager, "unset")
		detail := "dns=" + dns + " rc-manager=" + rcm
		for _, from := range []struct{ key, path string }{{"dns", in.nm.dnsFrom}, {"rc-manager", in.nm.rcFrom}} {
			if from.path != "" {
				detail += "; " + from.key + " from " + from.path
			}
		}
		if in.nmActive {
			detail += "; NetworkManager is running"
		}
		note("NetworkManager configuration", detail)
		for _, path := range in.nm.unreadable {
			note("NetworkManager configuration", path+" cannot be read")
		}
	}
	if in.resolvedActive {
		detail := "systemd-resolved is running"
		if in.resolvedMode != "" {
			detail += "; it reads /etc/resolv.conf as " + in.resolvedMode
		}
		note("systemd-resolved", detail)
	}
	if in.debianResolvconf {
		note("resolvconf", "/etc/resolvconf/resolv.conf.d exists")
	}
	if in.openresolv {
		note("openresolv", "/etc/resolvconf.conf exists")
	}
	if in.netconfig != "" {
		note("netconfig", "NETCONFIG_DNS_POLICY="+in.netconfig)
	}

	switch o.ID {
	case "systemd-resolved":
		o.Confidence = "declared"
		if in.resolvedActive && (in.resolvedMode == "" || in.resolvedMode == rc.Mode || rc.Target == "") {
			o.Confidence = "confirmed"
		}
		if !in.resolvedActive {
			conflict("/etc/resolv.conf points at systemd-resolved, but systemd-resolved is not running, so programs reach no resolver.")
		}
	case "networkmanager":
		o.Confidence = "declared"
		if in.nmActive {
			o.Confidence = "confirmed"
		} else {
			conflict("NetworkManager wrote /etc/resolv.conf but is not running, so nothing keeps the file current.")
		}
		if in.nm.rcManager == "unmanaged" {
			conflict("NetworkManager is configured with rc-manager=unmanaged, yet the file carries its mark: it is a leftover nothing will update.")
		}
		if in.nm.dns == "systemd-resolved" && o.Chain != "stub" {
			conflict("NetworkManager is configured to hand DNS to systemd-resolved (dns=systemd-resolved), but /etc/resolv.conf does not point programs at resolved's stub.")
		}
	case "resolvconf":
		o.Confidence = "declared"
		if in.debianResolvconf {
			o.Confidence = "confirmed"
		}
	case "openresolv":
		o.Confidence = "declared"
		if in.openresolv {
			o.Confidence = "confirmed"
		}
	case "netconfig":
		o.Confidence = "declared"
		if in.netconfig != "" {
			o.Confidence = "confirmed"
		}
	case "dhcpcd", "dhclient", "tailscale", "wsl":
		o.Confidence = "declared"
	case "local-cache", "static":
		o.Confidence = "inferred"
	default:
		o.Confidence = "unknown"
	}
	if o.ID == "tailscale" {
		conflict("tailscaled rewrites /etc/resolv.conf while it runs, so another manager's change is overwritten until Tailscale's DNS is turned off.")
	}

	o.DashboardWrites = in.resolvedActive && (o.Chain == "stub" || o.Chain == "uplink")
	if in.resolvedActive && !o.DashboardWrites {
		ask := strings.Join(rc.Nameservers, ", ")
		if ask == "" {
			ask = "no server at all"
		}
		conflict(fmt.Sprintf("systemd-resolved is running, but programs ask %s because %s decides /etc/resolv.conf; the resolved drop-in written on this page does not reach them.", ask, o.Name))
	}
	if o.Chain == "uplink" {
		conflict("Programs read resolved's upstream list and ask those servers themselves: servers set here reach them, but routing domains, DNS over TLS, DNSSEC and the cache do not.")
	}
	o.Handoff, o.HandoffHref = resolverHandoff(o, in)
	return o
}

// resolverChain is how programs reach a resolver, and what that resolver is.
func resolverChain(rc ResolvConf, listeners []DNSListener) (string, string) {
	switch {
	case rc.Mode == "missing":
		return "missing", "nothing"
	case rc.Mode == "uplink":
		return "uplink", "the listed servers"
	case len(rc.Nameservers) == 0:
		return "direct", "nothing"
	}
	stub := true
	loopback := ""
	for _, ns := range rc.Nameservers {
		sv, err := parseDNSServer(ns)
		if err != nil {
			stub = false
			continue
		}
		address := sv.addr.String()
		if address != resolvedStub && address != resolvedStubAlt {
			stub = false
		}
		if sv.addr.IsLoopback() && loopback == "" && address != resolvedStub && address != resolvedStubAlt {
			loopback = address
		}
	}
	if stub {
		return "stub", "systemd-resolved"
	}
	if loopback != "" {
		for _, l := range listeners {
			listen, err := netip.ParseAddr(l.Address)
			if err != nil || l.Kind == "resolved-stub" || l.Kind == "docker-proxy" {
				continue
			}
			if l.Address == loopback || listen.IsUnspecified() {
				name := dnsKindName(l.Kind)
				if l.Kind == "other" && l.Process != "" {
					name = l.Process
				}
				return "local-cache", name
			}
		}
		return "local-cache", "an unidentified resolver on " + loopback
	}
	return "direct", "the listed servers"
}

// dnsKindName is how a port-53 listener's kind is said.
func dnsKindName(kind string) string {
	switch kind {
	case "dnsmasq":
		return "dnsmasq"
	case "unbound":
		return "Unbound"
	case "named":
		return "BIND"
	case "adguardhome":
		return "AdGuard Home"
	case "pihole":
		return "Pi-hole"
	case "technitium":
		return "Technitium"
	case "resolved-stub":
		return "systemd-resolved"
	}
	return "a local resolver"
}

func resolverHandoff(o ResolverOwner, in resolverOwnerInputs) (string, string) {
	perLink := "Per-link servers and domains come from each link's network manager; split DNS for a link is edited through its native profile."
	switch o.ID {
	case "systemd-resolved":
		if !in.resolvedActive {
			return "Start systemd-resolved, or point /etc/resolv.conf at real servers through the host's network manager.", ""
		}
		text := "Global upstreams, fallback, DNS over TLS, DNSSEC and the cache are set on this page in resolved's drop-in. " + perLink
		if o.Chain == "uplink" {
			text += " Link /etc/resolv.conf to /run/systemd/resolve/stub-resolv.conf to give programs resolved's split DNS, encryption and validation."
		}
		return text, "#dns-links"
	case "networkmanager":
		text := "NetworkManager writes this file from its connection profiles: change DNS on the connection — the interface's native profile on the Interfaces page, or nmcli connection modify <profile> ipv4.dns … — and it rewrites the file."
		if in.nm.dns == "dnsmasq" {
			text = "Programs ask NetworkManager's dnsmasq plugin (dns=dnsmasq), which NetworkManager feeds from its connection profiles: change DNS on the connection — the interface's native profile on the Interfaces page, or nmcli — not in dnsmasq."
		}
		return text, "/network/interfaces"
	case "resolvconf":
		return "Debian's resolvconf assembles this file from interface configuration (dns-nameservers in /etc/network/interfaces) and /etc/resolvconf/resolv.conf.d/head and base; run resolvconf -u after editing them.", ""
	case "openresolv":
		return "openresolv builds this file from /etc/resolvconf.conf (name_servers=, search_domains=) and what interfaces report; run resolvconf -u after editing it.", ""
	case "netconfig":
		return "SUSE's netconfig writes this file from /etc/sysconfig/network/config (NETCONFIG_DNS_STATIC_SERVERS and NETCONFIG_DNS_POLICY); run netconfig update -f afterwards, or use YaST.", ""
	case "dhcpcd":
		return "dhcpcd writes this file from DHCP: set static domain_name_servers= in /etc/dhcpcd.conf, or nohook resolv.conf to stop it writing the file.", ""
	case "dhclient":
		return "dhclient-script writes this file from DHCP leases: use supersede domain-name-servers in /etc/dhcp/dhclient.conf.", ""
	case "tailscale":
		return "tailscaled writes this file for MagicDNS: DNS is set in the tailnet's DNS settings, and tailscale set --accept-dns=false hands the file back to the host.", ""
	case "wsl":
		return "WSL generates this file: set generateResolvConf=false under [network] in /etc/wsl.conf to manage it yourself.", ""
	case "local-cache":
		return "Programs ask " + o.Resolver + " on loopback; its upstreams are set in its own configuration (for dnsmasq, its server= lines), not on this page.", ""
	case "static":
		return "/etc/resolv.conf is a plain file that nothing on this host is seen to regenerate: edit it directly, or hand it to a network manager.", ""
	case "missing":
		return "/etc/resolv.conf does not exist, so programs ask 127.0.0.1. Create it through the host's network manager.", ""
	}
	return "/etc/resolv.conf links to " + in.rc.Target + ", which this page does not recognise: change DNS through whatever writes that file.", ""
}

// resolverOwnerReason is the sentence a refused change gives on a host where
// systemd-resolved is not what the page could write to.
func resolverOwnerReason(o ResolverOwner) string {
	return "systemd-resolved is not running here; " + o.Name + " decides /etc/resolv.conf, so the upstreams are changed there rather than on this page. " + o.Handoff
}
