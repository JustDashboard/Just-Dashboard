package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// The site builder, the live TLS report and certbot.
//
// Mounted from mountProxyRoutes so the whole proxy surface stays readable in
// one place, the same way the Docker write surface lives in its own file but
// is mounted from mountDockerRoutes.
func (s *Server) mountSiteRoutes(r chi.Router) {
	// Stream forwarding is a sibling of the site builder rather than part of
	// it: nginx's stream block is a top-level context, not something a server
	// file can reach, and pretending otherwise in the API would invite a
	// stream to be written where nginx never reads it.
	r.Route("/proxy/streams", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleStreamList))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/preview", s.handle(s.handleStreamPreview))
			r.Method(http.MethodPost, "/", s.handle(s.handleStreamApply))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/{name}", s.handle(s.handleStreamDelete))
			})
		})
	})

	// Password files for the site form's basic-auth option. Behind
	// system.admin throughout: the listing names who may reach a protected
	// site, and setting one is handing out access to it.
	r.Route("/proxy/auth-files", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleAuthFileList))
		r.Method(http.MethodPost, "/", s.handle(s.handleAuthUserSet))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{file}", s.handle(s.handleAuthFileDelete))
			r.Method(http.MethodDelete, "/{file}/users/{user}", s.handle(s.handleAuthUserRemove))
		})
	})

	r.Route("/proxy/sites", func(r chi.Router) {
		r.Method(http.MethodGet, "/{name}", s.handle(s.handleSiteSpec))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			// Preview renders and touches nothing, but it lives inside the
			// admin group because the form that calls it is admin-only and a
			// separate gate would be a claim about a boundary that is not
			// there.
			r.Method(http.MethodPost, "/preview", s.handle(s.handleSitePreview))
			r.Method(http.MethodPost, "/", s.handle(s.handleSiteApply))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/{name}", s.handle(s.handleSiteDelete))
			})
		})
	})
}

func (s *Server) handleSiteSpec(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if name == "" || strings.ContainsAny(name, "/\\") {
		return httpx.BadRequest("invalid site name")
	}
	// Read through the same allowlist the config editor uses rather than
	// joining a path here: this endpoint takes a name, and a name that turns
	// out to be a path is exactly what that check exists for.
	var content string
	var err error
	// Both spellings of the conf.d layout: the listing on such a host reports
	// a name that already ends in .conf, and a host that was set up by hand
	// may have a file without it.
	for _, candidate := range []string{
		filepath.Join(s.Cfg.NginxDir, "sites-available", name),
		filepath.Join(s.Cfg.NginxDir, "conf.d", name),
		filepath.Join(s.Cfg.NginxDir, "conf.d", name+".conf"),
	} {
		content, err = s.modules.proxy.ReadConfig(candidate)
		if err == nil {
			break
		}
	}
	if err != nil {
		return httpx.ErrNotFound
	}
	spec, managed := proxysvc.ParseSiteSpec(name, content)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"spec": spec, "managed": managed, "content": content,
		"warnings": proxysvc.SpecWarnings(spec),
	})
	return nil
}

type siteRequest struct {
	Spec      proxysvc.SiteSpec `json:"spec"`
	Enable    bool              `json:"enable"`
	Reload    bool              `json:"reload"`
	Overwrite bool              `json:"overwrite"`
}

// handleSitePreview shows the config a spec would produce, live, as the form
// is filled in. Rendering on the server is what keeps one implementation of
// "what does this spec mean"; a second one in the browser would drift, and the
// one that mattered would be the one nobody was reading.
func (s *Server) handleSitePreview(w http.ResponseWriter, r *http.Request) error {
	var req siteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	content, err := proxysvc.RenderNginx(&req.Spec)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"content": content, "warnings": proxysvc.SpecWarnings(&req.Spec),
	})
	return nil
}

