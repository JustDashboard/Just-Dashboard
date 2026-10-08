package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Namespace is a network namespace on this host: one made with `ip netns`, or
// the one a running container lives in. Each has its own devices, addresses
// and routes, which is why the Interfaces page lists them apart.
type Namespace struct {
	Name string `json:"name"`
	// Kind is "named" for an `ip netns` namespace and "container" for a
	// running container's.
	Kind string `json:"kind"`
	// ID is the kernel's namespace id where it has assigned one.
	ID *int `json:"id,omitempty"`
	// Managed marks a namespace the dashboard made, which it may remove.
	Managed bool `json:"managed"`
	// Image and PID are a container's.
	Image   string            `json:"image,omitempty"`
	PID     int               `json:"pid,omitempty"`
	Devices []NamespaceDevice `json:"devices"`
}

// NamespaceDevice is a device inside a namespace.
type NamespaceDevice struct {
	Name      string   `json:"name"`
	State     string   `json:"state"`
	MTU       int      `json:"mtu"`
	MAC       string   `json:"mac,omitempty"`
	Addresses []string `json:"addresses"`
}

// ipNetns is one entry of `ip -j netns list`.
type ipNetns struct {
	Name string `json:"name"`
	ID   *int   `json:"id"`
}

// containerReads bounds how many containers' namespaces are entered at once.
// Each read is a process and a namespace switch; a host with two hundred
// containers should not start two hundred of them together.
const containerReads = 4

// Namespaces lists the named namespaces and one per running container, each
// with its devices. A namespace whose devices cannot be read — a container
// that exited between the listing and the read — is listed without them
// rather than failing the page.
func (s *Service) Namespaces(ctx context.Context, inv Inventory) ([]Namespace, error) {
	out, err := run(ctx, "ip", "-j", "netns", "list")
	if err != nil {
		return nil, err
	}
	named, err := parseNamespaces(out)
	if err != nil {
		return nil, err
	}
	sp, err := s.loadSpec()
	if err != nil {
		sp = emptySpec()
	}
	all := []Namespace{}
	for _, n := range named {
		all = append(all, Namespace{Name: n.Name, Kind: "named", ID: n.ID, Managed: hasNamespace(sp, n.Name), Devices: []NamespaceDevice{}})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	first := len(all)
	for _, c := range inv.Containers {
		if c.PID <= 0 {
			continue
		}
		all = append(all, Namespace{Name: c.Name, Kind: "container", Image: c.Image, PID: c.PID, Devices: []NamespaceDevice{}})
	}
	sort.SliceStable(all[first:], func(i, j int) bool { return all[first+i].Name < all[first+j].Name })

	sem := make(chan struct{}, containerReads)
	var wg sync.WaitGroup
	for i := range all {
		wg.Add(1)
		sem <- struct{}{}
		go func(n *Namespace) {
			defer wg.Done()
			defer func() { <-sem }()
			var raw string
			var err error
			if n.Kind == "container" {
				raw, err = run(ctx, "nsenter", "--target", strconv.Itoa(n.PID), "--net", "--", "ip", "-j", "addr", "show")
			} else {
				raw, err = run(ctx, "ip", "-n", n.Name, "-j", "addr", "show")
			}
			if err != nil {
				return
			}
			if devices, err := parseNamespaceDevices(raw); err == nil {
				n.Devices = devices
			}
		}(&all[i])
	}
	wg.Wait()
	return all, nil
}

func parseNamespaces(out string) ([]ipNetns, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var list []ipNetns
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("ip netns printed something unreadable: %w", err)
	}
	return list, nil
}

// fallbackTunnels are the devices the kernel makes in every new namespace once
// a tunnel module is loaded. They hold nothing and are never used; listing
// them would put five rows of noise under every container.
var fallbackTunnels = map[string]bool{
	"gre0": true, "gretap0": true, "erspan0": true, "ip6tnl0": true, "ip6gre0": true,
	"tunl0": true, "sit0": true, "ip_vti0": true, "ip6_vti0": true,
}

