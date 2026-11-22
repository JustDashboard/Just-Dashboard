package appdomain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// IssueOptions are how an issuance asked for by a person runs.
type IssueOptions struct {
	// DryRun runs the whole order against the staging authority, saving
	// nothing, as the Certificates page's test run does.
	DryRun bool
	// Force renews a wildcard that is not due yet.
	Force bool
}

// certificate is the wildcard's state for the view: the lineage on disk, or
// issuing while this process orders it, or failed with the last error.
func (s *Service) certificate(ctx context.Context, st State) *Certificate {
	c := &Certificate{Name: proxysvc.AppDomainLineage(st.Handle), State: "missing"}
	s.mu.Lock()
	issuing, jobID := s.issuing, s.jobID
	s.mu.Unlock()
	c.JobID = strPtr(jobID)
	if s.certs != nil && st.Zone != "" {
		lineage := s.certs.AppDomainCertificate(ctx, st.Handle, st.Zone)
		c.State = lineage.State
		if lineage.ExpiresAt > 0 {
			c.ExpiresAt = ptr(lineage.ExpiresAt)
		}
		c.Error = strPtr(lineage.Error)
	}
	switch {
	case issuing:
		c.State, c.Error = "issuing", nil
	case st.CertificateError != "" && c.State != "valid":
		c.State, c.Error = "failed", strPtr(st.CertificateError)
	}
	return c
}

