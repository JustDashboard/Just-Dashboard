package proxysvc

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Certificates, issued and renewed from the page that shows they are expiring.
//
// The dashboard already knows a certificate has eleven days left; leaving the
// operator to go and remember certbot's arguments is where every panel in this
// class stops and where the actual work starts. The arguments are also the
// part that is easy to get wrong in a way that costs an outage — --standalone
// on a host running nginx binds port 80 and fails, --force-renewal against
// Let's Encrypt's rate limit locks the domain out for a week.

// CertbotCert is one lineage: the renewal configuration certbot keeps, and
// the certificate it points at.
type CertbotCert struct {
	Name     string    `json:"name"`
	Domains  []string  `json:"domains"`
	Expiry   time.Time `json:"expiry"`
	DaysLeft int       `json:"daysLeft"`
	Valid    bool      `json:"valid"`
	CertPath string    `json:"certPath,omitempty"`
	KeyPath  string    `json:"keyPath,omitempty"`
	Serial   string    `json:"serial,omitempty"`
	// Staging is a test certificate: renewed from a staging authority, or
	// signed by one. Browsers refuse it however many days it has left.
	Staging bool `json:"staging,omitempty"`
	// Error is why the lineage's certificate could not be read.
	Error string `json:"error,omitempty"`
}

// CertbotState is everything the Certificates tab needs about certbot.
type CertbotState struct {
	Available bool          `json:"available"`
	Version   string        `json:"version,omitempty"`
	Certs     []CertbotCert `json:"certs"`
	// AutoRenew reports whether anything is scheduled to renew these. An
	// expired Let's Encrypt certificate is almost never a forgotten renewal;
	// it is a renewal timer that stopped months ago and told nobody.
	AutoRenew   bool   `json:"autoRenew"`
	RenewSource string `json:"renewSource,omitempty"`
	// RenewUnit is a certbot timer systemd knows about but is not running —
	// the thing to turn on when AutoRenew is false. Empty when nothing is
	// scheduled and there is no unit to enable either.
	RenewUnit string `json:"renewUnit,omitempty"`
	// Error is why the lineages could not be read. The renewal fields are
	// answered regardless: they come from systemd and cron, not from certbot.
	Error string `json:"error,omitempty"`
	// Directory is the ACME directory issuance orders from when it is not
	// Let's Encrypt's, so the page can say whom a test run talks to: that
	// authority itself, not a staging one, and not under Let's Encrypt's limits.
	Directory string `json:"directory,omitempty"`
}

func (s *Service) CertbotState(ctx context.Context) *CertbotState {
	state := &CertbotState{Certs: []CertbotCert{}}
	rt, _ := loadCertbotRuntime(ctx)
	if rt == nil {
		return state
	}
	state.Available = true
	state.Version = rt.version
	state.Directory = ACMEDirectoryURL()
	// Before the lineages, and whatever they say: whether anything renews
	// them is a separate question, and a lineage read that failed used to
	// return before it was asked — every certbot run read as "renewal off".
	state.AutoRenew, state.RenewSource = renewalScheduled(ctx)
	if !state.AutoRenew {
		state.RenewUnit = renewalCandidate(ctx)
	}
	certs, err := readCertbotLineages(letsencryptDir)
	if err != nil {
		state.Error = err.Error()
	}
	state.Certs = certs
	return state
}

// certbotTimers are the units certbot's packages install, in the order the
// distributions are likely to be met.
var certbotTimers = []string{
	"certbot.timer",            // Debian, Ubuntu, Arch
	"certbot-renew.timer",      // Fedora, RHEL and the rest of the RPM world
	"snap.certbot.renew.timer", // the snap
}

// renewalCandidate finds a certbot timer that is installed but not running,
// so the page can offer to start it rather than only report that nothing
// will renew. systemd answers "not-found" for a unit it has never seen and
// "loaded" for one it could start.
func renewalCandidate(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, unit := range certbotTimers {
		out, err := hostexec.CommandOnHost(ctx, "systemctl", "show", "-p", "LoadState", "--value", unit).Output()
		if err == nil && strings.TrimSpace(string(out)) == "loaded" {
			return unit
		}
	}
	return ""
}

