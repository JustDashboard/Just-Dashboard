package netx

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// wgPackage is what installs `wg` and `wg-quick`, for the hand-off a missing
// tool turns into.
const wgPackage = "wireguard-tools"

// wgOnlineWithin is how recently a peer must have completed a handshake to be
// called online. WireGuard sends no keepalive of its own and renegotiates
// every two minutes while traffic flows, so three minutes covers one missed
// rekey without calling a quiet peer dead.
const wgOnlineWithin = 180

// Where the module looks on the host, as variables so tests point them at a
// directory they built.
var (
	procNetRoot = "/proc/net"
	procSysRoot = "/proc/sys"
	sysRoot     = "/sys"
	libModules  = "/lib/modules"
	wgNow       = time.Now
)

var wgNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// validWGName is wg-quick's rule for an interface name, on top of the
// kernel's: the name becomes a systemd instance (wg-quick@NAME) and a file
// name, so it holds nothing either would read as something else.
func validWGName(name string) error {
	if err := ValidIfName(name); err != nil {
		return err
	}
	if !wgNameRe.MatchString(name) {
		return fmt.Errorf("a WireGuard interface name may hold only letters, digits and . - _")
	}
	return nil
}

// WireGuardView is the WireGuard half of the VPN page.
type WireGuardView struct {
	// Installed is both tools present: `wg` reads and edits a running
	// interface, `wg-quick` brings one up from its file.
	Installed bool    `json:"installed"`
	Tools     WGTools `json:"tools"`
	Package   string  `json:"package"`
	// Kernel is whether this kernel can run WireGuard: the module is loaded
	// or on disk to be loaded when the first interface is made.
	Kernel     bool          `json:"kernel"`
	Systemd    bool          `json:"systemd"`
	Interfaces []WGInterface `json:"interfaces"`
	Error      string        `json:"error,omitempty"`
}

// WGTools says which of the two commands exist.
type WGTools struct {
	Wg      bool `json:"wg"`
	WgQuick bool `json:"wgQuick"`
}

// WGInterface is one tunnel: what its file says, what the kernel has, and
// what systemd will do with it at boot.
type WGInterface struct {
	Name string `json:"name"`
	// Managed is a file Just Dashboard created and may edit. False is a tunnel
	// somebody wrote by hand, or one with no file at all: shown, never touched.
	Managed bool `json:"managed"`
	// Configured is a file in the WireGuard directory; Up is the interface
	// existing in the kernel now.
	Configured bool `json:"configured"`
	Up         bool `json:"up"`
	// Enabled and Active are wg-quick@NAME's state: started at boot, running.
	Enabled    bool     `json:"enabled"`
	Active     bool     `json:"active"`
	PublicKey  string   `json:"publicKey"`
	ListenPort int      `json:"listenPort"`
	Addresses  []string `json:"addresses"`
	MTU        int      `json:"mtu"`
	DNS        []string `json:"dns"`
	// Endpoint is the host:port the dashboard puts in client configurations.
	Endpoint string `json:"endpoint"`
	// ExitNode is a NAT entry for this tunnel's network: clients may send all
	// their traffic through this server.
	ExitNode bool `json:"exitNode"`
	// Subnet is the tunnel's network, the first address masked.
	Subnet string   `json:"subnet"`
	Peers  []WGPeer `json:"peers"`
}

// WGPeer is one far end of a tunnel. It never carries a key but the public one.
type WGPeer struct {
	// ID is the dashboard's number for a peer it made, which the routes take;
	// zero is a peer somebody else added, shown and left alone.
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Kind is device, site, or peer for one the dashboard did not make.
	Kind      string `json:"kind"`
	PublicKey string `json:"publicKey"`
	// Address is the peer's own address inside the tunnel.
	Address    string   `json:"address"`
	AllowedIPs []string `json:"allowedIps"`
	// Endpoint is where the peer was last seen from, or the address this
	// server dials when it is configured with one.
	Endpoint string `json:"endpoint"`
	// LatestHandshake is unix seconds, zero for never.
	LatestHandshake int64  `json:"latestHandshake"`
	Online          bool   `json:"online"`
	RxBytes         uint64 `json:"rxBytes"`
	TxBytes         uint64 `json:"txBytes"`
	Keepalive       int    `json:"keepalive"`
	// HasConfig is a stored client configuration that has not been forgotten.
	HasConfig bool `json:"hasConfig"`
	// CreatedAt is unix seconds, zero when unknown.
	CreatedAt int64 `json:"createdAt"`
}

