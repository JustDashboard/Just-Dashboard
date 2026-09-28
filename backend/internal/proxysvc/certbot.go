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
	// NotBefore is when the certificate was issued: the other end of the
	// meter, which a ninety-day guess drew wrong for any other term.
	NotBefore time.Time `json:"notBefore"`
	// How certbot renews it, from its renewal configuration: the plugin
	// that proves control ("nginx", "webroot", "dns-cloudflare"), the one
	// that deploys the result, the webroot folders, and the DNS provider by
	// name where this dashboard knows the plugin.
	Authenticator string   `json:"authenticator,omitempty"`
	Installer     string   `json:"installer,omitempty"`
	Webroots      []string `json:"webroots,omitempty"`
	DNSProvider   string   `json:"dnsProvider,omitempty"`
	// DeployHook is a hook of the lineage's own that certbot runs after
	// renewing it (--deploy-hook), and PostHook one it runs once the whole
	// renewal run is over (--post-hook); what they do is not read here.
	DeployHook bool `json:"deployHook,omitempty"`
	PostHook   bool `json:"postHook,omitempty"`
	// ServedBy are the enabled nginx sites whose certificate is this
	// lineage's: the ones left on the old certificate until nginx reloads.
	ServedBy []string `json:"servedBy,omitempty"`
	// WillFail is what will make the next renewal fail, each a certainty in
	// certbot's code, found before the run that would find it.
	WillFail []string `json:"willFail,omitempty"`
	// LastFailure is why the last renewal run failed on this lineage, when
	// it did and nothing has renewed it since.
	LastFailure *RenewalFailure `json:"lastFailure,omitempty"`
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
	// one of Let's Encrypt's, so the page can say whom a test run talks to:
	// that authority itself, not a staging one, and not under Let's
	// Encrypt's limits.
	Directory string `json:"directory,omitempty"`
	// TestAuthority is a configured directory that signs test certificates,
	// Let's Encrypt's staging one among them: a real issuance from here
	// brings back one browsers refuse, so the page offers no "real" one.
	TestAuthority bool `json:"testAuthority,omitempty"`
	// Health is what the renewal schedule did last and does next, when
	// there is one.
	Health *RenewalHealth `json:"health,omitempty"`
	// ReloadHook is the deploy hook that reloads nginx after a renewal.
	ReloadHook *RenewalHook `json:"reloadHook,omitempty"`
	// NginxReloads says whether the certbot here can reload nginx itself
	// after renewing a lineage installed with its nginx plugin.
	NginxReloads bool `json:"nginxReloads,omitempty"`
	// Runtime is the certbot all of the above was read from and every job
	// runs: which side, and the methods it can prove control with.
	Runtime CertbotRuntimeView `json:"runtime"`
	// Installs is, for each plugin this dashboard drives that the runtime
	// lacks, the package that brings it or why none can; filled by
	// FillInstalls, which needs the host's package manager.
	Installs map[string]CertbotInstall `json:"installs,omitempty"`
}

