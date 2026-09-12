package api

import (
	"regexp"
	"strings"
	"testing"
)

// The generated label is the deployment's public identity, so it has to be a
// legal DNS label whatever the operator called the deployment — and it has to
// differ between two deployments that were called the same thing.
func TestHostnameSlugIsALegalUniqueLabel(t *testing.T) {
	label := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	for _, name := range []string{
		"api-production", "We Smoke Fish", "  ", "Ünïcodé", "----",
		strings.Repeat("very-long-deployment-name", 8), "app.v2",
	} {
		slug := hostnameSlug(name)
		if !label.MatchString(slug) {
			t.Fatalf("hostnameSlug(%q) = %q, which is not a DNS label", name, slug)
		}
		if slug == hostnameSlug(name) {
			t.Fatalf("hostnameSlug(%q) repeated itself; two deployments would collide", name)
		}
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
