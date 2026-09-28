package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
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
	s.mountPrivateCertificateRoutes(r)
	// What a listed file holds, read from disk: the chain, and of the key
	// only its path, mode and whether it matches.
	r.Method(http.MethodGet, "/detail", s.handle(s.handleCertDetail))
	// Decodes pasted text in memory; nothing is fetched or kept.
	r.Method(http.MethodPost, "/decode", s.handle(s.handleCertDecode))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// A handshake with each site that names a certificate: loopback
		// dials only, and only the page's reload offer needs them.
		r.Method(http.MethodGet, "/served", s.handle(s.handleCertServed))
		// The timeline reads the audit trail and the renewal record, both
		// administrators' reading.
		r.Method(http.MethodGet, "/history", s.handle(s.handleCertHistory))
		r.Method(http.MethodPost, "/issue", s.handle(s.handleCertIssue))
		r.Method(http.MethodPost, "/import", s.handle(s.handleCertImport))
		// Writes nothing, but it may fetch an intermediate from the address
		// a certificate names, which is why it sits behind admin too.
		r.Method(http.MethodPost, "/import/inspect", s.handle(s.handleCertImportInspect))
		r.Method(http.MethodPost, "/dns-credentials", s.handle(s.handleDNSCredentials))
		// A DNS-01 dry run against a domain the caller names: an outbound
		// exchange with the authority and a write to that zone's DNS.
		r.Method(http.MethodPost, "/dns-credentials/{provider}/test", s.handle(s.handleDNSCredentialsTest))
		// Installing a package runs its maintainer's scripts as root, the
		// same tier as /packages/install.
		r.Method(http.MethodPost, "/certbot/install", s.handle(s.handleCertbotInstall))
		r.Method(http.MethodPost, "/renew", s.handle(s.handleCertRenew))
		// The renewal schedule's own record: the timer's service journal,
		// or certbot's log on a host that renews from cron, which holds
		// the ACME exchange itself.
		r.Method(http.MethodGet, "/renewal/log", s.handle(s.handleRenewalLog))
		r.Method(http.MethodPost, "/renewal/run", s.handle(s.handleRenewalRun))
		r.Method(http.MethodPut, "/renewal-hook", s.handle(s.handleRenewalHookInstall))
		s.mountCertTransparencyRoutes(r)
		// Caddy's release copies whose domain no route and no release names:
		// what the prune below would remove.
		r.Method(http.MethodGet, "/evidence", s.handle(s.handleCertEvidence))
		s.destructive(r, func(r chi.Router) {
			// Removing the hook stops nginx reloading after renewals; the
			// same switch puts it back, so no phrase.
			r.Method(http.MethodDelete, "/renewal-hook", s.handle(s.handleRenewalHookRemove))
			// Removing a saved DNS token is recoverable — paste it again
			// — so it takes the ordinary confirmation and no phrase.
			r.Method(http.MethodDelete, "/dns-credentials/{provider}", s.handle(s.handleDNSCredentialsRemove))
			// Removing an orphaned release copy loses only the record of a
			// certificate Caddy has since renewed or let go.
			r.Method(http.MethodDelete, "/evidence", s.handle(s.handleCertEvidencePrune))
			// Revocation cannot be undone: the authority publishes that
			// the certificate is no longer to be trusted, and every
			// client holding it starts refusing the site.
			r.Method(http.MethodPost, "/revoke", s.handle(s.handleCertRevoke))
			// Deleting without revoking leaves the certificate valid where
			// a copy of it is, and removes only this host's files and its
			// renewal: the ordinary confirmation, no phrase.
			r.Method(http.MethodPost, "/delete", s.handle(s.handleCertDelete))
			r.Method(http.MethodDelete, "/imported/{name}", s.handle(s.handleImportedDelete))
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

// handleCertDetail answers with the certificate file at ?path=, which must be
// one the inventory lists.
func (s *Server) handleCertDetail(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path is required")
	}
	detail, err := s.modules.proxy.CertificateDetail(path)
	if errors.Is(err, proxysvc.ErrCertificateNotListed) {
		return httpx.Err(http.StatusForbidden, "not_listed", "Only a certificate the list shows can be read.")
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// handleCertHistory is the timeline of the certificate named ?name=: the
// versions certbot archived, what was done to it here, and the renewal runs
// that failed it.
func (s *Server) handleCertHistory(w http.ResponseWriter, r *http.Request) error {
	name := r.URL.Query().Get("name")
	if name == "" {
		return httpx.BadRequest("name is required")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	events := proxysvc.ArchivedVersions(name)
	entries, _, err := s.Audit.List(ctx, audit.Filter{Action: "certificates.", Limit: 1000})
	if err != nil {
		return httpx.Internal(err)
	}
	for _, e := range entries {
		// Batch actions name every certificate they touched, joined.
		if !slices.Contains(strings.Split(e.Target, ", "), name) {
			continue
		}
		title := e.Action
		if !e.Success {
			title += " (failed)"
		}
		events = append(events, proxysvc.CertificateEvent{
			Time: e.TS, Kind: "audit", Title: title, Detail: "by " + e.Username,
		})
	}
	var logNote string
	if proxysvc.CertbotLineageExists(name) {
		log, err := s.modules.proxy.RenewalLog(ctx)
		if err != nil {
			logNote = "Renewal failures could not be read: " + err.Error()
		}
		events = append(events, proxysvc.RenewalFailuresFor(log, name)...)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.After(events[j].Time) })
	if events == nil {
		events = []proxysvc.CertificateEvent{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": events, "note": logNote})
	return nil
}

// handleCertDecode decodes a pasted PEM certificate chain or signing request.
func (s *Server) handleCertDecode(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		PEM string `json:"pem"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, proxysvc.MaxDecodeBytes+4<<10)
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if len(req.PEM) > proxysvc.MaxDecodeBytes {
		return httpx.Err(http.StatusRequestEntityTooLarge, "too_large", "Paste at most 256 KiB.")
	}
	decoded, err := proxysvc.DecodePEM(req.PEM)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, decoded)
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
	state.FillInstalls(ctx, s.modules.updates.Manager())
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
		map[string]any{"method": req.Method, "testRun": req.Staging, "dryRun": req.DryRun, "replacesTestCertificate": replacing,
			"credentialsSaved": credentials != nil, "streamed": true})
	s.rememberCertbotEmail(r.Context(), req.Email)

	title := "Issuing a certificate for " + target
	switch {
	case req.DryRun:
		title = "Dry run for " + target
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
		case req.Staging || req.DryRun:
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
		changed, err := certbotJob(ctx, out, args, kept)
		if err != nil {
			return err
		}
		if err := s.reloadAfterRenewal(ctx, out, changed); err != nil {
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
// printing a problem. It answers with the lineages the run renewed or issued.
//
// kept, when set, is what to say if certbot exits 0 having replaced nothing.
// It does that when a certificate is not due — "no action taken" on an
// issuance, "not due for renewal" on a renewal — and the job read as a
// success that had done what was asked. The lineages' serials, compared
// before and after, tell the two apart whatever certbot's wording.
func certbotJob(ctx context.Context, out jobs.Emitter, args []string, kept string) ([]string, error) {
	environment, err := proxysvc.CertbotEnvironment()
	if err != nil {
		return nil, err
	}
	cmd, err := proxysvc.CertbotCommand(ctx, environment, args...)
	if err != nil {
		return nil, err
	}
	before, beforeErr := proxysvc.CertbotSerials()
	code, err := out.RunCmd(cmd, append([]string{"certbot"}, args...))
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("certbot exited %d — the last lines above say why", code)
	}
	if beforeErr != nil {
		return nil, nil
	}
	after, err := proxysvc.CertbotSerials()
	if err != nil {
		return nil, nil
	}
	if kept != "" && len(before) > 0 && maps.Equal(before, after) {
		out.Status("%s", kept)
	}
	return proxysvc.ChangedLineages(before, after), nil
}

// reloadAfterRenewal reloads nginx once a run has renewed a certificate an
// enabled site serves. nginx keeps the certificate it read at its last
// reload, and certbot reloads it only for a lineage its nginx plugin
// installed — never for a brand-new one, whatever hooks are installed. The
// reload tests the configuration first, and a failing test is the job's
// failure: the certificate is renewed, but no browser gets it.
func (s *Server) reloadAfterRenewal(ctx context.Context, out jobs.Emitter, changed []string) error {
	sites := s.modules.proxy.SitesServingLineages(changed)
	if len(sites) == 0 {
		return nil
	}
	res, err := s.modules.proxy.Reload(ctx, proxysvc.KindNginx)
	if err != nil {
		why := err.Error()
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			why = "its configuration test failed"
			if res != nil && res.Validation != nil {
				for _, line := range strings.Split(strings.TrimSpace(res.Validation.Output), "\n") {
					out.Line("stderr", line)
				}
			}
		}
		return proxysvc.NotReloadedFor(sites, why)
	}
	out.Status("%s", proxysvc.ReloadedFor(sites))
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
		changed, err := certbotJob(ctx, out, args, kept)
		if err != nil {
			return err
		}
		return s.reloadAfterRenewal(ctx, out, changed)
	})
}

// handleRenewalRun starts the service the renewal timer starts, now, and
// reports its run. It is the timer's renewal rather than one of this page's:
// systemd records how it went, so the page's reading of the schedule
// changes with it — what an operator wants after fixing why it failed.
func (s *Server) handleRenewalRun(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	service, err := proxysvc.RenewalServiceFor(ctx)
	if errors.Is(err, proxysvc.ErrRenewalNotSystemd) {
		return httpx.Err(http.StatusConflict, "renewal_not_systemd",
			"certbot renews from cron here, so there is no service to start. Use Renew all due instead.")
	}
	if err != nil {
		return httpx.Err(http.StatusConflict, "renewal_not_scheduled", err.Error())
	}
	httpx.SetAudit(r, "certificates.renewal.run", service, map[string]any{"streamed": true})
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.renewal", Title: "Running " + service, Target: service, Timeout: 15 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		before, _ := proxysvc.CertbotSerials()
		previous, running, err := proxysvc.RenewalInvocation(ctx, service)
		if err != nil {
			return err
		}
		if running {
			// Starting a run in progress waits for it: that run is ours.
			previous = ""
			out.Status("%s is already running; waiting for it to finish.", service)
		}
		out.Status("%s is the renewal the timer runs: certbot renews every certificate that is due, and systemd records how it went.", service)
		started := make(chan renewalStart, 1)
		go func() {
			code, err := out.RunCmd(proxysvc.StartRenewalCommand(ctx, service), []string{"systemctl", "start", service})
			started <- renewalStart{code, err}
		}()
		var start renewalStart
		select {
		case start = <-started:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			return s.stopRenewalRun(ctx, out, service, before, started)
		}
		code, err := start.code, start.err
		if err != nil {
			return err
		}
		run, failures, reason, readErr := proxysvc.RenewalRunAfter(ctx, service, previous)
		if readErr == nil {
			for _, line := range run.Lines {
				if !line.Systemd {
					out.Line("stdout", line.Text)
				}
			}
		}
		changed := []string{}
		if after, err := proxysvc.CertbotSerials(); err == nil {
			changed = proxysvc.ChangedLineages(before, after)
		}
		if len(changed) > 0 {
			out.Status("Renewed %s.", strings.Join(changed, ", "))
		}
		failed := code != 0 || (run != nil && run.Result == "failed")
		if !failed {
			if len(changed) == 0 {
				out.Status("Nothing was due for renewal, so certbot changed nothing.")
			}
			return s.reloadAfterRenewal(ctx, out, changed)
		}
		// What did renew still has to reach nginx.
		if err := s.reloadAfterRenewal(ctx, out, changed); err != nil {
			out.Status("%s", err.Error())
		}
		return renewalRunFailure(service, code, failures, reason, readErr)
	})
}

// renewalStart is how the `systemctl start` that waits on a renewal run
// ended.
type renewalStart struct {
	code int
	err  error
}

// stopRenewalRun stops the run of the renewal service a stopped or timed-out
// job started. The systemctl that waits on the run is only a client:
// killing it leaves systemd's start and certbot running. The job holds
// certbot's lock until the service has stopped, and a certificate certbot
// saved before it stopped is renewed all the same, so nginx is reloaded for
// it as after any run.
func (s *Server) stopRenewalRun(ctx context.Context, out jobs.Emitter, service string, before map[string]string, started <-chan renewalStart) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	out.Status("Stopping %s: systemd ends certbot's run.", service)
	output, err := proxysvc.StopRenewalCommand(stopCtx, service).CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			out.Line("stderr", line)
		}
	}
	if err != nil {
		out.Status("%s could not be stopped (%v), so it keeps running and the page reads it as running until it ends.", service, err)
		return ctx.Err()
	}
	// The start waiting on the run returns once the run is over.
	select {
	case <-started:
	case <-stopCtx.Done():
	}
	out.Status("Stopped %s.", service)
	if after, err := proxysvc.CertbotSerials(); err == nil {
		if changed := proxysvc.ChangedLineages(before, after); len(changed) > 0 {
			out.Status("Renewed %s before it stopped.", strings.Join(changed, ", "))
			if err := s.reloadAfterRenewal(stopCtx, out, changed); err != nil {
				out.Status("%s", err.Error())
			}
		}
	}
	return ctx.Err()
}

// renewalRunFailure is a failed run of the renewal service, in the words its
// journal gives.
func renewalRunFailure(service string, code int, failures []proxysvc.RenewalFailure, reason string, readErr error) error {
	switch {
	case len(failures) > 0:
		parts := make([]string, 0, len(failures))
		for _, f := range failures {
			parts = append(parts, f.Lineage+": "+f.Reason)
		}
		return fmt.Errorf("%s failed to renew %s", service, strings.Join(parts, "; "))
	case reason != "":
		return fmt.Errorf("%s failed: %s", service, reason)
	case readErr != nil && code != 0:
		return fmt.Errorf("systemctl start %s exited %d — the lines above say why", service, code)
	}
	return fmt.Errorf("%s failed (exit %d)", service, code)
}

// handleRenewalLog answers with the renewal schedule's recent runs.
func (s *Server) handleRenewalLog(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	log, err := s.modules.proxy.RenewalLog(ctx)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "renewal_log", err.Error()).Retry()
	}
	httpx.JSON(w, http.StatusOK, log)
	return nil
}

// handleRenewalHookInstall installs the deploy hook that reloads nginx after
// every renewal, or restores it over a copy changed by hand.
func (s *Server) handleRenewalHookInstall(w http.ResponseWriter, r *http.Request) error {
	before, _ := proxysvc.RenewalHookStatus()
	hook, err := proxysvc.InstallRenewalHook()
	if errors.Is(err, proxysvc.ErrRenewalHookForeign) {
		return httpx.Err(http.StatusConflict, "hook_foreign",
			hook.Path+" is not this dashboard's file, so it is left as it is.")
	}
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "hook_write_failed", "The hook could not be written: "+err.Error())
	}
	httpx.SetAudit(r, "certificates.renewal-hook.install", hook.Path, map[string]any{"was": before.State})
	httpx.JSON(w, http.StatusOK, hook)
	return nil
}

func (s *Server) handleRenewalHookRemove(w http.ResponseWriter, r *http.Request) error {
	before, _ := proxysvc.RenewalHookStatus()
	hook, err := proxysvc.RemoveRenewalHook()
	switch {
	case errors.Is(err, proxysvc.ErrRenewalHookForeign):
		return httpx.Err(http.StatusConflict, "hook_foreign",
			hook.Path+" is not this dashboard's file, so it is left as it is.")
	case errors.Is(err, proxysvc.ErrRenewalHookMissing):
		return httpx.Err(http.StatusNotFound, "not_found", "The hook is not installed.")
	case err != nil:
		return httpx.Err(http.StatusInternalServerError, "hook_write_failed", "The hook could not be removed: "+err.Error())
	}
	httpx.SetAudit(r, "certificates.renewal-hook.remove", hook.Path, map[string]any{"was": before.State})
	httpx.JSON(w, http.StatusOK, hook)
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
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.revoke", Title: "Revoking " + req.Name, Target: req.Name,
		Timeout: 5 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		_, err := certbotJob(ctx, out, args, "")
		return err
	})
}

type certDeleteRequest struct {
	// Names are certbot lineages, deleted one after another in one job:
	// certbot holds one lock for all of its work.
	Names []string `json:"names"`
	// Force deletes a lineage an enabled site serves.
	Force bool `json:"force"`
}

// handleCertDelete runs certbot delete over lineages, which removes their
// files and renewal configuration and revokes nothing. A lineage an enabled
// site serves is refused before the job starts unless the operator
// acknowledged it.
func (s *Server) handleCertDelete(w http.ResponseWriter, r *http.Request) error {
	var req certDeleteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if len(req.Names) == 0 {
		return httpx.BadRequest("names is required")
	}
	runs := make([][]string, 0, len(req.Names))
	var served []string
	for _, name := range req.Names {
		args, err := s.modules.proxy.DeleteArgs(name)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		if err := s.modules.proxy.LineageInUse(name, req.Force); err != nil {
			return certificateInUse(err)
		}
		served = append(served, s.modules.proxy.SitesServingLineages([]string{name})...)
		runs = append(runs, args)
	}
	target := strings.Join(req.Names, ", ")
	httpx.SetAudit(r, "certificates.delete", target,
		map[string]any{"force": req.Force, "servedBy": served, "streamed": true})
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.delete", Title: "Deleting " + target, Target: target, Timeout: 5 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("certbot deletes the files and the renewal configuration and revokes nothing: a copy of the certificate elsewhere stays valid until it expires.")
		for i, args := range runs {
			if _, err := certbotJob(ctx, out, args, ""); err != nil {
				return fmt.Errorf("%s: %w", req.Names[i], err)
			}
			out.Status("Deleted %s.", req.Names[i])
		}
		if len(served) > 0 {
			out.Status("nginx keeps serving what it read at its last reload. Point %s at another certificate before the next reload, which fails until then.",
				strings.Join(served, ", "))
		}
		return nil
	})
}

// handleImportedDelete removes an imported certificate and its key.
func (s *Server) handleImportedDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	force := r.URL.Query().Get("force") == "1"
	err := s.modules.proxy.DeleteImportedCertificate(name, force)
	var inUse *proxysvc.CertificateInUseError
	switch {
	case errors.As(err, &inUse):
		return certificateInUse(err)
	case errors.Is(err, proxysvc.ErrImportNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case err != nil:
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.imported.delete", name, map[string]any{"force": force})
	httpx.NoContent(w)
	return nil
}

// certificateInUse is the refusal to delete a certificate a site serves; its
// message names the sites.
func certificateInUse(err error) error {
	return httpx.Err(http.StatusConflict, "in_use", err.Error())
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

type dnsTestRequest struct {
	Domain string `json:"domain"`
}

// handleDNSCredentialsTest rehearses a DNS-01 challenge through a provider's
// saved credentials: certbot's --dry-run, streamed as a job, nothing saved.
func (s *Server) handleDNSCredentialsTest(w http.ResponseWriter, r *http.Request) error {
	var req dnsTestRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	provider := chi.URLParam(r, "provider")
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	args, err := proxysvc.DNSTestArgs(ctx, provider, req.Domain)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	name := provider
	if p, ok := proxysvc.DNSProviderFor(provider); ok {
		name = p.Name
	}
	httpx.SetAudit(r, "certificates.dns.credentials.test", provider, map[string]any{"domain": req.Domain, "streamed": true})
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.dns-test", Title: "Testing the " + name + " credentials on " + req.Domain,
		Target: req.Domain, Timeout: 10 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("A dry run: certbot writes the challenge record through %s, has the authority check it, and saves no certificate.", name)
		_, err := certbotJob(ctx, out, args, "")
		if err == nil {
			out.Status("The %s credentials work for %s.", name, req.Domain)
		}
		return err
	})
}

type certbotInstallRequest struct {
	// Plugin is the certbot plugin to install, or empty for certbot itself.
	Plugin string `json:"plugin"`
}

// handleCertbotInstall installs certbot, or one of its plugins, with the
// host's package manager, then asks certbot whether it now has it: a package
// can install beside a certbot that never loads it, and the job says so
// rather than succeeding.
func (s *Server) handleCertbotInstall(w http.ResponseWriter, r *http.Request) error {
	var req certbotInstallRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	manager := s.modules.updates.Manager()
	pkg, err := proxysvc.CertbotPackage(ctx, manager, req.Plugin)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	name, args, env, err := s.modules.updates.InstallCommand([]string{pkg})
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	what := "certbot"
	if req.Plugin != "" {
		what = "certbot's " + req.Plugin + " plugin"
	}
	httpx.SetAudit(r, "certificates.certbot.install", pkg, map[string]any{"manager": name, "plugin": req.Plugin})
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.install", Title: "Installing " + what, Target: pkg, Timeout: time.Hour,
	}, func(ctx context.Context, out jobs.Emitter) error {
		defer s.modules.updates.Invalidate()
		code, err := out.RunEnv(ctx, env, name, args...)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("%s exited %d — the last lines above say why", name, code)
		}
		ready, err := proxysvc.CertbotInstalled(ctx, req.Plugin)
		if err != nil {
			return err
		}
		out.Status("%s", ready)
		return nil
	})
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

func (s *Server) handleCertEvidence(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	released, err := s.modules.deployRuns.ReleasedHostnames(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	orphans, err := s.modules.proxy.OrphanedCertificateEvidence(ctx, released)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, orphans)
	return nil
}

// handleCertEvidencePrune removes the release copies the operator confirmed,
// each checked again against the routes and releases as they are now.
func (s *Server) handleCertEvidencePrune(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Names []string `json:"names"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if len(req.Names) == 0 {
		return httpx.BadRequest("names is required")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	released, err := s.modules.deployRuns.ReleasedHostnames(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	result, err := s.modules.proxy.PruneCertificateEvidence(ctx, released, req.Names)
	httpx.SetAudit(r, "certificates.evidence.prune", strings.Join(result.Removed, ","), map[string]any{
		"removed": result.Removed, "kept": result.Kept,
	})
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// handleCertImport takes a certificate somebody bought or was given.
//
// The key is checked against the certificate before either is written: a
// mismatched pair is accepted by every text editor and refused by nginx at
// reload, and finding that out on a live server is the expensive way.
func (s *Server) handleCertImport(w http.ResponseWriter, r *http.Request) error {
	var req proxysvc.ImportInput
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := s.modules.proxy.Import(ctx, req)
	var exists *proxysvc.ExistingImportError
	if errors.As(err, &exists) {
		return httpx.Err(http.StatusConflict, "certificate_exists", err.Error())
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.import", res.Name,
		map[string]any{"domains": res.Cert.Domains, "expires": res.Cert.NotAfter, "replaced": res.Replaced,
			"pfx": len(req.PFX) > 0, "fetchedIssuer": req.FetchIssuer})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// handleCertImportInspect answers what an import would do — the names, the
// chain, the expiry, and the import it would replace — without writing.
func (s *Server) handleCertImportInspect(w http.ResponseWriter, r *http.Request) error {
	var req proxysvc.ImportInput
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := s.modules.proxy.InspectImport(ctx, req)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
