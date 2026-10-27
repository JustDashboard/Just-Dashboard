package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Issuing the certificate a deployment's own route needs.
//
// Certificates are the Certificates feature's business, and activation still
// refuses to move traffic onto an HTTPS route it cannot resolve an existing
// pair for. What changes here is who presses the button: a deployment that
// asked for HTTPS on a name that resolves to this host should not stop and wait
// for somebody to go and issue one by hand, and then fail its cutover for
// having no certificate. So the run orders it, through the same certbot this
// host already uses, before anything is started.
//
// The narrowness is the safety. It only ever issues for the exact hostnames in
// the run's frozen plan — a wildcard among them is ordered over DNS-01 through
// credentials the Certificates page already holds (deployment_certificate_dns.go),
// everything else over HTTP-01 — and it verifies afterwards by reading the
// certificate back rather than by trusting an exit code.

// CertificateOutcome is what a deployment's certificate step did.
type CertificateOutcome string

const (
	// CertificateReused means a valid certificate already covered every name.
	CertificateReused CertificateOutcome = "reused"
	// CertificateIssued means certbot obtained one during this run.
	CertificateIssued CertificateOutcome = "issued"
)

// DeploymentCertificate is the resolved pair plus how it was obtained. It
// carries paths, never key material.
type DeploymentCertificate struct {
	Outcome  CertificateOutcome `json:"outcome"`
	Name     string             `json:"name,omitempty"`
	CertPath string             `json:"certPath"`
	KeyPath  string             `json:"keyPath"`
	Method   string             `json:"method,omitempty"`
	// DNSProvider names whose saved credentials answered a DNS challenge.
	DNSProvider string   `json:"dnsProvider,omitempty"`
	Domains     []string `json:"domains"`
	// Registered reports whether certbot already had an ACME account. Without
	// one the order registers a new account, which is the only part of this
	// that is not repeatable, and is worth saying in the evidence.
	Registered bool `json:"accountExisted"`
}

var (
	// ErrCertbotUnavailable is "this host cannot issue one", which is an
	// honest unavailable rather than a failure of the deployment's own work.
	ErrCertbotUnavailable = errors.New("certbot is not installed on this host, so a certificate cannot be issued here")
	// ErrCertificateWildcard is a wildcard this host cannot prove: no saved
	// DNS credentials answer for its zone, or the Docker Caddy ingress
	// serves the route. It is refused before certbot is asked, with what to
	// do instead (wildcardError), rather than relayed from the authority a
	// minute later.
	ErrCertificateWildcard = errors.New("a wildcard certificate needs a DNS challenge this host cannot answer")
)

// EnsureDeploymentCertificate resolves a certificate covering every hostname,
// issuing one if none does: over DNS-01 when a name is a wildcard, over
// HTTP-01 otherwise.
//
// Reuse is checked first and is the ordinary answer: a second deployment onto
// the same wildcard — a preview under one issued for every preview — or a
// redeploy inside the renewal window, asks the authority for nothing. A
// wildcard name is reused too, which it was not while wildcards were refused
// before the check. Issuance is serialized with the route lock for the
// same reason a cutover is — two runs ordering certificates for the same host
// at once is how an account hits a rate limit it did not need to.
func (s *Service) EnsureDeploymentCertificate(
	ctx context.Context,
	domains []string,
	log func(stream, text string),
) (result DeploymentCertificate, retErr error) {
	if len(domains) == 0 {
		return DeploymentCertificate{}, errors.New("a TLS deployment route needs at least one domain")
	}
	names := make([]string, 0, len(domains))
	wildcard := ""
	for _, domain := range domains {
		name := strings.ToLower(strings.TrimSpace(domain))
		if !certDomainRe.MatchString(name) {
			return DeploymentCertificate{}, fmt.Errorf("%q is not a valid domain name", domain)
		}
		if strings.HasPrefix(name, "*.") && wildcard == "" {
			wildcard = name
		}
		names = append(names, name)
	}
	// What is on the host already, and an ingress that orders for itself,
	// need no certbot and wait for nobody's.
	if result, done, err := s.deploymentCertificateWithoutCertbot(ctx, names, wildcard, log); done || err != nil {
		return result, err
	}
	if wildcard != "" {
		return s.issueDeploymentWildcard(ctx, names, wildcard, log)
	}
	return s.issueDeploymentHTTP(ctx, names, log)
}

