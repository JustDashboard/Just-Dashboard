package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// The app domain's wildcard: one certificate for *.<handle>.<zone> and
// <handle>.<zone>, proved over DNS-01 through just-dashboard.com.
//
// The zone is the site's, so this server holds no credential for it. certbot
// runs with its manual plugin, and the two hooks are the jd command line,
// which asks the running backend to publish or withdraw the value through the
// account link (backend/internal/appdomain). The lineage's renewal
// configuration keeps the hooks, so certbot's own timer renews it the same
// way for as long as the server holds the handle. Per-app certificates are
// never ordered under the zone: Let's Encrypt counts certificates per
// registered domain, and every server shares that budget.

// AppDomainLineagePrefix starts the name of every app domain lineage.
const AppDomainLineagePrefix = "jd-app-domain-"

// AppDomainLineage is the lineage a handle's wildcard is kept under.
func AppDomainLineage(handle string) string { return AppDomainLineagePrefix + handle }

// isAppDomainLineage reports a lineage this feature owns, by its name.
func isAppDomainLineage(name string) bool { return strings.HasPrefix(name, AppDomainLineagePrefix) }

// AppDomainHookCommand is the hook certbot runs, as the renewal
// configuration stores it: the host's jd, told where the backend left the
// hook's credential. One spelling serves both sides, because the stack mounts
// the data directory and /usr/local/bin's jd at the same paths in the
// backend's container and on the host.
func AppDomainHookCommand(action, credentials string) string {
	return AppDomainHookBinary + " app-domain challenge " + action + " --credentials " + credentials
}

// AppDomainHookBinary is the jd the hooks run: where install.sh and
// `manage.sh install-cli` put it, and where the image has it. A variable for
// tests, which run a jd of their own.
var AppDomainHookBinary = "/usr/local/bin/jd"

// SetAppDomainZone tells the service which zone holds app domains, read
// each time a deployment asks for a certificate. A name under it is refused
// by EnsureDeploymentCertificate before anything is ordered: the app domain
// service answers for those names with the handle's wildcard.
func (s *Service) SetAppDomainZone(zone func() string) { s.appDomainZone.Store(&zone) }

// ErrAppDomainName is a deployment asking this service to order a
// certificate for a name under the app domain zone.
var ErrAppDomainName = errors.New("names under the just-dashboard.com address zone are covered only by the handle's wildcard, which is never ordered per name")

func (s *Service) refuseAppDomainNames(names []string) error {
	zone := ""
	if f := s.appDomainZone.Load(); f != nil && *f != nil {
		zone = strings.ToLower(strings.TrimSpace((*f)()))
	}
	if zone == "" {
		return nil
	}
	for _, name := range names {
		if name == zone || strings.HasSuffix(name, "."+zone) {
			return fmt.Errorf("%w: %s", ErrAppDomainName, name)
		}
	}
	return nil
}

// AppDomainOrder is one issuance of a handle's wildcard.
type AppDomainOrder struct {
	Handle, Zone string
	// Credentials is the hook credential's path, which the hooks are told.
	Credentials string
	// DryRun runs the whole order against the staging authority with nothing
	// saved, as the Certificates page's test runs do.
	DryRun bool
	// Force renews a lineage that is not due yet.
	Force bool
}

func (o AppDomainOrder) names() []string {
	base := o.Handle + "." + o.Zone
	return []string{"*." + base, base}
}

// AppDomainCertificateState is what the lineage holds now.
type AppDomainCertificateState struct {
	Name string
	// State is "missing", "valid", "expiring" or "failed" (on disk but
	// unusable: expired, a test certificate, or not covering both names).
	State     string
	ExpiresAt int64
	Error     string
	CertPath  string
	KeyPath   string
}

