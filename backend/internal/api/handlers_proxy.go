package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountProxyRoutes(r chi.Router) {
	r.Route("/proxy", func(r chi.Router) {
		r.Method(http.MethodGet, "/status", s.handle(s.handleProxyStatus))
		r.Method(http.MethodGet, "/vhosts", s.handle(s.handleVHostList))
		r.Method(http.MethodGet, "/config", s.handle(s.handleProxyConfigRead))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			// Validation is not a read: nginx cannot test a config it cannot
			// see at its own path, so validating puts the candidate on disk
			// for the length of one `nginx -t`. That is the same trust as
			// writing it, so it is gated the same way.
			r.Method(http.MethodPost, "/validate", s.handle(s.handleProxyValidate))
			r.Method(http.MethodPut, "/config", s.handle(s.handleProxyConfigWrite))
			// A test of what is on disk touches nothing, but it runs the
			// host's own binary and is the same sentence of trust as a
			// reload, so it is gated with it.
			r.Method(http.MethodPost, "/test", s.handle(s.handleProxyTest))
			r.Method(http.MethodPost, "/reload", s.handle(s.handleProxyReload))
			s.destructive(r, func(r chi.Router) {
				// Disabling a vhost takes a site offline, and the handler
				// asks for the site's own name before it will.
				r.Method(http.MethodPost, "/vhosts/{name}/enabled", s.handle(s.handleVHostToggle))
			})
		})
	})

	r.Route("/certificates", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleCertList))
		r.Method(http.MethodGet, "/certbot", s.handle(s.handleCertbot))
		r.Method(http.MethodGet, "/dns-providers", s.handle(s.handleDNSProviders))
		r.Method(http.MethodGet, "/watched", s.handle(s.handleWatchedDomains))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			// These checks emit traffic to caller-chosen destinations. Keeping
			// them with the host-administration routes prevents a read-only
			// account from turning the server into an internal network scanner.
			r.Method(http.MethodGet, "/check", s.handle(s.handleCertCheck))
			r.Method(http.MethodGet, "/scan", s.handle(s.handleTLSScan))
			r.Method(http.MethodGet, "/dns", s.handle(s.handleDomainDNS))
			r.Method(http.MethodPost, "/watched", s.handle(s.handleWatchDomain))
			r.Method(http.MethodDelete, "/watched/{id}", s.handle(s.handleUnwatchDomain))
			r.Method(http.MethodPost, "/issue", s.handle(s.handleCertIssue))
			r.Method(http.MethodPost, "/import", s.handle(s.handleCertImport))
			r.Method(http.MethodPost, "/dns-credentials", s.handle(s.handleDNSCredentials))
			r.Method(http.MethodPost, "/renew", s.handle(s.handleCertRenew))
			s.destructive(r, func(r chi.Router) {
				// Removing a saved DNS token is recoverable — paste it again
				// — so it takes the ordinary confirmation and no phrase.
				r.Method(http.MethodDelete, "/dns-credentials/{provider}", s.handle(s.handleDNSCredentialsRemove))
				// Revocation cannot be undone: the authority publishes that
				// the certificate is no longer to be trusted, and every
				// client holding it starts refusing the site.
				r.Method(http.MethodPost, "/revoke", s.handle(s.handleCertRevoke))
			})
		})
	})

	// The site builder and the live TLS report, in handlers_proxy_sites.go.
	s.mountSiteRoutes(r)

	r.Method(http.MethodGet, "/ports", s.handle(s.handlePortList))
}

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.proxy.Availability(r.Context()))
	return nil
}

func (s *Server) handleVHostList(w http.ResponseWriter, r *http.Request) error {
	hosts, err := s.modules.proxy.ListVHosts(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, hosts)
	return nil
}