// renewalScheduled looks for whatever is meant to be renewing. certbot ships
// as a systemd timer on most distributions and as a cron entry on the rest,
// and a snap install has its own; all three are worth finding, because the
// answer the operator needs is yes or no rather than which.
func renewalScheduled(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, unit := range certbotTimers {
		out, err := hostexec.CommandOnHost(ctx, "systemctl", "is-active", unit).Output()
		if err == nil && strings.TrimSpace(string(out)) == "active" {
			return true, unit
		}
	}
	for _, path := range []string{
		"/etc/cron.d/certbot", "/etc/cron.daily/certbot", "/etc/cron.weekly/certbot",
		// Alpine has no systemd and no /etc/cron.daily either: busybox crond
		// runs /etc/periodic, and a host renewing perfectly well there used
		// to be told nothing was scheduled.
		"/etc/periodic/daily/certbot", "/etc/periodic/weekly/certbot",
	} {
		if _, err := os.Stat(path); err == nil {
			return true, path
		}
	}
	return false, ""
}

// IssueRequest is a certificate to obtain.
type IssueRequest struct {
	Domains []string `json:"domains"`
	Email   string   `json:"email"`
	// Method is how certbot proves control: nginx, webroot, standalone or dns.
	Method  string `json:"method"`
	WebRoot string `json:"webRoot,omitempty"`
	// DNSProvider names the certbot plugin for a DNS challenge. It is the
	// only method that can issue a wildcard, and the only one that works for
	// a domain behind a CDN — the request for an HTTP challenge never reaches
	// this host at all.
	DNSProvider string `json:"dnsProvider,omitempty"`
	// DNSWait overrides the provider's default propagation delay.
	DNSWait int `json:"dnsWait,omitempty"`
	// Credentials, when set, are the DNS provider's to save before the
	// challenge. They stand in for saved ones here and are written only once
	// the whole request has been accepted, so a refused request leaves no
	// token on disk.
	Credentials string `json:"credentials,omitempty"`
	// Staging asks for a test run: certbot's --dry-run, the whole exchange
	// against the staging authority with nothing saved and nothing counted
	// against the rate limit. It is the right first attempt for anybody who
	// has not done this before, because the real limit is five failures an
	// hour and it is easy to reach.
	Staging bool `json:"staging"`
	// Install lets certbot edit the nginx config to use the new certificate.
	// Off by default: this dashboard writes those files, and two things
	// editing the same file is how a site ends up with two ssl_certificate
	// directives.
	Install bool `json:"install"`
}

var (
	certDomainRe = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)
	emailRe      = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,190}\.[A-Za-z]{2,24}$`)
	certNameRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._*-]{0,126})$`)
)

