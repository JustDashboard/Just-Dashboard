package netx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Spec is everything the dashboard has made on this host's network, and the
// only thing the boot unit restores.
//
// It is a file beside what is rendered from it rather than rows in SQLite,
// because it describes the host and not the dashboard: the bridges and port
// forwards are still there at the next boot whether or not the dashboard is,
// and a dashboard reinstalled over an existing host finds what the last one
// made rather than reporting it as somebody else's.
type Spec struct {
	Version int `json:"version"`
	// NextID numbers every entry that has no natural name, so an entry keeps
	// its id across edits and a delete names exactly one thing.
	NextID int `json:"nextId"`

	Links      []LinkSpec      `json:"links"`
	Addresses  []AddressSpec   `json:"addresses"`
	Routes     []RouteSpec     `json:"routes"`
	Rules      []RuleSpec      `json:"rules"`
	Namespaces []NamespaceSpec `json:"namespaces"`
	Shaping    []ShapeSpec     `json:"shaping"`
	Forwards   []ForwardSpec   `json:"forwards"`
	NAT        []NATSpec       `json:"nat"`
	Limits     []LimitSpec     `json:"limits"`
	Blocklists []BlocklistSpec `json:"blocklists"`
	// Trusted are addresses no drop in the gateway table may match, beyond
	// loopback and the allowlist: the operator's own, added the first time
	// they make a protection entry and kept until they remove it.
	Trusted []string `json:"trusted"`
	// Sysctls are the kernel settings set here, by key.
	Sysctls map[string]string `json:"sysctls"`
}

const specVersion = 1

// Made is who made an entry and when, for every entry the operator creates.
type Made struct {
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy,omitempty"`
}

// LinkSpec is a device the dashboard created.
type LinkSpec struct {
	Name string `json:"name"`
	// Kind is the kernel's: bridge, vlan, vxlan, gre, gretap, ip6gre,
	// ip6gretap, dummy, macvlan or veth.
	Kind string `json:"kind"`
	// Parent is the device a VLAN or macvlan rides on, and the device a VXLAN
	// sends from.
	Parent string `json:"parent,omitempty"`
	VLANID int    `json:"vlanId,omitempty"`
	VNI    int    `json:"vni,omitempty"`
	// Local and Remote are a tunnel's ends; Group is a VXLAN's multicast
	// group, the alternative to a single remote.
	Local  string `json:"local,omitempty"`
	Remote string `json:"remote,omitempty"`
	Group  string `json:"group,omitempty"`
	Port   int    `json:"port,omitempty"`
	TTL    int    `json:"ttl,omitempty"`
	Key    uint32 `json:"key,omitempty"`
	// Mode is a macvlan's (bridge, private, vepa, passthru).
	Mode string `json:"mode,omitempty"`
	// Peer and PeerNamespace are a veth pair's other end and where it lives.
	Peer          string `json:"peer,omitempty"`
	PeerNamespace string `json:"peerNamespace,omitempty"`
	MTU           int    `json:"mtu,omitempty"`
	STP           bool   `json:"stp,omitempty"`
	// Master is the bridge this device was made a port of.
	Master string `json:"master,omitempty"`
	// Addresses are CIDRs, the host bits kept.
	Addresses []string `json:"addresses,omitempty"`
	Up        bool     `json:"up"`
	Made
}

// AddressSpec is an address added to a device the dashboard did not create.
type AddressSpec struct {
	ID   int    `json:"id"`
	Link string `json:"link"`
	CIDR string `json:"cidr"`
	Made
}

// RouteSpec is a route the dashboard added.
type RouteSpec struct {
	ID          int    `json:"id"`
	Family      string `json:"family"`
	Destination string `json:"destination"`
	// Type is unicast, blackhole, unreachable or prohibit; the last three
	// carry no gateway or device.
	Type    string `json:"type"`
	Gateway string `json:"gateway,omitempty"`
	Device  string `json:"device,omitempty"`
	Table   int    `json:"table"`
	Metric  int    `json:"metric,omitempty"`
	Source  string `json:"source,omitempty"`
	Comment string `json:"comment,omitempty"`
	Made
}