func (s *Server) handleSiteApply(w http.ResponseWriter, r *http.Request) error {
	var req siteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.ApplySite(ctx, &req.Spec, req.Enable, req.Reload, req.Overwrite)
	if err != nil {
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		}
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, map[string]any{
		"domains": req.Spec.Domains, "kind": req.Spec.Kind,
		"tls": req.Spec.TLS, "reloaded": res.Reloaded,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleSiteDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if err := s.modules.proxy.DeleteSite(r.Context(), name); err != nil {
		return httpx.BadRequest("%v", err)
	}
	// The site is gone from disk; nginx keeps serving it until it reloads,
	// which is reported rather than hidden.
	reload, reloadErr := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
	httpx.SetAudit(r, "proxy.site.delete", name, map[string]any{"reloaded": reloadErr == nil})
	out := map[string]any{"name": name, "reload": reload}
	if reloadErr != nil {
		out["reloadError"] = reloadErr.Error()
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleTLSScan is the live report: protocol versions, the chain as presented,
// and the headers the site actually sends. Everything else on this page reads
// files, and a file is not what a visitor gets.
func (s *Server) handleTLSScan(w http.ResponseWriter, r *http.Request) error {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		return httpx.BadRequest("domain query parameter is required")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	scan := proxysvc.ScanTLS(ctx, domain, atoiDefault(r.URL.Query().Get("port"), 443))
	httpx.JSON(w, http.StatusOK, scan)
	return nil
}

func (s *Server) handleDomainDNS(w http.ResponseWriter, r *http.Request) error {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		return httpx.BadRequest("domain query parameter is required")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, proxysvc.CheckDomainDNS(ctx, domain))
	return nil
}

// handleCertIssue starts an issuance and hands back the job to watch.
//
// The validation is synchronous and the command is not. A bad email, an
// unknown DNS provider or a wildcard over an HTTP challenge are all mistakes
// the operator should hear about in the response to their own click — not a
// minute later as a job that failed. Everything past that point is an ACME
// exchange with a certificate authority, which is exactly the kind of wait
// that wants a console rather than a spinner.
func (s *Server) handleCertIssue(w http.ResponseWriter, r *http.Request) error {
	var req proxysvc.IssueRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	args, err := s.modules.proxy.IssueArgs(req)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	target := strings.Join(req.Domains, ", ")
	httpx.SetAudit(r, "certificates.issue", target,
		map[string]any{"method": req.Method, "staging": req.Staging, "streamed": true})

	title := "Issuing a certificate for " + target
	if req.Staging {
		title = "Test issuance for " + target
	}
	s.startJob(w, r, jobs.Spec{
		Kind: "certbot.issue", Title: title, Target: target, Timeout: 10 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		if req.Staging {
			out.Status("Using Let's Encrypt's staging authority: the certificate will not be trusted by browsers, and this run does not count against the rate limit.")
		}
		return certbotJob(ctx, out, args)
	})
	return nil
}

// certbotJob runs certbot and turns a non-zero exit into an error, so a failed
// order reads as a failed job rather than as a job that succeeded while
// printing a problem.
func certbotJob(ctx context.Context, out jobs.Emitter, args []string) error {
	environment, err := proxysvc.CertbotEnvironment()
	if err != nil {
		return err
	}
	code, err := out.RunEnv(ctx, environment, "certbot", args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("certbot exited %d — the last lines above say why", code)
	}
	return nil
}

type renewRequest struct {
	Name   string `json:"name"`
	DryRun bool   `json:"dryRun"`
	Force  bool   `json:"force"`
}

func (s *Server) handleCertRenew(w http.ResponseWriter, r *http.Request) error {
	var req renewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	args, err := s.modules.proxy.RenewArgs(req.Name, req.DryRun, req.Force)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	target := req.Name
	if target == "" {
		target = "every certificate due"
	}
	httpx.SetAudit(r, "certificates.renew", target,
		map[string]any{"dryRun": req.DryRun, "force": req.Force, "streamed": true})

	title := "Renewing " + target
	if req.DryRun {
		title = "Dry run: renewing " + target
	}
	s.startJob(w, r, jobs.Spec{
		Kind: "certbot.renew", Title: title, Target: target, Timeout: 10 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		if req.DryRun {
			out.Status("A dry run performs the whole exchange against the staging authority and changes nothing on disk.")
		}
		if req.Force {
			out.Status("Forced renewal spends one of the five duplicate certificates Let's Encrypt allows per week.")
		}
		return certbotJob(ctx, out, args)
	})
	return nil
}

type revokeRequest struct {
	Name string `json:"name"`
}

// handleCertRevoke is irreversible in the way that matters: the authority
// records the certificate as untrusted and there is no undo, so every client
// holding it starts refusing the site.
func (s *Server) handleCertRevoke(w http.ResponseWriter, r *http.Request) error {
	var req revokeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := httpx.RequireTypedConfirmation(w, r, "revoke "+req.Name); err != nil {
		return err
	}
	args, err := s.modules.proxy.RevokeArgs(req.Name)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.revoke", req.Name, map[string]any{"streamed": true})
	s.startJob(w, r, jobs.Spec{
		Kind: "certbot.revoke", Title: "Revoking " + req.Name, Target: req.Name,
		Timeout: 5 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		return certbotJob(ctx, out, args)
	})
	return nil
}

func (s *Server) handleStreamList(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.proxy.Streams(r.Context()))
	return nil
}

type streamRequest struct {
	Spec      proxysvc.StreamSpec `json:"spec"`
	Reload    bool                `json:"reload"`
	Overwrite bool                `json:"overwrite"`
}

func (s *Server) handleStreamPreview(w http.ResponseWriter, r *http.Request) error {
	var req streamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	content, err := proxysvc.RenderStream(&req.Spec)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"content": content})
	return nil
}

