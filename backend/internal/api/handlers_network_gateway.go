package api

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkGatewayRoutes mounts port forwards, NAT, rate limits, blocklists
// and kernel protections under /network.
//
// Reads are `read`: they say what is forwarded and dropped, which is not more
// than the firewall page already shows. Every change is system.admin, and a
// change that takes something away — a removal, a disable, a setting moved
// away from its recommendation, a trusted address forgotten — is destructive:
// each is a protection or a service that stops, and the pages confirm it.
//
// A PUT that disables an entry is the routine request and the destructive one
// on the same path, so its handler reads the body and asks for the
// destructive capability by hand (requireDestructive), as the power routes of
// the Databases section do.
func (s *Server) mountNetworkGatewayRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/gateway", s.handle(s.handleNetworkGateway))
	r.Method(http.MethodGet, "/protection", s.handle(s.handleNetworkProtection))

	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/gateway/forwards", s.handle(s.handleForwardSave))
		r.Method(http.MethodPut, "/gateway/forwards/{id}", s.handle(s.handleForwardSave))
		r.Method(http.MethodPost, "/gateway/nat", s.handle(s.handleNATSave))
		r.Method(http.MethodPut, "/gateway/nat/{id}", s.handle(s.handleNATSave))
		// Previews and the target check change nothing on the host; they are
		// admin-only because they read listeners, connections and targets.
		r.Method(http.MethodPost, "/gateway/preview", s.handle(s.handleGatewayPreview))
		r.Method(http.MethodPost, "/gateway/verify", s.handle(s.handleForwardVerify))

		r.Method(http.MethodPost, "/protection/limits", s.handle(s.handleLimitSave))
		r.Method(http.MethodPut, "/protection/limits/{id}", s.handle(s.handleLimitSave))
		r.Method(http.MethodPost, "/protection/blocklists", s.handle(s.handleBlocklistSave))
		r.Method(http.MethodPut, "/protection/blocklists/{id}", s.handle(s.handleBlocklistSave))
		r.Method(http.MethodPost, "/protection/blocklists/{id}/refresh", s.handle(s.handleBlocklistRefresh))
		r.Method(http.MethodPost, "/protection/settings", s.handle(s.handleProtectionSettings))
		r.Method(http.MethodPost, "/protection/preview", s.handle(s.handleBlocklistPreview))
		r.Method(http.MethodPut, "/protection/trusted", s.handle(s.handleTrustedUpdate))
		r.Method(http.MethodGet, "/protection/pressure", s.handle(s.handleProtectionPressure))
		r.Method(http.MethodPost, "/protection/sessions/preview", s.handle(s.handleSessionPreview))

		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/gateway/admission/repair", s.handle(s.handleGatewayAdmissionRepair))
			r.Method(http.MethodDelete, "/gateway/forwards/{id}", s.handle(s.handleForwardDelete))
			r.Method(http.MethodDelete, "/gateway/nat/{id}", s.handle(s.handleNATDelete))
			r.Method(http.MethodDelete, "/protection/limits/{id}", s.handle(s.handleLimitDelete))
			r.Method(http.MethodDelete, "/protection/blocklists/{id}", s.handle(s.handleBlocklistDelete))
			r.Method(http.MethodDelete, "/protection/settings/{key}", s.handle(s.handleProtectionReset))
			r.Method(http.MethodDelete, "/protection/trusted", s.handle(s.handleTrustedRemove))
			// An exception gives part of a blocklist away and its removal can
			// refuse someone who relies on it; both directions are
			// destructive. Ending sessions cannot be undone.
			r.Method(http.MethodPost, "/protection/exceptions", s.handle(s.handleExceptionAdd))
			r.Method(http.MethodDelete, "/protection/exceptions/{id}", s.handle(s.handleExceptionDelete))
			r.Method(http.MethodPost, "/protection/sessions/revoke", s.handle(s.handleSessionRevoke))
		})
	})
}

// externalObservations are the retained external TCP measurements, for an
// administrator's Gateway read. Missing or unready external checks leave
// the forwards unverified rather than failing the page.
func (s *Server) externalObservations(r *http.Request) []netx.ExternalObservation {
	p := httpx.MustPrincipal(r)
	if s.modules.networkVantages == nil || !p.Can(auth.CapSystemAdmin) || s.modules.networkVantages.Ready() != nil {
		return nil
	}
	vantages, err := s.modules.networkVantages.Vantages(r.Context())
	if err != nil {
		return nil
	}
	checks, err := s.modules.networkVantages.Checks(r.Context())
	if err != nil {
		return nil
	}
	return externalObservationsOf(vantages, checks)
}