// IssueArgs validates a request and returns the certbot argv it means.
//
// Split from running it because issuance is streamed now: the validation has
// to answer the request synchronously — a bad email or a wildcard over HTTP is
// a 400, not a job that fails a minute later — while the command itself
// belongs to a job that outlives the request.
func (s *Service) IssueArgs(ctx context.Context, req IssueRequest) ([]string, error) {
	if len(req.Domains) == 0 {
		return nil, fmt.Errorf("at least one domain is required")
	}
	if len(req.Domains) > 100 {
		return nil, fmt.Errorf("a certificate may cover at most 100 domains")
	}
	wildcard := false
	for _, d := range req.Domains {
		if !certDomainRe.MatchString(d) {
			return nil, fmt.Errorf("%q is not a valid domain name", d)
		}
		if strings.HasPrefix(d, "*.") {
			wildcard = true
		}
	}
	// Saying this before the attempt rather than relaying certbot's version of
	// it afterwards: "Wildcard domains are not supported by the HTTP-01
	// challenge" is accurate and tells nobody what to do instead.
	if wildcard && req.Method != "dns" {
		return nil, fmt.Errorf("a wildcard certificate can only be issued with a DNS challenge — Let's Encrypt will not sign one any other way")
	}
	if !emailRe.MatchString(req.Email) {
		return nil, fmt.Errorf("a contact email is required for the ACME account")
	}

	args := []string{}
	// A DNS challenge has no web server to install into, and a test run
	// installs nothing (certbot refuses --dry-run outside certonly and
	// renew): certonly is the only shape either takes.
	plugin := req.Method
	if req.Install && req.Method == "nginx" && !req.Staging {
		args = append(args, "--nginx")
	} else {
		args = append(args, "certonly")
		switch req.Method {
		case "nginx":
			args = append(args, "--nginx")
		case "webroot":
			if !absPathRe.MatchString(req.WebRoot) {
				return nil, fmt.Errorf("the webroot must be an absolute path")
			}
			args = append(args, "--webroot", "-w", req.WebRoot)
		case "standalone":
			args = append(args, "--standalone")
		case "dns":
			provider, ok := DNSProviderFor(req.DNSProvider)
			if !ok {
				return nil, fmt.Errorf("choose a DNS provider for the challenge")
			}
			if provider.Key != "route53" && !HasDNSCredentials(provider.Key) && strings.TrimSpace(req.Credentials) == "" {
				return nil, fmt.Errorf("%s has no credentials saved yet", provider.Name)
			}
			wait := req.DNSWait
			if wait <= 0 {
				wait = provider.DefaultWait
			}
			if wait > 3600 {
				return nil, fmt.Errorf("the propagation wait is too long")
			}
			plugin = provider.Plugin
			args = append(args, dnsIssueArgs(provider, wait)...)
		default:
			return nil, fmt.Errorf("method must be nginx, webroot, standalone or dns")
		}
	}
	// webroot and standalone are part of certbot itself; the nginx and DNS
	// plugins are packages the certbot that runs the job has or has not got.
	if plugin == "nginx" || req.Method == "dns" {
		rt, err := loadCertbotRuntime(ctx)
		if err != nil {
			return nil, err
		}
		if rt == nil {
			return nil, fmt.Errorf("certbot is not installed on this host")
		}
		if !rt.authenticators[plugin] {
			return nil, fmt.Errorf("the certbot that runs here (%s) has no %s plugin", rt.where(), plugin)
		}
	}
	args = append(args, "--non-interactive", "--agree-tos", "-m", req.Email,
		// Without this a re-run inside the renewal window fails rather than
		// quietly succeeding, which is the wrong answer for a button somebody
		// may press twice.
		"--keep-until-expiring")
	if req.Staging {
		// Not --staging: that wrote a real lineage holding an untrusted
		// certificate, left it where sites could name it, and made the real
		// issuance that followed a no-op — the lineage was not due.
		args = append(args, "--dry-run")
	} else if lineage, leaf, ok := lineageFor(letsencryptDir, req.Domains); ok && lineage.testCertificate(leaf) {
		// These names already have a test certificate from a staging
		// authority, and certbot keeps a lineage until it is due whatever
		// signed it. Replacing it is the point of asking for a real one.
		args = append(args, "--force-renewal")
	}
	args = append(args, acmeDirectory().certbotArgs()...)
	for _, d := range req.Domains {
		args = append(args, "-d", d)
	}
	return args, nil
}

// RenewArgs builds a renewal. DryRun runs the whole exchange against the
// staging authority and changes nothing, which is the only safe way to find
// out whether renewal will work before the day it has to.
func (s *Service) RenewArgs(name string, dryRun, force bool) ([]string, error) {
	if name != "" && !certNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid certificate name")
	}
	args := []string{"renew", "--non-interactive"}
	if name != "" {
		args = append(args, "--cert-name", name)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if force {
		// Let's Encrypt allows five duplicate certificates per week, and
		// forcing renewal is how people spend them; the UI says so before it
		// offers the switch.
		args = append(args, "--force-renewal")
	}
	return args, nil
}

// Revoke tells the authority the certificate is no longer to be trusted, and
// removes it. Irreversible in the sense that matters: the certificate cannot
// be un-revoked, and every client that has it will start refusing the site.
func (s *Service) RevokeArgs(name string) ([]string, error) {
	if !certNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid certificate name")
	}
	return []string{"revoke", "--non-interactive", "--cert-name", name, "--delete-after-revoke"}, nil
}

// lastMeaningfulLine picks the line worth putting in an error toast. certbot
// prints a paragraph and buries the reason near the end.
func lastMeaningfulLine(out string) string {
	lines := strings.Split(out, "\n")
	// Validation details precede the generic failure summary and help footer.
	var details []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Detail:") {
			details = append(details, strings.TrimSpace(strings.TrimPrefix(line, "Detail:")))
		}
	}
	if len(details) > 0 {
		return strings.Join(details, "; ")
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "-") ||
			strings.HasPrefix(line, "Ask for help or search for solutions") ||
			strings.HasPrefix(line, "See the logfile") || strings.HasPrefix(line, "Saving debug log") {
			continue
		}
		return line
	}
	return out
}
