package proxysvc

import (
	"context"
	"crypto/tls"
	"slices"
	"strings"
	"testing"
)

func TestSuitesAreRatedByWhatTheyGiveAway(t *testing.T) {
	for _, c := range []struct {
		openssl string
		rating  string
		reasons string
	}{
		{"TLS_AES_128_GCM_SHA256", "strong", ""},
		{"ECDHE-ECDSA-AES128-GCM-SHA256", "strong", ""},
		{"DHE-RSA-CHACHA20-POLY1305", "strong", ""},
		{"ECDHE-RSA-AES128-SHA", "weak", "CBC mode"},
		{"AES128-GCM-SHA256", "weak", "no forward secrecy"},
		{"AES256-SHA", "weak", "no forward secrecy, CBC mode"},
		{"ECDHE-RSA-DES-CBC3-SHA", "weak", "64-bit block, CBC mode"},
		{"ECDHE-ECDSA-AES128-CCM8", "weak", "short tag"},
		{"ECDH-RSA-AES128-GCM-SHA256", "weak", "no forward secrecy"},
		{"RC4-SHA", "insecure", "RC4, broken"},
		{"DES-CBC-SHA", "insecure", "56-bit DES"},
		{"EXP-RC4-MD5", "insecure", "export grade, 40-bit"},
		{"NULL-SHA256", "insecure", "no encryption"},
		{"ADH-AES128-GCM-SHA256", "insecure", "no authentication"},
		{"AECDH-NULL-SHA", "insecure", "no encryption, no authentication"},
	} {
		var found *suiteInfo
		for i := range suiteCatalogue {
			if suiteCatalogue[i].openssl == c.openssl {
				found = &suiteCatalogue[i]
			}
		}
		if found == nil {
			t.Errorf("%s is not in the catalogue", c.openssl)
			continue
		}
		rating, reasons := rateSuite(*found)
		if rating != c.rating || strings.Join(reasons, ", ") != c.reasons {
			t.Errorf("%s = %s (%s), want %s (%s)", c.openssl, rating, strings.Join(reasons, ", "), c.rating, c.reasons)
		}
	}
}

func TestTheCatalogueHasEachSuiteOnce(t *testing.T) {
	ids, names := map[uint16]bool{}, map[string]bool{}
	for _, s := range suiteCatalogue {
		if ids[s.id] || names[s.openssl] {
			t.Errorf("0x%04x %s appears twice", s.id, s.openssl)
		}
		ids[s.id], names[s.openssl] = true, true
	}
	// Go's own suite names are IANA's, so the ones Go knows must agree.
	for _, s := range append(tls.CipherSuites(), tls.InsecureCipherSuites()...) {
		if mine, ok := suiteByID[s.ID]; ok && mine.name != s.Name {
			t.Errorf("0x%04x is %s here and %s in Go", s.ID, mine.name, s.Name)
		}
	}
}

func TestEachVersionIsOfferedOnlyWhatItCanUse(t *testing.T) {
	for _, id := range suitesFor(tls.VersionTLS13) {
		if id>>8 != 0x13 {
			t.Errorf("TLS 1.3 offered 0x%04x", id)
		}
	}
	tls10 := suitesFor(tls.VersionTLS10)
	if slices.Contains(tls10, 0xc02f) || slices.Contains(tls10, 0x1301) || !slices.Contains(tls10, 0xc013) {
		t.Errorf("TLS 1.0 offered %v", tls10)
	}
	if tls12 := suitesFor(tls.VersionTLS12); !slices.Contains(tls12, 0xc02f) || slices.Contains(tls12, 0x1301) {
		t.Errorf("TLS 1.2 offered %v", tls12)
	}
}

