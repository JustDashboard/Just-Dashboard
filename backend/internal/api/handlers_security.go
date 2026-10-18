package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// The Security operations: SSH, the connection
// table and the diagnostic tools.
//
// Mounted from mountNetSecRoutes rather than from Routes, so the whole
// security surface still has one place to be read — the same arrangement
// handlers_docker_manage.go has with Docker.
func (s *Server) mountSecurityRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/security/services", s.handle(s.handleServiceCatalogue))
	r.Method(http.MethodGet, "/connections", s.handle(s.handleConnections))
	s.mountConnectionDetailRoutes(r)

	// Listing the logins and ending one are the same subtree, and they have to
	// be registered in the same place: chi mounts a Route as a subrouter, so a
	// Route and a Method on the same pattern are not two routes — the second
	// takes the path and the first quietly stops existing. Split across two
	// mount functions, that is exactly what happened, and `GET /ssh-sessions`
	// answered 404 while the page went on polling it.
	r.Route("/ssh-sessions", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleSSHSessions))
		// Ending somebody's session is destructive — capability, the tighter
		// budget, an audit entry — but recoverable: a SIGHUP the operator
		// reconnects past. So it takes the ordinary confirmation and not a
		// typed phrase, by the frequency test in
		// docs/internal/security/invariants.md.
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPost, "/{pid}/disconnect", s.handle(s.handleDisconnectSession))
			})
		})
	})

	r.Route("/ssh", func(r chi.Router) {
		// The SSH configuration names the accounts that hold keys, which is a
		// map of who can reach this machine. That belongs with the capability
		// that already implies full access.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/config", s.handle(s.handleSSHConfig))
		s.destructive(r, func(r chi.Router) {
			// Not destructive in the sense of losing data. Destructive in the
			// sense the marker exists for: get it wrong and the way back into
			// the machine is gone, and no amount of undo in this UI helps
			// because reaching this UI is what you have lost.
			r.Method(http.MethodPost, "/config", s.handle(s.handleSSHApply))
		})
	})

	// The interface summary and the probes moved to mountNetworkRoutes, which
	// owns everything under /network.

	s.mountSecurityIntrusionRoutes(r)
	s.mountSecurityBoundaryRoutes(r)
}

func ptr[T any](v T) *T { return &v }

// handleServiceCatalogue hands the port catalogue to the rule form, so the
// names and the warnings are the server's list rather than a second copy in
// TypeScript that would drift from the one the audit reads.
func (s *Server) handleServiceCatalogue(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, netsec.ServiceCatalogue)
	return nil
}

func (s *Server) handleFirewallApps(w http.ResponseWriter, r *http.Request) error {
	profiles, err := s.modules.netsec.AppProfiles(r.Context())
	if err != nil {
		// A host without ufw has no profiles, which is information rather
		// than a failure.
		httpx.JSON(w, http.StatusOK, []netsec.AppProfile{})
		return nil
	}
	httpx.JSON(w, http.StatusOK, profiles)
	return nil
}

func (s *Server) handleSSHConfig(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.netsec.SSHDStatus(r.Context()))
	return nil
}

type sshApplyRequest struct {
	Settings map[string]string `json:"settings"`
	// AcknowledgeBoundary says the operator saw what the change does to the
	// way the dashboard is reached (a moved port, forwarding, AllowUsers).
	AcknowledgeBoundary bool `json:"acknowledgeBoundary,omitempty"`
}

