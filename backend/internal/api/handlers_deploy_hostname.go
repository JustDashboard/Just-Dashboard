package api

import (
	"context"
	"encoding/hex"
	"fmt"
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
	// NameTaken answers, for the `name` this was asked with, whether a live
	// project already owns it. The name is unique in the schema, so without
	// this the collision was a refusal at the very end of the setup — after
	// the source, the detection, the configuration and the preflight.
	//
	// A pointer because "no" and "not asked" are different answers: asking
	// about a hostname is a certificate question and carries no claim about
	// project names, and `omitempty` on a bool would have made a free name
	// indistinguishable from that silence.
	NameTaken *bool `json:"nameTaken,omitempty"`
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

	name := r.URL.Query().Get("name")
	// Answered alongside the hostname because the page asks for both at the
	// same moment, and because the alternative — the schema's own UNIQUE
	// refusal — arrives at commit, after the whole setup has been filled in.
	taken := s.deploymentNameTaken(ctx, name)
	if base, certName := s.wildcardBase(ctx); base != "" {
		slug := s.suggestHostnameSlug(ctx, name, base)
		httpx.JSON(w, http.StatusOK, hostnameSuggestion{
			Hostname: slug + "." + base, Base: base, Covered: true, CertificateName: certName,
			Method: "wildcard", NameTaken: &taken,
			Detail: "Covered by the existing wildcard certificate for *." + base + ".",
		})
		return nil
	}

	address := firstIPv4(proxysvc.PublicAddresses())
	if address == "" {
		httpx.JSON(w, http.StatusOK, hostnameSuggestion{
			Method: "none", NameTaken: &taken,
			Detail: "This server has no globally routable address, so a public hostname cannot be generated. Enter a domain that already points here.",
		})
		return nil
	}
	base := strings.ReplaceAll(address, ".", "-") + ".sslip.io"
	slug := s.suggestHostnameSlug(ctx, name, base)
	suggestion := hostnameSuggestion{
		Hostname: slug + "." + base, Base: base, Method: "sslip", Address: address,
		NameTaken: &taken,
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

// hostnameSlug turns a deployment name into one DNS label. It carries no
// randomness of its own — suggestHostnameSlug appends the deterministic
// suffix that used to live here.
func hostnameSlug(name string) string {
	slug := hostnameSlugStripRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "app"
	}
	return slug
}

// maxHostnameSuffixAttempts bounds the counter suggestHostnameSlug appends
// when a deterministic suggestion collides with a domain already in managed
// use. Exhausting it needs a 24-bit HMAC collision or that many distinct
// projects sharing one name on one host — vanishingly unlikely either way.
const maxHostnameSuffixAttempts = 20

// hostnameSuffix derives the label suffix that used to be random bytes from
// an HMAC of the requested name (and, past the first attempt, a counter that
// disambiguates a suggestion already in use), so the same name always
// proposes the same hostname instead of a fresh one on every call.
func (s *Server) hostnameSuffix(name string, attempt int) string {
	data := []byte(strings.ToLower(strings.TrimSpace(name)))
	if attempt > 0 {
		data = append(data, []byte(fmt.Sprintf("#%d", attempt+1))...)
	}
	sum := s.Sealer.DeriveHMAC("deploy.hostname", data)
	return hex.EncodeToString(sum[:3])
}

// suggestHostnameSlug is hostnameSlug's public-facing form: the deterministic
// label plus a deterministic suffix, advanced by a counter only when the
// resulting hostname is already a managed domain of another project.
func (s *Server) suggestHostnameSlug(ctx context.Context, name, domainSuffix string) string {
	base := hostnameSlug(name)
	for attempt := 0; attempt < maxHostnameSuffixAttempts; attempt++ {
		candidate := base + "-" + s.hostnameSuffix(name, attempt)
		if !s.hostnameDomainTaken(ctx, candidate+"."+domainSuffix) {
			return candidate
		}
	}
	return base
}

// deploymentNameTaken reports whether a live project already answers to this
// name. It compares exactly the way the schema's UNIQUE constraint does —
// `deploy_projects.name` is BINARY-collated, and an archived project releases
// its name into `archived_name` — so the answer here and the refusal at commit
// can never disagree.
func (s *Server) deploymentNameTaken(ctx context.Context, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	var exists int
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT 1 FROM deploy_projects WHERE name = ? LIMIT 1`, name).Scan(&exists)
	return err == nil
}

// hostnameDomainTaken reports whether an active project already configured
// hostname as a managed domain.
func (s *Server) hostnameDomainTaken(ctx context.Context, hostname string) bool {
	var exists int
	err := s.Store.DB.QueryRowContext(ctx, `
		SELECT 1 FROM deploy_dependencies d
		  JOIN deploy_environments e ON e.id = d.environment_id
		  JOIN deploy_projects p ON p.id = e.project_id
		 WHERE d.kind = 'domain' AND p.archived_at = 0 AND lower(d.resource_id) = lower(?)
		 LIMIT 1`, hostname).Scan(&exists)
	return err == nil
}

func firstIPv4(addresses []string) string {
	for _, address := range addresses {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil && proxysvc.IsPublicAddress(ip) {
			return address
		}
	}
	return ""
}
