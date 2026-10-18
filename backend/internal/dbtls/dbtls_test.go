package dbtls

import (
	"crypto/tls"
	"crypto/x509"
	"reflect"
	"strings"
	"testing"
	"time"
)

var epoch = time.Date(2026, time.October, 1, 9, 30, 0, 0, time.UTC)

func newTestCA(t *testing.T) (*CA, []byte, []byte) {
	t.Helper()
	certPEM, keyPEM, err := NewCA(epoch)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return ca, certPEM, keyPEM
}

func TestCAIsAConstrainedAuthority(t *testing.T) {
	_, certPEM, keyPEM := newTestCA(t)
	info, err := Inspect(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.NotAfter.Sub(epoch); got != CAValidity {
		t.Errorf("authority lasts %s, want %s", got, CAValidity)
	}
	cert, err := parseCertificate(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA || cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Errorf("authority must be a CA that signs no further CA: IsCA=%v MaxPathLen=%d", cert.IsCA, cert.MaxPathLen)
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("authority cannot sign certificates")
	}
	if strings.Contains(string(certPEM), "PRIVATE KEY") || !strings.Contains(string(keyPEM), "PRIVATE KEY") {
		t.Error("the certificate carries no key and the key file carries one")
	}
}

func TestLeafCarriesEveryNameAndVerifiesAgainstTheAuthority(t *testing.T) {
	ca, caPEM, _ := newTestCA(t)
	names := []string{"127.0.0.1", "::1", "localhost", "db-7.jd.internal", "jd-postgres", "203.0.113.9"}
	leaf, err := ca.Issue(names, epoch)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"127.0.0.1", "::1", "localhost", "db-7.jd.internal", "jd-postgres", "203.0.113.9"} {
		if err := Verifies(caPEM, leaf.CertPEM, name, epoch.Add(time.Hour)); err != nil {
			t.Errorf("a client dialling %s would refuse the certificate: %v", name, err)
		}
	}
	for _, name := range []string{"198.51.100.1", "db-8.jd.internal", "example.com"} {
		if err := Verifies(caPEM, leaf.CertPEM, name, epoch.Add(time.Hour)); err == nil {
			t.Errorf("the certificate was accepted for %s, which it does not name", name)
		}
	}
	want := []string{"127.0.0.1", "203.0.113.9", "::1", "db-7.jd.internal", "jd-postgres", "localhost"}
	if !reflect.DeepEqual(leaf.Names, want) {
		t.Errorf("names = %v, want %v", leaf.Names, want)
	}
	info, err := Inspect(leaf.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(info.Names, leaf.Names) || info.Serial != leaf.Serial {
		t.Errorf("inspecting what was issued gave %v / %s, issued %v / %s", info.Names, info.Serial, leaf.Names, leaf.Serial)
	}
}

func TestLeafLifetimeAndRenewalWindow(t *testing.T) {
	ca, _, _ := newTestCA(t)
	leaf, err := ca.Issue([]string{"127.0.0.1"}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if got := leaf.NotAfter.Sub(epoch); got != LeafValidity {
		t.Errorf("certificate lasts %s, want %s", got, LeafValidity)
	}
	if !leaf.NotBefore.Before(epoch) {
		t.Error("certificate starts after it was made, so a server whose clock runs slow would refuse it")
	}
	if NeedsRenewal(leaf.NotAfter, epoch) {
		t.Error("a new certificate is due for renewal")
	}
	if NeedsRenewal(leaf.NotAfter, leaf.NotAfter.Add(-RenewWindow-time.Hour)) {
		t.Error("renewed before the window opens")
	}
	if !NeedsRenewal(leaf.NotAfter, leaf.NotAfter.Add(-RenewWindow+time.Hour)) {
		t.Error("not renewed inside the window")
	}
	if !NeedsRenewal(leaf.NotAfter, leaf.NotAfter.Add(time.Hour)) {
		t.Error("an expired certificate is not due for renewal")
	}
}

func TestLeafNeverOutlivesTheAuthority(t *testing.T) {
	ca, _, _ := newTestCA(t)
	late := epoch.Add(CAValidity - 24*time.Hour)
	leaf, err := ca.Issue([]string{"127.0.0.1"}, late)
	if err != nil {
		t.Fatal(err)
	}
	if !leaf.NotAfter.Equal(ca.cert.NotAfter) {
		t.Errorf("certificate ends %s, after the authority's %s", leaf.NotAfter, ca.cert.NotAfter)
	}
	if _, err := ca.Issue([]string{"127.0.0.1"}, epoch.Add(CAValidity+time.Hour)); err != ErrCAExpired {
		t.Errorf("an expired authority signed: %v", err)
	}
}

func TestExpiredLeafIsRefusedByAVerifyingClient(t *testing.T) {
	ca, caPEM, _ := newTestCA(t)
	leaf, err := ca.Issue([]string{"127.0.0.1"}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verifies(caPEM, leaf.CertPEM, "127.0.0.1", leaf.NotAfter.Add(time.Minute)); err == nil {
		t.Error("an expired certificate verified")
	}
}

func TestLeafKeyPairsAndSpeaksTLS(t *testing.T) {
	ca, _, _ := newTestCA(t)
	leaf, err := ca.Issue([]string{"127.0.0.1"}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(leaf.CertPEM, leaf.KeyPEM); err != nil {
		t.Errorf("the key does not belong to the certificate: %v", err)
	}
	if _, err := tls.X509KeyPair(leaf.CombinedPEM(), leaf.CombinedPEM()); err != nil {
		t.Errorf("the combined file MongoDB reads is not usable: %v", err)
	}
	other, err := ca.Issue([]string{"127.0.0.1"}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if other.Serial == leaf.Serial || string(other.KeyPEM) == string(leaf.KeyPEM) {
		t.Error("two issues share a serial or a key")
	}
}

func TestLoadCARefusesAKeyThatIsNotTheAuthoritys(t *testing.T) {
	_, certPEM, _ := newTestCA(t)
	_, _, otherKey := newTestCA(t)
	if _, err := LoadCA(certPEM, otherKey); err == nil {
		t.Error("an authority loaded with somebody else's key")
	}
	if _, err := LoadCA(certPEM, []byte("nope")); err == nil {
		t.Error("an authority loaded from text that is not a key")
	}
	ca, _, _ := newTestCA(t)
	leaf, err := ca.Issue([]string{"localhost"}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCA(leaf.CertPEM, leaf.KeyPEM); err == nil {
		t.Error("a server certificate loaded as an authority")
	}
}

func TestNamesAreNormalisedAndValidated(t *testing.T) {
	got, err := Names([]string{" Localhost ", "127.0.0.1", "[::1]", "localhost", "DB-1.jd.internal", "127.0.0.1", ""})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1", "::1", "db-1.jd.internal", "localhost"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	for _, bad := range []string{"jd_postgres", "-leading.example", "has space", "exa*mple", "a..b"} {
		if _, err := Names([]string{"127.0.0.1", bad}); err == nil {
			t.Errorf("%q was accepted as a name", bad)
		}
	}
	if _, err := Names([]string{" ", ""}); err == nil {
		t.Error("an empty list of names was accepted")
	}
	if !SameNames([]string{"localhost", "127.0.0.1"}, []string{"127.0.0.1", "LOCALHOST"}) {
		t.Error("the same names in another order and case read as different")
	}
	if SameNames([]string{"127.0.0.1"}, []string{"127.0.0.1", "203.0.113.9"}) {
		t.Error("different names read as the same")
	}
	if SameNames([]string{"127.0.0.1"}, []string{"jd_x"}) {
		t.Error("an invalid list read as the same as a valid one")
	}
}

func TestIsHostName(t *testing.T) {
	for name, want := range map[string]bool{
		"db-7.jd.internal": true, "jd-postgres": true, "a": true, "jd_postgres": false, "": false, "a.": false,
		strings.Repeat("a", 64): false,
	} {
		if got := IsHostName(name); got != want {
			t.Errorf("IsHostName(%q) = %v, want %v", name, got, want)
		}
	}
}