// AppDomainCertificate reads handle's lineage from certbot's files.
func (s *Service) AppDomainCertificate(ctx context.Context, handle, zone string) AppDomainCertificateState {
	name := AppDomainLineage(handle)
	out := AppDomainCertificateState{Name: name, State: "missing"}
	certificates, err := s.ListCertificates(ctx)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	order := AppDomainOrder{Handle: handle, Zone: zone}
	for _, certificate := range certificates {
		if certificate.Name != name && filepath.Base(filepath.Dir(certificate.Path)) != name {
			continue
		}
		out.CertPath = certificate.Path
		out.KeyPath = filepath.Join(filepath.Dir(certificate.Path), "privkey.pem")
		if !certificate.NotAfter.IsZero() {
			out.ExpiresAt = certificate.NotAfter.Unix()
		}
		switch {
		case certificate.Error != "":
			out.State, out.Error = "failed", certificate.Error
		case certificate.Expired:
			out.State, out.Error = "failed", "the certificate has expired"
		case certificate.Staging && !acmeDirectory().staging():
			// A test certificate is what a staging directory issues, and
			// the server was told to use one (JD_ACME_DIRECTORY); under any
			// other it is a certificate browsers refuse, to issue again.
			out.State, out.Error = "failed", "the certificate is a test one from a staging authority"
		case !certificateCoversAll(certificate.Domains, order.names()):
			out.State, out.Error = "failed", "the certificate does not cover "+strings.Join(order.names(), " and ")
		case certificate.Expiring:
			out.State = "expiring"
		default:
			out.State = "valid"
		}
		return out
	}
	return out
}

// ErrAppDomainHookMissing is a certbot that cannot run the hooks: the jd
// command line is not where the renewal configuration will name it.
var ErrAppDomainHookMissing = errors.New("the jd command line is not installed where certbot runs it")

// IssueAppDomainCertificate orders the handle's wildcard through certbot's
// manual plugin, with the jd hooks, and reads it back. It holds certbot's
// turn (certbot_gate.go) while certbot runs, as a deployment's order does,
// but not the route lock: the hooks it runs call back into this process.
func (s *Service) IssueAppDomainCertificate(ctx context.Context, order AppDomainOrder, log func(stream, text string)) (DeploymentCertificate, error) {
	if order.Handle == "" || order.Zone == "" || order.Credentials == "" {
		return DeploymentCertificate{}, errors.New("an app domain order needs a handle, a zone and the hook's credential")
	}
	names := order.names()
	for _, name := range names {
		if !certDomainRe.MatchString(name) {
			return DeploymentCertificate{}, fmt.Errorf("%q is not a valid domain name", name)
		}
	}
	rt, err := deploymentCertbot(ctx)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	if missing := rt.missingFiles(ctx, []string{AppDomainHookBinary}); missing[AppDomainHookBinary] {
		side := "on this host"
		if !rt.onHost {
			side = "in the dashboard's container"
		}
		return DeploymentCertificate{}, fmt.Errorf("%w: certbot runs %s, and %s is missing %s. Run sudo ./scripts/manage.sh install-cli, then issue again",
			ErrAppDomainHookMissing, rt.where(), AppDomainHookBinary, side)
	}
	directory := acmeDirectory()
	if err := directory.validate(); err != nil {
		return DeploymentCertificate{}, err
	}
	environment, err := CertbotEnvironment()
	if err != nil {
		return DeploymentCertificate{}, err
	}
	// Two hook runs, each allowed two minutes for the zone's nameservers,
	// on top of the ordinary order.
	issueCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	release, err := HoldCertbot(issueCtx, "the just-dashboard.com address's certificate for "+strings.Join(names, ", "), func(holder string) {
		if log != nil {
			log("status", "Waiting for certbot, which is busy with "+holder+".")
		}
	})
	if err != nil {
		return DeploymentCertificate{}, err
	}
	defer release()
	registered := acmeAccountExists(letsencryptDir, certbotServer(false))
	args := appDomainIssueArgs(order, registered, directory)
	cmd, err := CertbotCommand(issueCtx, environment, args...)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	if log != nil {
		log("status", "Proving "+strings.Join(names, ", ")+" over a DNS challenge through just-dashboard.com")
	}
	output, err := cmd.CombinedOutput()
	if log != nil {
		for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				log("stdout", line)
			}
		}
	}
	attempt := DeploymentCertificate{Method: "app-domain", Domains: names, Registered: registered, Name: AppDomainLineage(order.Handle)}
	if err != nil {
		return attempt, fmt.Errorf("certbot could not issue the certificate for %s: %s", strings.Join(names, ", "), lastMeaningfulLine(string(output)))
	}
	if order.DryRun {
		attempt.Outcome = CertificateReused
		return attempt, nil
	}
	state := s.AppDomainCertificate(issueCtx, order.Handle, order.Zone)
	if state.State != "valid" && state.State != "expiring" {
		why := state.Error
		if why == "" {
			why = "no certificate is on disk under " + state.Name
		}
		return attempt, fmt.Errorf("certbot reported success but %s", why)
	}
	attempt.Outcome, attempt.CertPath, attempt.KeyPath = CertificateIssued, state.CertPath, state.KeyPath
	return attempt, nil
}