// RuleSpec is a policy rule the dashboard added. Its priority is always in
// [rulePriorityMin, rulePriorityMax].
type RuleSpec struct {
	ID       int    `json:"id"`
	Family   string `json:"family"`
	Priority int    `json:"priority"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	IIF      string `json:"iif,omitempty"`
	OIF      string `json:"oif,omitempty"`
	FWMark   string `json:"fwmark,omitempty"`
	// Action is lookup (Table), blackhole, unreachable or prohibit.
	Action  string `json:"action"`
	Table   int    `json:"table,omitempty"`
	Comment string `json:"comment,omitempty"`
	Made
}

// The policy-rule priorities the dashboard writes in. Distributions use 0,
// 32766 and 32767, Tailscale the 52xx, and nothing common the five-digit
// range below 20000.
const (
	rulePriorityMin = 10000
	rulePriorityMax = 19999
)

// NamespaceSpec is a named network namespace the dashboard created.
type NamespaceSpec struct {
	Name string `json:"name"`
	Made
}

// ShapeSpec is one device's queue discipline and speed limits.
type ShapeSpec struct {
	Device string `json:"device"`
	// Qdisc is the root discipline when no egress limit is set: fq_codel,
	// cake or fq. Empty leaves the kernel's default.
	Qdisc string `json:"qdisc,omitempty"`
	// EgressKbit and IngressKbit are the upload and download limits in
	// kilobits a second; zero is no limit.
	EgressKbit  int `json:"egressKbit,omitempty"`
	IngressKbit int `json:"ingressKbit,omitempty"`
	Made
}

// ForwardSpec is a port forward: traffic arriving on a port is sent on to
// another address and port.
type ForwardSpec struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Protocol is tcp, udp or both.
	Protocol string `json:"protocol"`
	// Interface is the device the traffic arrives on; empty is any.
	Interface string `json:"interface,omitempty"`
	// Ports is the port or range arriving, in nft's 8000-8010 form.
	Ports string `json:"ports"`
	// Target is the address it is sent to, and TargetPort the port there;
	// empty keeps the port it arrived on (a range maps one to one).
	Target     string `json:"target"`
	TargetPort string `json:"targetPort,omitempty"`
	// SourceNAT is auto, always or never. Auto masquerades unless the target
	// is on a network this host is the gateway of, where the reply comes back
	// through here anyway and the visitor's own address can be kept.
	SourceNAT string `json:"sourceNat"`
	// Sources narrows who may use it; empty is anyone.
	Sources []string `json:"sources,omitempty"`
	Enabled bool     `json:"enabled"`
	Made
}

// NATSpec translates a network's outgoing traffic: a private bridge, a
// namespace or a VPN reaching the internet through this host.
type NATSpec struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Source is the network translated.
	Source string `json:"source"`
	// Interface is the device it leaves through.
	Interface string `json:"interface"`
	// ToAddress, when set, is a fixed source address (SNAT) instead of
	// whatever the interface holds (masquerade).
	ToAddress string `json:"toAddress,omitempty"`
	// Owner names what made it when that was not a person directly: a
	// WireGuard exit ("wireguard:wg0"). Removing the owner removes the entry.
	Owner   string `json:"owner,omitempty"`
	Enabled bool   `json:"enabled"`
	Made
}

// LimitSpec caps how fast connections may arrive at a port.
type LimitSpec struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Ports    string `json:"ports"`
	// Rate new connections per Per (second, minute or hour), allowing a
	// Burst over it. Zero Rate is no rate limit, leaving only MaxConnections.
	Rate  int    `json:"rate,omitempty"`
	Per   string `json:"per,omitempty"`
	Burst int    `json:"burst,omitempty"`
	// PerSource counts each source address separately; otherwise the limit
	// is the port's as a whole.
	PerSource bool `json:"perSource"`
	// MaxConnections refuses a source holding more than this many at once.
	MaxConnections int `json:"maxConnections,omitempty"`
	// Action is drop or reject.
	Action  string `json:"action"`
	Enabled bool   `json:"enabled"`
	Made
}

// BlocklistSpec is a set of networks whose traffic is dropped.
type BlocklistSpec struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Kind is manual, country or feed.
	Kind      string   `json:"kind"`
	Countries []string `json:"countries,omitempty"`
	URL       string   `json:"url,omitempty"`
	// Entries are a manual list's networks. A fetched list's are cached in
	// lists/<id>.txt instead, so the spec stays small enough to read.
	Entries []string `json:"entries,omitempty"`
	Enabled bool     `json:"enabled"`
	// Refreshed, Count and Error describe the last fetch.
	Refreshed time.Time `json:"refreshed,omitempty"`
	Count     int       `json:"count"`
	Error     string    `json:"error,omitempty"`
	Made
}

// emptySpec is what a host the dashboard has never changed reads as.
func emptySpec() *Spec {
	return &Spec{Version: specVersion, NextID: 1, Sysctls: map[string]string{}}
}

func (s *Service) specPath() string { return filepath.Join(s.paths.Dir, "spec.json") }

// loadSpec reads the spec, or the empty one where none was written yet. A
// spec that cannot be parsed is an error rather than an empty spec: treating
// it as empty would render boot files that drop everything it describes.
func (s *Service) loadSpec() (*Spec, error) {
	b, err := os.ReadFile(s.specPath())
	if errors.Is(err, fs.ErrNotExist) {
		return emptySpec(), nil
	}
	if err != nil {
		return nil, err
	}
	spec := emptySpec()
	if err := json.Unmarshal(b, spec); err != nil {
		return nil, fmt.Errorf("%s cannot be read (%v); fix or move it before changing the network here", s.specPath(), err)
	}
	if spec.Sysctls == nil {
		spec.Sysctls = map[string]string{}
	}
	if spec.NextID < 1 {
		spec.NextID = 1
	}
	return spec, nil
}

// Spec is a copy of what the dashboard has made, for the read routes.
func (s *Service) Spec() (*Spec, error) {
	return s.loadSpec()
}

// takeID hands out the next entry id.
func (sp *Spec) takeID() int {
	id := sp.NextID
	sp.NextID++
	return id
}

// clone is a deep copy, so a change is made to a copy and the original stays
// what is on disk until the apply has worked.
func (sp *Spec) clone() *Spec {
	b, _ := json.Marshal(sp)
	out := emptySpec()
	_ = json.Unmarshal(b, out)
	return out
}

// link returns the managed device of that name.
func (sp *Spec) link(name string) (*LinkSpec, bool) {
	for i := range sp.Links {
		if sp.Links[i].Name == name {
			return &sp.Links[i], true
		}
	}
	return nil, false
}

// writeFileAtomic writes through a temporary file and a rename, so a reader
// — the boot unit, sysctl, systemd-resolved — never sees half of a file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