// deploymentCertificateWithoutCertbot answers what needs no order, under the
// route lock: a wildcard the ingress cannot serve, a certificate that already
// covers every name, or the Docker Caddy ingress, which orders for itself.
// done reports that it answered.
func (s *Service) deploymentCertificateWithoutCertbot(
	ctx context.Context,
	names []string,
	wildcard string,
	log func(stream, text string),
) (DeploymentCertificate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Before reuse: the Docker Caddy ingress cannot serve a wildcard route at
	// all, so a certificate that covers one is no answer there.
	if wildcard != "" {
		if err := s.wildcardCaddyRefusal(ctx, wildcard); err != nil {
			return DeploymentCertificate{}, true, err
		}
	}
	if reused, ok := s.reuseDeploymentCertificate(ctx, names); ok {
		return reused, true, nil
	}
	if wildcard != "" {
		return DeploymentCertificate{}, false, nil
	}
	edge, err := s.provisionIngress(ctx)
	if err != nil {
		return DeploymentCertificate{}, true, err
	}
	if edge != nil {
		result, err := s.ensureDockerCaddyCertificate(ctx, edge, names, log)
		return result, true, err
	}
	return DeploymentCertificate{}, false, nil
}

// reuseDeploymentCertificate is a certificate on this host that already
// covers every name, which is the ordinary answer. The caller holds s.mu.
func (s *Service) reuseDeploymentCertificate(ctx context.Context, names []string) (DeploymentCertificate, bool) {
	certPath, keyPath, err := s.ResolveDeploymentCertificate(ctx, names)
	if err != nil {
		return DeploymentCertificate{}, false
	}
	return DeploymentCertificate{
		Outcome: CertificateReused, CertPath: certPath, KeyPath: keyPath,
		Name: certificateNameForPath(certPath), Domains: names,
	}, true
}

// holdCertbotFor takes certbot's turn for a run ordering for names, within
// ctx — the step's own bound — telling the transcript whom it waits for.
// The turn comes before the route lock, never inside it: the page's job
// holding the turn reloads nginx under that lock when it is done.
func holdCertbotFor(ctx context.Context, names []string, log func(stream, text string)) (func(), error) {
	return HoldCertbot(ctx, "a deployment run issuing a certificate for "+strings.Join(names, ", "), func(holder string) {
		if log != nil {
			log("status", "Waiting for certbot, which is busy with "+holder+"; the certificate step continues when it finishes.")
		}
	})
}

// issueDeploymentHTTP orders names over HTTP-01 through certbot: webroot
// behind the host's nginx, standalone on a host with nothing on port 80.
func (s *Service) issueDeploymentHTTP(
	ctx context.Context,
	names []string,
	log func(stream, text string),
) (result DeploymentCertificate, retErr error) {
	availability := s.Availability(ctx)
	if !availability.Certbot {
		return DeploymentCertificate{}, ErrCertbotUnavailable
	}
	// Bounded well above a normal order: an ACME exchange is two round trips
	// and a validation, and a host with slow DNS is the case this has to
	// survive rather than the case it should hang for. The wait for
	// certbot's turn comes out of the same bound, so a page job that runs
	// long fails this step in the step's own time rather than holding the
	// run for the job's.
	issueCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	release, err := holdCertbotFor(issueCtx, names, log)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	defer release()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Whoever had certbot may have issued exactly this: the page, for the
	// same names, or the run queued before this one.
	if reused, ok := s.reuseDeploymentCertificate(issueCtx, names); ok {
		return reused, nil
	}
	if err := validateDeploymentHTTPDomains(ctx, names, net.DefaultResolver.LookupIPAddr); err != nil {
		return DeploymentCertificate{}, err
	}
	challenge, err := s.deploymentChallenge(ctx)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	method := challenge.method
	if method == "webroot" {
		cleanup, err := s.prepareDeploymentWebroot(ctx, names, deploymentACMEWebroot, challenge.listeners, func(ctx context.Context, domains []string) error {
			return probeDeploymentWebroot(ctx, domains, deploymentACMEWebroot, challenge.listeners)
		})
		if err != nil {
			return DeploymentCertificate{}, err
		}
		// Always restore the challenge-only route, including after cancellation.
		// Restoration failure must prevent a successful certificate step.
		defer func() {
			if err := cleanup(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("certificate challenge cleanup failed: %w", err))
				result = DeploymentCertificate{}
			}
		}()
	}

	registered := certbotAccountExists()
	args := []string{"certonly", "--" + method, "--non-interactive", "--agree-tos",
		// A run inside the renewal window is a no-op rather than a failure,
		// which is what makes this safe to put in every deployment.
		"--keep-until-expiring", "--no-eff-email"}
	if method == "webroot" {
		args = append(args, "--webroot-path", deploymentACMEWebroot)
	}
	if !registered {
		// A first order on this host has to register an ACME account, and the
		// only thing that needs is an address to send expiry warnings to. This
		// is deliberately not asked for: Let's Encrypt accepts an account
		// without one, renewal is certbot's own timer rather than a reply to
		// that mail, and the dashboard watches the dates itself. An operator
		// who wants the warnings registers with an address once on the
		// Certificates page, and every later order reuses that account.
		args = append(args, "--register-unsafely-without-email")
	}
	for _, name := range names {
		args = append(args, "-d", name)
	}
	output, err := hostexec.Command(issueCtx, "certbot", args...).CombinedOutput()
	if log != nil {
		for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				log("stdout", line)
			}
		}
	}
	if err != nil {
		// The method goes back with the failure, as the DNS path's does, for
		// the audit entry of an order that reached the authority.
		return DeploymentCertificate{Method: method, Domains: names, Registered: registered},
			fmt.Errorf("certbot could not issue a certificate for %s over the %s challenge: %s",
				strings.Join(names, ", "), method, lastMeaningfulLine(string(output)))
	}

	// Read the certificate back rather than trusting the exit code. certbot
	// exits zero on paths that did not produce what was asked for — a reused
	// lineage under a different name among them — and the thing activation
	// will look for is a file that covers these exact names.
	certPath, keyPath, err := s.ResolveDeploymentCertificate(issueCtx, names)
	if err != nil {
		return DeploymentCertificate{}, fmt.Errorf(
			"certbot reported success but no certificate on this host covers %s",
			strings.Join(names, ", "))
	}
	return DeploymentCertificate{
		Outcome: CertificateIssued, CertPath: certPath, KeyPath: keyPath,
		Name: certificateNameForPath(certPath), Method: method, Domains: names,
		Registered: registered,
	}, nil
}