func externalObservationsOf(vantages []netvantage.Vantage, checks []netvantage.Check) []netx.ExternalObservation {
	named := map[string]netvantage.Vantage{}
	for _, v := range vantages {
		named[v.ID] = v
	}
	var out []netx.ExternalObservation
	for _, c := range checks {
		if c.Result == nil || c.CompletedAt == nil {
			continue
		}
		o := netx.ExternalObservation{
			CheckID: c.ID, Vantage: named[c.VantageID].Name, Location: named[c.VantageID].Location, Placement: named[c.VantageID].Placement,
			Address: c.Result.Address, Port: c.Request.Port, Family: c.Request.Family, CompletedAt: *c.CompletedAt,
		}
		for _, st := range c.Result.Stages {
			if st.Name == "tcp" {
				o.TCP, o.Detail = st.State, st.Detail
			}
		}
		out = append(out, o)
	}
	return out
}

func (s *Server) handleGatewayPreview(w http.ResponseWriter, r *http.Request) error {
	var req netx.GatewayPreviewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	v, err := s.modules.network.PreviewGateway(ctx, req, s.networkClient(r), s.protectedPorts(r))
	if err != nil {
		return mapGatewayError(err)
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleForwardVerify(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ForwardID int `json:"forwardId"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.ForwardID < 1 {
		return httpx.BadRequest("forwardId is required")
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	c, err := s.modules.network.VerifyForward(ctx, req.ForwardID)
	if err != nil {
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.forward.verify", strconv.Itoa(req.ForwardID), map[string]any{"target": c.Target, "status": c.Status})
	httpx.JSON(w, http.StatusOK, c)
	return nil
}

func (s *Server) handleBlocklistPreview(w http.ResponseWriter, r *http.Request) error {
	var req netx.BlocklistPreviewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// A country or feed preview fetches like a save does.
	ctx, cancel := timeoutCtx(r, 3*time.Minute)
	defer cancel()
	v, err := s.modules.network.PreviewBlocklist(ctx, req, s.networkClient(r))
	if err != nil {
		return mapGatewayError(err)
	}
	// A fetched preview reaches out to the feed, so it is recorded; it
	// changes nothing here.
	httpx.SetAudit(r, "network.blocklist.preview", req.List.Name, map[string]any{"id": req.ID, "kind": req.List.Kind, "countries": req.List.Countries, "url": req.List.URL, "preset": req.List.Preset, "networks": v.Networks})
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleProtectionPressure(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	v, err := s.modules.network.Pressure(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleSessionPreview(w http.ResponseWriter, r *http.Request) error {
	var req netx.SessionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	v, err := s.modules.network.PreviewSessions(ctx, req, s.networkClient(r))
	if err != nil {
		return mapGatewayError(err)
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) error {
	var req netx.SessionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	v, err := s.modules.network.RevokeSessions(ctx, req, s.networkClient(r))
	if err != nil {
		auditChange(r, "network.sessions.revoke", req.Network, req, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.sessions.revoke", v.Network, map[string]any{"blocklistId": req.BlocklistID, "matched": v.Matched, "ended": v.Ended, "failed": v.Failed})
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleExceptionAdd(w http.ResponseWriter, r *http.Request) error {
	var req netx.ExceptionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	v, err := s.modules.network.AddException(ctx, req, actor(r))
	if err != nil {
		auditChange(r, "network.exception.add", req.Address, req, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.exception.add", v.Address, req)
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleExceptionDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil || id == 0 {
		return httpx.BadRequest("invalid id")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteException(ctx, id, s.networkClient(r)); err != nil {
		auditChange(r, "network.exception.delete", strconv.Itoa(id), nil, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.exception.delete", strconv.Itoa(id), nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleTrustedUpdate(w http.ResponseWriter, r *http.Request) error {
	var req netx.TrustedRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// An expiry ends trust at a fixed time, which can refuse the address
	// after it passes; a reason or a review gives nothing away.
	if req.ExpiresAt != "" {
		if err := s.requireDestructive(r, "nettrusted", "setting a trusted address to expire"); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.network.UpdateTrusted(ctx, req, s.networkClient(r), actor(r)); err != nil {
		auditChange(r, "network.trusted.update", req.Address, req, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.trusted.update", req.Address, req)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleGatewayAdmissionRepair(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.gateway.admission.repair", "owned admission rules", nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.RepairGatewayAdmission(ctx); err != nil {
		return mapGatewayError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"repaired": true})
	return nil
}

// mapGatewayError adds the one code the gateway has of its own to the module's
// shared mapping: forwarding being off is not a bad request, it is a state of
// the host with a page that fixes it.
func mapGatewayError(err error) error {
	var off *netx.ForwardingRequiredError
	if errors.As(err, &off) {
		return httpx.Err(http.StatusConflict, "forwarding_off", off.Error())
	}
	return mapNetworkError(err)
}

// auditChange records a change that was made, or the refusal of one that a
// guard made — the same record the firewall's lockout refusal leaves.
func auditChange(r *http.Request, action, target string, detail any, err error) {
	var guard *netx.GuardError
	if errors.As(err, &guard) {
		httpx.SetAudit(r, action, target, map[string]any{"result": "refused_guard", "reason": guard.Reason})
		return
	}
	httpx.SetAudit(r, action, target, detail)
}

// requireDestructive is s.destructive for a request that is only sometimes the
// destructive kind: the capability, and the destructive limiter's budget.
func (s *Server) requireDestructive(r *http.Request, bucket, why string) error {
	p := httpx.MustPrincipal(r)
	if !p.Can(auth.CapDestructive) {
		return httpx.Err(http.StatusForbidden, "forbidden", why+" needs the destructive capability, which your role does not have")
	}
	if !s.destrLim.Allow(p.Username() + "|" + bucket) {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many destructive actions; try again shortly")
	}
	return nil
}

// entryID reads the {id} of an entry route; absent is zero, which the save
// handlers read as a create.
func entryID(r *http.Request) (int, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return 0, httpx.BadRequest("invalid id")
	}
	return id, nil
}

// protectedPorts are the ports this server is itself reached on, which a
// forward must not take over: SSH, the dashboard's own, and the port the
// request came to.
func (s *Server) protectedPorts(r *http.Request) []int {
	ports := []int{22, 80, 443}
	add := func(n int) {
		if n < 1 || n > 65535 {
			return
		}
		for _, p := range ports {
			if p == n {
				return
			}
		}
		ports = append(ports, n)
	}
	for _, hostport := range []string{r.Host, s.Cfg.Addr} {
		if _, p, err := net.SplitHostPort(hostport); err == nil {
			n, _ := strconv.Atoi(p)
			add(n)
		}
	}
	if s.modules.netsec != nil {
		// The port sshd is actually on, which is not 22 on a host that moved it.
		for _, raw := range s.modules.netsec.SSHDStatus(r.Context()).Ports {
			n, _ := strconv.Atoi(raw)
			add(n)
		}
	}
	return ports
}

func (s *Server) handleNetworkGateway(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	v, err := s.modules.network.Gateway(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	netx.AttachExternalEvidence(v, s.externalObservations(r))
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleNetworkProtection(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	v, err := s.modules.network.Protection(ctx, s.networkClient(r))
	if err != nil {
		return mapNetworkError(err)
	}
	if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		v.HideOperatorActivity()
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

// ---- forwards

func (s *Server) handleForwardSave(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil {
		return err
	}
	var req netx.ForwardRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if id != 0 && req.Enabled != nil && !*req.Enabled {
		if err := s.requireDestructive(r, "netforward", "turning a port forward off"); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	action, v := "network.forward.add", netx.ForwardView{}
	if id == 0 {
		v, err = s.modules.network.AddForward(ctx, req, s.networkClient(r), actor(r), s.protectedPorts(r))
	} else {
		action = "network.forward.update"
		v, err = s.modules.network.UpdateForward(ctx, id, req, s.networkClient(r), actor(r), s.protectedPorts(r))
	}
	if err != nil {
		auditChange(r, action, req.Name, req, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, action, v.Name, req)
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleForwardDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil || id == 0 {
		return httpx.BadRequest("invalid id")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteForward(ctx, id); err != nil {
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.forward.delete", strconv.Itoa(id), nil)
	httpx.NoContent(w)
	return nil
}

// ---- NAT

func (s *Server) handleNATSave(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil {
		return err
	}
	var req netx.NATRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if id != 0 && req.Enabled != nil && !*req.Enabled {
		if err := s.requireDestructive(r, "netnat", "turning NAT off for a network"); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	action, v := "network.nat.add", netx.NATView{}
	if id == 0 {
		v, err = s.modules.network.AddNAT(ctx, req, s.networkClient(r), actor(r))
	} else {
		action = "network.nat.update"
		v, err = s.modules.network.UpdateNAT(ctx, id, req, s.networkClient(r), actor(r))
	}
	if err != nil {
		auditChange(r, action, req.Name, req, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, action, v.Name, req)
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleNATDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil || id == 0 {
		return httpx.BadRequest("invalid id")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteNAT(ctx, id); err != nil {
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.nat.delete", strconv.Itoa(id), nil)
	httpx.NoContent(w)
	return nil
}

// ---- limits

func (s *Server) handleLimitSave(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil {
		return err
	}
	var req netx.LimitRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if id != 0 && req.Enabled != nil && !*req.Enabled {
		if err := s.requireDestructive(r, "netlimit", "turning a limit off"); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	action, v := "network.limit.add", netx.LimitView{}
	if id == 0 {
		v, err = s.modules.network.AddLimit(ctx, req, s.networkClient(r), actor(r))
	} else {
		action = "network.limit.update"
		v, err = s.modules.network.UpdateLimit(ctx, id, req, s.networkClient(r), actor(r))
	}
	if err != nil {
		auditChange(r, action, req.Name, req, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, action, v.Name, req)
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleLimitDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil || id == 0 {
		return httpx.BadRequest("invalid id")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteLimit(ctx, id); err != nil {
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.limit.delete", strconv.Itoa(id), nil)
	httpx.NoContent(w)
	return nil
}

// ---- blocklists

func (s *Server) handleBlocklistSave(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil {
		return err
	}
	var req netx.BlocklistRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if id != 0 && req.Enabled != nil && !*req.Enabled {
		if err := s.requireDestructive(r, "netblocklist", "turning a blocklist off"); err != nil {
			return err
		}
	}
	// A country or feed list is fetched before it is saved, which is up to a
	// request for each address family of each country.
	ctx, cancel := timeoutCtx(r, 3*time.Minute)
	defer cancel()
	action, v := "network.blocklist.add", netx.BlocklistView{}
	if id == 0 {
		v, err = s.modules.network.AddBlocklist(ctx, req, s.networkClient(r), actor(r))
	} else {
		action = "network.blocklist.update"
		v, err = s.modules.network.UpdateBlocklist(ctx, id, req, s.networkClient(r), actor(r))
	}
	if err != nil {
		auditChange(r, action, req.Name, req, err)
		return mapGatewayError(err)
	}
	// Entries are not recorded: a manual list can be ten thousand lines.
	httpx.SetAudit(r, action, v.Name, map[string]any{"kind": v.Kind, "countries": v.Countries, "url": v.URL, "count": v.Count, "enabled": v.Enabled, "refresh": v.Refresh, "integrity": v.Integrity})
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleBlocklistRefresh(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil || id == 0 {
		return httpx.BadRequest("invalid id")
	}
	ctx, cancel := timeoutCtx(r, 3*time.Minute)
	defer cancel()
	if err := s.modules.network.RefreshBlocklist(ctx, id); err != nil {
		return mapGatewayError(err)
	}
	v, err := s.modules.network.Blocklist(ctx, id, s.networkClient(r))
	if err != nil {
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.blocklist.refresh", v.Name, map[string]any{"count": v.Count})
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleBlocklistDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := entryID(r)
	if err != nil || id == 0 {
		return httpx.BadRequest("invalid id")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteBlocklist(ctx, id, s.networkClient(r)); err != nil {
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.blocklist.delete", strconv.Itoa(id), nil)
	httpx.NoContent(w)
	return nil
}

// ---- kernel settings and the trusted list

type protectionSettingsRequest struct {
	Values map[string]string `json:"values"`
}

func (s *Server) handleProtectionSettings(w http.ResponseWriter, r *http.Request) error {
	var req protectionSettingsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Moving a setting away from what the kernel is doing is routine when it
	// strengthens it and destructive when it gives protection away; whether it
	// does is judged by the module, which knows each setting's direction, and a
	// value it cannot read is judged the worse way.
	if s.modules.network.WeakensProtection(req.Values) {
		if err := s.requireDestructive(r, "netsettings", "weakening a kernel protection"); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.SetProtections(ctx, req.Values, actor(r)); err != nil {
		return mapGatewayError(err)
	}
	settings, err := s.modules.network.ProtectionSettings()
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.protection.set", "kernel settings", req.Values)
	httpx.JSON(w, http.StatusOK, map[string]any{"settings": settings})
	return nil
}

func (s *Server) handleProtectionReset(w http.ResponseWriter, r *http.Request) error {
	key := chi.URLParam(r, "key")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.ResetProtection(ctx, key); err != nil {
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.protection.reset", key, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleTrustedRemove(w http.ResponseWriter, r *http.Request) error {
	address := r.URL.Query().Get("address")
	if address == "" {
		return httpx.BadRequest("address is required")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := s.modules.network.RemoveTrusted(ctx, address, s.networkClient(r)); err != nil {
		auditChange(r, "network.trusted.remove", address, nil, err)
		return mapGatewayError(err)
	}
	httpx.SetAudit(r, "network.trusted.remove", address, nil)
	httpx.NoContent(w)
	return nil
}