// handleSSHApply plans synchronously and applies as a job.
//
// The split is deliberate and is where the value is. Every refusal — an
// unknown directive, a value out of range, and above all a change that would
// leave nobody a way in — has to reach the operator as the answer to their own
// click. What follows is a write, an `sshd -t` and a reload, each worth
// watching: this is the one operation where "it said it worked" is not the
// same as knowing the daemon came back.
func (s *Server) handleSSHApply(w http.ResponseWriter, r *http.Request) error {
	var req sshApplyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// A browser on loopback reached the dashboard through an SSH tunnel, the
	// one way in that rides on sshd's TCP forwarding: turning it off leaves
	// the current tunnel up and refuses the next one. The jump-host
	// section's recommendation is "no", so this is the guard that keeps it
	// from being followed from inside a tunnel.
	if v, ok := req.Settings["allowtcpforwarding"]; ok && (v == "no" || v == "remote") {
		if addr, err := netip.ParseAddr(httpx.ClientIP(r)); err == nil && addr.IsLoopback() {
			httpx.SetAudit(r, "ssh.config", "", map[string]any{"result": "refused_lockout"})
			return httpx.Err(http.StatusConflict, "would_lock_you_out",
				"you reach the dashboard through an SSH tunnel, which needs sshd's TCP forwarding; turning it off would refuse your next tunnel")
		}
	}
	pending, err := s.pendingSSHApply(r)
	if err != nil {
		return err
	}
	if err := s.boundaryGate(r, "ssh.config", netsec.BoundaryProposal{Kind: "ssh", Settings: req.Settings}, req.AcknowledgeBoundary); err != nil {
		return err
	}
	plan, err := s.modules.netsec.PlanSSHSettings(r.Context(), req.Settings)
	if err != nil {
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "ssh.config", "", map[string]any{"result": "refused_lockout"})
			return httpx.Err(http.StatusConflict, "would_lock_you_out", err.Error())
		}
		return httpx.BadRequest("%v", err)
	}
	if pending {
		return s.startPendingSSHApply(w, r, plan)
	}
	httpx.SetAudit(r, "ssh.config", plan.File,
		map[string]any{"applied": plan.Applied, "streamed": true})

	s.startJob(w, r, jobs.Spec{
		Kind:   "ssh.apply",
		Title:  "Applying SSH settings: " + strings.Join(plan.Applied, ", "),
		Target: plan.File, Timeout: 2 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		res, err := s.modules.netsec.ApplySSHPlan(ctx, plan, out)
		if err != nil {
			if errors.Is(err, netsec.ErrSSHInvalid) {
				return fmt.Errorf("sshd rejected the configuration and the previous file was restored")
			}
			return err
		}
		if res.ReloadError != "" {
			return fmt.Errorf("the configuration is valid and written, but sshd did not reload: %s", res.ReloadError)
		}
		return nil
	})
	return nil
}

// pendingSSHApply reads the opt-in a network mutation uses. It is refused
// for anything but an administrator's interactive session, because only a
// session can return the reconnection challenge that keeps the change.
func (s *Server) pendingSSHApply(r *http.Request) (bool, error) {
	mode := r.Header.Get(networkApplyHeader)
	if mode == "" {
		return false, nil
	}
	if mode != "pending" {
		return false, httpx.BadRequest("%s accepts only pending", networkApplyHeader)
	}
	p := httpx.MustPrincipal(r)
	if p.Kind != "session" || p.SessionID == "" || p.UserID() <= 0 || !p.Can(auth.CapSystemAdmin) {
		return false, httpx.Err(http.StatusForbidden, "session_required",
			"Pending SSH apply requires an administrator's interactive session.")
	}
	if s.modules.network == nil {
		return false, httpx.Err(http.StatusServiceUnavailable, "network_unavailable",
			"The network module that keeps the recovery journal is not running.")
	}
	return true, nil
}

// startPendingSSHApply enrols the apply in the network journal before the
// first write, so the independent host watchdog restores the previous files
// and reloads sshd unless this session confirms a fresh response in time.
// Every guard PlanSSHSettings ran still applies; this adds recovery, not
// permission.
func (s *Server) startPendingSSHApply(w http.ResponseWriter, r *http.Request, plan *netsec.SSHApplyPlan) error {
	files := []netx.SSHFile{}
	for _, f := range plan.Files() {
		files = append(files, netx.SSHFile{Path: f.Path, Candidate: []byte(f.Content)})
	}
	ctx := netx.WithPendingConfirmation(r.Context(), httpx.MustPrincipal(r).UserID())
	readCtx, cancel := timeoutCtx(r, 30*time.Second)
	before := s.accessBoundary(readCtx, r)
	cancel()
	change, err := s.modules.network.BeginSSHChange(ctx, files, plan.SocketUnit())
	if err != nil {
		httpx.SetAudit(r, "ssh.config", plan.File, map[string]any{"result": "refused_pending", "pending": true})
		return mapNetworkConfirmationError(err)
	}
	rememberBoundary(change.ID, before)
	httpx.SetAudit(r, "ssh.config", plan.File,
		map[string]any{"applied": plan.Applied, "streamed": true, "pending": true, "change": change.ID})
	w.Header().Set("X-JD-Network-Change", change.ID)
	w.Header().Set("X-JD-Network-Expires", change.ExpiresAt.Format(time.RFC3339Nano))
	s.startJob(w, r, jobs.Spec{
		Kind:   "ssh.apply",
		Title:  "Applying SSH settings until confirmed: " + strings.Join(plan.Applied, ", "),
		Target: plan.File, Timeout: 2 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		res, err := s.modules.netsec.ApplySSHPlan(ctx, plan, out)
		if err == nil {
			err = res.Failure()
		}
		status, finishErr := s.modules.network.FinishSSHChange(ctx, change, err)
		if finishErr != nil {
			if status != nil && status.Phase == "recovered" {
				return fmt.Errorf("the previous SSH configuration was restored: %w", finishErr)
			}
			return fmt.Errorf("SSH recovery needs attention: %w", finishErr)
		}
		out.Status("Applied until confirmed. Verify a new dashboard response and confirm before %s, or the host restores the previous SSH configuration.",
			change.ExpiresAt.Local().Format(time.TimeOnly))
		return nil
	})
	return nil
}

