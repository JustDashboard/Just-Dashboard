package proxysvc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
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

// CertbotCert is one lineage as certbot itself describes it, which includes
// the renewal configuration the PEM files on disk do not reveal.
type CertbotCert struct {
	Name     string    `json:"name"`
	Domains  []string  `json:"domains"`
	Expiry   time.Time `json:"expiry"`
	DaysLeft int       `json:"daysLeft"`
	Valid    bool      `json:"valid"`
	CertPath string    `json:"certPath,omitempty"`
	KeyPath  string    `json:"keyPath,omitempty"`
	Serial   string    `json:"serial,omitempty"`
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
	Raw         string `json:"raw,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (s *Service) CertbotState(ctx context.Context) *CertbotState {
	state := &CertbotState{Certs: []CertbotCert{}}
	if !hostexec.Available("certbot") {
		return state
	}
	state.Available = true
	if out, err := hostexec.Command(ctx, "certbot", "--version").CombinedOutput(); err == nil {
		state.Version = strings.TrimSpace(string(out))
	}
	out, err := certbotRun(ctx, 60*time.Second, "certificates")
	if err != nil {
		state.Error = err.Error()
		state.Raw = out
		return state
	}
	state.Raw = out
	state.Certs = ParseCertbotCertificates(out)
	state.AutoRenew, state.RenewSource = renewalScheduled(ctx)
	return state
}

var (
	certbotNameRe   = regexp.MustCompile(`^\s*Certificate Name:\s*(\S+)`)
	certbotExpiryRe = regexp.MustCompile(`Expiry Date:\s*([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8})[^(]*\(([^)]*)\)`)
)

// ParseCertbotCertificates reads `certbot certificates`, whose output is a
// labelled block per lineage. Parsed by label rather than by position: the
// order of the lines has changed between certbot releases and the labels have
// not.
func ParseCertbotCertificates(out string) []CertbotCert {
	certs := []CertbotCert{}
	var current *CertbotCert
	flush := func() {
		if current != nil {
			certs = append(certs, *current)
			current = nil
		}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if m := certbotNameRe.FindStringSubmatch(line); m != nil {
			flush()
			current = &CertbotCert{Name: m[1], Domains: []string{}}
			continue
		}
		if current == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Domains:"):
			current.Domains = strings.Fields(strings.TrimPrefix(trimmed, "Domains:"))
		case strings.HasPrefix(trimmed, "Serial Number:"):
			current.Serial = strings.TrimSpace(strings.TrimPrefix(trimmed, "Serial Number:"))
		case strings.HasPrefix(trimmed, "Certificate Path:"):
			current.CertPath = strings.TrimSpace(strings.TrimPrefix(trimmed, "Certificate Path:"))
		case strings.HasPrefix(trimmed, "Private Key Path:"):
			current.KeyPath = strings.TrimSpace(strings.TrimPrefix(trimmed, "Private Key Path:"))
		case strings.HasPrefix(trimmed, "Expiry Date:"):
			if m := certbotExpiryRe.FindStringSubmatch(trimmed); m != nil {
				if t, err := time.Parse("2006-01-02 15:04:05", m[1]); err == nil {
					current.Expiry = t.UTC()
				}
				note := m[2]
				current.Valid = !strings.Contains(strings.ToUpper(note), "INVALID")
				current.DaysLeft = parseDaysLeft(note)
			}
		}
	}
	flush()
	return certs
}

// parseDaysLeft reads certbot's parenthetical, which is "VALID: 43 days" or
// "INVALID: EXPIRED".
func parseDaysLeft(note string) int {
	fields := strings.Fields(note)
	for i, f := range fields {
		if n, err := strconv.Atoi(f); err == nil && i+1 < len(fields) &&
			strings.HasPrefix(fields[i+1], "day") {
			return n
		}
	}
	return 0
}

// renewalScheduled looks for whatever is meant to be renewing. certbot ships
// as a systemd timer on most distributions and as a cron entry on the rest,
// and a snap install has its own; all three are worth finding, because the
// answer the operator needs is yes or no rather than which.
func renewalScheduled(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, unit := range []string{
		"certbot.timer",            // Debian, Ubuntu, Arch
		"certbot-renew.timer",      // Fedora, RHEL and the rest of the RPM world
		"snap.certbot.renew.timer", // the snap
	} {
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
	// Staging issues from Let's Encrypt's test authority, which is not
	// trusted by browsers and is not rate-limited. It is the right first
	// attempt for anybody who has not done this before, because the real
	// limit is five failures an hour and it is easy to reach.
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
func (s *Service) IssueArgs(req IssueRequest) ([]string, error) {
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
		return nil, fmt.Errorf("a contact email is required — it is where expiry warnings go")
	}

	args := []string{}
	// A DNS challenge has no web server to install into; certonly is the only
	// shape it takes.
	if req.Install && req.Method == "nginx" {
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
			if !provider.Installed && !certbotPluginInstalled(provider.Plugin) {
				return nil, fmt.Errorf("certbot's %s plugin is not installed on this host", provider.Plugin)
			}
			if provider.Key != "route53" && !HasDNSCredentials(provider.Key) {
				return nil, fmt.Errorf("%s has no credentials saved yet", provider.Name)
			}
			wait := req.DNSWait
			if wait <= 0 {
				wait = provider.DefaultWait
			}
			if wait > 3600 {
				return nil, fmt.Errorf("the propagation wait is too long")
			}
			args = append(args, dnsIssueArgs(provider, wait)...)
		default:
			return nil, fmt.Errorf("method must be nginx, webroot, standalone or dns")
		}
	}
	args = append(args, "--non-interactive", "--agree-tos", "-m", req.Email,
		// Without this a re-run inside the renewal window fails rather than
		// quietly succeeding, which is the wrong answer for a button somebody
		// may press twice.
		"--keep-until-expiring")
	if req.Staging {
		args = append(args, "--staging")
	}
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

func certbotRun(ctx context.Context, limit time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := hostexec.Command(ctx, "certbot", args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("certbot: %s", lastMeaningfulLine(text))
	}
	return text, nil
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
