package netx

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// A port kept to the networks that may reach it.
//
// A container's published port is a NAT rule Docker writes in front of the
// host firewall's input chain, so an ufw or firewalld rule admitting port
// 5432 from one address restricts nothing for a published database: the
// connection is translated and forwarded before that chain is consulted. The
// gateway table's prerouting chain sees the connection first, addressed to
// this host and not yet translated, so a drop there refuses a source for a
// published container and a native listener alike — and, being a drop in a
// table of its own, it holds whatever ufw, firewalld or Docker accept later.
//
// The drop sits after the table's early returns: established connections,
// loopback and the trusted sets. Those are the networks the dashboard itself
// is reached from, and anybody on them can already do everything to the
// database the dashboard can; the reading names them so the page can too.
// It sits before the exceptions, which are about blocklists and limits: an
// exception for a network is not a way past a list somebody keeps a port to.

// PortAllowlistSpec keeps one port of this host to the networks it names: a
// new connection to it from anywhere else is dropped. Another feature owns
// each one, a database's Network page, and it goes with the reach it was
// made for.
type PortAllowlistSpec struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Owner is what made it, as kind:name ("database:shop-db"). An owner
	// keeps one port.
	Owner    string `json:"owner"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	// Sources are the networks let in, masked, merged and sorted, a single
	// address written bare.
	Sources []string `json:"sources"`
	Made
}

// maxPortSources bounds a list to what an operator keeps by hand. The host
// firewall gets a rule per source as well, and a list longer than this is a
// network that wants a CIDR.
const maxPortSources = 16

var portOwnerRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}:[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// NormalizePortSources turns the addresses an operator typed into the list a
// port is kept to: each an address or a network, masked to its network,
// merged with any it lies inside, IPv4 first. An empty list, every address,
// loopback (always let in) and anything that is not a unicast source are
// refused with what to do instead.
func NormalizePortSources(raw []string) ([]string, error) {
	var nets []netip.Prefix
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		p, err := ParsePrefix(entry)
		if err != nil {
			return nil, err
		}
		p = p.Masked()
		a := p.Addr()
		switch {
		case p.Bits() == 0:
			return nil, fmt.Errorf("%s is every address there is; choose Anywhere instead of a list", entry)
		case a.Is4() && p.Bits() < 8 || a.Is6() && p.Bits() < 16:
			return nil, fmt.Errorf("%s is wider than a /8 (IPv4) or /16 (IPv6), which is most of the internet; choose Anywhere, or name the networks", entry)
		case a.IsLoopback():
			return nil, fmt.Errorf("%s is this server, which always reaches it", entry)
		case a.IsUnspecified() || a.IsMulticast():
			return nil, fmt.Errorf("%s is not an address a connection comes from", entry)
		}
		nets = append(nets, p)
	}
	nets = mergePrefixes(nets)
	if len(nets) == 0 {
		return nil, fmt.Errorf("name at least one address or network to let in")
	}
	if len(nets) > maxPortSources {
		return nil, fmt.Errorf("a port is kept to at most %d addresses or networks; this list has %d, so join neighbours into one network", maxPortSources, len(nets))
	}
	out := make([]string, len(nets))
	for i, p := range nets {
		out[i] = prefixList([]netip.Prefix{p})
	}
	return out, nil
}

// normPortAllowlist validates an entry and returns it as it is stored and
// rendered.
func normPortAllowlist(p PortAllowlistSpec) (PortAllowlistSpec, error) {
	var err error
	if p.Name, err = CleanLabel(p.Name, 64); err != nil {
		return p, err
	}
	if !portOwnerRe.MatchString(p.Owner) {
		return p, fmt.Errorf("%q is not an owner a kept port can name", p.Owner)
	}
	if p.Protocol, err = ParseProtocol(p.Protocol, false); err != nil {
		return p, err
	}
	if p.Port < 1 || p.Port > 65535 {
		return p, fmt.Errorf("%d is not a port; ports run from 1 to 65535", p.Port)
	}
	if p.Sources, err = NormalizePortSources(p.Sources); err != nil {
		return p, err
	}
	return p, nil
}

// normPortAllowlists checks every entry, and that no two keep the same port
// or belong to the same owner: two lists for one port would each drop what
// the other admits.
func normPortAllowlists(sp *Spec) ([]PortAllowlistSpec, error) {
	out := make([]PortAllowlistSpec, 0, len(sp.PortAllowlists))
	owners, ports := map[string]bool{}, map[string]string{}
	for _, raw := range sp.PortAllowlists {
		p, err := normPortAllowlist(raw)
		if err != nil {
			return nil, fmt.Errorf("kept port %d (%s): %w", raw.ID, raw.Name, err)
		}
		key := p.Protocol + "/" + strconv.Itoa(p.Port)
		if owners[p.Owner] {
			return nil, fmt.Errorf("kept port %d: %s already keeps a port", p.ID, p.Owner)
		}
		if other, taken := ports[key]; taken {
			return nil, fmt.Errorf("kept port %d: %s is already kept to its own addresses for %s", p.ID, key, other)
		}
		owners[p.Owner], ports[key] = true, p.Owner
		out = append(out, p)
	}
	return out, nil
}

// portAllowlistKey is the comment an entry's rules and counters carry.
func portAllowlistKey(id int) string { return "port-allowlist:" + strconv.Itoa(id) }

// portAllowlistRules are an entry's rules in the pre chain, one per family.
// The sources are an anonymous set in the rule itself, the way a forward's
// are, so the rule reads as what it does. A family the list names nothing
// in is dropped whole: a list of IPv4 networks lets no IPv6 source in.
//
// fib daddr type local is what makes the rule about this host's port rather
// than any 5432 passing through it — a container's outbound connection to
// another server's database is routed, never local. The port is the one the
// visitor dialled: prerouting at mangle priority runs before Docker's or a
// forward's translation rewrites it.
func portAllowlistRules(p PortAllowlistSpec) []string {
	var v4, v6 []netip.Prefix
	for _, raw := range p.Sources {
		if n, err := ParsePrefix(raw); err == nil {
			if n.Addr().Is4() {
				v4 = append(v4, n.Masked())
			} else {
				v6 = append(v6, n.Masked())
			}
		}
	}
	match := fmt.Sprintf("meta l4proto %s th dport %d fib daddr type local", p.Protocol, p.Port)
	tail := fmt.Sprintf("counter drop comment %q", portAllowlistKey(p.ID))
	family := func(saddr, nfproto string, nets []netip.Prefix) string {
		if len(nets) == 0 {
			return fmt.Sprintf("%s meta nfproto %s %s", match, nfproto, tail)
		}
		set := prefixList(nets)
		if len(nets) > 1 {
			set = "{ " + set + " }"
		}
		return fmt.Sprintf("%s %s != %s %s", match, saddr, set, tail)
	}
	return []string{family("ip saddr", "ipv4", v4), family("ip6 saddr", "ipv6", v6)}
}

// PortAllowlistRequest is what an owner keeps its port to.
type PortAllowlistRequest struct {
	Owner    string
	Name     string
	Protocol string
	Port     int
	Sources  []string
}

// SetPortAllowlist keeps a port to the networks named, creating the owner's
// entry or replacing what it holds. The table is replaced in one load, so a
// connection is judged by the old list or the new one, never by neither.
func (s *Service) SetPortAllowlist(ctx context.Context, req PortAllowlistRequest, actor string) (PortAllowlistSpec, error) {
	var saved PortAllowlistSpec
	err := s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		entry, err := normPortAllowlist(PortAllowlistSpec{
			Name: req.Name, Owner: req.Owner, Protocol: req.Protocol, Port: req.Port, Sources: req.Sources,
		})
		if err != nil {
			return false, err
		}
		idx := -1
		for i, p := range next.PortAllowlists {
			switch {
			case p.Owner == entry.Owner:
				idx = i
			case p.Port == entry.Port && p.Protocol == entry.Protocol:
				return false, fmt.Errorf("%w: port %d/%s is already kept to its own addresses for %s", ErrExists, p.Port, p.Protocol, p.Owner)
			}
		}
		if idx >= 0 {
			entry.ID, entry.Made = next.PortAllowlists[idx].ID, next.PortAllowlists[idx].Made
			next.PortAllowlists[idx] = entry
		} else {
			entry.ID, entry.Made = next.takeID(), gwStamp(actor)
			next.PortAllowlists = append(next.PortAllowlists, entry)
		}
		saved = entry
		// Only drops, like a limit: it needs no admission past the host's
		// other filters, so it does not wait on the gateway's capability.
		return false, nil
	})
	if err != nil {
		return PortAllowlistSpec{}, err
	}
	return saved, nil
}

// RemovePortAllowlist lets the owner's port be reached from anywhere its
// binding and the host firewall allow again. An owner with no entry is not
// an error: there is nothing left to take back.
func (s *Service) RemovePortAllowlist(ctx context.Context, owner string) (bool, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return false, err
	}
	if !holdsOwner(sp, owner) {
		return false, nil
	}
	err = s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		kept := next.PortAllowlists[:0]
		for _, p := range next.PortAllowlists {
			if p.Owner != owner {
				kept = append(kept, p)
			}
		}
		next.PortAllowlists = kept
		return false, nil
	})
	return err == nil, err
}

func holdsOwner(sp *Spec, owner string) bool {
	for _, p := range sp.PortAllowlists {
		if p.Owner == owner {
			return true
		}
	}
	return false
}

// PortAllowlistRules is the rules an owner's port would be kept by with
// these sources, as SetPortAllowlist would load them: the entry's own id
// where it has one, the next the spec hands out where it has not.
func (s *Service) PortAllowlistRules(req PortAllowlistRequest) ([]string, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	entry, err := normPortAllowlist(PortAllowlistSpec{
		Name: req.Name, Owner: req.Owner, Protocol: req.Protocol, Port: req.Port, Sources: req.Sources,
	})
	if err != nil {
		return nil, err
	}
	entry.ID = sp.NextID
	for _, p := range sp.PortAllowlists {
		if p.Owner == entry.Owner {
			entry.ID = p.ID
		}
	}
	return portAllowlistRules(entry), nil
}

// PortAllowlistReading is every kept port, whether the kernel holds its
// rules, and who each lets in besides its own list.
type PortAllowlistReading struct {
	// Available is false where nft is not installed, so nothing here could
	// keep a port to a list; Reason says so.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Loaded is the gateway table being in the kernel.
	Loaded bool `json:"loaded"`
	// AlsoAdmitted are the networks the table returns early for before any
	// drop, loopback aside: the dashboard's allowlist ranges and the
	// addresses kept on the Protection page.
	AlsoAdmitted []string            `json:"alsoAdmitted"`
	Entries      []PortAllowlistView `json:"entries"`
}

// PortAllowlistView is one kept port as the kernel has it.
type PortAllowlistView struct {
	PortAllowlistSpec
	// Enforced is both of its rules being in the loaded table.
	Enforced bool `json:"enforced"`
	// Refused is the connection attempts its rules dropped since the table
	// was last loaded.
	Refused uint64   `json:"refused"`
	Rules   []string `json:"rules"`
}

// PortAllowlists reads the kept ports.
func (s *Service) PortAllowlists(ctx context.Context) (PortAllowlistReading, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return PortAllowlistReading{}, err
	}
	out := PortAllowlistReading{Available: true, AlsoAdmitted: []string{}, Entries: []PortAllowlistView{}}
	for _, p := range s.trustedFor(sp) {
		if !p.Addr().IsLoopback() {
			out.AlsoAdmitted = append(out.AlsoAdmitted, prefixList([]netip.Prefix{p}))
		}
	}
	live := liveCounters{Counters: map[string]RuleCounter{}, Rules: map[string]int{}}
	listing, err := run(ctx, "nft", "-t", "-j", "list", "table", "inet", gatewayTable)
	var missing *UnavailableError
	switch {
	case errors.As(err, &missing):
		out.Available, out.Reason = false, "nftables is not installed on this host, so nothing here can refuse the addresses a list leaves out."
	case err == nil:
		if parsed, perr := parseLiveCounters(listing); perr == nil {
			live = parsed
		}
	}
	out.Loaded = live.Loaded
	for _, p := range sp.PortAllowlists {
		key := portAllowlistKey(p.ID)
		out.Entries = append(out.Entries, PortAllowlistView{
			PortAllowlistSpec: p,
			Enforced:          live.Rules[key] == 2,
			Refused:           live.Counters[key].Packets,
			Rules:             portAllowlistRules(p),
		})
	}
	return out, nil
}
