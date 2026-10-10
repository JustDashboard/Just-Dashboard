package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// The WireGuard record and the changes that act on a peer after it exists:
// trends, endpoint history and lifecycle, endpoint evidence, editing, usage
// budgets, site verification and restoring an archived tunnel. Every route is
// under /vpn, so system.admin; the mutations are audited, and taking a network
// away from a site, changing where it is dialled or clearing a budget spends
// the destructive budget.

func (s *Server) handleWireGuardHistory(w http.ResponseWriter, r *http.Request) error {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	h, err := s.modules.network.WireGuardHistory(ctx, chi.URLParam(r, "iface"), r.URL.Query().Get("peer"), window)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, h)
	return nil
}

func (s *Server) handleWireGuardEndpoint(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	e, err := s.modules.network.WireGuardEndpoint(ctx, chi.URLParam(r, "iface"))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, e)
	return nil
}

func (s *Server) handleWireGuardPeerEdit(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	id, err := peerID(r)
	if err != nil {
		return err
	}
	var req netx.WGPeerEdit
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	detail := map[string]any{"iface": iface, "id": id}
	if req.Name != nil {
		detail["name"] = *req.Name
	}
	if req.RemoteNetworks != nil {
		detail["remoteNetworks"] = *req.RemoteNetworks
	}
	if req.Endpoint != nil {
		detail["endpoint"] = *req.Endpoint
	}
	if req.Keepalive != nil {
		detail["keepalive"] = *req.Keepalive
	}
	if req.FullTunnel != nil {
		detail["fullTunnel"] = *req.FullTunnel
	}
	if req.ShareNetworks != nil {
		detail["shareNetworks"] = len(*req.ShareNetworks)
	}
	httpx.SetAudit(r, "network.vpn.peer.edit", iface+"/"+strconv.Itoa(id), detail)
	// The module decides against the peer under its lock whether the edit
	// withdraws something, so the budget is spent only when it does.
	res, err := s.modules.network.EditWireGuardPeer(ctx, iface, id, req, s.networkClient(r), actor(r), func() error {
		return s.requireDestructive(r, "wgpeeredit", "withdrawing a site's network or changing where it is dialled")
	})
	if err != nil {
		return mapNetworkError(err)
	}
	// A regenerated configuration holds the peer's private key.
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleWireGuardPeerQuota(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	id, err := peerID(r)
	if err != nil {
		return err
	}
	var req netx.WGQuotaRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.peer.quota.set", iface+"/"+strconv.Itoa(id), map[string]any{"period": req.Period, "limitBytes": req.LimitBytes})
	q, err := s.modules.network.SetWireGuardPeerQuota(ctx, iface, id, req, actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, q)
	return nil
}

func (s *Server) handleWireGuardPeerQuotaClear(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	id, err := peerID(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.peer.quota.clear", iface+"/"+strconv.Itoa(id), nil)
	if err := s.modules.network.ClearWireGuardPeerQuota(ctx, iface, id, actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleWireGuardSiteVerify(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	id, err := peerID(r)
	if err != nil {
		return err
	}
	var req netx.WGSiteVerifyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	// Pings leave this host, so the verification is recorded like a probe.
	httpx.SetAudit(r, "network.vpn.site.verify", iface+"/"+strconv.Itoa(id), map[string]any{"target": req.Target})
	v, err := s.modules.network.VerifyWireGuardSite(ctx, iface, id, req, actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleWireGuardArchive(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	list, err := s.modules.network.WireGuardArchive(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleWireGuardRestore(w http.ResponseWriter, r *http.Request) error {
	file := chi.URLParam(r, "file")
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.wireguard.restore", file, nil)
	res, err := s.modules.network.RestoreWireGuard(ctx, file, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.vpn.wireguard.restore", res.Interface.Name, map[string]any{"file": file, "port": res.Interface.ListenPort})
	firewall := s.openWireGuardPort(ctx, res.Interface, s.networkClient(r))
	s.noteFirewallOpening(ctx, res.Interface.Name, firewall, actor(r))
	httpx.JSON(w, http.StatusOK, wgCreateResponse{Interface: res.Interface, Warnings: res.Warnings, Firewall: firewall})
	return nil
}