// WireGuard reads every tunnel: the union of what `wg show` reports live and
// the files in the WireGuard directory, so a tunnel that is configured but
// down is on the page beside one that is up.
func (s *Service) WireGuard(ctx context.Context) (*WireGuardView, error) {
	return s.readWireGuard(ctx, "", true)
}

// readWireGuard is WireGuard for one interface or all of them. The store is
// read only when a page needs to know which peers have a configuration to
// show again; the Overview's counts do not.
func (s *Service) readWireGuard(ctx context.Context, only string, withStore bool) (*WireGuardView, error) {
	v := &WireGuardView{
		Package:    wgPackage,
		Tools:      WGTools{Wg: has("wg"), WgQuick: has("wg-quick")},
		Kernel:     wgKernelSupported(),
		Systemd:    has("systemctl"),
		Interfaces: []WGInterface{},
	}
	v.Installed = v.Tools.Wg && v.Tools.WgQuick

	var live map[string]*wgLiveIface
	if v.Tools.Wg {
		out, err := run(ctx, "wg", "show", "all", "dump")
		if err != nil {
			v.Error = err.Error()
		} else {
			live = parseWGDump(out)
		}
	}
	confs, err := s.listWGConfs()
	if err != nil {
		return v, err
	}
	var sp *Spec
	if loaded, err := s.loadSpec(); err == nil {
		sp = loaded
	}

	names := map[string]bool{}
	for n := range live {
		names[n] = true
	}
	for n := range confs {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		if only == "" || n == only {
			sorted = append(sorted, n)
		}
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		ifc := WGInterface{Name: name, Addresses: []string{}, DNS: []string{}, Peers: []WGPeer{}}
		var clients map[string]VPNClient
		if withStore {
			clients, err = s.vpn.byPublicKey(ctx, name)
			if err != nil {
				return v, err
			}
		}
		conf := confs[name]
		l := live[name]
		s.fillInterface(&ifc, conf, l, clients, sp)
		if v.Systemd && conf != nil {
			unit := "wg-quick@" + name
			enabled, _ := run(ctx, "systemctl", "is-enabled", unit)
			active, _ := run(ctx, "systemctl", "is-active", unit)
			ifc.Enabled = strings.TrimSpace(enabled) == "enabled"
			ifc.Active = strings.TrimSpace(active) == "active"
		}
		v.Interfaces = append(v.Interfaces, ifc)
	}
	return v, nil
}

