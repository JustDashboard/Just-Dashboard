package selfcfg

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The one configuration in which a browser opening this dashboard sees an
// ordinary padlock and no warning.
//
// Everything else about reaching a private dashboard over TLS is a compromise.
// Caddy's internal CA encrypts perfectly well but nothing outside this machine
// has heard of the issuer, so every browser says "not secure" — which teaches
// the operator to click through certificate warnings, on the panel that has
// root on their server. Public ACME cannot help either: there is no public DNS
// name and no public address to validate against.
//
// Tailscale can, and it is the reason this file exists. A tailnet with HTTPS
// enabled gets Let's Encrypt certificates for its MagicDNS names through
// Tailscale's own DNS-01 validation, so `tailscale cert box.tailnet.ts.net`
// returns a genuinely trusted certificate for a name that resolves only inside
// the tailnet. The dashboard then answers on https://box.tailnet.ts.net:8443
// with no warning to click through and nothing to explain.
//
// The certificate is short-lived, which is what makes this a keeper rather
// than an install step: it is checked daily and renewed well before it runs
// out, and the proxy is restarted afterwards because Caddy reads a file-based
// certificate once, at start-up.

const (
	// CertFile and KeyFile live in a directory both the backend and the proxy
	// mount, under the names the Caddyfile expects.
	CertFile = "site.crt"
	KeyFile  = "site.key"
	// renewBefore is how much life a certificate must have left to be left
	// alone. Tailscale issues for 90 days; a month of slack means a machine
	// that is off for a fortnight still comes back to a valid certificate.
	renewBefore = 30 * 24 * time.Hour
	// checkInterval is deliberately not "once at boot". A dashboard that is
	// never restarted is the normal case for this product, and it is exactly
	// the install whose certificate would otherwise expire underneath it.
	checkInterval = 12 * time.Hour
	// firstRetry is the wait after a check that failed, doubling up to
	// maxRetry while it keeps failing. A tailnet that will not issue is
	// usually one switch in the admin console away from issuing, and the
	// operator who has just flipped it is looking at the browser warning: the
	// first re-attempt is a minute later, not twelve hours.
	firstRetry = time.Minute
	maxRetry   = 10 * time.Minute
)

// CertKeeper issues and renews the Tailscale certificate.
type CertKeeper struct {
	site string
	mode string
	dir  string
	// restartProxy is what makes a renewed certificate take effect. Caddy
	// loads a file-based certificate at start-up and does not watch the file,
	// so a renewal nothing acted on would be a renewal that changed nothing
	// until the next reboot.
	restartProxy func(ctx context.Context) error
	log          *slog.Logger

	stop context.CancelFunc
}

func NewCertKeeper(site, mode, dataDir string, restartProxy func(context.Context) error, log *slog.Logger) *CertKeeper {
	return &CertKeeper{
		site:         strings.TrimSpace(site),
		mode:         strings.ToLower(strings.TrimSpace(mode)),
		dir:          filepath.Join(dataDir, "certs"),
		restartProxy: restartProxy,
		log:          log,
	}
}

