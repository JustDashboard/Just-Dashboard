package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// The Network section: the host's devices, routing, gateway, protection,
// shaping, VPN and resolver.
//
// Every read is `read` unless it names who connects (VPN peers, the programs
// behind each connection), which is system.admin for the reason the SSH
// configuration is. Every change is system.admin, and every one that can cost
// access or traffic — a device set down or deleted, a route, rule or forward
// removed, forwarding turned off — is inside s.destructive. The guards in
// netx refuse what would cut the reader off; the capability decides who may
// ask.
//
// The section's two older routes, the interface summary and the probes, were
// mounted beside the security routes as a Method and a Route on the same
// prefix. They are here now, in the one Route that owns /network, because chi
// mounts a Route as a subrouter and a Method on the same pattern beside it
// quietly stops existing (handlers_security.go records the time that
// happened to /ssh-sessions).
func (s *Server) mountNetworkRoutes(r chi.Router) {
	r.Route("/network", func(r chi.Router) {
		r.Use(s.pendingNetworkApply)
		r.Method(http.MethodGet, "/", s.handle(s.handleNetworkInfo))
		// Probes make the server emit traffic to an address the caller
		// chose. That is a scanner if it is handed to everybody, so it sits
		// behind the capability that already means "this person administers
		// the host".
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/probe", s.handle(s.handleNetworkProbe))
		})

		r.Method(http.MethodGet, "/overview", s.handle(s.handleNetworkOverview))
		r.Method(http.MethodGet, "/links", s.handle(s.handleNetworkLinks))
		r.Method(http.MethodGet, "/capabilities", s.handle(s.handleNetworkCapabilities))
		r.Method(http.MethodGet, "/traffic/live", s.handle(s.handleNetworkTrafficLive))
		r.Method(http.MethodGet, "/traffic/history", s.handle(s.handleNetworkTrafficHistory))

		s.mountNetworkDiagnosticRoutes(r)
		s.mountNetworkCaptureRoutes(r)
		s.mountNetworkVantageRoutes(r)
		s.mountNetworkIPAMRoutes(r)
		s.mountNetworkFlowRoutes(r)
		s.mountNetworkInvestigatorRoutes(r)
		s.mountNetworkChangeRoutes(r)
		s.mountNativeManagerRoutes(r)
		s.mountNetworkDriftRoutes(r)
		s.mountNetworkLinkRoutes(r)
		s.mountNetworkRoutingRoutes(r)
		s.mountNetworkGatewayRoutes(r)
		s.mountNetworkShapingRoutes(r)
		s.mountNetworkVPNRoutes(r)
		s.mountNetworkDNSRoutes(r)
		s.mountNetworkTrafficRoutes(r)
	})
}

// handleNetworkLinks is every device with its owner, role, guard and rate.
func (s *Server) handleNetworkLinks(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	links, err := s.modules.network.ReadLinks(ctx, s.networkInventory(ctx), s.networkClient(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, links)
	return nil
}

// handleNetworkTrafficLive is the two-second ring for every device since a
// moment, so a poll draws only what it has not drawn.
func (s *Server) handleNetworkTrafficLive(w http.ResponseWriter, r *http.Request) error {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"now":    time.Now().Unix(),
		"series": s.modules.network.Sampler().Live(since),
	})
	return nil
}

// handleNetworkTrafficHistory is the recorded window, bucketed in SQL.
func (s *Server) handleNetworkTrafficHistory(w http.ResponseWriter, r *http.Request) error {
	window := time.Hour
	if raw := r.URL.Query().Get("window"); raw != "" {
		d, err := metrics.ParseWindow(raw)
		if err != nil || d <= 0 || d > 31*24*time.Hour {
			return httpx.BadRequest("window is a duration up to 31d, such as 1h, 6h, 24h or 7d")
		}
		window = d
	}
	points := 240
	if raw := r.URL.Query().Get("points"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 2 || n > 1000 {
			return httpx.BadRequest("points is between 2 and 1000")
		}
		points = n
	}
	h, err := s.modules.network.Sampler().History(r.Context(), window, points, r.URL.Query().Get("iface"))
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, h)
	return nil
}

// networkPIDs remembers each running container's init process by id and
// start time, so the inventory a link list is joined with costs one container
// listing and no inspects once it is warm: a PID changes only when the
// container restarts, and a restart changes its start time.
var networkPIDs = struct {
	sync.Mutex
	byKey map[string]int
}{byKey: map[string]int{}}

