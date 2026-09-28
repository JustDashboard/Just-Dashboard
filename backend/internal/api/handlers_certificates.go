package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
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
		// A handshake with each site that names a certificate: loopback
		// dials only, and only the page's reload offer needs them.
		r.Method(http.MethodGet, "/served", s.handle(s.handleCertServed))
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

// handleCertServed answers which certificate each enabled nginx site naming
// ?path= serves, asked of nginx over a handshake. ?settle=1 asks again for a
// few seconds while a site serves another: what to read right after a reload,
// which nginx finishes a moment after the signal.
func (s *Server) handleCertServed(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path is required")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	served, err := s.modules.proxy.ServedCertificates(ctx, path, r.URL.Query().Get("settle") == "1")
	if errors.Is(err, proxysvc.ErrCertificateNotListed) {
		return httpx.Err(http.StatusNotFound, "not_found", "No listed certificate has that path.")
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, served)
	return nil
}

func (s *Server) handleCertList(w http.ResponseWriter, r *http.Request) error {
	certs, err := s.modules.proxy.CertificateInventory(r.Context())
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
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	args, err := s.modules.proxy.IssueArgs(ctx, req)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	// Checked with the rest of the request and saved by the job, first
	// thing: a refused request, or one that finds certbot busy, leaves no
	// token on disk.
	var credentials *proxysvc.DNSCredentials
	if req.Method == "dns" && strings.TrimSpace(req.Credentials) != "" {
		checked, err := proxysvc.CheckDNSCredentials(req.DNSProvider, req.Credentials)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		credentials = &checked
	}
	// IssueArgs forces the renewal only over a test certificate for exactly
	// these names, so the argv says whether this issuance replaces one.
	replacing := slices.Contains(args, "--force-renewal")
	authority := proxysvc.CertbotAuthorityInUse()
	target := strings.Join(req.Domains, ", ")
	httpx.SetAudit(r, "certificates.issue", target,
		map[string]any{"method": req.Method, "testRun": req.Staging, "replacesTestCertificate": replacing,
			"credentialsSaved": credentials != nil, "streamed": true})

	title := "Issuing a certificate for " + target
	switch {
	case req.Staging:
		title = "Test issuance for " + target
	case replacing:
		title = "Replacing the test certificate for " + target
	}
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.issue", Title: title, Target: target, Timeout: 10 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		if credentials != nil {
			path, err := credentials.Save()
			if err != nil {
				return fmt.Errorf("the %s credentials could not be saved: %w", credentials.Provider().Name, err)
			}
			out.Status("Saved the %s credentials to %s, readable by root only.", credentials.Provider().Name, path)
		}
		kept := ""
		switch {
		case req.Staging:
			// certbot's --dry-run asks its staging authority only when no
			// other server is named; with one configured it rehearses there.
			if authority.Directory != "" {
				out.Status("A test run: certbot goes through the whole exchange with %s and saves nothing — no certificate is written.", authority.Directory)
			} else {
				out.Status("A test run: certbot goes through the whole exchange with Let's Encrypt's staging authority and saves nothing — no certificate is written, and nothing counts against the real rate limits.")
			}
		case replacing:
			out.Status("These names have a test certificate from a staging authority. certbot replaces it with a real one rather than keeping it until it is due.")
		default:
			if authority.Staging {
				out.Status("JD_ACME_DIRECTORY names a staging authority: the certificate it signs is a test one, and browsers refuse it.")
			}
			kept = "certbot did not issue a new certificate: the one these names already have is not due for renewal yet, so it was kept as it is."
		}
		if err := certbotJob(ctx, out, args, kept); err != nil {
			return err
		}
		if replacing {
			// Whether nginx serves the new files is asked of nginx: certonly
			// reloads nothing itself, but it runs certbot's deploy hooks on a
			// renewed lineage, and one that reloads nginx is how most hosts
			// keep certonly certificates served.
			served, err := s.modules.proxy.ServedLineage(ctx, req.Domains, true)
			if err != nil {
				out.Status("The real certificate replaced the test one on disk. Which certificate nginx serves could not be checked: %v.", err)
			} else {
				out.Status("%s", proxysvc.ReplacementServed(served))
			}
		}
		return nil
	})
}