func proberFor(t *testing.T, addr string) *deepProber {
	t.Helper()
	p, err := newDeepProber(addr, "scan.test")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func suiteNames(v VersionSuites) []string {
	out := []string{}
	for _, s := range v.Suites {
		out = append(out, s.OpenSSL)
	}
	slices.Sort(out)
	return out
}

// A server limited to two suites lists exactly those two, and the versions it
// does not speak as refused.
func TestAServerLimitedToTwoSuitesListsThoseTwo(t *testing.T) {
	cert, _ := scanTestCert(t, 41, nil)
	addr := tlsListener(t, &tls.Config{
		Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
	}, func(*tls.Conn) {})
	p := proberFor(t, addr)
	ctx := context.Background()

	tls12 := p.listSuites(ctx, "TLS 1.2", tls.VersionTLS12)
	if tls12.Status != "accepted" || !tls12.Complete ||
		strings.Join(suiteNames(tls12), " ") != "ECDHE-ECDSA-AES128-GCM-SHA256 ECDHE-ECDSA-AES256-SHA" {
		t.Fatalf("TLS 1.2 = %+v", tls12)
	}
	if tls12.Order == "" {
		t.Errorf("two suites should say whose order picks: %+v", tls12)
	}
	for _, v := range []struct {
		name    string
		version uint16
	}{{"TLS 1.3", tls.VersionTLS13}, {"TLS 1.1", tls.VersionTLS11}, {"SSL 3.0", versionSSL30}} {
		if got := p.listSuites(ctx, v.name, v.version); got.Status != "refused" || len(got.Suites) != 0 || got.Detail == "" {
			t.Errorf("%s = %+v", v.name, got)
		}
	}
}

// TLS 1.3's suites cannot be chosen through Go's client, which offers all of
// its own; the deep scan's hello asks for them one by one. Go's server has the
// three that matter and no CCM.
func TestTLS13SuitesAreListed(t *testing.T) {
	cert, _ := scanTestCert(t, 42, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, func(*tls.Conn) {})
	got := proberFor(t, addr).listSuites(context.Background(), "TLS 1.3", tls.VersionTLS13)
	if got.Status != "accepted" || !got.Complete ||
		strings.Join(suiteNames(got), " ") != "TLS_AES_128_GCM_SHA256 TLS_AES_256_GCM_SHA384 TLS_CHACHA20_POLY1305_SHA256" {
		t.Fatalf("TLS 1.3 = %+v", got)
	}
}

// A server taking only X25519 refuses the post-quantum hybrid, and one on Go's
// defaults takes it and picks it when a browser offers it first.
func TestKeyExchangeGroupsAreAskedOneByOne(t *testing.T) {
	cert, _ := scanTestCert(t, 43, nil)
	status := func(groups []GroupResult) map[string]string {
		out := map[string]string{}
		for _, g := range groups {
			out[g.Name] = g.Status
		}
		return out
	}

	x25519 := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.X25519}}, func(*tls.Conn) {})
	groups, browser, err := proberFor(t, x25519).listGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := status(groups)
	for name, want := range map[string]string{
		"X25519MLKEM768": "refused", "SecP256r1MLKEM768": "refused", "X25519": "accepted",
		"P-256": "refused", "X448": "refused", "ffdhe2048": "refused",
	} {
		if got[name] != want {
			t.Errorf("X25519 only: %s = %s, want %s", name, got[name], want)
		}
	}
	if browser != "X25519" {
		t.Errorf("a browser's offer got %q", browser)
	}

	defaults := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, func(*tls.Conn) {})
	groups, browser, err = proberFor(t, defaults).listGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got = status(groups)
	for _, name := range []string{"X25519MLKEM768", "SecP256r1MLKEM768", "SecP384r1MLKEM1024", "X25519", "P-256", "P-384"} {
		if got[name] != "accepted" {
			t.Errorf("Go's defaults: %s = %s", name, got[name])
		}
	}
	if browser != "X25519MLKEM768" {
		t.Errorf("a browser's offer got %q", browser)
	}
}

// Without TLS 1.3, the key exchange a browser gets is TLS 1.2's curve, named
// in the ServerKeyExchange after the certificate.
func TestTheTLS12CurveABrowserGets(t *testing.T) {
	cert, _ := scanTestCert(t, 44, nil)
	config := &tls.Config{Certificates: []tls.Certificate{cert}, MaxVersion: tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.CurveP384}}
	addr := tlsListener(t, config, func(*tls.Conn) {})
	if got := proberFor(t, addr).tls12Curve(context.Background(), []uint16{0xc02b, 0xc02c}); got != "P-384" {
		t.Fatalf("got %q", got)
	}
	host, port := deepServer(t, config, false, "")
	d := DeepScanTLS(context.Background(), host, port, nil)
	if d.BrowserGroup != "P-384" || d.BrowserGroupVersion != "TLS 1.2" || len(d.Groups) != 0 {
		t.Fatalf("browser group %q in %q, groups %+v", d.BrowserGroup, d.BrowserGroupVersion, d.Groups)
	}
}
