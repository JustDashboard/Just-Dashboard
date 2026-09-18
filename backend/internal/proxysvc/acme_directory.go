package proxysvc

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ACMEDirectory is the certificate authority certificates are asked from.
// Let's Encrypt unless JD_ACME_DIRECTORY names another ACME directory: the
// staging endpoint while an operator rehearses, a private authority such as
// Pebble or step-ca on a network that never sees the public internet. When
// that authority signs with its own roots, JD_ACME_CA_ROOT names a PEM bundle
// to trust: the roots that sign the certificates, and the root behind the
// directory's own TLS listener if that is private too.
//
// Read on each use rather than once: the values are configuration, and a
// test that points a Caddy at a throwaway authority must not have to restart
// the process to do it.
type ACMEDirectory struct {
	URL    string
	CARoot string
}

// dockerCaddyACMERoot is where the trust bundle lives inside a Caddy
// container this dashboard drives, beside its routes.
const dockerCaddyACMERoot = dockerCaddyRoot + "/acme-root.pem"

func acmeDirectory() ACMEDirectory {
	return ACMEDirectory{
		URL:    strings.TrimSpace(os.Getenv("JD_ACME_DIRECTORY")),
		CARoot: strings.TrimSpace(os.Getenv("JD_ACME_CA_ROOT")),
	}
}

// configured reports whether anything but the default authority was asked for.
func (d ACMEDirectory) configured() bool { return d.URL != "" }

// private reports an authority with its own trust roots: one no public DNS
// check can say anything about, and one the system's root store does not know.
func (d ACMEDirectory) private() bool { return d.CARoot != "" }

func (d ACMEDirectory) validate() error {
	if d.URL != "" {
		parsed, err := url.Parse(d.URL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || strings.ContainsAny(d.URL, " \t\r\n\"{}") {
			return fmt.Errorf("JD_ACME_DIRECTORY %q is not an ACME directory URL", d.URL)
		}
	}
	if d.CARoot != "" {
		if !filepath.IsAbs(d.CARoot) {
			return errors.New("JD_ACME_CA_ROOT must be an absolute path to a PEM bundle")
		}
		if _, err := d.roots(); err != nil {
			return err
		}
	}
	return nil
}

// roots is the pool an issued certificate is verified against: the bundle
// when one is configured, otherwise nil, which x509 reads as the system's.
func (d ACMEDirectory) roots() (*x509.CertPool, error) {
	if d.CARoot == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(d.CARoot)
	if err != nil {
		return nil, fmt.Errorf("JD_ACME_CA_ROOT: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("JD_ACME_CA_ROOT %s holds no PEM certificates", d.CARoot)
	}
	return pool, nil
}

// caddyIssuer is the tls block a Caddy route needs to ask the configured
// authority instead of Let's Encrypt; empty when the default applies.
func (d ACMEDirectory) caddyIssuer() string {
	if !d.configured() {
		return ""
	}
	block := "  tls {\n    issuer acme {\n      dir " + d.URL + "\n"
	if d.private() {
		block += "      trusted_roots " + dockerCaddyACMERoot + "\n"
	}
	return block + "    }\n  }\n"
}

// certbotArgs is the same choice for certbot: the directory to order from.
// Staging keeps its own flag, because certbot knows that endpoint by name.
func (d ACMEDirectory) certbotArgs(staging bool) []string {
	if !d.configured() || staging {
		return nil
	}
	return []string{"--server", d.URL}
}