// networkInventory reads what Docker knows about the host's network: each
// running container with its init process, and each network with its bridge.
// A host without Docker answers an empty inventory.
func (s *Server) networkInventory(ctx context.Context) netx.Inventory {
	var inv netx.Inventory
	if s.modules.docker == nil {
		return inv
	}
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err == nil {
		seen := map[string]bool{}
		for _, c := range containers {
			if c.State != "running" {
				continue
			}
			key := c.ID
			if c.StartedAt != nil {
				key += "@" + c.StartedAt.UTC().Format(time.RFC3339Nano)
			}
			seen[key] = true
			networkPIDs.Lock()
			pid, ok := networkPIDs.byKey[key]
			networkPIDs.Unlock()
			if !ok {
				pid, err = s.modules.docker.ContainerPID(ctx, c.ID)
				if err != nil {
					inv.UnjoinedContainers = append(inv.UnjoinedContainers, c.Name)
					continue
				}
				networkPIDs.Lock()
				networkPIDs.byKey[key] = pid
				networkPIDs.Unlock()
			}
			inv.Containers = append(inv.Containers, netx.ContainerNet{
				ID: c.ID, Name: c.Name, Image: c.Image, PID: pid,
			})
		}
		networkPIDs.Lock()
		for key := range networkPIDs.byKey {
			if !seen[key] {
				delete(networkPIDs.byKey, key)
			}
		}
		networkPIDs.Unlock()
	} else {
		inv.ContainersError = err.Error()
	}
	if networks, err := s.modules.docker.ListNetworks(ctx); err == nil {
		for _, n := range networks {
			inv.Networks = append(inv.Networks, netx.DockerNet{
				ID: n.ID, Name: n.Name, Driver: n.Driver, Bridge: n.Bridge, IPv6: n.IPv6, Subnets: n.Subnets,
			})
		}
	} else {
		inv.DockerNetworksUnknown = true
		inv.DockerNetworksError = err.Error()
	}
	return inv
}

// mapNetworkError turns the module's sentinel errors into the codes the pages
// key off. A guard's refusal is a 409 with its sentence, the same code the
// firewall's lockout guard answers with, so one handler in the frontend
// renders both. Anything else the module returns is a sentence about the
// request or the host — a name already taken, a tool that printed an error —
// and is a 400 carrying it, never a 500 swallowing it.
func mapNetworkError(err error) error {
	var confirmation *netx.ConfirmationError
	var guard *netx.GuardError
	var missing *netx.UnavailableError
	var readOnly *netx.ReadOnlyError
	var apiErr *httpx.APIError
	switch {
	case errors.As(err, &apiErr):
		return apiErr
	case errors.As(err, &confirmation):
		return httpx.Err(http.StatusConflict, "network_confirmation", confirmation.Reason)
	case errors.As(err, &guard):
		return httpx.Err(http.StatusConflict, "would_lock_you_out", guard.Reason)
	case errors.As(err, &missing):
		e := httpx.Err(http.StatusServiceUnavailable, "tool_unavailable", missing.Error())
		if missing.Package != "" {
			e = e.Because("install "+missing.Package, missing.Tool)
		}
		return e
	case errors.As(err, &readOnly):
		// A 409 rather than the firewall's 501: the request was understood
		// and this host's other software owns the thing, which is an expected
		// answer and must not be logged as a server fault.
		return httpx.Err(http.StatusConflict, "network_read_only", readOnly.Reason)
	case errors.Is(err, netx.ErrNotManaged):
		return httpx.Err(http.StatusConflict, "not_managed", err.Error())
	case errors.Is(err, netx.ErrNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, netx.ErrExists):
		return httpx.Err(http.StatusConflict, "exists", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return httpx.Err(http.StatusGatewayTimeout, "timeout", "the host did not answer in time").Retry()
	}
	return httpx.Wrap(http.StatusBadRequest, "bad_request", err)
}

// networkClient is the address the network guards protect for a request
// (netx.OperatorAddress): the browser's own, or the SSH session's behind a
// tunnel to loopback, which a route or a blocklist can cut as surely.
func (s *Server) networkClient(r *http.Request) string {
	return s.modules.network.OperatorAddress(r.Context(), httpx.ClientIP(r))
}

// allowlistStrings renders the allowlist for modules that take it as text.
func allowlistStrings(nets []*net.IPNet) []string {
	out := make([]string, 0, len(nets))
	for _, n := range nets {
		out = append(out, n.String())
	}
	return out
}

// actor is who is making a change, for the Made stamp on what the network
// module records.
func actor(r *http.Request) string {
	if p, ok := httpx.PrincipalFrom(r.Context()); ok {
		return p.Username()
	}
	return ""
}