// CertbotInstall is how a missing plugin gets onto the host.
type CertbotInstall struct {
	Package string `json:"package,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// FillInstalls fills Installs for manager, the host's package manager.
func (state *CertbotState) FillInstalls(ctx context.Context, manager string) {
	have := map[string]bool{}
	for _, plugin := range state.Runtime.Plugins {
		have[plugin] = true
	}
	// With the list unreadable nothing is known to be missing.
	if state.Runtime.PluginsError != "" {
		return
	}
	plugins := []string{"nginx"}
	for _, p := range dnsProviders {
		plugins = append(plugins, p.Plugin)
	}
	state.Installs = map[string]CertbotInstall{}
	for _, plugin := range plugins {
		if have[plugin] {
			continue
		}
		pkg, err := CertbotPackage(ctx, manager, plugin)
		if err != nil {
			state.Installs[plugin] = CertbotInstall{Reason: err.Error()}
			continue
		}
		state.Installs[plugin] = CertbotInstall{Package: pkg}
	}
}

func (s *Service) CertbotState(ctx context.Context) *CertbotState {
	state := &CertbotState{Certs: []CertbotCert{}}
	rt, rtErr := loadCertbotRuntime(ctx)
	if rt == nil {
		return state
	}
	state.Available = true
	state.Version = rt.version
	state.Runtime = certbotRuntimeView(rt, rtErr)
	authority := CertbotAuthorityInUse()
	state.Directory, state.TestAuthority = authority.Directory, authority.Staging
	// Before the lineages, and whatever they say: whether anything renews
	// them is a separate question, and a lineage read that failed used to
	// return before it was asked — every certbot run read as "renewal off".
	state.AutoRenew, state.RenewSource = renewalScheduled(ctx)
	if !state.AutoRenew {
		state.RenewUnit = renewalCandidate(ctx)
	}
	state.NginxReloads = rt.installers["nginx"]
	if hook, err := RenewalHookStatus(); err == nil {
		state.ReloadHook = &hook
	}
	confs, err := readRenewalConfs(letsencryptDir)
	// When each lineage was last saved, which tells a failure a later
	// renewal fixed from one still standing; nil when that is unknown.
	var written map[string]time.Time
	if err != nil {
		state.Error = err.Error()
	} else {
		written = map[string]time.Time{}
		for _, conf := range confs {
			// A certificate that cannot be stat'ed is still a lineage: its
			// failure stands, as one nothing is known to have renewed.
			at, _ := conf.written()
			written[conf.Name] = at
		}
	}
	state.Certs = lineagesFrom(confs)
	problems := s.renewalProblems(ctx, rt, confs)
	names := make([]string, 0, len(state.Certs))
	for _, cert := range state.Certs {
		names = append(names, cert.Name)
	}
	served := s.lineageSites(names)
	for i := range state.Certs {
		state.Certs[i].WillFail = problems[state.Certs[i].Name]
		state.Certs[i].ServedBy = served[state.Certs[i].Name]
	}
	if state.AutoRenew {
		state.Health = renewalHealth(ctx, state.RenewSource, written)
		for _, failure := range state.Health.Failures {
			for i := range state.Certs {
				if state.Certs[i].Name == failure.Lineage && !failure.RenewedSince {
					state.Certs[i].LastFailure = &failure
				}
			}
		}
	}
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

// certbotCronFiles are where certbot's packages schedule it without systemd.
// Alpine has no systemd and no /etc/cron.daily either: busybox crond runs
// /etc/periodic, and a host renewing perfectly well there used to be told
// nothing was scheduled. A variable for tests.
var certbotCronFiles = []string{
	"/etc/cron.d/certbot", "/etc/cron.daily/certbot", "/etc/cron.weekly/certbot",
	"/etc/periodic/daily/certbot", "/etc/periodic/weekly/certbot",
}

// systemdRunDir exists when systemd is the init system (sd_booted). A
// variable for tests.
var systemdRunDir = "/run/systemd/system"

// systemdGuardRe is the test Debian's and Ubuntu's cron entry runs before
// certbot, `test -x /usr/bin/certbot -a \! -d /run/systemd/system`: the
// entry stands aside wherever systemd is init, for the timer to renew.
var systemdGuardRe = regexp.MustCompile(`(?m)^[^#\n]*\\?!\s*-d\s+/run/systemd/system\b`)

// renewalScheduled looks for whatever is meant to be renewing. certbot ships
// as a systemd timer on most distributions and as a cron entry on the rest,
// and a snap install has its own; all three are worth finding, because the
// answer the operator needs is yes or no rather than which.
//
// Debian and Ubuntu install both, and their cron entry does nothing on a host
// that runs systemd. With the timer stopped there, nothing renews, and the
// cron file is not a schedule.
func renewalScheduled(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, unit := range certbotTimers {
		out, err := hostexec.CommandOnHost(ctx, "systemctl", "is-active", unit).Output()
		if err == nil && strings.TrimSpace(string(out)) == "active" {
			return true, unit
		}
	}
	_, err := os.Stat(systemdRunDir)
	systemd := err == nil
	for _, path := range certbotCronFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			// There, but unreadable, is still a schedule.
			if _, statErr := os.Stat(path); statErr != nil {
				continue
			}
		}
		if systemd && systemdGuardRe.Match(content) {
			continue
		}
		return true, path
	}
	return false, ""
}

// IssueRequest is a certificate to obtain.
type IssueRequest struct {
	Domains []string `json:"domains"`
	// Email is the contact to register the ACME account with. Empty is
	// accepted only when certbot already has an account with the authority
	// this run orders from: certbot asks for an email only to register one.
	Email string `json:"email"`
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
	// DryRun has certbot run the whole order against the staging authority
	// and save nothing: the way to find out a challenge reaches this host
	// without spending one of the five failures an hour the real one allows.
	DryRun bool `json:"dryRun,omitempty"`
	// Install lets certbot edit the nginx config to use the new certificate.
	// Off by default: this dashboard writes those files, and two things
	// editing the same file is how a site ends up with two ssl_certificate
	// directives.
	Install bool `json:"install"`
	// KeyType is "ecdsa" or "rsa"; empty leaves certbot's default for a new
	// certificate and an existing one's own key type.
	KeyType string `json:"keyType,omitempty"`
	// RSAKeySize is 2048, 3072 or 4096, for an RSA key only; 2048 when empty.
	RSAKeySize int `json:"rsaKeySize,omitempty"`
	// CertName is certbot's name for the lineage: a new certificate's name,
	// or an existing one's to give these names instead of its own. That is
	// how names are added to a certificate — the domains replace its list —
	// without --expand, which fails unless the new list holds the old one.
	CertName string `json:"certName,omitempty"`
	// CA is the authority to order from: a key of ACMEAuthorities, "custom"
	// for Directory, or empty for what JD_ACME_DIRECTORY configures.
	CA        string `json:"ca,omitempty"`
	Directory string `json:"directory,omitempty"`
	// EABKeyID and EABHMACKey are an External Account Binding for
	// registering the account, saved sealed once the request is accepted.
	// They reach certbot in a temporary 0600 --config file, never argv.
	EABKeyID   string `json:"eabKeyId,omitempty"`
	EABHMACKey string `json:"eabHmacKey,omitempty"`
}

