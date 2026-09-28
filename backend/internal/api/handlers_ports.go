package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// portListTimeout bounds the walk over every process's descriptors, which on
// a host with tens of thousands of them is the slow part. The page polls it,
// so a stuck walk must answer rather than pile up behind itself.
const portListTimeout = 10 * time.Second

// mountPortRoutes is the host's listening sockets. A subrouter rather than a
// single route so the page's detail views can mount beside it; chi serves the
// index at both /ports and /ports/, and the dashboard asks for the first.
func (s *Server) mountPortRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handlePortList))
	r.Method(http.MethodGet, "/meta", s.handle(s.handlePortsMeta))
	s.mountPortHistoryRoutes(r)
}

// portsMeta is what the ports page reads besides the sockets, apart from the
// list so that stays the plain array other pages already read.
type portsMeta struct {
	// EphemeralRange is null where the kernel's range cannot be read, and
	// the page then offers nothing that depends on it.
	EphemeralRange *proxysvc.PortRange `json:"ephemeralRange"`
}

// handlePortsMeta tells the page which ports the kernel hands out on its own,
// so it can set aside the loopback sockets that were given one.
func (s *Server) handlePortsMeta(w http.ResponseWriter, r *http.Request) error {
	meta := portsMeta{}
	if span, err := proxysvc.EphemeralPorts(); err == nil {
		meta.EphemeralRange = &span
	}
	httpx.JSON(w, http.StatusOK, meta)
	return nil
}

func (s *Server) handlePortList(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, portListTimeout)
	defer cancel()
	// The firewall's status is the posture's other input to a port's level,
	// read beside the walk rather than after it.
	firewall := make(chan *netsec.FirewallStatus, 1)
	go func() {
		status, err := s.modules.netsec.Status(ctx)
		if err != nil {
			status = nil
		}
		firewall <- status
	}()
	listeners, err := proxysvc.ListListeners(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return httpx.Err(http.StatusGatewayTimeout, "timeout",
			"Listing the host's sockets took longer than 10 seconds.").Retry()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	placeListeners(listeners, netsec.ReadHostNetwork(ctx), <-firewall)
	httpx.JSON(w, http.StatusOK, s.withFirstSeen(r, listeners))
	return nil
}

// placeListeners places each socket on its network and grades it by the
// posture's own judgement, so the page cannot call a database critical that
// the posture calls a warning, nor credit a firewall the posture does not:
// the tailnet on tailscale0, Docker's bridge docker0, a port Docker
// publishes past ufw's deny.
func placeListeners(listeners []proxysvc.Listener, network netsec.HostNetwork, firewall *netsec.FirewallStatus) {
	for i := range listeners {
		l := &listeners[i]
		place := network.Place(l.Address)
		l.Reach = string(place.Reach)
		l.Network = string(place.Network)
		l.Interface = place.Interface
		grade := netsec.GradePort(netsec.ExposedPort{
			Port: l.Port, Protocol: l.Protocol, Address: l.Address, Process: l.Process, Exposed: l.Exposed,
		}, network, firewall)
		l.Level = grade.Level
		l.InboundDefault = grade.InboundDefault
		l.PastFirewall = string(grade.PastFirewall)
		l.FirewallRule = grade.FirewallRule
	}
}
