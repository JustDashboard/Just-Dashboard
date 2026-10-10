package api

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/go-chi/chi/v5"
)

// One remote address in full, and the blocks made from the connection table.
//
// The summary at /connections is a reading any role has. The full tuples with
// their byte counters are which program carried how much to whom, the
// question /network/traffic/processes answers per program, and they carry the
// same capability.
func (s *Server) mountConnectionDetailRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/connections/{address}", s.handle(s.handleConnectionDetail))
	})
}

// mountFirewallBlockRoutes mounts the recorded blocks inside /firewall. A
// block is a deny like the rules beside it, so reading them is the rules'
// standing; adding one is an administrator's change and lifting one opens
// the firewall back up, which is destructive like deleting a rule.
func (s *Server) mountFirewallBlockRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/blocks", s.handle(s.handleFirewallBlocks))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/blocks", s.handle(s.handleFirewallBlockAdd))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/blocks/{id}", s.handle(s.handleFirewallBlockLift))
		})
	})
}

// connectionSocket is a live tuple with TCP's counters joined on where ss
// listed the same socket. A UDP tuple, or a TCP one ss did not list (it
// closed between the two reads), has none: absent, not zero.
type connectionSocket struct {
	netsec.Connection
	RxBytes       *uint64  `json:"rxBytes,omitempty"`
	TxBytes       *uint64  `json:"txBytes,omitempty"`
	RTTMs         *float64 `json:"rttMs,omitempty"`
	Retransmitted *uint64  `json:"retransmitted,omitempty"`
	Congestion    string   `json:"congestion,omitempty"`
}

type connectionDetail struct {
	Address           string                    `json:"address"`
	Private           bool                      `json:"private"`
	Sockets           []connectionSocket        `json:"sockets"`
	Closed            []netsec.ClosedConnection `json:"closed"`
	Quality           netsec.ConnectionQuality  `json:"quality"`
	CountersError     string                    `json:"countersError,omitempty"`
	CountersTruncated bool                      `json:"countersTruncated"`
}

type socketJoin struct {
	local  string
	lport  int
	remote string
	rport  int
}

func (s *Server) handleConnectionDetail(w http.ResponseWriter, r *http.Request) error {
	address := chi.URLParam(r, "address")
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return httpx.BadRequest("the peer is an IPv4 or IPv6 address")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	peer, err := s.modules.netsec.PeerDetail(ctx, address)
	if err != nil {
		if errors.Is(err, netsec.ErrInvalidAddress) {
			return httpx.BadRequest("%v", err)
		}
		return httpx.Err(http.StatusServiceUnavailable, "connections_unavailable", "the host's connection table could not be read")
	}
	out := connectionDetail{Address: peer.Address, Private: peer.Private, Closed: peer.Closed, Quality: peer.Quality,
		Sockets: make([]connectionSocket, 0, len(peer.Sockets))}
	counters := map[socketJoin]int{}
	list, truncated, err := s.modules.network.TCPSocketsTo(ctx, addr)
	if err != nil {
		out.CountersError = err.Error()
	}
	out.CountersTruncated = truncated
	for i, c := range list {
		counters[socketJoin{c.LocalAddr, c.LocalPort, c.PeerAddr, c.PeerPort}] = i
	}
	for _, c := range peer.Sockets {
		sock := connectionSocket{Connection: c}
		if c.Protocol == "tcp" {
			key := socketJoin{unmappedAddr(c.LocalAddr), int(c.LocalPort), unmappedAddr(c.RemoteAddr), int(c.RemotePort)}
			if i, ok := counters[key]; ok {
				m := list[i]
				sock.RxBytes, sock.TxBytes, sock.Retransmitted = &m.RxBytes, &m.TxBytes, &m.Retransmitted
				sock.RTTMs, sock.Congestion = m.RTTMs, m.Congestion
			}
		}
		out.Sockets = append(out.Sockets, sock)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func unmappedAddr(s string) string {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().String()
	}
	return s
}

func (s *Server) handleFirewallBlocks(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	list, err := s.modules.blocks.List(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func mapBlockError(err error) error {
	switch {
	case errors.Is(err, netsec.ErrLockout):
		return httpx.Err(http.StatusConflict, "would_lock_you_out", err.Error())
	case errors.Is(err, netsec.ErrBlockExists):
		return httpx.Err(http.StatusConflict, "exists", err.Error())
	case errors.Is(err, netsec.ErrBlockNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, netsec.ErrBlockInvalid):
		return httpx.BadRequest("%v", err)
	}
	return mapFirewallError(err)
}

func (s *Server) handleFirewallBlockAdd(w http.ResponseWriter, r *http.Request) error {
	var req netsec.BlockRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	detail := map[string]any{"reason": req.Reason, "durationSeconds": req.DurationSeconds, "incidentRunId": req.IncidentRunID}
	if req.IncidentRunID != "" {
		if _, err := s.modules.diagnostics.Get(r.Context(), req.IncidentRunID); err != nil {
			httpx.SetAudit(r, "firewall.block.add", req.Address, map[string]any{"result": "incident_absent"})
			return httpx.Err(http.StatusConflict, "block_incident_absent", "The selected saved diagnostic is absent or unreadable; select it again")
		}
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// The caller's own address goes down with the request: the firewall
	// layer refuses a deny that would end this very session.
	blk, err := s.modules.blocks.Create(ctx, req, s.networkClient(r), actor(r))
	if err != nil {
		if errors.Is(err, netsec.ErrLockout) {
			detail["result"] = "refused_lockout"
		}
		httpx.SetAudit(r, "firewall.block.add", req.Address, detail)
		return mapBlockError(err)
	}
	detail["id"] = blk.ID
	httpx.SetAudit(r, "firewall.block.add", blk.Address, detail)
	httpx.JSON(w, http.StatusOK, blk)
	return nil
}

func (s *Server) handleFirewallBlockLift(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	blk, err := s.modules.blocks.Lift(ctx, id, actor(r))
	if err != nil {
		httpx.SetAudit(r, "firewall.block.lift", id, map[string]any{"error": err.Error()})
		return mapBlockError(err)
	}
	httpx.SetAudit(r, "firewall.block.lift", blk.Address, map[string]any{"id": blk.ID})
	httpx.JSON(w, http.StatusOK, blk)
	return nil
}

// expireBlocks lifts ended blocks every minute for as long as the server
// runs, each outcome in the audit log as the system's own action.
func (s *Server) expireBlocks(ctx context.Context) {
	s.modules.blocks.Run(ctx, time.Minute, func(o netsec.ExpiryOutcome) {
		entry := audit.Entry{Actor: "system", Action: "firewall.block.expire", Target: o.Block.Address, Success: o.Err == nil,
			Detail: audit.Detail(map[string]any{"id": o.Block.ID, "reason": o.Block.Reason})}
		if o.Err != nil {
			entry.Detail = audit.Detail(map[string]any{"id": o.Block.ID, "error": o.Err.Error()})
			s.Log.Warn("an ended block could not be lifted", "address", o.Block.Address, "err", o.Err)
		}
		s.Audit.Record(ctx, entry)
	})
}