// IssuePlan is the argv an issuance runs and what it replaces, for the job
// to say and the preview to show.
type IssuePlan struct {
	Args []string `json:"args"`
	// ReplacesTestCertificate is these names' test certificate from a staging
	// authority being replaced by a real one.
	ReplacesTestCertificate bool `json:"replacesTestCertificate"`
	// ReplacesKeyOf names the lineage whose key is of another type or size
	// than the one asked for: certbot keeps a lineage until it is due, so the
	// new key is a forced renewal.
	ReplacesKeyOf string `json:"replacesKeyOf,omitempty"`
	// Authority is whom the run orders from.
	Authority IssueAuthority `json:"authority"`
}

var rsaKeySizes = []int{2048, 3072, 4096}

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
	plan, err := s.PlanIssue(ctx, req)
	return plan.Args, err
}

// PlanIssue is IssueArgs with what the argv replaces.
func (s *Service) PlanIssue(ctx context.Context, req IssueRequest) (IssuePlan, error) {
	args, plan, err := s.planIssue(ctx, req)
	plan.Args = args
	return plan, err
}

func (s *Service) planIssue(ctx context.Context, req IssueRequest) ([]string, IssuePlan, error) {
	plan := IssuePlan{}
	if len(req.Domains) == 0 {
		return nil, plan, fmt.Errorf("at least one domain is required")
	}
	if len(req.Domains) > 100 {
		return nil, plan, fmt.Errorf("a certificate may cover at most 100 domains")
	}
	wildcard := false
	for _, d := range req.Domains {
		if !certDomainRe.MatchString(d) {
			return nil, plan, fmt.Errorf("%q is not a valid domain name", d)
		}
		if strings.HasPrefix(d, "*.") {
			wildcard = true
		}
	}
	// Saying this before the attempt rather than relaying certbot's version of
	// it afterwards: "Wildcard domains are not supported by the HTTP-01
	// challenge" is accurate and tells nobody what to do instead.
	if wildcard && req.Method != "dns" {
		return nil, plan, fmt.Errorf("a wildcard certificate can only be issued with a DNS challenge — no ACME authority signs one any other way")
	}
	authority, err := authorityFor(req)
	if err != nil {
		return nil, plan, err
	}
	plan.Authority = authority
	if req.Email == "" {
		if !authority.Account {
			return nil, plan, fmt.Errorf("a contact email is required: certbot has no account with %s yet", authority.Server)
		}
	} else if !emailRe.MatchString(req.Email) {
		return nil, plan, fmt.Errorf("%q is not an email address the certificate authority will take", req.Email)
	}
	keyArgs, err := issueKeyArgs(req)
	if err != nil {
		return nil, plan, err
	}
	if req.CertName != "" && !certNameRe.MatchString(req.CertName) {
		return nil, plan, fmt.Errorf("a certificate name is letters, digits, dots, dashes and underscores")
	}

	args := []string{}
	// A DNS challenge has no web server to install into, and a test run
	// installs nothing (certbot refuses --dry-run outside certonly and
	// renew): certonly is the only shape either takes.
	plugin := req.Method
	if req.Install && req.Method == "nginx" && !req.Staging && !req.DryRun {
		args = append(args, "--nginx")
	} else {
		args = append(args, "certonly")
		switch req.Method {
		case "nginx":
			args = append(args, "--nginx")
		case "webroot":
			if !absPathRe.MatchString(req.WebRoot) {
				return nil, plan, fmt.Errorf("the webroot must be an absolute path")
			}
			if err := checkWebroot(ctx, req.WebRoot); err != nil {
				return nil, plan, err
			}
			args = append(args, "--webroot", "-w", req.WebRoot)
		case "standalone":
			args = append(args, "--standalone")
		case "dns":
			provider, ok := DNSProviderFor(req.DNSProvider)
			if !ok {
				return nil, plan, fmt.Errorf("choose a DNS provider for the challenge")
			}
			if provider.Key != "route53" && !HasDNSCredentials(provider.Key) && strings.TrimSpace(req.Credentials) == "" {
				return nil, plan, fmt.Errorf("%s has no credentials saved yet", provider.Name)
			}
			wait := req.DNSWait
			if wait <= 0 {
				wait = provider.DefaultWait
			}
			if wait > 3600 {
				return nil, plan, fmt.Errorf("the propagation wait is too long")
			}
			plugin = provider.Plugin
			args = append(args, dnsIssueArgs(provider, wait)...)
		default:
			return nil, plan, fmt.Errorf("method must be nginx, webroot, standalone or dns")
		}
	}
	// webroot and standalone are part of certbot itself; the nginx and DNS
	// plugins are packages the certbot that runs the job has or has not got.
	if plugin == "nginx" || req.Method == "dns" {
		rt, err := loadCertbotRuntime(ctx)
		if err != nil {
			return nil, plan, err
		}
		if rt == nil {
			return nil, plan, fmt.Errorf("certbot is not installed on this host")
		}
		if !rt.authenticators[plugin] {
			return nil, plan, fmt.Errorf("the certbot that runs here (%s) has no %s plugin", rt.where(), plugin)
		}
	}
	args = append(args, "--non-interactive", "--agree-tos")
	if req.Email != "" {
		args = append(args, "-m", req.Email)
	}
	// Without this a re-run inside the renewal window fails rather than
	// quietly succeeding, which is the wrong answer for a button somebody may
	// press twice.
	args = append(args, "--keep-until-expiring")
	args = append(args, keyArgs...)
	if req.CertName != "" {
		args = append(args, "--cert-name", req.CertName)
	}
	lineage, leaf, found := lineageFor(letsencryptDir, req.Domains)
	if req.KeyType != "" && found && certNameRe.MatchString(lineage.Name) && !keyMatches(leaf, req.KeyType, req.RSAKeySize) {
		// certbot refuses a key type other than the lineage's under
		// --non-interactive unless --cert-name names it too, and keeps the
		// lineage's old key until it is due unless the renewal is forced.
		if req.CertName == "" {
			args = append(args, "--cert-name", lineage.Name)
		}
		plan.ReplacesKeyOf = lineage.Name
	}
	if req.Staging || req.DryRun {
		// Not --staging: that wrote a real lineage holding an untrusted
		// certificate, left it where sites could name it, and made the real
		// issuance that followed a no-op — the lineage was not due.
		args = append(args, "--dry-run")
	} else if found && lineage.testCertificate(leaf) && !authority.Staging {
		// These names already have a test certificate from a staging
		// authority, and certbot keeps a lineage until it is due whatever
		// signed it. Replacing it is the point of asking for a real one —
		// unless the configured directory is a staging one too, when the
		// replacement would be another test certificate.
		args = append(args, "--force-renewal")
		plan.ReplacesTestCertificate = true
	} else if plan.ReplacesKeyOf != "" {
		args = append(args, "--force-renewal")
	}
	args = append(args, authority.certbotArgs()...)
	for _, d := range req.Domains {
		args = append(args, "-d", d)
	}
	return args, plan, nil
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

// DeleteArgs removes a lineage without revoking it: its live, archive and
// renewal files go, so the renewal schedule stops trying to renew it — an
// expired lineage for a domain that has moved elsewhere otherwise fails
// every run. The certificate itself stays valid until it expires.
func (s *Service) DeleteArgs(name string) ([]string, error) {
	if !certNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid certificate name")
	}
	return []string{"delete", "--non-interactive", "--cert-name", name}, nil
}

// CertificateInUseError refuses to delete a certificate an enabled nginx
// site names: nginx keeps serving what it read at its last reload, and its
// next configuration test fails on the missing file.
type CertificateInUseError struct {
	Name  string
	Sites []string
}

func (e *CertificateInUseError) Error() string {
	who := "those sites name"
	if len(e.Sites) == 1 {
		who = "that site names"
	}
	return fmt.Sprintf("%s is served by %s. Deleting it makes nginx's next reload fail until %s another certificate.",
		e.Name, strings.Join(e.Sites, ", "), who)
}

// LineageInUse refuses a lineage an enabled site names, unless force.
func (s *Service) LineageInUse(name string, force bool) error {
	sites := s.lineageSites([]string{name})[name]
	if len(sites) == 0 || force {
		return nil
	}
	return &CertificateInUseError{Name: name, Sites: sites}
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
