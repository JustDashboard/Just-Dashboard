package proxysvc

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// The fleet scan: every name this server serves over TLS, and every watched
// endpoint, graded in one pass. One report at a time answers "is this site
// right"; the question after a config change or a renewal run is "is any of
// them wrong", and asking it one name at a time is how the one that is gets
// missed.

// FleetConcurrency is how many reports run at once. A report is a few dozen
// handshakes against one host, so four keeps a pass over forty names to a few
// minutes without looking like a scan to whoever runs the far end.
const FleetConcurrency = 4

// FleetTarget is one name and port the fleet scan reaches, and why it is on
// the list: the sites that declare it and whether it is watched.
type FleetTarget struct {
	Host    string   `json:"host"`
	Port    int      `json:"port"`
	Sites   []string `json:"sites"`
	Watched bool     `json:"watched"`
}

// FleetTargets is the fleet from the sites and the watch list: each enabled
// TLS site's names on each port it listens with ssl, and each watched
// endpoint, once per host and port. A wildcard, a regex or nginx's catch-all
// "_" names no host a handshake can ask for, so they are left out rather than
// scanned under a name nobody visits. A watched endpoint pinned to an address
// is scanned by its name, since the report dials what the name resolves to.
func FleetTargets(vhosts []VHost, watched []WatchedEndpoint) []FleetTarget {
	byKey := map[string]*FleetTarget{}
	add := func(host string, port int) *FleetTarget {
		key := host + "\x00" + strconv.Itoa(port)
		t, ok := byKey[key]
		if !ok {
			t = &FleetTarget{Host: host, Port: port, Sites: []string{}}
			byKey[key] = t
		}
		return t
	}
	for _, v := range vhosts {
		if !v.Enabled {
			continue
		}
		for _, t := range vhostTargets(v) {
			ft := add(t.Host, t.Port)
			if !slices.Contains(ft.Sites, v.Name) {
				ft.Sites = append(ft.Sites, v.Name)
			}
		}
	}
	for _, e := range watched {
		// A network probe connects and nothing more; it has no certificate
		// to grade.
		if e.Kind == WatchTCP {
			continue
		}
		t, err := ParseScanTarget(e.Domain, e.Port)
		if err != nil {
			continue
		}
		add(t.Host, t.Port).Watched = true
	}
	out := make([]FleetTarget, 0, len(byKey))
	for _, t := range byKey {
		slices.Sort(t.Sites)
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b FleetTarget) int {
		return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(a.Port, b.Port))
	})
	return out
}

// vhostTargets is what one site serves over TLS. An nginx file's names are
// read across all its server blocks, so a name only in the port-80 redirect
// block is scanned on the ssl port too; its report then says what that name
// actually gets there.
func vhostTargets(v VHost) []ScanTarget {
	var out []ScanTarget
	if v.Kind == KindCaddy {
		// The Caddyfile entry carries no listen and no TLS flag: Caddy serves
		// HTTPS for every site address that does not say http:// or name a
		// bare port.
		if len(v.Listen) > 0 && !v.TLS {
			return nil
		}
		for _, name := range v.ServerNames {
			if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, ":") || strings.Contains(name, "*") {
				continue
			}
			if t, err := ParseScanTarget(name, 0); err == nil {
				out = append(out, t)
			}
		}
		return out
	}
	if !v.TLS {
		return nil
	}
	ports := tlsListenPorts(v.Listen)
	if len(ports) == 0 {
		ports = []int{443}
	}
	for _, name := range v.ServerNames {
		name = strings.TrimPrefix(name, ".")
		if name == "" || name == "_" || strings.HasPrefix(name, "~") || strings.ContainsAny(name, "*$") {
			continue
		}
		for _, port := range ports {
			if t, err := ParseScanTarget(name, port); err == nil {
				out = append(out, t)
			}
		}
	}
	return out
}

// tlsListenPorts is the ports of the listen directives that take ssl. An
// address without a port listens on 80, as nginx reads it.
func tlsListenPorts(listen []string) []int {
	var ports []int
	for _, l := range listen {
		fields := strings.Fields(l)
		if len(fields) == 0 || !slices.Contains(fields[1:], "ssl") || strings.HasPrefix(fields[0], "unix:") {
			continue
		}
		addr := fields[0]
		if i := strings.LastIndex(addr, "]:"); i >= 0 {
			addr = addr[i+2:]
		} else if strings.HasPrefix(addr, "[") {
			addr = "80"
		} else if i := strings.LastIndex(addr, ":"); i >= 0 {
			addr = addr[i+1:]
		}
		port, err := strconv.Atoi(addr)
		if err != nil {
			port = 80
		}
		if port > 0 && port <= 65535 && !slices.Contains(ports, port) {
			ports = append(ports, port)
		}
	}
	return ports
}

// ScanFleet reports on each target, FleetConcurrency at a time, and hands
// each finished report to done one at a time, with how many have finished.
// A report cut short because ctx ended is not handed on: it says nothing
// about the target. Targets not started when ctx ends are not started.
func ScanFleet(ctx context.Context, targets []FleetTarget,
	scan func(ctx context.Context, host string, port int) *TLSScan,
	done func(finished int, target FleetTarget, report *TLSScan),
) {
	var (
		mu       sync.Mutex
		finished int
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, FleetConcurrency)
	for _, t := range targets {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			report := scan(ctx, t.Host, t.Port)
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			finished++
			done(finished, t, report)
		}()
	}
	wg.Wait()
}