// parseNamespaceDevices reads `ip -j addr show` of one namespace. Loopback and
// the fallback tunnel devices are left out: they are plumbing, not something
// to look at.
func parseNamespaceDevices(out string) ([]NamespaceDevice, error) {
	var raw []struct {
		Name     string `json:"ifname"`
		State    string `json:"operstate"`
		MTU      int    `json:"mtu"`
		MAC      string `json:"address"`
		LinkType string `json:"link_type"`
		AddrInfo []struct {
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("ip addr printed something unreadable: %w", err)
	}
	devices := []NamespaceDevice{}
	for _, d := range raw {
		if d.LinkType == "loopback" || (fallbackTunnels[d.Name] && len(d.AddrInfo) == 0) {
			continue
		}
		dev := NamespaceDevice{Name: d.Name, State: strings.ToLower(d.State), MTU: d.MTU, MAC: d.MAC, Addresses: []string{}}
		for _, a := range d.AddrInfo {
			dev.Addresses = append(dev.Addresses, a.Local+"/"+strconv.Itoa(a.PrefixLen))
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

// NamespaceRequest is a namespace to create, optionally with a veth pair that
// connects it to this host.
type NamespaceRequest struct {
	Name string       `json:"name"`
	Veth *VethRequest `json:"veth,omitempty"`
}

// VethRequest is the pair that connects a new namespace. HostName stays here
// (as a port of Bridge when one is named, or with HostAddress), PeerName goes
// into the namespace and takes PeerAddress.
type VethRequest struct {
	HostName    string `json:"hostName"`
	PeerName    string `json:"peerName"`
	Bridge      string `json:"bridge,omitempty"`
	HostAddress string `json:"hostAddress,omitempty"`
	PeerAddress string `json:"peerAddress,omitempty"`
}

// CreateNamespace makes a named namespace, and with a veth request the pair
// that connects it, in one change.
func (s *Service) CreateNamespace(ctx context.Context, req NamespaceRequest, client, actor string) (*NamespaceSpec, error) {
	name := strings.TrimSpace(req.Name)
	if err := ValidNamespace(name); err != nil {
		return nil, err
	}
	var veth LinkSpec
	var peerAddr string
	if v := req.Veth; v != nil {
		hostAddr := ""
		if strings.TrimSpace(v.HostAddress) != "" {
			a, err := cleanAddresses([]string{v.HostAddress})
			if err != nil {
				return nil, err
			}
			hostAddr = a[0]
		}
		if strings.TrimSpace(v.PeerAddress) != "" {
			a, err := cleanAddresses([]string{v.PeerAddress})
			if err != nil {
				return nil, err
			}
			peerAddr = a[0]
		}
		l, err := LinkRequest{
			Name: v.HostName, Kind: "veth", Peer: v.PeerName, PeerNamespace: name,
			Master: v.Bridge, Up: true,
		}.spec()
		if err != nil {
			return nil, err
		}
		if hostAddr != "" {
			l.Addresses = []string{hostAddr}
		}
		veth = l
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	next := sp.clone()
	if hasNamespace(next, name) {
		return nil, fmt.Errorf("%s: %w", name, ErrExists)
	}
	out, err := run(ctx, "ip", "-j", "netns", "list")
	if err != nil {
		return nil, err
	}
	existing, err := parseNamespaces(out)
	if err != nil {
		return nil, err
	}
	for _, n := range existing {
		if n.Name == name {
			return nil, fmt.Errorf("%s: %w", name, ErrExists)
		}
	}
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return nil, err
	}
	if veth.Name != "" {
		if _, ok := st.by[veth.Name]; ok {
			return nil, fmt.Errorf("%s: %w", veth.Name, ErrExists)
		}
		if veth.Master != "" {
			bridge, err := st.bridgeTarget(veth.Master)
			if err != nil {
				return nil, err
			}
			if len(veth.Addresses) > 0 {
				return nil, guarded("%s would be a port of %s, and a bridge port's addresses stop working; give the address to the namespace's end", veth.Name, bridge.Name)
			}
		}
	}

	ns := NamespaceSpec{Name: name, Made: stamp(actor)}
	next.Namespaces = append(next.Namespaces, ns)
	mini := &Spec{Namespaces: []NamespaceSpec{ns}}
	if veth.Name != "" {
		veth.Made = stamp(actor)
		next.Links = append(next.Links, veth)
		mini.Links = []LinkSpec{veth}
		if peerAddr != "" {
			a := AddressSpec{ID: next.takeID(), Link: veth.Peer, CIDR: peerAddr, Made: stamp(actor)}
			next.Addresses = append(next.Addresses, a)
			mini.Addresses = []AddressSpec{a}
		}
	}
	lines := batchLines(mini)
	undo := func(ctx context.Context) {
		if veth.Name != "" {
			s.best(ctx, "ip", "link", "del", veth.Name)
		}
		s.best(ctx, "ip", "netns", "del", name)
	}
	err = s.commit(ctx, next, step{
		apply:  applying(func(ctx context.Context) error { return applyBatch(ctx, lines) }, undo),
		undo:   undo,
		verify: verifyPath(st.path),
	})
	if err != nil {
		return nil, err
	}
	return &ns, nil
}

// DeleteNamespace removes a namespace the dashboard made, with the veth pairs
// that lead into it. Processes still running inside keep their devices until
// they exit; the name is what goes.
func (s *Service) DeleteNamespace(ctx context.Context, name, client, actor string) error {
	if err := ValidNamespace(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := sp.clone()
	if !hasNamespace(next, name) {
		out, err := run(ctx, "ip", "-j", "netns", "list")
		if err != nil {
			return err
		}
		existing, err := parseNamespaces(out)
		if err != nil {
			return err
		}
		for _, n := range existing {
			if n.Name == name {
				return fmt.Errorf("%s: %w", name, ErrNotManaged)
			}
		}
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return err
	}
	var gone []LinkSpec
	var kept []LinkSpec
	for _, l := range next.Links {
		if l.Kind == "veth" && l.PeerNamespace == name {
			gone = append(gone, l)
			continue
		}
		kept = append(kept, l)
	}
	var restore Spec
	for _, l := range gone {
		if live, ok := st.by[l.Name]; ok && live.Guard != "" {
			return guarded("%s leads into %s and cannot be removed: %s", l.Name, name, live.Guard)
		}
		if dep := dependents(next, l.Name); len(dep) > 0 {
			return fmt.Errorf("%s is used by a %s; remove that first", l.Name, dep[0])
		}
	}
	for _, ns := range next.Namespaces {
		if ns.Name == name {
			restore.Namespaces = append(restore.Namespaces, ns)
		}
	}
	restore.Links = gone
	var nsKept []NamespaceSpec
	for _, ns := range next.Namespaces {
		if ns.Name != name {
			nsKept = append(nsKept, ns)
		}
	}
	var addrKept []AddressSpec
	for _, a := range next.Addresses {
		if _, ok := peerOf(gone, a.Link); ok {
			restore.Addresses = append(restore.Addresses, a)
			continue
		}
		addrKept = append(addrKept, a)
	}
	// Members of a bridge lose their master with the bridge, not with the
	// namespace, so nothing else in the spec refers to what is removed here.
	next.Links, next.Namespaces, next.Addresses = kept, nsKept, addrKept

	undo := func(ctx context.Context) {
		// The name or some pairs may still exist after a partial deletion.
		// Continue past those so every pair already removed is restored.
		if _, err := runStdin(ctx, []byte(strings.Join(batchLines(&restore), "\n")+"\n"), "ip", "-force", "-batch", "-"); err != nil {
			s.log.Warn("network: namespace rollback batch reported failures", "err", err)
		}
	}
	return s.commit(ctx, next, step{
		apply: applying(func(ctx context.Context) error {
			for _, l := range gone {
				if _, err := run(ctx, "ip", "link", "del", l.Name); err != nil && !isGone(err) {
					return err
				}
			}
			if _, err := run(ctx, "ip", "netns", "del", name); err != nil && !isGone(err) {
				return err
			}
			return nil
		}, undo),
		undo:   undo,
		verify: verifyPath(st.path),
	})
}
