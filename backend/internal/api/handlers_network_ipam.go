package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netipam"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNetworkIPAMRoutes(r chi.Router) {
	r.Route("/ipam", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method("GET", "/", s.handle(s.handleNetworkIPAM))
		r.Method("POST", "/preview", s.handle(s.handleNetworkIPAMPreview))
		r.Method("POST", "/pools", s.handle(s.handleNetworkIPAMPool))
		r.Method("POST", "/reservations", s.handle(s.handleNetworkIPAMReserve))
		s.destructive(r, func(r chi.Router) {
			r.Method("DELETE", "/pools/{id}", s.handle(s.handleNetworkIPAMRetire))
			r.Method("DELETE", "/reservations/{id}", s.handle(s.handleNetworkIPAMRelease))
		})
	})
}
func (s *Server) ipamReady() error {
	if e := s.modules.ipam.Ready(); e != nil {
		return httpx.Err(503, "ipam_unavailable", fmt.Sprintf("Shared address planning is unavailable: %s", e))
	}
	return nil
}
func (s *Server) handleNetworkIPAM(w http.ResponseWriter, r *http.Request) error {
	if e := s.ipamReady(); e != nil {
		return e
	}
	ctx, cancel := timeoutCtx(r, 25*time.Second)
	defer cancel()
	view, e := s.modules.ipam.View(ctx)
	if e != nil {
		return httpx.Internal(e)
	}
	httpx.JSON(w, 200, view)
	return nil
}
func (s *Server) handleNetworkIPAMPreview(w http.ResponseWriter, r *http.Request) error {
	if e := s.ipamReady(); e != nil {
		return e
	}
	var input struct {
		Prefix string `json:"prefix"`
	}
	if e := httpx.DecodeJSON(r, &input); e != nil {
		return e
	}
	if _, e := netipam.Canonical(input.Prefix); e != nil {
		return httpx.BadRequest("%s", e)
	}
	httpx.SetAudit(r, "network.ipam.preview", input.Prefix, nil)
	ctx, cancel := timeoutCtx(r, 25*time.Second)
	defer cancel()
	preview, e := s.modules.ipam.Preview(ctx, input.Prefix)
	if e != nil {
		return httpx.BadRequest("%s", e)
	}
	httpx.JSON(w, 200, preview)
	return nil
}
func (s *Server) handleNetworkIPAMPool(w http.ResponseWriter, r *http.Request) error {
	if e := s.ipamReady(); e != nil {
		return e
	}
	var input netipam.PoolRequest
	if e := httpx.DecodeJSON(r, &input); e != nil {
		return e
	}
	httpx.SetAudit(r, "network.ipam.pool", input.Name, map[string]any{"prefix": input.Prefix, "allocationBits": input.AllocationBits})
	pool, e := s.modules.ipam.CreatePool(r.Context(), input)
	if e != nil {
		return httpx.BadRequest("%s", e)
	}
	httpx.JSON(w, 201, pool)
	return nil
}
func (s *Server) handleNetworkIPAMReserve(w http.ResponseWriter, r *http.Request) error {
	if e := s.ipamReady(); e != nil {
		return e
	}
	var input netipam.ReserveRequest
	if e := httpx.DecodeJSON(r, &input); e != nil {
		return e
	}
	httpx.SetAudit(r, "network.ipam.reserve", input.Resource, map[string]any{"poolId": input.PoolID, "owner": input.Owner, "prefix": input.Prefix, "acknowledgeUnknown": input.AcknowledgeUnknown})
	ctx, cancel := timeoutCtx(r, 25*time.Second)
	defer cancel()
	reservation, e := s.modules.ipam.Reserve(ctx, input, actor(r))
	if e != nil {
		return httpx.Err(409, "ipam_reservation_refused", e.Error())
	}
	httpx.SetAudit(r, "network.ipam.reserve", reservation.ID, map[string]any{"prefix": reservation.Prefix, "owner": reservation.Owner, "resource": reservation.Resource})
	httpx.JSON(w, 201, reservation)
	return nil
}
func (s *Server) handleNetworkIPAMRetire(w http.ResponseWriter, r *http.Request) error {
	if e := s.ipamReady(); e != nil {
		return e
	}
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.ipam.retire", id, nil)
	if e := s.modules.ipam.RetirePool(r.Context(), id); e != nil {
		return httpx.Err(409, "ipam_pool_active", e.Error())
	}
	httpx.JSON(w, 200, map[string]bool{"retired": true, "nativeChanged": false})
	return nil
}
func (s *Server) handleNetworkIPAMRelease(w http.ResponseWriter, r *http.Request) error {
	if e := s.ipamReady(); e != nil {
		return e
	}
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.ipam.release", id, nil)
	if e := s.modules.ipam.Release(r.Context(), id); e != nil {
		return httpx.Err(409, "ipam_reservation_active", e.Error())
	}
	httpx.JSON(w, 200, map[string]bool{"released": true, "nativeChanged": false})
	return nil
}
func (s *Server) ipamInventory(ctx context.Context) (netipam.Snapshot, error) {
	result := netipam.Snapshot{CheckedAt: time.Now().UTC(), Observations: []netipam.Observation{}, Coverage: []netipam.Coverage{}}
	inventory := netx.Inventory{}
	addCoverage := func(row netipam.Coverage) {
		if len(result.Coverage) < 127 {
			result.Coverage = append(result.Coverage, row)
		} else if len(result.Coverage) == 127 {
			result.Coverage = append(result.Coverage, netipam.Coverage{Source: "bounded_coverage", State: "unknown", Detail: "Native source evidence exceeded the 128-item inspection bound", CheckedAt: time.Now().UTC()})
		}
	}
	addObservation := func(row netipam.Observation) {
		if len(result.Observations) < netipam.MaxObservations {
			result.Observations = append(result.Observations, row)
			return
		}
		if len(result.Observations) == netipam.MaxObservations && !hasIPAMCoverage(result.Coverage, "bounded_inventory") {
			addCoverage(netipam.Coverage{Source: "bounded_inventory", State: "unknown", Detail: "Native prefix inventory exceeded the 4096-prefix inspection bound", CheckedAt: time.Now().UTC()})
		}
	}
	if s.modules.docker == nil {
		addCoverage(netipam.Coverage{Source: "docker", State: "unreadable", Detail: "Docker owner unavailable", CheckedAt: time.Now().UTC()})
	} else {
		networks, e := s.modules.docker.ListNetworks(ctx)
		state, detail := "observed", "Fresh Docker Engine IPAM prefixes were read; memberships are not address utilization"
		if e != nil {
			state, detail = "unreadable", e.Error()
		}
		addCoverage(netipam.Coverage{Source: "docker", State: state, Detail: detail, CheckedAt: time.Now().UTC()})
		if len(networks) > 512 {
			networks = networks[:512]
			addCoverage(netipam.Coverage{Source: "bounded_docker_networks", State: "unknown", Detail: "Docker network inventory exceeded the 512-network inspection bound", CheckedAt: time.Now().UTC()})
		}
		for _, n := range networks {
			inventory.Networks = append(inventory.Networks, netx.DockerNet{ID: n.ID, Name: n.Name, Bridge: n.Bridge, Driver: n.Driver, IPv6: n.IPv6})
			for _, prefix := range n.Subnets {
				if len(result.Observations) == netipam.MaxObservations {
					addObservation(netipam.Observation{})
					break
				}
				addObservation(netipam.Observation{Prefix: prefix, Owner: "docker", Resource: n.Name, Domain: "docker_ipam", Basis: "observed_native"})
			}
			if len(n.Subnets) == 0 && n.Driver != "host" && n.Driver != "null" && n.Driver != "none" {
				addCoverage(netipam.Coverage{Source: "docker/" + n.Name, State: "unknown", Detail: "The native network has no readable IPAM prefix; custom-driver scope is unknown", CheckedAt: time.Now().UTC()})
			}
		}
	}
	if s.modules.network == nil {
		addCoverage(netipam.Coverage{Source: "native_host", State: "unreadable", Detail: "Native network owner unavailable", CheckedAt: time.Now().UTC()})
	} else {
		native := s.modules.network.IPAMInventory(ctx, inventory)
		for _, p := range native.Prefixes {
			addObservation(netipam.Observation{Prefix: p.Prefix, Owner: p.Owner, Resource: p.Resource, Domain: p.Domain, Basis: p.Basis})
		}
		for _, c := range native.Sources {
			addCoverage(netipam.Coverage{Source: c.Source, State: c.State, Detail: c.Detail, CheckedAt: c.CheckedAt})
		}
	}
	result.FinishedAt = time.Now().UTC()
	return result, nil
}

func hasIPAMCoverage(rows []netipam.Coverage, source string) bool {
	for _, row := range rows {
		if row.Source == source {
			return true
		}
	}
	return false
}
