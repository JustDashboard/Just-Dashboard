package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NamespaceDetail is one namespace read as a network of its own: its devices,
// its routes in both families, the resolver its processes are told to use
// and what listens inside it. Each part reports whether it was read, so a
// namespace whose routes could not be read never looks like one without
// routes. Nothing here enters the namespace to change it.
type NamespaceDetail struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Managed   bool      `json:"managed"`
	Image     string    `json:"image,omitempty"`
	PID       int       `json:"pid,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`

	Devices     []NamespaceDevice `json:"devices"`
	DevicesRead Reading           `json:"devicesRead"`
	Routes      []NamespaceRoute  `json:"routes"`
	RoutesRead  Reading           `json:"routesRead"`
	// DNS is the resolver configuration processes in the namespace read.
	DNS     *NamespaceDNS `json:"dns,omitempty"`
	DNSRead Reading       `json:"dnsRead"`
	// Listeners are the TCP and UDP sockets listening inside it.
	Listeners     []NamespaceListener `json:"listeners"`
	ListenersRead Reading             `json:"listenersRead"`
}

// NamespaceRoute is one route of a namespace's own tables.
type NamespaceRoute struct {
	Family      string `json:"family"`
	Destination string `json:"destination"`
	Type        string `json:"type,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	Device      string `json:"device,omitempty"`
	Source      string `json:"source,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Metric      int    `json:"metric,omitempty"`
	Table       string `json:"table,omitempty"`
}

// NamespaceDNS is a resolv.conf as the namespace's processes read it.
type NamespaceDNS struct {
	Nameservers []string `json:"nameservers"`
	Search      []string `json:"search"`
	Options     []string `json:"options"`
}

// NamespaceListener is one listening socket.
type NamespaceListener struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
}

// namespaceTarget is a namespace resolved from a request: a named one this
// host has, or a running container's by name. Commands are aimed with it.
type namespaceTarget struct {
	name, kind, image string
	pid               int
	managed           bool
}

// ip runs ip inside the namespace.
func (t namespaceTarget) ip(ctx context.Context, args ...string) (string, error) {
	if t.kind == "container" {
		return run(ctx, "nsenter", append([]string{"--target", strconv.Itoa(t.pid), "--net", "--", "ip"}, args...)...)
	}
	return run(ctx, "ip", append([]string{"-n", t.name}, args...)...)
}

// tool runs another command inside the namespace's network.
func (t namespaceTarget) tool(ctx context.Context, name string, args ...string) (string, error) {
	if t.kind == "container" {
		return run(ctx, "nsenter", append([]string{"--target", strconv.Itoa(t.pid), "--net", "--", name}, args...)...)
	}
	return run(ctx, "ip", append([]string{"netns", "exec", t.name, name}, args...)...)
}

// resolveNamespace finds the namespace a request names. A container is found
// in the inventory the api layer read, never by a PID the client supplies.
func (s *Service) resolveNamespace(ctx context.Context, kind, name string, inv Inventory) (namespaceTarget, error) {
	switch kind {
	case "named", "":
		if err := ValidNamespace(name); err != nil {
			return namespaceTarget{}, err
		}
		out, err := run(ctx, "ip", "-j", "netns", "list")
		if err != nil {
			return namespaceTarget{}, err
		}
		named, err := parseNamespaces(out)
		if err != nil {
			return namespaceTarget{}, err
		}
		for _, n := range named {
			if n.Name == name {
				sp, err := s.loadSpec()
				if err != nil {
					sp = emptySpec()
				}
				return namespaceTarget{name: name, kind: "named", managed: hasNamespace(sp, name)}, nil
			}
		}
		return namespaceTarget{}, fmt.Errorf("namespace %s: %w", name, ErrNotFound)
	case "container":
		for _, c := range inv.Containers {
			if c.Name == name && c.PID > 0 {
				return namespaceTarget{name: c.Name, kind: "container", image: c.Image, pid: c.PID}, nil
			}
		}
		if inv.ContainersError != "" {
			return namespaceTarget{}, fmt.Errorf("Docker's container list could not be read: %s", firstLines(inv.ContainersError, 1))
		}
		return namespaceTarget{}, fmt.Errorf("running container %s: %w", name, ErrNotFound)
	}
	return namespaceTarget{}, fmt.Errorf("a namespace kind is named or container")
}