// appDomainIssueArgs is the argv of an app domain order.
func appDomainIssueArgs(order AppDomainOrder, registered bool, directory ACMEDirectory) []string {
	args := []string{
		"certonly", "--manual", "--preferred-challenges", "dns",
		"--manual-auth-hook", AppDomainHookCommand("add", order.Credentials),
		"--manual-cleanup-hook", AppDomainHookCommand("remove", order.Credentials),
		"--cert-name", AppDomainLineage(order.Handle),
		"--non-interactive", "--agree-tos", "--no-eff-email",
	}
	if !registered {
		args = append(args, "--register-unsafely-without-email")
	}
	switch {
	case order.DryRun:
		// The staging authority, nothing saved, nothing counted: what the
		// Certificates page's test run does.
		args = append(args, "--dry-run")
	case order.Force:
		args = append(args, "--force-renewal")
	default:
		args = append(args, "--keep-until-expiring")
	}
	args = append(args, directory.certbotArgs()...)
	for _, name := range order.names() {
		args = append(args, "-d", name)
	}
	return args
}

// ServeAppDomainRenewal has what serves the lineage read it again: the
// Docker Caddy ingress's copies, and the nginx sites naming it.
func (s *Service) ServeAppDomainRenewal(ctx context.Context, handle string) error {
	var failures []error
	if _, err := s.RefreshIngressCertificates(ctx); err != nil && !errors.Is(err, ErrIngressUnavailable) {
		failures = append(failures, NotRefreshedFor(err))
	}
	if sites := s.SitesServingLineages([]string{AppDomainLineage(handle)}); len(sites) > 0 {
		if _, err := s.Reload(ctx, KindNginx); err != nil {
			failures = append(failures, NotReloadedFor(sites, err.Error()))
		}
	}
	return errors.Join(failures...)
}

// The lookups the TXT wait makes, variables for tests: a test must never ask
// the network who serves just-dashboard.com.
var (
	appDomainNSLookup   nsLookup = net.DefaultResolver.LookupNS
	appDomainHostLookup          = net.DefaultResolver.LookupIPAddr
	appDomainTXTQuery            = func(ctx context.Context, server, name string) ([]string, error) {
		answer, err := Query(ctx, server, name, dnsmessage.TypeTXT)
		if err != nil {
			return nil, err
		}
		values := []string{}
		for _, record := range answer.Records {
			if record.Type == "TXT" {
				values = append(values, record.Value)
			}
		}
		return values, nil
	}
	appDomainTXTEvery = 2 * time.Second
)

// UseAppDomainDNSForTest answers the TXT wait's lookups from the functions
// given, for a test in another package, and restores the real ones.
func UseAppDomainDNSForTest(
	ns func(context.Context, string) ([]*net.NS, error),
	host func(context.Context, string) ([]net.IPAddr, error),
	txt func(ctx context.Context, server, name string) ([]string, error),
	every time.Duration,
) (restore func()) {
	pns, phost, ptxt, pevery := appDomainNSLookup, appDomainHostLookup, appDomainTXTQuery, appDomainTXTEvery
	appDomainNSLookup, appDomainHostLookup, appDomainTXTQuery, appDomainTXTEvery = ns, host, txt, every
	return func() {
		appDomainNSLookup, appDomainHostLookup, appDomainTXTQuery, appDomainTXTEvery = pns, phost, ptxt, pevery
	}
}