func (s *Server) handleStreamApply(w http.ResponseWriter, r *http.Request) error {
	var req streamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.ApplyStream(ctx, &req.Spec, req.Reload, req.Overwrite)
	if err != nil {
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		}
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, map[string]any{
		"listen": req.Spec.Listen, "protocol": req.Spec.Protocol, "upstream": req.Spec.Upstream,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleStreamDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if err := s.modules.proxy.DeleteStream(r.Context(), name); err != nil {
		return httpx.BadRequest("%v", err)
	}
	reload, reloadErr := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
	httpx.SetAudit(r, "proxy.stream.delete", name, map[string]any{"reloaded": reloadErr == nil})
	out := map[string]any{"name": name, "reload": reload}
	if reloadErr != nil {
		out["reloadError"] = reloadErr.Error()
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleAuthFileList(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.proxy.ListAuthFiles())
	return nil
}

type authUserRequest struct {
	File     string `json:"file"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// handleAuthUserSet adds or replaces one entry. The password is hashed in
// process and never becomes an argument to anything — /proc/*/cmdline is
// world-readable, which is the same reason dbx keeps database passwords out of
// argv.
func (s *Server) handleAuthUserSet(w http.ResponseWriter, r *http.Request) error {
	var req authUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	file, err := s.modules.proxy.SetAuthUser(req.File, req.User, req.Password)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	// The password is deliberately absent from the audit detail.
	httpx.SetAudit(r, "proxy.auth.set", req.File, map[string]any{"user": req.User})
	httpx.JSON(w, http.StatusOK, file)
	return nil
}

func (s *Server) handleAuthUserRemove(w http.ResponseWriter, r *http.Request) error {
	file, user := chi.URLParam(r, "file"), chi.URLParam(r, "user")
	updated, err := s.modules.proxy.RemoveAuthUser(file, user)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "proxy.auth.remove", file, map[string]any{"user": user})
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) handleAuthFileDelete(w http.ResponseWriter, r *http.Request) error {
	file := chi.URLParam(r, "file")
	if err := s.modules.proxy.DeleteAuthFile(file); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "proxy.auth.delete", file, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleDNSProviders(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.proxy.ListDNSProviders())
	return nil
}

type dnsCredentialsRequest struct {
	Provider    string `json:"provider"`
	Credentials string `json:"credentials"`
}

// handleDNSCredentials stores an API token for a DNS plugin. The token is a
// credential for somebody's whole DNS zone, so it is written 0600 into
// certbot's own tree and never read back out — the UI shows whether one exists,
// not what it is.
func (s *Server) handleDNSCredentials(w http.ResponseWriter, r *http.Request) error {
	var req dnsCredentialsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	path, err := proxysvc.WriteDNSCredentials(req.Provider, req.Credentials)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.dns.credentials", req.Provider, map[string]any{"file": path})
	httpx.JSON(w, http.StatusOK, map[string]any{"provider": req.Provider, "saved": true})
	return nil
}

type certImportRequest struct {
	Name        string `json:"name"`
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

// handleCertImport takes a certificate somebody bought or was given.
//
// The key is checked against the certificate before either is written: a
// mismatched pair is accepted by every text editor and refused by nginx at
// reload, and finding that out on a live server is the expensive way.
func (s *Server) handleCertImport(w http.ResponseWriter, r *http.Request) error {
	var req certImportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := proxysvc.ImportCertificate(req.Name, req.Certificate, req.Key)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.import", req.Name,
		map[string]any{"domains": res.Cert.Domains, "expires": res.Cert.NotAfter})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