// NamespaceDetail reads one namespace's devices, routes, resolver and
// listeners.
func (s *Service) NamespaceDetail(ctx context.Context, kind, name string, inv Inventory) (*NamespaceDetail, error) {
	t, err := s.resolveNamespace(ctx, kind, name, inv)
	if err != nil {
		return nil, err
	}
	d := &NamespaceDetail{
		Name: t.name, Kind: t.kind, Managed: t.managed, Image: t.image, PID: t.pid,
		CheckedAt: time.Now().UTC(),
		Devices:   []NamespaceDevice{}, Routes: []NamespaceRoute{}, Listeners: []NamespaceListener{},
	}
	if raw, err := t.ip(ctx, "-j", "addr", "show"); err != nil {
		d.DevicesRead = readingOf(err)
	} else if devices, err := parseNamespaceDevices(raw); err != nil {
		d.DevicesRead = Reading{State: "failed", Reason: err.Error()}
	} else {
		d.Devices, d.DevicesRead = devices, readingOK()
	}

	d.RoutesRead = readingOK()
	for _, family := range []string{"inet", "inet6"} {
		args := []string{"-j", "route", "show", "table", "all"}
		if family == "inet6" {
			args = []string{"-j", "-6", "route", "show", "table", "all"}
		}
		raw, err := t.ip(ctx, args...)
		if err != nil {
			d.RoutesRead = readingOf(err)
			d.RoutesRead.Reason = familyLabel(family) + ": " + d.RoutesRead.Reason
			continue
		}
		routes, err := parseNamespaceRoutes(raw, family)
		if err != nil {
			d.RoutesRead = Reading{State: "failed", Reason: familyLabel(family) + ": " + err.Error()}
			continue
		}
		d.Routes = append(d.Routes, routes...)
	}

	var resolv []byte
	if t.kind == "container" {
		resolv, err = os.ReadFile(filepath.Join(procRoot, strconv.Itoa(t.pid), "root", "etc", "resolv.conf"))
	} else {
		// ip netns exec puts /etc/netns/<name>/resolv.conf in place where
		// one exists, exactly as the namespace's own processes see it.
		var out string
		out, err = t.tool(ctx, "cat", "/etc/resolv.conf")
		resolv = []byte(out)
	}
	if err != nil {
		d.DNSRead = readingOf(err)
	} else {
		d.DNS, d.DNSRead = parseResolvConf(string(resolv)), readingOK()
	}

	if raw, err := t.tool(ctx, "ss", "-H", "-l", "-t", "-u", "-n"); err != nil {
		d.ListenersRead = readingOf(err)
	} else {
		d.Listeners, d.ListenersRead = parseListeners(raw), readingOK()
	}
	return d, nil
}

// NamespaceLookup asks a namespace's kernel how it would route to a literal
// address: the same question the Routing page asks of the host. It sends
// nothing.
func (s *Service) NamespaceLookup(ctx context.Context, kind, name, target, source string, inv Inventory) (*Path, error) {
	addr, err := ParseAddr(target)
	if err != nil {
		return nil, err
	}
	args := []string{"-j"}
	if addr.Is6() {
		args = append(args, "-6")
	}
	args = append(args, "route", "get", addr.String())
	if strings.TrimSpace(source) != "" {
		src, err := ParseAddr(source)
		if err != nil {
			return nil, err
		}
		if src.Is4() != addr.Is4() {
			return nil, fmt.Errorf("the source and the target are one family")
		}
		args = append(args, "from", src.String())
	}
	t, err := s.resolveNamespace(ctx, kind, name, inv)
	if err != nil {
		return nil, err
	}
	out, err := t.ip(ctx, args...)
	if err != nil {
		return nil, err
	}
	p, err := parseRouteGet(out, Path{Address: addr.String()})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// parseNamespaceRoutes reads `ip -j route show table all`, leaving out the
// kernel's local and broadcast entries that every namespace has.
func parseNamespaceRoutes(out, family string) ([]NamespaceRoute, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return []NamespaceRoute{}, nil
	}
	var raw []struct {
		Dst      string `json:"dst"`
		Type     string `json:"type"`
		Gateway  string `json:"gateway"`
		Dev      string `json:"dev"`
		PrefSrc  string `json:"prefsrc"`
		Protocol string `json:"protocol"`
		Metric   int    `json:"metric"`
		Table    string `json:"table"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("ip route printed something unreadable: %w", err)
	}
	routes := []NamespaceRoute{}
	for _, r := range raw {
		if r.Table == "local" || r.Type == "local" || r.Type == "broadcast" || r.Type == "anycast" || r.Type == "multicast" {
			continue
		}
		if family == "inet6" && strings.HasPrefix(r.Dst, "fe80::") {
			continue
		}
		routes = append(routes, NamespaceRoute{
			Family: family, Destination: r.Dst, Type: r.Type, Gateway: r.Gateway, Device: r.Dev,
			Source: r.PrefSrc, Protocol: r.Protocol, Metric: r.Metric, Table: r.Table,
		})
	}
	return routes, nil
}

// parseResolvConf reads the nameservers, search domains and options.
func parseResolvConf(text string) *NamespaceDNS {
	d := &NamespaceDNS{Nameservers: []string{}, Search: []string{}, Options: []string{}}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") || strings.HasPrefix(fields[0], ";") {
			continue
		}
		switch fields[0] {
		case "nameserver":
			d.Nameservers = append(d.Nameservers, fields[1])
		case "search", "domain":
			d.Search = append(d.Search, fields[1:]...)
		case "options":
			d.Options = append(d.Options, fields[1:]...)
		}
	}
	return d
}

// parseListeners reads `ss -H -l -t -u -n`: netid, state, queues, local,
// peer.
func parseListeners(out string) []NamespaceListener {
	listeners := []NamespaceListener{}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		local := fields[4]
		i := strings.LastIndex(local, ":")
		if i <= 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil {
			continue
		}
		address := strings.Trim(local[:i], "[]")
		if j := strings.Index(address, "%"); j >= 0 {
			address = address[:j]
		}
		l := NamespaceListener{Protocol: fields[0], Address: address, Port: port}
		key := l.Protocol + " " + l.Address + " " + local[i+1:]
		if seen[key] {
			continue
		}
		seen[key] = true
		listeners = append(listeners, l)
	}
	sort.SliceStable(listeners, func(i, j int) bool {
		if listeners[i].Port != listeners[j].Port {
			return listeners[i].Port < listeners[j].Port
		}
		return listeners[i].Protocol < listeners[j].Protocol
	})
	return listeners
}
