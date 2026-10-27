package proxysvc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// A wildcard a deployment run serves.
//
// Only a DNS challenge proves *.example.com, so a run that serves one has to
// write a TXT record into the zone, and that takes a credential for the zone.
// The run never asks for one. It uses what the Certificates page already
// saved — one file per provider, 0600 inside certbot's tree, which this code
// names to certbot and never opens — and orders through the same certbot,
// from the same authority, with the plugin arguments the page builds
// (dnsIssueArgs). What it leaves
// is an ordinary lineage whose renewal configuration names that plugin and
// that file, so certbot's own schedule renews it like any other the page
// lists. When nothing saved can answer, the run stops before it starts
// anything and says where a credential is saved.

// certificatesPage is where an operator saves a DNS provider's credentials,
// in the words of the dashboard's navigation and then as its address, so a
// refusal read in a notification or a commit status still leads there.
const certificatesPage = "Proxy → Certificates → DNS challenge providers (/proxy/certificates)"

// wildcardError is a wildcard this host cannot have issued, with what to do
// instead. It is ErrCertificateWildcard underneath, so the run can tell it
// from an order the authority refused.
type wildcardError struct{ message string }

func (e *wildcardError) Error() string { return e.message }
func (e *wildcardError) Unwrap() error { return ErrCertificateWildcard }

func wildcardRefusal(format string, args ...any) error {
	return &wildcardError{message: fmt.Sprintf(format, args...)}
}

// nsLookup is how a zone's nameservers are asked for: net.Resolver.LookupNS,
// or a test's table.
type nsLookup func(context.Context, string) ([]*net.NS, error)

// deploymentNSLookup is the lookup a run and its preflight use. A variable
// for tests, which must not ask the network who hosts example.test.
var deploymentNSLookup nsLookup = net.DefaultResolver.LookupNS

// UseDeploymentNSLookupForTest answers a run's nameserver lookups from
// lookup, for a test in another package, and restores the resolver.
func UseDeploymentNSLookupForTest(lookup func(context.Context, string) ([]*net.NS, error)) (restore func()) {
	previous := deploymentNSLookup
	deploymentNSLookup = lookup
	return func() { deploymentNSLookup = previous }
}

// zoneNameservers finds the zone a name sits in by asking it and then each
// parent for NS records, stopping short of the top-level domain, and answers
// with that zone and its nameservers. Nothing found is "not known", never "no
// provider": a resolver that cannot answer says nothing about the zone.
func zoneNameservers(ctx context.Context, name string, lookup nsLookup) (string, []string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for zone := name; strings.Contains(zone, "."); _, zone, _ = strings.Cut(zone, ".") {
		records, err := lookup(ctx, zone)
		if ctx.Err() != nil {
			return "", nil
		}
		if err != nil || len(records) == 0 {
			continue
		}
		hosts := make([]string, 0, len(records))
		for _, record := range records {
			hosts = append(hosts, strings.ToLower(strings.TrimSuffix(record.Host, ".")))
		}
		return zone, hosts
	}
	return "", nil
}

// savedDNSProviders are the providers the Certificates page has credentials
// for, in its own order.
func savedDNSProviders() []DNSProvider {
	saved := []DNSProvider{}
	for _, provider := range dnsProviders {
		if HasDNSCredentials(provider.Key) {
			saved = append(saved, provider)
		}
	}
	return saved
}

// chooseDeploymentDNSProvider is whose saved credentials answer a wildcard's
// challenge. Credentials are kept per provider rather than per zone, so the
// zone's own nameservers decide when they name a provider this dashboard
// drives; failing that, the one provider with credentials and a plugin to use
// them does. Two of those and nothing to tell them apart is a refusal rather
// than a guess, because a wrong guess is an order the authority counts as a
// failed validation — five in an hour and the name is locked out.
//
// plugins are the authenticators of the certbot that will run, and where is
// how a message names it.
func chooseDeploymentDNSProvider(
	ctx context.Context,
	wildcard string,
	saved []DNSProvider,
	plugins map[string]bool,
	where string,
	lookup nsLookup,
) (DNSProvider, error) {
	lead := wildcard + " is a wildcard, which only a DNS challenge can prove"
	if len(saved) == 0 {
		return DNSProvider{}, wildcardRefusal(
			"%s, and no DNS provider has credentials saved on this server. Save your DNS provider's API token under %s, then deploy again.",
			lead, certificatesPage)
	}
	unusable := func(provider DNSProvider) error {
		return wildcardRefusal(
			"%s. %s has credentials saved, but the certbot that runs here (%s) has no %s plugin to use them. Install it under %s, then deploy again.",
			lead, provider.Name, where, provider.Plugin, certificatesPage)
	}
	zone, hosts := zoneNameservers(ctx, strings.TrimPrefix(wildcard, "*."), lookup)
	// A zone served by two providers at once is told apart no better than
	// one served by neither.
	hosted := []DNSProvider{}
	for _, provider := range dnsProviders {
		for _, host := range hosts {
			if provider.nameservers != nil && provider.nameservers.MatchString(host) {
				hosted = append(hosted, provider)
				break
			}
		}
	}
	if len(hosted) == 1 {
		provider := hosted[0]
		for _, candidate := range saved {
			if candidate.Key != provider.Key {
				continue
			}
			if !plugins[candidate.Plugin] {
				return DNSProvider{}, unusable(candidate)
			}
			return candidate, nil
		}
		return DNSProvider{}, wildcardRefusal(
			"%s, and %s is hosted by %s (its nameservers are %s), which has no credentials saved on this server. Save %s's API token under %s, then deploy again.",
			lead, zone, provider.Name, strings.Join(hosts, ", "), provider.Name, certificatesPage)
	}
	usable := []DNSProvider{}
	for _, candidate := range saved {
		if plugins[candidate.Plugin] {
			usable = append(usable, candidate)
		}
	}
	switch len(usable) {
	case 1:
		return usable[0], nil
	case 0:
		return DNSProvider{}, unusable(saved[0])
	}
	names := make([]string, 0, len(usable))
	for _, candidate := range usable {
		names = append(names, candidate.Name)
	}
	unknown := "which of them hosts its zone could not be read from the zone's nameservers"
	if zone != "" {
		unknown = zone + "'s nameservers (" + strings.Join(hosts, ", ") + ") do not say which of them hosts it"
	}
	return DNSProvider{}, wildcardRefusal(
		"%s, and %s all have credentials saved here, but %s. Issue %s once under %s with the right one; this deployment and every later one reuse it.",
		lead, describeSites(names), unknown, wildcard, certificatesPage)
}