// startCertbotJob starts a certbot job unless another is running. A second
// certbot fails on the lock the first holds, and the page's console would
// swap the running job for the one that is about to fail.
func (s *Server) startCertbotJob(w http.ResponseWriter, r *http.Request, spec jobs.Spec, run jobs.Runner) error {
	spec.StartedBy = httpx.MustPrincipal(r).Username()
	job, ok := s.modules.jobs.StartExclusive("certbot.", spec, run)
	if !ok {
		return httpx.Err(http.StatusConflict, "certbot_busy",
			fmt.Sprintf("certbot is already running (%s, started by %s). Wait for it to finish.", job.Title, job.StartedBy))
	}
	httpx.JSON(w, http.StatusAccepted, job)
	return nil
}

// certbotJob runs certbot and turns a non-zero exit into an error, so a failed
// order reads as a failed job rather than as a job that succeeded while
// printing a problem.
//
// kept, when set, is what to say if certbot exits 0 having replaced nothing.
// It does that when a certificate is not due — "no action taken" on an
// issuance, "not due for renewal" on a renewal — and the job read as a
// success that had done what was asked. The lineages' serials, compared
// before and after, tell the two apart whatever certbot's wording.
func certbotJob(ctx context.Context, out jobs.Emitter, args []string, kept string) error {
	environment, err := proxysvc.CertbotEnvironment()
	if err != nil {
		return err
	}
	cmd, err := proxysvc.CertbotCommand(ctx, environment, args...)
	if err != nil {
		return err
	}
	before, beforeErr := proxysvc.CertbotSerials()
	code, err := out.RunCmd(cmd, append([]string{"certbot"}, args...))
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("certbot exited %d — the last lines above say why", code)
	}
	if kept != "" && beforeErr == nil {
		if after, err := proxysvc.CertbotSerials(); err == nil && len(before) > 0 && maps.Equal(before, after) {
			out.Status("%s", kept)
		}
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
	kept := ""
	switch {
	case req.DryRun || req.Force:
	case req.Name == "":
		kept = "Nothing was due for renewal, so certbot changed nothing."
	default:
		kept = req.Name + " is not due for renewal yet, so certbot left it as it was."
	}
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.renew", Title: title, Target: target, Timeout: 10 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		if req.DryRun {
			out.Status("A dry run performs the whole exchange against the staging authority and changes nothing on disk.")
		}
		if req.Force {
			out.Status("Forced renewal spends one of the five duplicate certificates Let's Encrypt allows per week.")
		}
		return certbotJob(ctx, out, args, kept)
	})
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
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.revoke", Title: "Revoking " + req.Name, Target: req.Name,
		Timeout: 5 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		return certbotJob(ctx, out, args, "")
	})
}

func (s *Server) handleDNSProviders(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	providers, err := s.modules.proxy.ListDNSProviders(ctx)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "certbot_plugins", err.Error()).Retry()
	}
	httpx.JSON(w, http.StatusOK, providers)
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
	// Replace overwrites an import of the same name, keeping the previous
	// pair beside it as .bak. Without it an existing name is a 409.
	Replace bool `json:"replace"`
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
	res, err := proxysvc.ImportCertificate(req.Name, req.Certificate, req.Key, req.Replace)
	var exists *proxysvc.ExistingImportError
	if errors.As(err, &exists) {
		return httpx.Err(http.StatusConflict, "certificate_exists", err.Error())
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.import", res.Name,
		map[string]any{"domains": res.Cert.Domains, "expires": res.Cert.NotAfter, "replaced": res.Replaced})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
