package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
)

// portExternalEvidence is one retained external check of a host port, read
// from the external-check owner: what an enrolled source measured, or that
// a check ended without a measurement.
type portExternalEvidence struct {
	CheckID   string `json:"checkId"`
	VantageID string `json:"vantageId"`
	Source    string `json:"source"`
	Location  string `json:"location,omitempty"`
	Placement string `json:"placement"`
	Port      int    `json:"port"`
	Family    string `json:"family"`
	TLS       bool   `json:"tls"`
	// Address is the literal the source connected to; Local is whether it is
	// on this host. One that is not reached it through a translation — a
	// provider's NAT or load balancer — this dashboard does not see.
	Address string `json:"address,omitempty"`
	Local   bool   `json:"local"`
	// Status is the check's lifecycle; State and Basis are its TCP stage,
	// "unknown" for a check that expired or was cancelled unmeasured.
	Status string    `json:"status"`
	State  string    `json:"state"`
	Basis  string    `json:"basis"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// portExternalScope is an enrolled source's scope as the ports page needs
// it: which host ports it may be asked to check, in which families.
type portExternalScope struct {
	VantageID string     `json:"vantageId"`
	Source    string     `json:"source"`
	Location  string     `json:"location,omitempty"`
	Placement string     `json:"placement"`
	ScopeID   string     `json:"scopeId"`
	Target    string     `json:"target"`
	Addresses []string   `json:"addresses"`
	Ports     []int      `json:"ports"`
	Families  []string   `json:"families"`
	LastSeen  *time.Time `json:"lastSeen,omitempty"`
}

// portsExternal is GET /ports/external: reachability from outside the host,
// as the enrolled sources measured it. It starts nothing.
type portsExternal struct {
	Evidence  []portExternalEvidence `json:"evidence"`
	Scopes    []portExternalScope    `json:"scopes"`
	CheckedAt time.Time              `json:"checkedAt"`
	// Error is the external-check owner being unavailable, which is a
	// different answer from no source being enrolled.
	Error string `json:"error,omitempty"`
}

// externalPortEvidence joins retained checks to their sources, newest first.
func externalPortEvidence(vantages []netvantage.Vantage, checks []netvantage.Check, local func(string) bool) portsExternal {
	out := portsExternal{Evidence: []portExternalEvidence{}, Scopes: []portExternalScope{}, CheckedAt: time.Now().UTC()}
	byID := map[string]netvantage.Vantage{}
	for _, v := range vantages {
		byID[v.ID] = v
		if v.EnrolledAt == nil || v.RevokedAt != nil {
			continue
		}
		for _, scope := range v.Scopes {
			out.Scopes = append(out.Scopes, portExternalScope{VantageID: v.ID, Source: v.Name, Location: v.Location, Placement: v.Placement,
				ScopeID: scope.ID, Target: scope.Target, Addresses: scope.Addresses, Ports: scope.Ports, Families: scope.Families, LastSeen: v.LastSeen})
		}
	}
	for _, c := range checks {
		v := byID[c.VantageID]
		e := portExternalEvidence{CheckID: c.ID, VantageID: c.VantageID, Source: v.Name, Location: v.Location, Placement: v.Placement,
			Port: c.Request.Port, Family: c.Request.Family, TLS: c.Request.TLS, Status: c.Status, State: "unknown", Basis: "unknown", At: c.CreatedAt}
		if c.CompletedAt != nil {
			e.At = *c.CompletedAt
		}
		if e.Source == "" {
			e.Source = "Removed source"
		}
		if r := c.Result; r != nil {
			e.Address = r.Address
			e.Local = r.Address != "" && local(r.Address)
			e.At = r.EndedAt
			for _, stage := range r.Stages {
				if stage.Name == "tcp" {
					e.State, e.Basis, e.Detail = stage.State, stage.Basis, stage.Detail
				}
			}
		}
		out.Evidence = append(out.Evidence, e)
	}
	sort.SliceStable(out.Evidence, func(i, j int) bool { return out.Evidence[i].At.After(out.Evidence[j].At) })
	return out
}

// hostAddressSet tells whether a literal is one of this host's addresses.
// The backend shares the host's network namespace.
func hostAddressSet() func(string) bool {
	set := map[netip.Addr]bool{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if prefix, err := netip.ParsePrefix(a.String()); err == nil {
				set[prefix.Addr().Unmap()] = true
			}
		}
	}
	return func(value string) bool {
		a, err := netip.ParseAddr(value)
		return err == nil && set[a.Unmap()]
	}
}

// readPortsExternal reads the retained evidence, or says why it could not.
func (s *Server) readPortsExternal(ctx context.Context) portsExternal {
	empty := portsExternal{Evidence: []portExternalEvidence{}, Scopes: []portExternalScope{}, CheckedAt: time.Now().UTC()}
	if s.modules.networkVantages == nil {
		empty.Error = "External checks are not available on this dashboard."
		return empty
	}
	if err := s.modules.networkVantages.Ready(); err != nil {
		empty.Error = "External-check state is unavailable: " + err.Error()
		return empty
	}
	vantages, err := s.modules.networkVantages.Vantages(ctx)
	if err != nil {
		empty.Error = "Enrolled sources could not be read: " + err.Error()
		return empty
	}
	checks, err := s.modules.networkVantages.Checks(ctx)
	if err != nil {
		empty.Error = "Retained checks could not be read: " + err.Error()
		return empty
	}
	return externalPortEvidence(vantages, checks, hostAddressSet())
}

// handlePortsExternal is GET /ports/external. Admin-only, like the external
// checks it reads: the scopes name the addresses sources are allowed to
// reach, and a check is started through that owner's own route.
func (s *Server) handlePortsExternal(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.readPortsExternal(r.Context()))
	return nil
}

// externalMeasurements is the published-path investigator's view of the
// same evidence: the TCP measurements of one host port.
func (s *Server) externalMeasurements(ctx context.Context, port int) ([]netpath.ExternalMeasurement, error) {
	reading := s.readPortsExternal(ctx)
	out := []netpath.ExternalMeasurement{}
	for _, e := range reading.Evidence {
		if e.Port != port || e.Basis != "measured" {
			continue
		}
		out = append(out, netpath.ExternalMeasurement{Source: e.Source, Placement: e.Placement, Address: e.Address, State: e.State, Basis: e.Basis, At: e.At, Local: e.Local})
	}
	if reading.Error != "" && len(out) == 0 {
		return out, errors.New(reading.Error)
	}
	return out, nil
}