// DeploymentDNSProvider answers, without ordering anything, how a run would
// prove a wildcard: the provider whose saved credentials it would use, or the
// refusal it would stop with. Preflight and the Domains sheet ask it, so the
// answer before a run and the run's own are one decision. The proxy serving
// the route does not come into it: certbot proves the wildcard on the host
// either way, and the Docker Caddy ingress serves a copy of what it issued.
func (s *Service) DeploymentDNSProvider(ctx context.Context, wildcard string) (DNSProvider, error) {
	rt, err := deploymentCertbot(ctx)
	if err != nil {
		return DNSProvider{}, err
	}
	return chooseDeploymentDNSProvider(ctx, wildcard, savedDNSProviders(), rt.authenticators, rt.where(), deploymentNSLookup)
}

// deploymentCertbot is the certbot the Certificates page runs, which is the
// one whose plugins decide what renews: a lineage this run leaves has to be
// one that certbot can renew.
func deploymentCertbot(ctx context.Context) (*certbotRuntime, error) {
	rt, err := loadCertbotRuntime(ctx)
	if rt == nil {
		return nil, ErrCertbotUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCertbotUnavailable, err)
	}
	return rt, nil
}

// issueDeploymentWildcard orders the run's names over a DNS challenge
// through the provider chooseDeploymentDNSProvider picks. All of them, since
// activation serves one certificate covering every name of the route, and a
// DNS challenge proves a plain name as well as a wildcard. No public address
// is checked first: a DNS challenge never reaches this host.
func (s *Service) issueDeploymentWildcard(
	ctx context.Context,
	names []string,
	wildcard string,
	log func(stream, text string),
) (DeploymentCertificate, error) {
	rt, err := deploymentCertbot(ctx)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	provider, err := chooseDeploymentDNSProvider(ctx, wildcard, savedDNSProviders(), rt.authenticators, rt.where(), deploymentNSLookup)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	directory := acmeDirectory()
	if err := directory.validate(); err != nil {
		return DeploymentCertificate{}, err
	}
	environment, err := CertbotEnvironment()
	if err != nil {
		return DeploymentCertificate{}, err
	}
	// The HTTP path's bound, plus the propagation wait certbot sleeps
	// through between writing the record and asking for validation. The
	// wait for certbot's turn comes out of it too.
	issueCtx, cancel := context.WithTimeout(ctx, 5*time.Minute+time.Duration(provider.DefaultWait)*time.Second)
	defer cancel()
	release, err := holdCertbotFor(issueCtx, names, log)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	defer release()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Whoever had certbot may have issued the wildcard: the page, or the
	// run queued before this one.
	if reused, ok := s.reuseDeploymentCertificate(issueCtx, names); ok {
		return reused, nil
	}
	registered := acmeAccountExists(letsencryptDir, certbotServer(false))
	args := append([]string{"certonly"}, dnsIssueArgs(provider, provider.DefaultWait)...)
	args = append(args, "--non-interactive", "--agree-tos", "--keep-until-expiring", "--no-eff-email")
	if !registered {
		// As on the HTTP path: an account needs no address, and one an
		// operator registered with an address on the Certificates page is
		// the one this order uses.
		args = append(args, "--register-unsafely-without-email")
	}
	args = append(args, directory.certbotArgs()...)
	for _, name := range names {
		args = append(args, "-d", name)
	}
	cmd, err := CertbotCommand(issueCtx, environment, args...)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	if log != nil {
		log("status", "Proving "+strings.Join(names, ", ")+" over a DNS challenge with the "+provider.Name+" credentials saved under Certificates")
	}
	output, err := cmd.CombinedOutput()
	if log != nil {
		for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				log("stdout", line)
			}
		}
	}
	// What was attempted is returned with a failure too: an order that
	// reached the authority wrote to the operator's zone, and the audit
	// entry says through whose credentials.
	attempt := DeploymentCertificate{
		Method: provider.Plugin, DNSProvider: provider.Name, Domains: names, Registered: registered,
	}
	if err != nil {
		return attempt, fmt.Errorf("certbot could not issue a certificate for %s over a DNS challenge through %s: %s",
			strings.Join(names, ", "), provider.Name, lastMeaningfulLine(string(output)))
	}
	certPath, keyPath, err := s.ResolveDeploymentCertificate(issueCtx, names)
	if err != nil {
		return attempt, fmt.Errorf("certbot reported success but no certificate on this host covers %s",
			strings.Join(names, ", "))
	}
	attempt.Outcome, attempt.CertPath, attempt.KeyPath = CertificateIssued, certPath, keyPath
	attempt.Name = certificateNameForPath(certPath)
	return attempt, nil
}