// WildcardCovers names the held handle's wildcard and reports whether it
// covers the handle's names now: valid, or valid and due for renewal. It is
// the state the Settings card shows, so a deployment page and the card never
// disagree about it.
func (s *Service) WildcardCovers(ctx context.Context) (string, bool) {
	if !s.Enabled() || s.certs == nil {
		return "", false
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil || !st.held() {
		return "", false
	}
	current := s.certs.AppDomainCertificate(ctx, st.Handle, st.Zone)
	return current.Name, current.State == "valid" || current.State == "expiring"
}

// IssueCertificate starts issuing or renewing the wildcard now, in the
// background, and answers the view with the certificate issuing.
func (s *Service) IssueCertificate(ctx context.Context, opts IssueOptions, by string) (View, error) {
	if !s.Enabled() {
		return View{}, ErrDisabled
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return View{}, err
	}
	if !st.held() {
		return View{}, ErrNoDomain
	}
	if err := s.ensureHook(ctx); err != nil {
		return View{}, err
	}
	s.startIssue(ctx, opts, by)
	return s.View(ctx)
}

// startIssue runs an issuance as a job, unless one is running.
func (s *Service) startIssue(ctx context.Context, opts IssueOptions, by string) {
	s.mu.Lock()
	if s.issuing || s.jobs == nil || s.certs == nil {
		s.mu.Unlock()
		return
	}
	s.issuing = true
	s.mu.Unlock()
	title := "Issue the just-dashboard.com address's certificate"
	if opts.DryRun {
		title = "Test the just-dashboard.com address's certificate"
	}
	job := s.jobs.Start(jobs.Spec{
		Kind: "app-domain.certificate", Title: title, StartedBy: by, Timeout: 15 * time.Minute,
		// The routes that start it are the account link's, for
		// administrators.
		Capability: auth.CapSystemAdmin,
	}, func(ctx context.Context, out jobs.Emitter) error {
		defer func() {
			s.mu.Lock()
			s.issuing = false
			s.mu.Unlock()
		}()
		_, err := s.issue(ctx, opts, func(stream, text string) {
			if stream == "status" {
				out.Status("%s", text)
			} else {
				out.Line(stream, text)
			}
		})
		return err
	})
	s.mu.Lock()
	s.jobID = job.ID
	s.mu.Unlock()
}

// issue orders the wildcard, one at a time, and records how it went.
func (s *Service) issue(ctx context.Context, opts IssueOptions, log func(stream, text string)) (proxysvc.DeploymentCertificate, error) {
	s.issueMu.Lock()
	defer s.issueMu.Unlock()
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return proxysvc.DeploymentCertificate{}, err
	}
	if !s.Enabled() || !st.held() {
		return proxysvc.DeploymentCertificate{}, ErrNoDomain
	}
	if !opts.DryRun && !opts.Force {
		// Whoever had the turn before may have issued it.
		if current := s.certs.AppDomainCertificate(ctx, st.Handle, st.Zone); current.State == "valid" {
			return proxysvc.DeploymentCertificate{
				Outcome: proxysvc.CertificateReused, Name: current.Name, CertPath: current.CertPath, KeyPath: current.KeyPath,
				Method: "app-domain",
			}, nil
		}
	}
	order := proxysvc.AppDomainOrder{Handle: st.Handle, Zone: st.Zone, Credentials: s.HookPath(), DryRun: opts.DryRun, Force: opts.Force}
	result, err := s.certs.IssueAppDomainCertificate(ctx, order, log)
	if opts.DryRun {
		s.record(ctx, "app.domain.certificate.test", st.Hostname, err == nil, errorDetail(err))
		return result, err
	}
	_, _ = update(context.WithoutCancel(ctx), s.st.DB, func(cur *State) (bool, error) {
		if cur.Handle != st.Handle {
			return false, nil
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if cur.CertificateError == msg {
			return false, nil
		}
		cur.CertificateError = msg
		return true, nil
	})
	s.record(ctx, "app.domain.certificate", st.Hostname, err == nil, errorDetail(err))
	if err != nil {
		return result, err
	}
	if serveErr := s.certs.ServeAppDomainRenewal(ctx, st.Handle); serveErr != nil && log != nil {
		log("status", serveErr.Error())
	}
	return result, nil
}

func errorDetail(err error) map[string]any {
	if err == nil {
		return nil
	}
	return map[string]any{"error": err.Error()}
}

// ErrNotHeldName is a deployment name under the zone that this server's
// handle does not cover.
var ErrNotHeldName = errors.New("the name is under the just-dashboard.com address zone but not under the handle this server holds")

// EnsureCertificate is a deployment's certificate step for names under the
// held handle: the wildcard as it is when it is valid, else issued now. It
// never orders a certificate for a single name.
func (s *Service) EnsureCertificate(ctx context.Context, names []string, log func(stream, text string)) (proxysvc.DeploymentCertificate, error) {
	for _, name := range names {
		if s.Classify(ctx, name) != Held {
			return proxysvc.DeploymentCertificate{}, fmt.Errorf("%w: %s", ErrNotHeldName, name)
		}
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return proxysvc.DeploymentCertificate{}, err
	}
	current := s.certs.AppDomainCertificate(ctx, st.Handle, st.Zone)
	if current.State == "valid" {
		return proxysvc.DeploymentCertificate{
			Outcome: proxysvc.CertificateReused, Name: current.Name, CertPath: current.CertPath, KeyPath: current.KeyPath,
			Method: "app-domain", Domains: names,
		}, nil
	}
	if log != nil {
		why := "is not on this server yet"
		if current.State == "expiring" {
			why = "is due for renewal"
		} else if current.Error != "" {
			why = "cannot be used: " + current.Error
		}
		log("status", "The wildcard for *."+st.base()+" "+why+"; issuing it through just-dashboard.com")
	}
	if err := s.ensureHook(ctx); err != nil {
		return proxysvc.DeploymentCertificate{}, err
	}
	result, err := s.issue(ctx, IssueOptions{}, log)
	if err != nil {
		return result, err
	}
	result.Domains = names
	return result, nil
}

// AddChallenge publishes a DNS-01 value certbot's hook was handed, and
// returns once the zone's nameservers answer it, or after two minutes with
// why not.
func (s *Service) AddChallenge(ctx context.Context, domain, value string) error {
	st, err := s.challengeState(ctx, domain)
	if err != nil {
		return err
	}
	if _, err := s.link.AddChallenge(ctx, value); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.txtWait)
	defer cancel()
	name := "_acme-challenge." + st.Hostname
	if err := s.waitTXT(ctx, name, value); err != nil {
		return fmt.Errorf("the site published the challenge, but %w", err)
	}
	return nil
}

// RemoveChallenge withdraws a value once certbot is done with it.
func (s *Service) RemoveChallenge(ctx context.Context, domain, value string) error {
	if _, err := s.challengeState(ctx, domain); err != nil {
		return err
	}
	return s.link.RemoveChallenge(ctx, value)
}

// challengeState checks that a hook's name is the held handle's: certbot
// names the base for both the wildcard and the base, so nothing else may be
// proved through this server's handle.
func (s *Service) challengeState(ctx context.Context, domain string) (State, error) {
	if !s.Enabled() {
		return State{}, ErrDisabled
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return State{}, err
	}
	if !st.held() {
		return State{}, ErrNoDomain
	}
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(domain), "*."), "."))
	if domain != st.Hostname {
		return State{}, fmt.Errorf("%w (%s, not %s)", ErrNotThisDomain, domain, st.Hostname)
	}
	return st, nil
}
