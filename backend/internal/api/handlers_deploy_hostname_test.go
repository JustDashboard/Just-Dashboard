package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The generated label is the deployment's public identity, so it has to be a
// legal DNS label whatever the operator called the deployment, and it has to
// be stable: a screen that re-fetches the same name must see the same label.
func TestHostnameSlugIsALegalStableLabel(t *testing.T) {
	label := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	for _, name := range []string{
		"api-production", "We Smoke Fish", "  ", "Ünïcodé", "----",
		strings.Repeat("very-long-deployment-name", 8), "app.v2",
	} {
		slug := hostnameSlug(name)
		if !label.MatchString(slug) {
			t.Fatalf("hostnameSlug(%q) = %q, which is not a DNS label", name, slug)
		}
		if slug != hostnameSlug(name) {
			t.Fatalf("hostnameSlug(%q) is not stable across calls: %q then %q", name, slug, hostnameSlug(name))
		}
	}
}

// suggestHostnameSlug must propose the same hostname every time it is asked
// about the same name, and must count past a suggestion another project has
// already claimed as a real managed domain instead of reusing it.
func TestSuggestHostnameSlugIsDeterministicAndAvoidsTakenDomains(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	const domainSuffix = "1-2-3-4.sslip.io"

	first := s.suggestHostnameSlug(ctx, "api-production", domainSuffix)
	second := s.suggestHostnameSlug(ctx, "api-production", domainSuffix)
	if first != second {
		t.Fatalf("suggestHostnameSlug is not deterministic: %q then %q", first, second)
	}
	wantFirst := hostnameSlug("api-production") + "-" + s.hostnameSuffix("api-production", 0)
	if first != wantFirst {
		t.Fatalf("suggestHostnameSlug = %q, want %q", first, wantFirst)
	}

	// Register the suggested hostname as a real managed domain of an active
	// project, the way committing a draft that kept it would.
	_, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	if _, err := s.Store.DB.Exec(`
		INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'domain', 'managed', 'proxy_site', ?, '{}', ?)`,
		environmentID, first+"."+domainSuffix, time.Now().UTC().Unix()); err != nil {
		t.Fatal(err)
	}

	third := s.suggestHostnameSlug(ctx, "api-production", domainSuffix)
	if third == first {
		t.Fatalf("suggestHostnameSlug reused a hostname already claimed by another project")
	}
	wantThird := hostnameSlug("api-production") + "-" + s.hostnameSuffix("api-production", 1)
	if third != wantThird {
		t.Fatalf("suggestHostnameSlug = %q, want %q (the next deterministic attempt)", third, wantThird)
	}
}

func TestFirstIPv4SkipsUnusableAddresses(t *testing.T) {
	if got := firstIPv4([]string{"100.110.34.31", "10.0.0.1", "8.8.8.8"}); got != "8.8.8.8" {
		t.Fatalf("firstIPv4 = %q, selected a private/VPN address ahead of the public address", got)
	}
	if got := firstIPv4([]string{"100.110.34.31", "192.168.1.1"}); got != "" {
		t.Fatalf("firstIPv4 = %q, want no public hostname for a private-only host", got)
	}
	if got := firstIPv4([]string{"2001:db8::1", "203.0.113.7"}); got != "203.0.113.7" {
		t.Fatalf("firstIPv4 = %q, want the IPv4 address", got)
	}
	if got := firstIPv4([]string{"not-an-address", "2001:db8::1"}); got != "" {
		t.Fatalf("firstIPv4 = %q, want no answer when there is no IPv4", got)
	}
}

// The detail line is what the operator reads instead of discovering after a
// failed release that activation wanted a certificate, so each of the three
// states has to say something different and actionable.
func TestCertificateDetailDistinguishesTheThreeStates(t *testing.T) {
	covered := certificateDetail(hostnameSuggestion{Covered: true, CertificateName: "example.com"})
	issuable := certificateDetail(hostnameSuggestion{CertificateMethod: "nginx"})
	neither := certificateDetail(hostnameSuggestion{})
	if !strings.Contains(covered, "example.com") {
		t.Fatalf("covered detail = %q, want it to name the certificate", covered)
	}
	if !strings.Contains(issuable, "can be issued") {
		t.Fatalf("issuable detail = %q, want it to offer issuance", issuable)
	}
	if !strings.Contains(neither, "certbot") {
		t.Fatalf("unavailable detail = %q, want it to name what is missing", neither)
	}
	if covered == issuable || issuable == neither {
		t.Fatal("certificate states are not distinguishable")
	}
}

func TestCertificateDetailPreservesChallengeFailure(t *testing.T) {
	reason := "port 80 is already used by caddy; configure challenge routing"
	if got := certificateDetail(hostnameSuggestion{CertificateIssue: reason}); got != reason {
		t.Fatalf("challenge error hidden by installation advice: %q", got)
	}
}

// A typed hostname is answered with the address its A record should name and,
// for an administrator, whether it already points here. localhost never does;
// a host without a public address of its own cannot compare at all, and a
// read-only account cannot make the server resolve a name of its choosing.
func TestTypedHostnameReportsWhetherItResolvesHere(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	ask := func(role auth.Role) hostnameSuggestion {
		t.Helper()
		caller := &client{t: t, h: routes, cookie: signInAs(t, s, "hostname-"+string(role), role)}
		response := caller.do(http.MethodGet, "/api/v1/deploy/hostname?hostname=localhost", "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("hostname as %s = %d %s", role, response.Code, response.Body.String())
		}
		var suggestion hostnameSuggestion
		if err := json.Unmarshal(response.Body.Bytes(), &suggestion); err != nil {
			t.Fatal(err)
		}
		if suggestion.Method != "custom" || suggestion.NameTaken != nil {
			t.Fatalf("suggestion as %s = %+v, want the custom answer", role, suggestion)
		}
		return suggestion
	}
	admin, reader := ask(auth.RoleAdmin), ask(auth.RoleReadOnly)
	if reader.Resolves != nil {
		t.Fatalf("read-only suggestion = %+v, want no DNS answer", reader)
	}
	address := firstIPv4(proxysvc.PublicAddresses())
	if reader.Address != address || admin.Address != address {
		t.Fatalf("addresses = %q / %q, want %q", admin.Address, reader.Address, address)
	}
	if address == "" {
		if admin.Resolves != nil {
			t.Fatalf("admin suggestion = %+v, want no comparison from a host without a public address", admin)
		}
	} else if admin.Resolves == nil || *admin.Resolves {
		t.Fatalf("admin suggestion = %+v, want a name that does not point here", admin)
	}
}