func (s *Server) handleProxyConfigRead(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	content, err := s.modules.proxy.ReadConfig(path)
	if err != nil {
		return mapProxyError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": path, "content": content})
	return nil
}

type proxyConfigRequest struct {
	Kind    proxysvc.Kind `json:"kind"`
	Path    string        `json:"path"`
	Content string        `json:"content"`
	Reload  bool          `json:"reload"`
}

// handleProxyValidate tells the operator whether a config would be accepted,
// and leaves what is currently serving traffic as it was. It is audited rather
// than skipped because the nginx path touches the real file to do it.
func (s *Server) handleProxyValidate(w http.ResponseWriter, r *http.Request) error {
	var req proxyConfigRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := s.modules.proxy.Validate(r.Context(), req.Kind, req.Path, req.Content)
	if err != nil {
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.config.validate", req.Path,
		map[string]any{"kind": req.Kind, "valid": res.Valid, "bytes": len(req.Content)})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleProxyConfigWrite(w http.ResponseWriter, r *http.Request) error {
	var req proxyConfigRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := s.modules.proxy.WriteConfig(r.Context(), req.Kind, req.Path, req.Content)
	if err != nil {
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			httpx.SetAudit(r, "proxy.config.write", req.Path, map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Output)
		}
		return mapProxyError(err)
	}
	out := map[string]any{"validation": res}
	if req.Reload {
		reload, err := s.modules.proxy.Reload(r.Context(), req.Kind)
		out["reload"] = reload
		if err != nil {
			httpx.SetAudit(r, "proxy.config.write", req.Path, map[string]any{"reloaded": false})
			return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
		}
	}
	httpx.SetAudit(r, "proxy.config.write", req.Path,
		map[string]any{"kind": req.Kind, "reloaded": req.Reload, "bytes": len(req.Content)})
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type vhostToggleRequest struct {
	Enabled bool `json:"enabled"`
	Reload  bool `json:"reload"`
}

func (s *Server) handleVHostToggle(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req vhostToggleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// No typed phrase: this is a toggle, and the same switch turns the site
	// back on. Nothing is written that cannot be unwritten by clicking it
	// again.
	if err := s.modules.proxy.SetVHostEnabled(r.Context(), name, req.Enabled); err != nil {
		return mapProxyError(err)
	}
	out := map[string]any{"name": name, "enabled": req.Enabled}
	if req.Reload {
		reload, err := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
		out["reload"] = reload
		if err != nil {
			// The symlink change is already applied; report the reload
			// failure rather than pretending the toggle did not happen.
			httpx.SetAudit(r, "proxy.vhost.toggle", name,
				map[string]any{"enabled": req.Enabled, "reloadError": err.Error()})
			return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
		}
	}
	httpx.SetAudit(r, "proxy.vhost.toggle", name, map[string]any{"enabled": req.Enabled})
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type reloadRequest struct {
	Kind proxysvc.Kind `json:"kind"`
}

// handleProxyTest answers "would a reload succeed right now" without
// reloading: the server's own config test against the files on disk.
func (s *Server) handleProxyTest(w http.ResponseWriter, r *http.Request) error {
	var req reloadRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Kind == "" {
		req.Kind = proxysvc.KindNginx
	}
	res := s.modules.proxy.Test(r.Context(), req.Kind)
	httpx.SetAudit(r, "proxy.config.test", string(req.Kind), map[string]any{"valid": res.Valid})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleProxyReload(w http.ResponseWriter, r *http.Request) error {
	var req reloadRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Kind == "" {
		req.Kind = proxysvc.KindNginx
	}
	res, err := s.modules.proxy.Reload(r.Context(), req.Kind)
	if err != nil {
		httpx.SetAudit(r, "proxy.reload", string(req.Kind), map[string]any{"result": "failed"})
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		}
		return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
	}
	httpx.SetAudit(r, "proxy.reload", string(req.Kind), nil)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func mapProxyError(err error) error {
	switch {
	case errors.Is(err, proxysvc.ErrUnsafePath):
		return httpx.Err(http.StatusForbidden, "outside_root", err.Error())
	case errors.Is(err, proxysvc.ErrProtectedFile):
		return httpx.Err(http.StatusForbidden, "protected_file", err.Error())
	case errors.Is(err, proxysvc.ErrNoProxy):
		return httpx.Err(http.StatusServiceUnavailable, "no_proxy", err.Error())
	default:
		return httpx.BadRequest("%v", err)
	}
}

func (s *Server) handleCertList(w http.ResponseWriter, r *http.Request) error {
	certs, err := s.modules.proxy.ListCertificates(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, certs)
	return nil
}

func (s *Server) handleCertCheck(w http.ResponseWriter, r *http.Request) error {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		return httpx.BadRequest("domain query parameter is required")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	cert, err := proxysvc.CheckDomain(ctx, domain, atoiDefault(r.URL.Query().Get("port"), 443))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, cert)
	return nil
}

// handleCertbot answers with certbot's own view, parsed.
//
// It knows things the PEM files do not: which lineage a certificate belongs
// to, and whether anything is scheduled to renew it. The second is the one
// that matters — an expired Let's Encrypt certificate is almost never a
// forgotten renewal, it is a renewal timer that stopped and told nobody.
func (s *Server) handleCertbot(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	state := s.modules.proxy.CertbotState(ctx)
	if !state.Available {
		return httpx.Err(http.StatusServiceUnavailable, "certbot_unavailable",
			"certbot is not installed on this host")
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

func (s *Server) handlePortList(w http.ResponseWriter, r *http.Request) error {
	listeners, err := proxysvc.ListListeners(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, listeners)
	return nil
}