// WaitForAuthoritativeTXT returns once every nameserver of the zone name sits
// in answers value at name, or fails when ctx ends first, saying which did
// not. The zone's own servers are asked, never a resolver, because a resolver
// may hold the empty answer it cached before the record existed for as long
// as the zone's negative TTL, and Let's Encrypt asks the authoritative ones.
func WaitForAuthoritativeTXT(ctx context.Context, name, value string) error {
	zone, hosts := zoneNameservers(ctx, strings.TrimPrefix(name, "_acme-challenge."), appDomainNSLookup)
	if len(hosts) == 0 {
		return fmt.Errorf("the nameservers of %s could not be found, so whether the challenge is published cannot be checked", name)
	}
	servers := map[string][]string{}
	for _, host := range hosts {
		addrs, err := appDomainHostLookup(ctx, host)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if addr.IP.To4() != nil {
				servers[host] = append(servers[host], addr.IP.String())
			}
		}
	}
	if len(servers) == 0 {
		return fmt.Errorf("none of %s's nameservers (%s) could be resolved", zone, strings.Join(hosts, ", "))
	}
	waiting := slices.Sorted(func(yield func(string) bool) {
		for host := range servers {
			if !yield(host) {
				return
			}
		}
	})
	for {
		pending := waiting[:0:0]
		for _, host := range waiting {
			if !nameserverAnswers(ctx, servers[host], name, value) {
				pending = append(pending, host)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		waiting = pending
		t := time.NewTimer(appDomainTXTEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("%s's nameservers %s did not answer the challenge at %s in time", zone, strings.Join(pending, ", "), name)
		case <-t.C:
		}
	}
}

func nameserverAnswers(ctx context.Context, addresses []string, name, value string) bool {
	for _, address := range addresses {
		values, err := appDomainTXTQuery(ctx, address, name)
		if err == nil && slices.Contains(values, value) {
			return true
		}
	}
	return false
}

// appDomainHookProblem is what will stop an app domain lineage renewing
// unattended, read from its renewal configuration: the hooks must be jd's,
// and jd must be where certbot runs it. Whether the server still holds the
// handle is the app domain service's to say; this is certbot's side.
func appDomainHookProblem(conf renewalConf, missing map[string]bool) string {
	want := AppDomainHookBinary + " app-domain challenge add"
	switch {
	case pluginName(conf.Authenticator) != "manual":
		return "It is the just-dashboard.com address's certificate, which renews only through the manual plugin and jd, and its renewal configuration names " + pluginName(conf.Authenticator) + "."
	case !strings.HasPrefix(conf.ManualAuthHook, want):
		return "It is the just-dashboard.com address's certificate, and its auth hook is not jd's (" + want + "), so certbot cannot publish the challenge when it renews. Issue it again from Settings → Connect."
	case !strings.HasPrefix(conf.ManualCleanupHook, AppDomainHookBinary+" app-domain challenge remove"):
		return "It is the just-dashboard.com address's certificate, and its cleanup hook is not jd's, so the challenge values stay published after a renewal. Issue it again from Settings → Connect."
	case missing[AppDomainHookBinary]:
		return "It renews through " + AppDomainHookBinary + ", which is missing where certbot runs. Run sudo ./scripts/manage.sh install-cli."
	}
	return ""
}

// DeleteAppDomainCertificate removes a handle's lineage once the server no
// longer holds the handle, so certbot's timer stops trying to renew a name
// the site will not prove for it. A lineage that is not there is no error.
func (s *Service) DeleteAppDomainCertificate(ctx context.Context, handle string) error {
	name := AppDomainLineage(handle)
	if handle == "" || !certNameRe.MatchString(name) {
		return fmt.Errorf("%q is not an app domain lineage", name)
	}
	if _, err := os.Stat(filepath.Join(letsencryptDir, "renewal", name+".conf")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	release, err := HoldCertbot(ctx, "removing the certificate of a released just-dashboard.com address", nil)
	if err != nil {
		return err
	}
	defer release()
	cmd, err := CertbotCommand(ctx, nil, "delete", "--cert-name", name, "--non-interactive")
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("certbot could not delete %s: %s", name, lastMeaningfulLine(string(out)))
	}
	return nil
}