// Existing certificates can serve private names, but a new HTTP-01 order
// requires public DNS destinations. Check before invoking certbot so saved
// releases with an old CGNAT-based suggestion fail with a useful remedy.
func validateDeploymentHTTPDomains(ctx context.Context, names []string,
	lookup func(context.Context, string) ([]net.IPAddr, error),
) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, name := range names {
		addresses, err := lookup(ctx, name)
		if err != nil {
			return fmt.Errorf("cannot resolve %s for certificate issuance: %w", name, err)
		}
		if len(addresses) == 0 {
			return fmt.Errorf("%s has no DNS addresses; point it at this server's public address before deploying", name)
		}
		for _, address := range addresses {
			if !IsPublicAddress(address.IP) {
				return fmt.Errorf("%s resolves to non-public address %s; Let's Encrypt HTTP-01 needs a public address reachable on port 80. Change the deployment hostname to a domain pointing at this server's public IP, or provision a certificate with DNS-01 for a domain you control before deploying", name, address.IP)
			}
		}
	}
	return nil
}

// certbotAccountExists reports whether this host has already registered with an
// ACME authority, which decides whether an address has to be offered at all.
func certbotAccountExists() bool {
	entries, err := os.ReadDir("/etc/letsencrypt/accounts")
	return err == nil && len(entries) > 0
}

// certificateNameForPath is certbot's lineage name: the directory under
// live/ that holds the pair.
func certificateNameForPath(path string) string {
	return filepath.Base(filepath.Dir(path))
}

// Plugin availability alone cannot decide how to answer HTTP-01: standalone
// needs an unused port, and the container's certbot cannot run host nginx.
// Webroot shares challenge files with the host without requiring its plugin.
func (s *Service) deploymentChallenge(ctx context.Context) (deploymentHTTPChallenge, error) {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err := hostexec.Command(listCtx, "certbot", "plugins", "--non-interactive").CombinedOutput()
	if err != nil {
		return deploymentHTTPChallenge{}, fmt.Errorf("%w: certbot could not list its challenge plugins", ErrCertbotUnavailable)
	}
	listeners, err := ListListeners(listCtx)
	if err != nil {
		return deploymentHTTPChallenge{}, fmt.Errorf("cannot inspect port 80 before certificate issuance: %w", err)
	}
	return chooseDeploymentHTTPChallenge(certbotAuthenticators(string(output)), listeners, hostexec.Available("nginx"))
}

// DeploymentCertificateMethod reports the same listener-aware choice issuance uses.
func (s *Service) DeploymentCertificateMethod(ctx context.Context) (string, error) {
	if edge, err := s.dockerCaddy(ctx); err != nil {
		return "", err
	} else if edge != nil {
		return "caddy", nil
	}
	if s.canProvisionIngress(ctx) {
		return "caddy", nil
	}
	challenge, err := s.deploymentChallenge(ctx)
	return challenge.method, err
}

// certbotAuthenticators reads `certbot plugins`, which names each plugin on a
// "* name" line and its capabilities on the "Interfaces:" line under it. Only
// an Authenticator can answer a challenge; the nginx entry is also an Installer
// and the distinction is the reason the interface line is read at all.
func certbotAuthenticators(output string) map[string]bool {
	found := map[string]bool{}
	name := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "* "):
			name = strings.TrimSpace(strings.TrimPrefix(line, "* "))
		case name != "" && strings.HasPrefix(line, "Interfaces:"):
			for _, role := range strings.Split(strings.TrimPrefix(line, "Interfaces:"), ",") {
				if strings.EqualFold(strings.TrimSpace(role), "Authenticator") {
					found[name] = true
				}
			}
			name = ""
		}
	}
	return found
}
