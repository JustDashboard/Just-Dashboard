package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkVPNRoutes mounts WireGuard, Tailscale and Headscale under /network.
//
// Everything here is system.admin, reads included: a peer list names the
// people and devices that connect, and a stored client configuration is a
// private key, for the reason the SSH configuration is admin-only. Removals,
// `down` and forgetting a configuration are inside s.destructive. The Overview
// reads only counts, through netx.VPNSummary.
func (s *Server) mountNetworkVPNRoutes(r chi.Router) {
	r.Route("/vpn", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleVPN))
		r.Method(http.MethodPost, "/tailscale", s.handle(s.handleTailscaleSet))

		r.Route("/wireguard", func(r chi.Router) {
			r.Method(http.MethodPost, "/", s.handle(s.handleWireGuardCreate))
			r.Route("/{iface}", func(r chi.Router) {
				r.Method(http.MethodPost, "/up", s.handle(s.handleWireGuardUp))
				r.Method(http.MethodPost, "/exit", s.handle(s.handleWireGuardExit))
				r.Method(http.MethodPost, "/peers", s.handle(s.handleWireGuardPeerAdd))
				r.Method(http.MethodGet, "/peers/{id}/config", s.handle(s.handleWireGuardPeerConfig))
				s.destructive(r, func(r chi.Router) {
					r.Method(http.MethodPost, "/down", s.handle(s.handleWireGuardDown))
					r.Method(http.MethodDelete, "/", s.handle(s.handleWireGuardRemove))
					r.Method(http.MethodDelete, "/peers/{id}", s.handle(s.handleWireGuardPeerRemove))
					r.Method(http.MethodDelete, "/peers/{id}/config", s.handle(s.handleWireGuardPeerForget))
				})
			})
		})
	})
}

// vpnView is the whole VPN page. Each part is read on its own and a part that
// fails says so in its own error field: Tailscale being down is not a reason
// to hide the WireGuard tunnels.
type vpnView struct {
	WireGuard *netx.WireGuardView `json:"wireguard"`
	Tailscale *netx.TailscaleView `json:"tailscale"`
	Headscale *netx.HeadscaleView `json:"headscale"`
}

func (s *Server) handleVPN(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	client := httpx.ClientIP(r)
	var out vpnView
	// Three independent reads of the host's own tools, each ending when its
	// command does; the page waits for the slowest rather than the sum.
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		v, err := s.modules.network.WireGuard(ctx)
		if v == nil {
			v = &netx.WireGuardView{Interfaces: []netx.WGInterface{}}
		}
		if err != nil {
			v.Error = err.Error()
		}
		out.WireGuard = v
	}()
	go func() {
		defer wg.Done()
		v, err := s.modules.network.Tailscale(ctx, client)
		if err != nil {
			v.Error = err.Error()
		}
		out.Tailscale = v
	}()
	go func() {
		defer wg.Done()
		out.Headscale = s.headscaleView(ctx)
	}()
	wg.Wait()
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// headscaleView reads a Headscale binary on the host, or else the running
// container whose image is Headscale. The Docker client is this package's, so
// the finding of the container is here and the reading of it is netx's.
func (s *Server) headscaleView(ctx context.Context) *netx.HeadscaleView {
	v := s.modules.network.Headscale(ctx)
	if v.Installed || s.modules.docker == nil {
		return v
	}
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return v
	}
	for _, c := range containers {
		if c.State == "running" && strings.Contains(strings.ToLower(c.Image), "headscale") {
			return s.modules.network.HeadscaleContainer(ctx, c.ID, strings.TrimPrefix(c.Name, "/"))
		}
	}
	return v
}

// wgFirewall is what became of the firewall rule for a new tunnel's port.
type wgFirewall struct {
	Opened bool   `json:"opened"`
	Reason string `json:"reason,omitempty"`
}

type wgCreateResponse struct {
	Interface netx.WGInterface `json:"interface"`
	Warnings  []string         `json:"warnings"`
	Firewall  wgFirewall       `json:"firewall"`
}

func (s *Server) handleWireGuardCreate(w http.ResponseWriter, r *http.Request) error {
	var req netx.WGServerRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.network.CreateWireGuard(ctx, req, actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.vpn.wireguard.create", res.Interface.Name, map[string]any{
		"port": res.Interface.ListenPort, "subnet": res.Interface.Subnet, "exitNode": res.Interface.ExitNode,
	})
	httpx.JSON(w, http.StatusOK, wgCreateResponse{
		Interface: res.Interface,
		Warnings:  res.Warnings,
		Firewall:  s.openWireGuardPort(ctx, res.Interface, httpx.ClientIP(r)),
	})
	return nil
}