// fillInterface joins the file, the kernel and the store into one tunnel.
func (s *Service) fillInterface(ifc *WGInterface, conf *wgConf, live *wgLiveIface, clients map[string]VPNClient, sp *Spec) {
	ifc.Configured = conf != nil
	ifc.Up = live != nil
	ifc.Managed = conf != nil && conf.managed
	if live != nil {
		ifc.PublicKey = live.publicKey
		ifc.ListenPort = live.listenPort
	}
	var filePeers []wgPeerConf
	if conf != nil {
		if sec := conf.iface(); sec != nil {
			ifc.Addresses = append(ifc.Addresses, sec.list("address")...)
			ifc.MTU, _ = strconv.Atoi(sec.get("mtu"))
			meta := sec.bodyMeta()
			ifc.Endpoint = meta["endpoint"]
			// For a tunnel made here, DNS is what clients are given; in a
			// file somebody wrote it is the server's own setting.
			if dns := meta["dns"]; dns != "" {
				ifc.DNS = append(ifc.DNS, splitList(dns)...)
			} else {
				ifc.DNS = append(ifc.DNS, sec.list("dns")...)
			}
			if ifc.ListenPort == 0 {
				ifc.ListenPort, _ = strconv.Atoi(sec.get("listenport"))
			}
			// A file's key gives the public half without asking the kernel,
			// which is how a tunnel that is down still shows what its peers
			// must be configured with.
			if ifc.PublicKey == "" {
				if pub, err := wgPublicKey(sec.get("privatekey")); err == nil {
					ifc.PublicKey = pub
				}
			}
		}
		for _, sec := range conf.peers() {
			filePeers = append(filePeers, peerConf(sec))
		}
	}
	for _, a := range ifc.Addresses {
		if p, err := ParsePrefix(a); err == nil {
			ifc.Subnet = p.Masked().String()
			break
		}
	}
	if sp != nil {
		for _, n := range sp.NAT {
			if n.Owner == wgOwner(ifc.Name) && n.Enabled {
				ifc.ExitNode = true
			}
		}
	}

	liveByKey := map[string]wgLivePeer{}
	if live != nil {
		for _, p := range live.peers {
			liveByKey[p.publicKey] = p
		}
	}
	now := wgNow().Unix()
	seen := map[string]bool{}
	build := func(fp *wgPeerConf, lp *wgLivePeer) WGPeer {
		p := WGPeer{Kind: "peer", AllowedIPs: []string{}}
		if fp != nil {
			p.ID, p.Name, p.PublicKey = fp.id, fp.name, fp.publicKey
			p.AllowedIPs = append(p.AllowedIPs, fp.allowedIPs...)
			p.Endpoint, p.Keepalive = fp.endpoint, fp.keepalive
			if fp.kind != "" {
				p.Kind = fp.kind
			}
			if !fp.created.IsZero() {
				p.CreatedAt = fp.created.Unix()
			}
		}
		if lp != nil {
			p.PublicKey = lp.publicKey
			if len(lp.allowedIPs) > 0 {
				p.AllowedIPs = append([]string{}, lp.allowedIPs...)
			}
			if lp.endpoint != "" {
				p.Endpoint = lp.endpoint
			}
			p.LatestHandshake, p.RxBytes, p.TxBytes = lp.handshake, lp.rx, lp.tx
			if lp.keepalive > 0 {
				p.Keepalive = lp.keepalive
			}
			p.Online = lp.handshake > 0 && now-lp.handshake <= wgOnlineWithin
		}
		p.Address = peerAddress(p.AllowedIPs, ifc.Subnet)
		if c, ok := clients[p.PublicKey]; ok && c.HasConfig && (p.ID == 0 || int(c.ID) == p.ID) {
			p.HasConfig = true
		}
		return p
	}
	for i := range filePeers {
		fp := &filePeers[i]
		var lp *wgLivePeer
		if x, ok := liveByKey[fp.publicKey]; ok {
			lp = &x
		}
		seen[fp.publicKey] = true
		ifc.Peers = append(ifc.Peers, build(fp, lp))
	}
	if live != nil {
		for _, x := range live.peers {
			if seen[x.publicKey] {
				continue
			}
			x := x
			ifc.Peers = append(ifc.Peers, build(nil, &x))
		}
	}
}

// peerAddress is the peer's own address inside the tunnel: the first host
// route it is allowed that lies in the tunnel's network, else the first thing
// it is allowed.
func peerAddress(allowed []string, subnet string) string {
	net, _ := netip.ParsePrefix(subnet)
	for _, a := range allowed {
		p, err := ParsePrefix(a)
		if err != nil {
			continue
		}
		if net.IsValid() && p.Bits() == p.Addr().BitLen() && net.Contains(p.Addr()) {
			return p.Addr().String()
		}
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return ""
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// wgOwner is the Owner a tunnel's NAT entry carries in the spec.
func wgOwner(iface string) string { return "wireguard:" + iface }

// listWGConfs parses every `*.conf` in the WireGuard directory. A file that
// cannot be read is skipped rather than failing the page: one root-only file
// should not hide the other tunnels.
func (s *Service) listWGConfs() (map[string]*wgConf, error) {
	out := map[string]*wgConf{}
	entries, err := os.ReadDir(s.paths.WireGuard)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.paths.WireGuard, err)
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".conf")
		if !ok || e.IsDir() || validWGName(name) != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.paths.WireGuard, e.Name()))
		if err != nil {
			continue
		}
		out[name] = parseWGConf(string(b))
	}
	return out, nil
}

// wgKernelSupported is whether this kernel can make a WireGuard interface.
// On most distributions the module is not loaded until the first interface is
// made, so its absence from /sys/module says nothing; its file on disk does.
func wgKernelSupported() bool {
	if _, err := os.Stat(filepath.Join(sysRoot, "module", "wireguard")); err == nil {
		return true
	}
	release, err := os.ReadFile(filepath.Join(procSysRoot, "kernel", "osrelease"))
	if err != nil {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(libModules, strings.TrimSpace(string(release)), "kernel", "drivers", "net", "wireguard", "wireguard.ko*"))
	return len(matches) > 0
}

// wgLiveIface is an interface as `wg show all dump` reports it. The dump
// prints the private key of every interface and the preshared key of every
// peer; neither has a field here, so no later change can put one in a
// response.
type wgLiveIface struct {
	name       string
	publicKey  string
	listenPort int
	peers      []wgLivePeer
}

