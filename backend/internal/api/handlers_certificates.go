package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountCertificateRoutes is the certificates on this host and certbot's
// lifecycle for them: issue, import, renew, revoke, and the DNS credentials a
// DNS-01 challenge needs.
func (s *Server) mountCertificateRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handleCertList))
	r.Method(http.MethodGet, "/certbot", s.handle(s.handleCertbot))
	r.Method(http.MethodGet, "/dns-providers", s.handle(s.handleDNSProviders))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
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
}

func (s *Server) handleCertList(w http.ResponseWriter, r *http.Request) error {
	certs, err := s.modules.proxy.ListCertificates(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, certs)
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

func (s *Server) handleDNSCredentialsRemove(w http.ResponseWriter, r *http.Request) error {
	provider := chi.URLParam(r, "provider")
	if err := proxysvc.RemoveDNSCredentials(provider); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.dns.credentials.remove", provider, nil)
	httpx.NoContent(w)
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