// openWireGuardPort allows the tunnel's UDP port through the firewall when the
// firewall would otherwise drop it. A failure here is reported, not returned:
// the tunnel exists and works on every host whose firewall lets it, and the
// operator can add the rule by hand where this one could not.
func (s *Server) openWireGuardPort(ctx context.Context, ifc netx.WGInterface, client string) wgFirewall {
	st, err := s.modules.netsec.Status(ctx)
	switch {
	case err != nil:
		return wgFirewall{Reason: "the firewall could not be read"}
	case !st.Available:
		return wgFirewall{Reason: "no firewall was found on this host"}
	case !st.Enabled:
		return wgFirewall{Reason: "the firewall is not enabled, so nothing blocks the port"}
	case strings.HasPrefix(strings.ToLower(st.Policy.Incoming), "allow"):
		return wgFirewall{Reason: "the firewall allows incoming traffic by default, so nothing blocks the port"}
	case !st.Capabilities.Editable:
		return wgFirewall{Reason: firstNonEmptyString(st.Capabilities.ReadOnlyReason, "this firewall cannot be edited from the dashboard")}
	}
	_, err = s.modules.netsec.AddRule(ctx, netsec.RuleRequest{
		Action: "allow", Direction: "in", Port: strconv.Itoa(ifc.ListenPort), Protocol: "udp",
		Comment: "WireGuard " + ifc.Name,
	}, client)
	if err != nil {
		return wgFirewall{Reason: err.Error()}
	}
	return wgFirewall{Opened: true}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *Server) handleWireGuardUp(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.wireguard.up", iface, nil)
	if err := s.modules.network.SetWireGuardUp(ctx, iface, true, httpx.ClientIP(r)); err != nil {
		return mapNetworkError(err)
	}
	return s.writeWireGuardInterface(ctx, w, iface)
}

func (s *Server) handleWireGuardDown(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.wireguard.down", iface, nil)
	if err := s.modules.network.SetWireGuardUp(ctx, iface, false, httpx.ClientIP(r)); err != nil {
		return mapNetworkError(err)
	}
	return s.writeWireGuardInterface(ctx, w, iface)
}

// writeWireGuardInterface answers a state change with the tunnel as it is now.
func (s *Server) writeWireGuardInterface(ctx context.Context, w http.ResponseWriter, iface string) error {
	v, err := s.modules.network.WireGuard(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	for _, ifc := range v.Interfaces {
		if ifc.Name == iface {
			httpx.JSON(w, http.StatusOK, map[string]any{"interface": ifc})
			return nil
		}
	}
	return mapNetworkError(netx.ErrNotFound)
}

type wgExitRequest struct {
	On bool `json:"on"`
}

func (s *Server) handleWireGuardExit(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	var req wgExitRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.wireguard.exit", iface, map[string]any{"on": req.On})
	res, err := s.modules.network.SetWireGuardExit(ctx, iface, req.On, actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"interface": res.Interface, "warnings": res.Warnings})
	return nil
}

func (s *Server) handleWireGuardRemove(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.wireguard.remove", iface, nil)
	if err := s.modules.network.RemoveWireGuard(ctx, iface, httpx.ClientIP(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleWireGuardPeerAdd(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	var req netx.WGPeerRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := s.modules.network.AddWireGuardPeer(ctx, iface, req, httpx.ClientIP(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	// The name and the networks are the operator's own words; the keys and the
	// configuration are never part of what is recorded.
	httpx.SetAudit(r, "network.vpn.peer.add", res.Peer.Name, map[string]any{
		"iface": iface, "kind": res.Peer.Kind, "fullTunnel": req.FullTunnel,
		"shareNetworks": len(req.ShareNetworks), "remoteNetworks": len(req.RemoteNetworks),
	})
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func peerID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		return 0, httpx.BadRequest("a peer id is a positive number")
	}
	return id, nil
}

func (s *Server) handleWireGuardPeerConfig(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	id, err := peerID(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	cfg, err := s.modules.network.WireGuardPeerConfig(ctx, iface, id)
	if errors.Is(err, netx.ErrForgotten) {
		return httpx.Err(http.StatusNotFound, "forgotten", "This configuration was forgotten, so it cannot be shown again. Remove the peer and add it again to make a new one.")
	}
	if err != nil {
		return mapNetworkError(err)
	}
	// Showing a private key is a reading worth a record, though it is a GET.
	s.recordAudit(r, "network.vpn.peer.config.view", cfg.Name, map[string]any{"iface": iface, "id": id})
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, cfg)
	return nil
}

func (s *Server) handleWireGuardPeerForget(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	id, err := peerID(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.peer.config.forget", iface+"/"+strconv.Itoa(id), nil)
	if err := s.modules.network.ForgetWireGuardPeerConfig(ctx, iface, id); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleWireGuardPeerRemove(w http.ResponseWriter, r *http.Request) error {
	iface := chi.URLParam(r, "iface")
	id, err := peerID(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.SetAudit(r, "network.vpn.peer.remove", iface+"/"+strconv.Itoa(id), nil)
	if err := s.modules.network.RemoveWireGuardPeer(ctx, iface, id, httpx.ClientIP(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleTailscaleSet(w http.ResponseWriter, r *http.Request) error {
	var req netx.TailscaleSetRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	detail := map[string]any{}
	if req.AdvertiseExitNode != nil {
		detail["advertiseExitNode"] = *req.AdvertiseExitNode
	}
	if req.AdvertiseRoutes != nil {
		detail["advertiseRoutes"] = *req.AdvertiseRoutes
	}
	httpx.SetAudit(r, "network.vpn.tailscale.set", "", detail)
	res, err := s.modules.network.SetTailscale(ctx, req, httpx.ClientIP(r))
	if errors.Is(err, netx.ErrForwardingOff) {
		return httpx.Err(http.StatusConflict, "forwarding_off",
			err.Error()+". Turn IP forwarding on from the Routing page, then try again.")
	}
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
