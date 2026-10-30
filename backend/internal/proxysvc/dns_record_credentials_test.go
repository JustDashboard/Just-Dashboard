package proxysvc

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// What is saved for each provider is read back as it was saved, the list
// follows the page's order, and a provider with nothing saved says where to
// save it.
func TestSavedDNSCredentialsAreReadAsTheyWereSaved(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(UseCertificateDirsForTest(filepath.Join(dir, "letsencrypt"), filepath.Join(dir, "imported")))
	if keys := SavedDNSProviderKeys(); len(keys) != 0 {
		t.Fatalf("nothing saved yet, listed %v", keys)
	}
	for key, content := range map[string]string{
		"gandi":      "dns_gandi_token = pat",
		"cloudflare": "dns_cloudflare_api_token = token",
	} {
		if _, err := WriteDNSCredentials(key, content); err != nil {
			t.Fatal(err)
		}
	}
	if keys := SavedDNSProviderKeys(); !slices.Equal(keys, []string{"cloudflare", "gandi"}) {
		t.Fatalf("saved = %v", keys)
	}
	if content, err := SavedDNSCredentials("cloudflare"); err != nil || strings.TrimSpace(content) != "dns_cloudflare_api_token = token" {
		t.Fatalf("cloudflare = %q %v", content, err)
	}
	if _, err := SavedDNSCredentials("linode"); err == nil || !strings.Contains(err.Error(), certificatesPage) {
		t.Fatalf("nothing saved for Linode = %v", err)
	}
	if _, err := SavedDNSCredentials("namecheap"); err == nil {
		t.Fatal("a provider that is not one was read")
	}
}

// A zone's nameservers name its provider; two providers' at once name none.
func TestZoneHostedByReadsTheZonesNameservers(t *testing.T) {
	previous := deploymentNSLookup
	t.Cleanup(func() { deploymentNSLookup = previous })
	deploymentNSLookup = nameservers(map[string][]string{
		"example.com":  {"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"},
		"example.net":  {"ns1.digitalocean.com", "ada.ns.cloudflare.com"},
		"example.org":  {"ns1.example.org"},
		"split.co.uk":  {"ns-1.awsdns-01.co.uk"},
		"preview.test": {"ns-cloud-a1.googledomains.com"},
	}, nil)
	for name, want := range map[string]string{
		"app.example.com":    "cloudflare",
		"app.example.net":    "",
		"app.example.org":    "",
		"www.split.co.uk":    "route53",
		"pr-1.preview.test":  "google",
		"nothing.invalid.zz": "",
	} {
		if got := ZoneHostedBy(context.Background(), name); got != want {
			t.Errorf("ZoneHostedBy(%q) = %q, want %q", name, got, want)
		}
	}
}