// handleDisconnectSession ends one interactive login.
//
// The PID is checked against the live session list inside netsec rather than
// here, because that check is what stops this being a "kill any process"
// route wearing a sensible name.
func (s *Server) handleDisconnectSession(w http.ResponseWriter, r *http.Request) error {
	pid, err := strconv.Atoi(chi.URLParam(r, "pid"))
	if err != nil {
		return httpx.BadRequest("invalid process id")
	}
	session, err := s.modules.netsec.Disconnect(r.Context(), int32(pid))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "ssh.session.disconnect", session.User,
		map[string]any{"pid": pid, "tty": session.TTY, "from": session.From})
	httpx.JSON(w, http.StatusOK, session)
	return nil
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) error {
	conns, err := s.modules.netsec.Connections(r.Context())
	if err != nil {
		return httpx.Err(http.StatusServiceUnavailable, "connections_unavailable",
			"the host's connection table could not be read")
	}
	httpx.JSON(w, http.StatusOK, conns)
	return nil
}

func (s *Server) handleNetworkInfo(w http.ResponseWriter, r *http.Request) error {
	info, err := s.modules.netsec.NetworkInfo(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, info)
	return nil
}

type probeRequest = netsec.ProbeRequest

func (s *Server) handleNetworkProbe(w http.ResponseWriter, r *http.Request) error {
	var req probeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	var err error
	req, err = netsec.ValidateProbeRequest(req)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	detail := map[string]any{"target": req.Target}
	if req.Option != "" {
		detail["option"] = req.Option
	}
	if req.Verify != "" {
		detail["verify"], detail["port"] = req.Verify, req.Port
	}
	httpx.SetAudit(r, "network.probe", req.Tool, detail)
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	started := time.Now()
	res, err := s.executeNetworkProbe(ctx, req)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	res.ResultID = quickResults.keep(httpx.MustPrincipal(r).Username(), req, res, started, time.Now())
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// Both quick and retained probes use the same existing tools and explicit argv.
func (s *Server) executeNetworkProbe(ctx context.Context, req netsec.ProbeRequest) (*netsec.ProbeResult, error) {
	var res *netsec.ProbeResult
	var err error
	switch req.Tool {
	case "ping":
		res, err = s.modules.netsec.Ping(ctx, req.Target)
	case "traceroute":
		res, err = s.modules.netsec.Traceroute(ctx, req.Target)
	case "dns":
		res, err = s.modules.netsec.Lookup(ctx, req.Target, req.Record)
	case "port":
		res, err = s.modules.netsec.PortCheck(ctx, req.Target, req.Port)
		if err == nil {
			s.correlatePortCheck(ctx, req, res)
		}
	case "scan":
		res, err = s.modules.netsec.PortScan(ctx, req.Target)
	case "http":
		res, err = s.modules.netsec.HTTPCheck(ctx, req.Target, req.Port)
	case "tls":
		res, err = s.modules.netsec.TLSCert(ctx, req.Target, req.Port)
	case "whois":
		res, err = s.modules.netsec.Whois(ctx, req.Target)
	case "dnsauth":
		res, err = s.modules.netsec.DNSAuthority(ctx, req.Target)
	case "banner":
		res, err = s.modules.netsec.BannerGrab(ctx, req.Target, req.Port)
	case "ssh":
		res, err = s.modules.netsec.SSHScan(ctx, req.Target, req.Port)
		if err == nil {
			s.compareSSHTrust(ctx, req, res)
		}
	case "starttls":
		res, err = s.modules.netsec.STARTTLSCheck(ctx, req.Target, req.Port, req.Option)
	case "tlssurvey":
		res, err = s.modules.netsec.TLSSurvey(ctx, req.Target, req.Port)
	case "dnsbl":
		res, err = s.modules.netsec.DNSBLCheck(ctx, req.Target)
	case "asn":
		res, err = s.modules.netsec.ASNLookup(ctx, req.Target)
	case "mx":
		res, err = s.modules.netsec.MXCheck(ctx, req.Target)
	case "httpsec":
		res, err = s.modules.netsec.HTTPSecurity(ctx, req.Target, req.Port, req.Option)
	case "siteaudit":
		res, err = s.modules.netsec.SiteAudit(ctx, req.Target, req.Port)
		if err == nil {
			netsec.AttributeSiteOwner(res, s.proxySiteFor(ctx, req.Target))
		}
	case "listeners":
		res, err = s.modules.netsec.Listeners(ctx)
	case "egress":
		res, err = s.modules.netsec.Egress(ctx)
	case "neigh":
		res, err = s.modules.netsec.Neighbours(ctx)
	case "route":
		res, err = s.modules.netsec.RouteLookup(ctx, req.Target)
		if err == nil && req.Port > 0 {
			s.joinRouteLayers(ctx, req, res)
		}
	case "mtu":
		res, err = s.modules.netsec.PathMTU(ctx, req.Target)
	case "capture":
		res, err = s.modules.netsec.PacketSnapshot(ctx, req.Target, req.Option)
	case "wol":
		res, err = s.modules.netsec.WakeOnLAN(ctx, req.Target, req.Option, req.Verify, req.Port)
	case "capabilities":
		res = networkSupportProbe(s.modules.network.HostSupport(ctx))
	default:
		return nil, fmt.Errorf("unknown network diagnostic tool %q", req.Tool)
	}
	return res, err
}

func (s *Server) handleJailConfig(w http.ResponseWriter, r *http.Request) error {
	cfg, err := s.modules.netsec.JailConfig(r.Context(), chi.URLParam(r, "jail"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, cfg)
	return nil
}

func (s *Server) handleJailPolicy(w http.ResponseWriter, r *http.Request) error {
	jail := chi.URLParam(r, "jail")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	var ports []string
	if jail == "sshd" {
		// Only the listening ports leave this read; the keyed accounts the
		// full SSH configuration names stay behind system.admin.
		ports = s.modules.netsec.SSHDStatus(ctx).Ports
	}
	policy, err := s.modules.netsec.JailPolicy(ctx, jail, ports)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, policy)
	return nil
}

type jailParamRequest struct {
	// Params carries every parameter at once, because they are one policy —
	// "this many failures inside this window earns this long a ban" — and
	// applying them one request at a time leaves a jail half-tuned if the tab
	// closes in between.
	Params map[string]int `json:"params"`
	// The single-parameter form the first version of this route took, kept so
	// a scripted caller does not break.
	Param string `json:"param,omitempty"`
	Value int    `json:"value,omitempty"`
}

func (s *Server) handleJailParam(w http.ResponseWriter, r *http.Request) error {
	var req jailParamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	params := req.Params
	if len(params) == 0 && req.Param != "" {
		params = map[string]int{req.Param: req.Value}
	}
	jail := chi.URLParam(r, "jail")
	res, err := s.modules.netsec.SetJailParams(r.Context(), jail, params)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "fail2ban.param", jail, map[string]any{
		"params": params, "persisted": res.Persisted,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type jailIgnoreRequest struct {
	IP  string `json:"ip"`
	Add bool   `json:"add"`
}

func (s *Server) handleJailIgnore(w http.ResponseWriter, r *http.Request) error {
	var req jailIgnoreRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	jail := chi.URLParam(r, "jail")
	out, err := s.modules.netsec.IgnoreIP(r.Context(), jail, req.IP, req.Add)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "fail2ban.ignore", jail, map[string]any{"ip": req.IP, "add": req.Add})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleJailUnbanAll(w http.ResponseWriter, r *http.Request) error {
	jail := chi.URLParam(r, "jail")
	count, err := s.modules.netsec.UnbanAll(r.Context(), jail)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "fail2ban.unban.all", jail, map[string]any{"count": count})
	httpx.JSON(w, http.StatusOK, map[string]int{"unbanned": count})
	return nil
}

// handleBanOffenders answers "who keeps coming back", which the jail's own
// list cannot: a ban expires, so the address banned eleven times this week is
// invisible the moment its eleventh ban lapses.
func (s *Server) handleBanOffenders(w http.ResponseWriter, r *http.Request) error {
	events, err := s.modules.netsec.BanHistory(r.Context(), 2000)
	if err != nil {
		return httpx.Internal(err)
	}
	if len(events) == 0 && !s.modules.netsec.Fail2banAvailable() {
		return httpx.Err(http.StatusServiceUnavailable, "fail2ban_unavailable",
			"fail2ban is not installed on this host")
	}
	top := atoiDefault(r.URL.Query().Get("top"), 10)
	if top < 1 || top > 100 {
		top = 10
	}
	httpx.JSON(w, http.StatusOK, netsec.SummariseBans(events, top))
	return nil
}
