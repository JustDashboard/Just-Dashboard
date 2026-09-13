package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// A deployment's first public address should not be a form field.
//
// Nobody's first deploy owns a domain, and "publish it and then work out DNS"
// is where a working container stops being a working website. The answer is a
// hostname that already resolves to this server before anything is written:
// sslip.io answers any name whose left-hand labels encode an address with that
// address, so <slug>.<a-b-c-d>.sslip.io reaches this host with no record to
// create and nothing to wait for.
//
// A wildcard certificate, when one exists, is preferred over it. An operator
// who has bought a domain and issued *.example.com has already done the work
// this fallback exists to avoid, and their own name is the better answer.
//
// The certificate half is reported, never assumed. Activation refuses to move
// traffic onto an HTTPS route it cannot resolve an existing certificate for —
// that rule belongs to the Certificates feature and is not weakened here — so
// what this says is whether one already covers the name and, failing that,
// which challenge this host could issue one with. That turns "no certificate
// covers every deployment domain", discovered after a failed release, into a
// button pressed before one.

type hostnameSuggestion struct {
	Hostname string `json:"hostname"`
	Base     string `json:"base,omitempty"`
	// Covered is whether a certificate on this host already covers the name.
	// It is the only thing activation accepts as evidence.
	Covered         bool   `json:"covered"`
	CertificateName string `json:"certificateName,omitempty"`
	// CertificateMethod is the challenge this host could issue one with now, or
	// empty when it has no way to. "webroot" keeps nginx serving while
	// certbot writes challenge files; "standalone" needs port 80 free.
	CertificateMethod string `json:"certificateMethod,omitempty"`
	CertificateIssue  string `json:"certificateIssue,omitempty"`
	Method            string `json:"method"`
	Detail            string `json:"detail"`
	Address           string `json:"address,omitempty"`
}

var hostnameSlugStripRE = regexp.MustCompile(`[^a-z0-9-]+`)

func (s *Server) handleDeploymentHostname(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()

	// A hostname that is given is being asked about, not replaced: the operator
	// has typed their own domain and wants to know whether HTTPS will work.
	if chosen := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("hostname"))); chosen != "" {
		suggestion := hostnameSuggestion{Hostname: chosen, Method: "custom"}
		suggestion.Covered, suggestion.CertificateName = s.certificateCovering(ctx, chosen)
		suggestion.CertificateMethod, suggestion.CertificateIssue = s.certificateMethod(ctx)
		suggestion.Detail = certificateDetail(suggestion)
		httpx.JSON(w, http.StatusOK, suggestion)
		return nil
	}

	slug := hostnameSlug(r.URL.Query().Get("name"))
	if base, name := s.wildcardBase(ctx); base != "" {
		httpx.JSON(w, http.StatusOK, hostnameSuggestion{
			Hostname: slug + "." + base, Base: base, Covered: true, CertificateName: name,
			Method: "wildcard",
			Detail: "Covered by the existing wildcard certificate for *." + base + ".",
		})
		return nil
	}

	address := firstIPv4(proxysvc.PublicAddresses())
	if address == "" {
		httpx.JSON(w, http.StatusOK, hostnameSuggestion{
			Method: "none",
			Detail: "This server has no globally routable address, so a public hostname cannot be generated. Enter a domain that already points here.",
		})
		return nil
	}
	base := strings.ReplaceAll(address, ".", "-") + ".sslip.io"
	suggestion := hostnameSuggestion{
		Hostname: slug + "." + base, Base: base, Method: "sslip", Address: address,
	}
	suggestion.CertificateMethod, suggestion.CertificateIssue = s.certificateMethod(ctx)
	suggestion.Covered, suggestion.CertificateName = s.certificateCovering(ctx, suggestion.Hostname)
	suggestion.Detail = "Resolves to " + address + " with no DNS record to create. " +
		certificateDetail(suggestion)
	httpx.JSON(w, http.StatusOK, suggestion)
	return nil
}

func certificateDetail(suggestion hostnameSuggestion) string {
	switch {
	case suggestion.Covered:
		return "The " + suggestion.CertificateName + " certificate already covers it."
	case suggestion.CertificateMethod != "":
		return "No certificate covers it yet; one can be issued for it from here."
	case suggestion.CertificateIssue != "":
		return suggestion.CertificateIssue
	default:
		return "Automatic HTTP-01 is unavailable; check certbot and the port 80 listener, or provision a certificate before the release."
	}
}

// certificateCovering reports the first valid certificate that covers the name.
func (s *Server) certificateCovering(ctx context.Context, hostname string) (bool, string) {
	for _, certificate := range s.liveCertificates(ctx) {
		for _, name := range certificate.Domains {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == hostname {
				return true, certificate.Name
			}
			if base, ok := strings.CutPrefix(name, "*."); ok {
				prefix, found := strings.CutSuffix(hostname, "."+base)
				if found && prefix != "" && !strings.Contains(prefix, ".") {
					return true, certificate.Name
				}
			}
		}
	}
	return false, ""
}

// wildcardBase is the zone of the first wildcard certificate this host holds.
func (s *Server) wildcardBase(ctx context.Context) (string, string) {
	for _, certificate := range s.liveCertificates(ctx) {
		for _, name := range certificate.Domains {
			if base, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(name)), "*."); ok && base != "" {
				return base, certificate.Name
			}
		}
	}
	return "", ""
}

// liveCertificates is the host's certificates minus the ones that cannot serve
// anything: unreadable, or past their expiry.
func (s *Server) liveCertificates(ctx context.Context) []proxysvc.Certificate {
	if s.modules.proxy == nil {
		return nil
	}
	certificates, err := s.modules.proxy.ListCertificates(ctx)
	if err != nil {
		return nil
	}
	live := certificates[:0]
	for _, certificate := range certificates {
		if certificate.Error == "" && !certificate.Expired {
			live = append(live, certificate)
		}
	}
	return live
}

// certificateMethod names the HTTP-01 challenge this host can run today.
//
// Use the same plugin and listener evidence as the certificate executor.
func (s *Server) certificateMethod(ctx context.Context) (string, string) {
	if s.modules.proxy == nil {
		return "", "The certificate service is unavailable. Check the dashboard service status."
	}
	method, err := s.modules.proxy.DeploymentCertificateMethod(ctx)
	if err != nil {
		return "", err.Error()
	}
	return method, ""
}

// hostnameSlug turns a deployment name into one DNS label, and appends enough
// randomness that two deployments called "app" on the same server do not ask
// for the same public name.
func hostnameSlug(name string) string {
	slug := hostnameSlugStripRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "app"
	}
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return slug
	}
	return slug + "-" + hex.EncodeToString(buf)
}

func firstIPv4(addresses []string) string {
	for _, address := range addresses {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil && proxysvc.IsPublicAddress(ip) {
			return address
		}
	}
	return ""
}
