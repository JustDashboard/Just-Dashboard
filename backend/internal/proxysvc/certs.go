package proxysvc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Certificate struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Domains    []string  `json:"domains"`
	Issuer     string    `json:"issuer"`
	NotBefore  time.Time `json:"notBefore"`
	NotAfter   time.Time `json:"notAfter"`
	DaysLeft   int       `json:"daysLeft"`
	Expired    bool      `json:"expired"`
	Expiring   bool      `json:"expiring"`
	SelfSigned bool      `json:"selfSigned"`
	Source     string    `json:"source"`
	Error      string    `json:"error,omitempty"`
	// UsedBy names the nginx sites whose ssl_certificate points at this file,
	// which is the answer to the question the list used to leave open: what
	// breaks when this one expires.
	UsedBy []string `json:"usedBy"`
}

// expiryWarningDays matches Let's Encrypt's own renewal window: certbot
// renews at 30 days, so anything inside that window and still un-renewed is
// worth flagging.
const expiryWarningDays = 30

// ListCertificates reads certbot's live directory plus any certificate paths
// referenced by the proxy config, so a manually installed certificate is not
// invisible just because certbot does not know about it.
func (s *Service) ListCertificates(ctx context.Context) ([]Certificate, error) {
	return listCertificates(letsencryptLiveDir, importedDir, s.nginxVHosts()), nil
}

const letsencryptLiveDir = "/etc/letsencrypt/live"

// listCertificates is ListCertificates with its directories as arguments, so
// the join between certificates and the sites that use them can be tested
// without /etc.
func listCertificates(liveDir, imported string, vhosts []VHost) []Certificate {
	index := map[string]int{}
	out := []Certificate{}

	// The same file reached by two paths — certbot's symlink and the target
	// a site names directly — is one certificate, and the sites that name it
	// are gathered onto that one entry rather than producing a second.
	add := func(path, source string, usedBy string) {
		resolved := path
		if r, err := filepath.EvalSymlinks(path); err == nil {
			resolved = r
		}
		if i, ok := index[resolved]; ok {
			if usedBy != "" {
				out[i].UsedBy = append(out[i].UsedBy, usedBy)
			}
			return
		}
		index[resolved] = len(out)
		cert, err := readCertificate(path)
		if err != nil {
			cert = &Certificate{
				Name: certificateName(path), Path: path,
				Error: err.Error(), Domains: []string{},
			}
		}
		cert.Source = source
		cert.UsedBy = []string{}
		if usedBy != "" {
			cert.UsedBy = append(cert.UsedBy, usedBy)
		}
		out = append(out, *cert)
	}

	if entries, err := os.ReadDir(liveDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			add(filepath.Join(liveDir, e.Name(), "fullchain.pem"), "certbot", "")
		}
	}
	// Imported certificates live outside certbot's tree on purpose — a
	// renewal run must never be able to prune one it did not issue — which
	// means they have to be looked for separately or they would be invisible
	// until a vhost happened to reference one.
	if entries, err := os.ReadDir(imported); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add(filepath.Join(imported, e.Name(), "fullchain.pem"), "imported", "")
			}
		}
	}
	for _, v := range vhosts {
		if v.CertPath != "" {
			add(v.CertPath, "nginx:"+v.Name, v.Name)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DaysLeft != out[j].DaysLeft {
			return out[i].DaysLeft < out[j].DaysLeft
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// genericCertDirs are directory names that say where a certificate is kept
// rather than what it is for. certbot names the directory after the lineage,
// so its parent is the right name; a file dropped into /etc/nginx/ssl is
// better named after itself than after "ssl".
var genericCertDirs = map[string]bool{
	"ssl": true, "certs": true, "certificates": true, "tls": true, "pki": true,
	"private": true, "nginx": true, "conf.d": true, "etc": true, "": true,
}

// certificateName is the name a certificate shows under in the list.
func certificateName(path string) string {
	parent := filepath.Base(filepath.Dir(path))
	if genericCertDirs[strings.ToLower(parent)] || parent == "." || parent == "/" {
		base := filepath.Base(path)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return parent
}

func readCertificate(path string) (*Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("not a PEM certificate")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return summarise(parsed, certificateName(path), path), nil
}

func summarise(c *x509.Certificate, name, path string) *Certificate {
	cert := &Certificate{
		Name: name, Path: path,
		Domains:   append([]string{}, c.DNSNames...),
		Issuer:    c.Issuer.CommonName,
		NotBefore: c.NotBefore.UTC(),
		NotAfter:  c.NotAfter.UTC(),
		UsedBy:    []string{},
	}
	if len(cert.Domains) == 0 && c.Subject.CommonName != "" {
		cert.Domains = []string{c.Subject.CommonName}
	}
	if cert.Issuer == "" {
		cert.Issuer = c.Issuer.String()
	}
	cert.DaysLeft = int(time.Until(c.NotAfter).Hours() / 24)
	cert.Expired = time.Now().After(c.NotAfter)
	cert.Expiring = !cert.Expired && cert.DaysLeft <= expiryWarningDays
	cert.SelfSigned = c.Issuer.String() == c.Subject.String()
	return cert
}

// CheckDomain opens a TLS connection and reports what the domain is actually
// serving. Reading the file on disk is not enough: a certificate can be renewed
// on disk and never reloaded, and only a live handshake catches that.
func CheckDomain(ctx context.Context, domain string, port int) (*Certificate, error) {
	if port == 0 {
		port = 443
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(domain, fmt.Sprint(port)), &tls.Config{
		ServerName: domain,
		// The certificate is being inspected, not trusted — a failed
		// verification is a finding to report, not a reason to give up.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return &Certificate{Name: domain, Domains: []string{domain}, Source: "live", Error: err.Error()}, nil
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("%s presented no certificate", domain)
	}
	cert := summarise(state.PeerCertificates[0], domain, "")
	cert.Source = "live"
	// Verify against the system roots separately so the report can say
	// "valid but untrusted" rather than conflating the two.
	roots, _ := x509.SystemCertPool()
	if _, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName:       domain,
		Roots:         roots,
		Intermediates: intermediates(state.PeerCertificates),
	}); err != nil {
		cert.Error = err.Error()
	}
	return cert, nil
}

func intermediates(chain []*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, c := range chain[1:] {
		pool.AddCert(c)
	}
	return pool
}