type wgLivePeer struct {
	publicKey  string
	endpoint   string
	allowedIPs []string
	handshake  int64
	rx, tx     uint64
	keepalive  int
}

// parseWGDump reads `wg show all dump`: tab-separated, with the interface
// name leading every line. An interface line has five fields (name, private
// key, public key, listen port, fwmark), a peer line nine (name, public key,
// preshared key, endpoint, allowed ips, latest handshake, rx, tx,
// persistent keepalive). "(none)" is an absent value and "off" an absent
// keepalive.
func parseWGDump(out string) map[string]*wgLiveIface {
	res := map[string]*wgLiveIface{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		switch len(f) {
		case 5:
			port, _ := strconv.Atoi(f[3])
			res[f[0]] = &wgLiveIface{name: f[0], publicKey: noneIsEmpty(f[2]), listenPort: port}
		case 9:
			ifc := res[f[0]]
			if ifc == nil {
				continue
			}
			p := wgLivePeer{publicKey: f[1], endpoint: noneIsEmpty(f[3])}
			if a := noneIsEmpty(f[4]); a != "" {
				p.allowedIPs = splitList(a)
			}
			p.handshake, _ = strconv.ParseInt(f[5], 10, 64)
			p.rx, _ = strconv.ParseUint(f[6], 10, 64)
			p.tx, _ = strconv.ParseUint(f[7], 10, 64)
			if f[8] != "off" {
				p.keepalive, _ = strconv.Atoi(f[8])
			}
			ifc.peers = append(ifc.peers, p)
		}
	}
	return res
}

func noneIsEmpty(s string) string {
	if s == "(none)" {
		return ""
	}
	return s
}

// VPNSummary is what the Overview shows of the VPNs: counts and traffic,
// never a name, a key or an address of anybody's.
type VPNSummary struct {
	WireGuard WGSummary `json:"wireguard"`
	Tailscale TSSummary `json:"tailscale"`
}

// WGSummary counts WireGuard's tunnels and peers.
type WGSummary struct {
	Installed  bool   `json:"installed"`
	Interfaces int    `json:"interfaces"`
	Peers      int    `json:"peers"`
	Online     int    `json:"online"`
	Rx         uint64 `json:"rx"`
	Tx         uint64 `json:"tx"`
}

// TSSummary counts Tailscale's peers and what this server offers the tailnet.
type TSSummary struct {
	Installed bool `json:"installed"`
	Running   bool `json:"running"`
	Peers     int  `json:"peers"`
	Online    int  `json:"online"`
	// ExitNode is this server advertising itself as an exit node.
	ExitNode     bool     `json:"exitNode"`
	SubnetRoutes int      `json:"subnetRoutes"`
	SelfIPs      []string `json:"selfIPs"`
}

// VPNSummary reads both VPNs for the Overview. A part that cannot be read
// counts as absent: the Overview is a reading, and one broken tool should
// not blank the other's numbers.
func (s *Service) VPNSummary(ctx context.Context) VPNSummary {
	sum := VPNSummary{Tailscale: TSSummary{SelfIPs: []string{}}}
	if wg, _ := s.readWireGuard(ctx, "", false); wg != nil {
		sum.WireGuard.Installed = wg.Installed
		sum.WireGuard.Interfaces = len(wg.Interfaces)
		for _, ifc := range wg.Interfaces {
			for _, p := range ifc.Peers {
				sum.WireGuard.Peers++
				if p.Online {
					sum.WireGuard.Online++
				}
				sum.WireGuard.Rx += p.RxBytes
				sum.WireGuard.Tx += p.TxBytes
			}
		}
	}
	if ts, _ := s.Tailscale(ctx, ""); ts != nil {
		sum.Tailscale.Installed = ts.Installed
		sum.Tailscale.Running = ts.Running
		sum.Tailscale.Peers = len(ts.Peers)
		for _, p := range ts.Peers {
			if p.Online {
				sum.Tailscale.Online++
			}
		}
		sum.Tailscale.ExitNode = ts.Prefs.AdvertisingExitNode
		sum.Tailscale.SubnetRoutes = len(ts.Prefs.AdvertiseRoutes)
		if ts.Self != nil {
			sum.Tailscale.SelfIPs = append(sum.Tailscale.SelfIPs, ts.Self.TailscaleIPs...)
		}
	}
	return sum
}
