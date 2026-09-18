package proxysvc

import (
	"net"
	"strings"
	"testing"
)

func TestCompareAddresses(t *testing.T) {
	here, proxied := compareAddresses([]string{"203.0.113.9"}, []string{"203.0.113.9", "2001:db8::1"})
	if !here || proxied {
		t.Fatalf("a matching address should point here: %v %v", here, proxied)
	}

	here, proxied = compareAddresses([]string{"198.51.100.4"}, []string{"203.0.113.9"})
	if here || proxied {
		t.Fatalf("a different address is neither: %v %v", here, proxied)
	}

	// Resolving to Cloudflare is not a misconfiguration, and reporting it as
	// one is the commonest false alarm a check like this produces.
	here, proxied = compareAddresses([]string{"104.16.1.1"}, []string{"203.0.113.9"})
	if here || !proxied {
		t.Fatalf("Cloudflare should be recognised: %v %v", here, proxied)
	}

	// A host behind a CDN that also resolves directly still points here, and
	// that answer wins.
	here, proxied = compareAddresses([]string{"104.16.1.1", "203.0.113.9"}, []string{"203.0.113.9"})
	if !here || proxied {
		t.Fatalf("a direct match should win: %v %v", here, proxied)
	}
}

func TestIsKnownProxyAddress(t *testing.T) {
	if !isKnownProxyAddress(net.ParseIP("104.16.1.1")) {
		t.Error("a Cloudflare v4 address not recognised")
	}
	if !isKnownProxyAddress(net.ParseIP("2606:4700::1")) {
		t.Error("a Cloudflare v6 address not recognised")
	}
	if isKnownProxyAddress(net.ParseIP("203.0.113.9")) {
		t.Error("an ordinary address recognised as Cloudflare")
	}
	if isKnownProxyAddress(nil) {
		t.Error("nil should not match")
	}
}

func TestDescribeDomainCheck(t *testing.T) {
	here := describeDomainCheck(&DomainCheck{PointsHere: true})
	if !strings.Contains(here, "this server") {
		t.Errorf("got %q", here)
	}
	cdn := describeDomainCheck(&DomainCheck{BehindProxy: true})
	if !strings.Contains(cdn, "Cloudflare") || !strings.Contains(cdn, "DNS challenge") {
		t.Errorf("the CDN case should explain what to do instead: %q", cdn)
	}
	elsewhere := describeDomainCheck(&DomainCheck{Addresses: []string{"198.51.100.4"}})
	if !strings.Contains(elsewhere, "198.51.100.4") {
		t.Errorf("got %q", elsewhere)
	}
	nothing := describeDomainCheck(&DomainCheck{})
	if !strings.Contains(nothing, "nothing") {
		t.Errorf("got %q", nothing)
	}
}

// A public DNS record can never point at a private address, so including one
// could only produce a false match.
func TestHostAddressesExcludesPrivateRanges(t *testing.T) {
	for _, addr := range hostAddresses() {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Errorf("%q is not an address", addr)
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			t.Errorf("%s should not be in the comparison set", addr)
		}
	}
}

func TestPublicAddressExcludesCGNAT(t *testing.T) {
	for _, address := range []string{
		"100.64.0.0", "100.110.34.31", "100.127.255.255", "::ffff:100.110.34.31",
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "169.254.1.1",
		"fd7a:115c:a1e0::1", "fe80::1", "::1", "0.0.0.0", "224.0.0.1", "invalid",
	} {
		if IsPublicAddress(net.ParseIP(address)) {
			t.Errorf("non-public address accepted: %s", address)
		}
	}
	for _, address := range []string{"100.63.255.255", "100.128.0.0", "8.8.8.8", "2606:4700::1111"} {
		if !IsPublicAddress(net.ParseIP(address)) {
			t.Errorf("public address rejected: %s", address)
		}
	}
}

// Every VPS behind provider NAT — AWS, Google Cloud, Azure and Oracle all hand
// the instance a private address and map a public one in front of it — has no
// routable address of its own to compare against. Reporting "does not point
// here" for a correctly configured domain there is a false alarm on the most
// common hosting arrangement there is, so the answer has to be that the check
// could not be made.
func TestDomainCheckSaysSoWhenTheHostHasNoPublicAddress(t *testing.T) {
	natted := &DomainCheck{
		Domain: "app.example.com", Addresses: []string{"203.0.113.9"},
		HostAddresses: []string{}, HostAddressesKnown: false,
	}
	summary := describeDomainCheck(natted)
	if strings.Contains(summary, "will fail") {
		t.Fatalf("reported a failure it cannot know about: %q", summary)
	}
	if !strings.Contains(summary, "no public address of its own") {
		t.Fatalf("did not say the check could not be made: %q", summary)
	}

	known := &DomainCheck{
		Domain: "app.example.com", Addresses: []string{"203.0.113.9"},
		HostAddresses: []string{"198.51.100.4"}, HostAddressesKnown: true,
	}
	if !strings.Contains(describeDomainCheck(known), "will fail") {
		t.Fatal("a host that does know its addresses should still report a mismatch")
	}
}