// Start keeps the certificate current for as long as ctx lives. It returns
// immediately: a dashboard must not refuse to boot because a tailnet was
// briefly unreachable, and the proxy has whatever certificate it was last
// given in the meantime.
func (k *CertKeeper) Start(ctx context.Context) {
	if k.mode != TLSTailscale {
		return
	}
	ctx, k.stop = context.WithCancel(ctx)
	go func() {
		retry := firstRetry
		for {
			wait := checkInterval
			if err := k.Ensure(ctx); err != nil {
				k.log.Warn("tailscale certificate not renewed", "site", k.site, "error", err, "retry", retry)
				wait, retry = retry, min(retry*2, maxRetry)
			} else {
				retry = firstRetry
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

func (k *CertKeeper) Stop() {
	if k.stop != nil {
		k.stop()
	}
}

// Ensure issues the certificate when there is not a healthy one already.
func (k *CertKeeper) Ensure(ctx context.Context) error {
	if k.mode != TLSTailscale {
		return nil
	}
	if k.site == "" || !strings.HasSuffix(strings.ToLower(k.site), ".ts.net") {
		return fmt.Errorf("JD_TLS is %q but JD_SITE (%q) is not a MagicDNS name, "+
			"so no certificate can be issued for it", k.mode, k.site)
	}
	if fresh, until := k.healthy(); fresh {
		k.log.Debug("tailscale certificate is current", "site", k.site, "expires", until)
		return nil
	}
	if err := Issue(ctx, k.site, k.dir); err != nil {
		return err
	}
	k.log.Info("tailscale certificate issued", "site", k.site, "dir", k.dir)
	if k.restartProxy == nil {
		return nil
	}
	if err := k.restartProxy(ctx); err != nil {
		return fmt.Errorf("the certificate was issued but the proxy could not be restarted to load it: %w", err)
	}
	return nil
}

// healthy reports whether the certificate on disk covers this site and has
// enough life left.
func (k *CertKeeper) healthy() (bool, time.Time) {
	cert, err := readCertificate(filepath.Join(k.dir, CertFile))
	if err != nil {
		return false, time.Time{}
	}
	if err := cert.VerifyHostname(k.site); err != nil {
		return false, cert.NotAfter
	}
	return time.Until(cert.NotAfter) > renewBefore, cert.NotAfter
}

// Issue runs `tailscale cert` on the host and writes the pair into dir.
//
// On the host, not in this container: tailscaled's socket is the host's, and
// the dashboard's own image deliberately does not carry a Tailscale client.
// The paths are the host's too, which is why the certificate directory is
// mounted at the same absolute path on both sides.
func Issue(ctx context.Context, site, dir string) error {
	if !hostexec.Available("tailscale") {
		return errors.New("tailscale is not installed on this host, so it cannot issue a certificate")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := hostexec.CommandOnHost(ctx, "tailscale", "cert",
		"--cert-file", filepath.Join(dir, CertFile),
		"--key-file", filepath.Join(dir, KeyFile),
		site)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		// Tailscale words the "HTTPS is switched off for this tailnet" refusal
		// as being about the account, which sends an operator to their billing
		// page for something that is one switch in the admin console. Say the
		// switch instead, in the same sentence the settings page uses.
		if strings.Contains(detail, "does not support getting TLS certs") {
			return fmt.Errorf("HTTPS is not enabled for this tailnet, so Tailscale will not issue a certificate "+
				"for %s. Turn it on at https://login.tailscale.com/admin/dns — it is one switch. Until then the "+
				"same address works with a self-signed certificate", site)
		}
		return fmt.Errorf("tailscale cert %s: %s", site, detail)
	}
	// The proxy runs as a different user inside its container and only ever
	// reads these, so the key is readable by root alone and the certificate by
	// anyone who can reach the directory.
	_ = os.Chmod(filepath.Join(dir, CertFile), 0o644)
	_ = os.Chmod(filepath.Join(dir, KeyFile), 0o640)
	return nil
}

// CertState is what is actually on disk for the configured address.
//
// It exists because JD_TLS=tailscale is a request, not a fact. The proxy falls
// back to Caddy's internal CA when there is no certificate to serve, so the
// dashboard answers either way — and a settings page that read the setting and
// announced "trusted" would be printing the one word the padlock disagrees
// with. This reads the certificate instead.
type CertState struct {
	Issued  bool       `json:"issued"`
	Expires *time.Time `json:"expires,omitempty"`
}

// Certificate reports whether a certificate covering site is on disk.
func Certificate(dataDir, site string) CertState {
	cert, err := readCertificate(filepath.Join(dataDir, "certs", CertFile))
	if err != nil || cert.VerifyHostname(site) != nil {
		return CertState{}
	}
	return CertState{Issued: true, Expires: &cert.NotAfter}
}

// DropStaleCertificate removes a certificate that does not cover site.
//
// The proxy decides between a real certificate and its internal CA by whether
// the files exist, so a certificate left over from the previous address would
// be served under the new name — a browser error about the wrong host, which
// is worse than the self-signed warning it replaced.
func DropStaleCertificate(dataDir, site string) {
	if Certificate(dataDir, site).Issued {
		return
	}
	dir := filepath.Join(dataDir, "certs")
	_ = os.Remove(filepath.Join(dir, CertFile))
	_ = os.Remove(filepath.Join(dir, KeyFile))
}

func readCertificate(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for len(b) > 0 {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(block.Bytes)
	}
	return nil, errors.New("no certificate in " + path)
}

// TailnetCIDR is the range every tailnet address falls in, and therefore the
// allowlist entry a Tailscale install needs.
const TailnetCIDR = "100.64.0.0/10"

// Identity is what this machine is on its tailnet, as far as the dashboard can
// tell by asking.
//
// It exists because the settings page was asking the operator for facts the
// machine already knew. Switching to a Tailscale certificate needs the MagicDNS
// name (which is on the certificate), the tailnet IP (which is what the proxy
// binds, since this container resolves names through Docker's resolver rather
// than the host's) and the tailnet range in the allowlist. Every one of those
// is one `tailscale status` away, and making somebody find them by hand — then
// rejecting the form when they guessed — is the dashboard refusing to do its
// own job.
type Identity struct {
	// Available is whether a Tailscale client exists on this host at all.
	Available bool `json:"available"`
	// Running is whether it is signed in and up. A stopped or logged-out
	// client can report a name it is not currently reachable at.
	Running bool   `json:"running"`
	State   string `json:"state,omitempty"`
	// Hostname is the MagicDNS name with the trailing dot removed, because
	// nobody types a DNS-correct FQDN into a browser.
	Hostname string `json:"hostname,omitempty"`
	IP4      string `json:"ip4,omitempty"`
	// HTTPSEnabled is whether the tailnet will issue certificates. It is read
	// from CertDomains, which is Tailscale's own answer to "may this node ask
	// for a certificate" — so the dashboard can say the switch is off *before*
	// an apply fails on it, rather than after.
	HTTPSEnabled bool `json:"httpsEnabled"`
	// Detail is why this cannot be used, in a sentence meant for the person
	// reading it.
	Detail string `json:"detail,omitempty"`
}

// Usable reports whether a Tailscale address can be filled in from this.
func (i Identity) Usable() bool { return i.Running && i.Hostname != "" }

// tailscaleStatus is the part of `tailscale status --json` this needs.
type tailscaleStatus struct {
	BackendState string `json:"BackendState"`
	CertDomains  []string
	Self         struct {
		DNSName      string
		TailscaleIPs []string
	}
}

// DetectTailscale asks the host what it is on its tailnet.
//
// On the host rather than in this container: tailscaled's socket is the host's,
// and this image deliberately carries no Tailscale client.
func DetectTailscale(ctx context.Context) Identity {
	if !hostexec.Available("tailscale") {
		return Identity{Detail: "Tailscale is not installed on this host."}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := hostexec.CommandOnHost(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return Identity{Available: true, Detail: "Tailscale is installed but did not answer: " + err.Error()}
	}
	return parseTailscaleStatus(out)
}

// parseTailscaleStatus is DetectTailscale without the subprocess, so the part
// that can silently break — reading fields out of somebody else's JSON — is
// testable against a fixture rather than against whatever this machine's
// tailnet happens to be doing today.
func parseTailscaleStatus(raw []byte) Identity {
	var st tailscaleStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return Identity{Available: true, Detail: "Tailscale's status could not be read: " + err.Error()}
	}

	id := Identity{
		Available: true,
		State:     st.BackendState,
		Running:   st.BackendState == "Running",
		// The trailing dot is DNS-correct and nobody types it into a browser.
		Hostname: strings.TrimSuffix(strings.TrimSpace(st.Self.DNSName), "."),
		// CertDomains is Tailscale's own answer to "may this node ask for a
		// certificate", which is exactly the question an operator is about to
		// find out the hard way.
		HTTPSEnabled: len(st.CertDomains) > 0,
	}
	for _, addr := range st.Self.TailscaleIPs {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
			id.IP4 = ip.String()
			break
		}
	}
	switch {
	case !id.Running:
		id.Detail = "This machine is not connected to a tailnet (" + st.BackendState + "). Run `tailscale up` on the host."
	case id.Hostname == "":
		id.Detail = "This machine has no MagicDNS name, so no certificate can be issued for it."
	case !id.HTTPSEnabled:
		id.Detail = "HTTPS is not enabled for this tailnet, so Tailscale will not issue a certificate for " +
			id.Hostname + " yet. Turn it on at https://login.tailscale.com/admin/dns — it is one switch."
	}
	return id
}

// WithTailnet returns the allowlist with the tailnet range added, if it is not
// already covered. Adding rather than replacing: the existing entries are the
// operator's, and a mode switch has no business deciding they were wrong.
func WithTailnet(list string) string {
	if nets, err := parseCIDRList(list); err == nil {
		if containsIP(nets, net.ParseIP("100.64.0.1")) {
			return list
		}
	}
	list = strings.TrimSpace(list)
	if list == "" {
		return TailnetCIDR
	}
	return TailnetCIDR + "," + list
}
