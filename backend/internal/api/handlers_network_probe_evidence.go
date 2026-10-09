package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// Joins between a quick tool's own answer and the owners that explain it:
// the route lookup's policy/NAT/firewall layers, the port check's listener
// and firewall evidence, the SSH scan's saved trust and the site audit's
// proxy site. Each join reads; none sends more traffic than the tool did.

// pathLayers runs the connection-path report for a host-source tuple without
// its measurement.
func (s *Server) pathLayers(ctx context.Context, target, address, protocol string, port int) (*netpath.Result, error) {
	a, err := netip.ParseAddr(address)
	if err != nil {
		return nil, err
	}
	family := "inet"
	if a.Unmap().Is6() {
		family = "inet6"
	}
	req := netpath.Request{SourceKind: "host", Target: target, Family: family, Protocol: protocol, Port: port}
	if _, err := netip.ParseAddr(target); err != nil {
		req.Address = a.Unmap().String()
	}
	return s.executeNetworkInvestigation(ctx, req)
}

func (s *Server) joinRouteLayers(ctx context.Context, req netsec.ProbeRequest, res *netsec.ProbeResult) {
	path, err := s.pathLayers(ctx, req.Target, req.Target, req.Option, req.Port)
	if err != nil {
		res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Policy, NAT and firewall layers", Value: "unavailable: " + err.Error(), Basis: netsec.BasisUnknown})
		return
	}
	netpath.JoinProbe(res, path, "rules", "firewall", "nat", "tunnel")
}

// correlatePortCheck pins the address the check used, so the listener and
// firewall evidence describe the same tuple.
func (s *Server) correlatePortCheck(ctx context.Context, req netsec.ProbeRequest, res *netsec.ProbeResult) {
	address := ""
	for _, f := range res.Facts {
		if f.Label == "Connected address" || f.Label == "Attempted address" {
			if ap, err := netip.ParseAddrPort(f.Value); err == nil {
				address = ap.Addr().Unmap().String()
			}
		}
	}
	if address == "" {
		if a, err := netip.ParseAddr(req.Target); err == nil {
			address = a.Unmap().String()
		}
	}
	if address == "" {
		res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Listener and firewall evidence", Value: "not joined: the name did not resolve to an address", Basis: netsec.BasisUnknown})
		return
	}
	path, err := s.pathLayers(ctx, req.Target, address, "tcp", req.Port)
	if err != nil {
		res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Listener and firewall evidence", Value: "unavailable: " + err.Error(), Basis: netsec.BasisUnknown})
		return
	}
	netpath.JoinProbe(res, path, "route", "firewall", "nat", "owner", "proxy")
	netpath.CorrelatePort(res, path)
}

func (s *Server) compareSSHTrust(ctx context.Context, req netsec.ProbeRequest, res *netsec.ProbeResult) {
	if s.modules.diagnostics == nil {
		netsec.CompareSSHKeys(res, nil)
		return
	}
	trust, err := s.modules.diagnostics.SSHTrustFor(ctx, netsec.SSHTrustTarget(req.Target, req.Port))
	if err != nil {
		res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Saved trust", Value: "unreadable: " + err.Error(), Basis: netsec.BasisUnknown})
	}
	netsec.CompareSSHKeys(res, trust)
}

// proxySiteFor names the enabled proxy site serving a host name, if any.
func (s *Server) proxySiteFor(ctx context.Context, target string) string {
	if s.modules.proxy == nil {
		return ""
	}
	vhosts, err := s.modules.proxy.ListVHosts(ctx)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	for _, v := range vhosts {
		if !v.Enabled {
			continue
		}
		for _, name := range v.ServerNames {
			name = strings.ToLower(strings.TrimSuffix(name, "."))
			if name == host || strings.HasPrefix(name, "*.") && strings.HasSuffix(host, name[1:]) && strings.Count(host, ".") == strings.Count(name, ".") {
				return v.Name
			}
		}
	}
	return ""
}

// quickResult is an interactive run's answer, held briefly so its owner can
// save exactly what they saw without sending the probe again.
type quickResult struct {
	owner   string
	request netsec.ProbeRequest
	result  netsec.ProbeResult
	started time.Time
	ended   time.Time
	expires time.Time
}

const (
	quickResultTTL = 15 * time.Minute
	quickResultMax = 64
)

type quickResultCache struct {
	mu      sync.Mutex
	entries map[string]quickResult
	now     func() time.Time
}

var quickResults = &quickResultCache{entries: map[string]quickResult{}, now: time.Now}

func (c *quickResultCache) keep(owner string, req netsec.ProbeRequest, res *netsec.ProbeResult, started, ended time.Time) string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(random[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
	for len(c.entries) >= quickResultMax {
		oldest := ""
		for key, entry := range c.entries {
			if oldest == "" || entry.expires.Before(c.entries[oldest].expires) {
				oldest = key
			}
		}
		delete(c.entries, oldest)
	}
	copy := *res
	copy.ResultID = ""
	c.entries[id] = quickResult{owner: owner, request: req, result: copy, started: started, ended: ended, expires: now.Add(quickResultTTL)}
	return id
}

// take removes and returns an unexpired result held for its owner.
func (c *quickResultCache) take(owner, id string) (quickResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok || entry.owner != owner || c.now().After(entry.expires) {
		return quickResult{}, fmt.Errorf("that result is no longer held; run the tool again to save it")
	}
	delete(c.entries, id)
	return entry, nil
}
