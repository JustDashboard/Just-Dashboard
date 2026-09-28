package proxysvc

import (
	"strings"
	"testing"
	"time"
)

func TestParseHSTS(t *testing.T) {
	if parseHSTS("") != nil {
		t.Error("an absent header should be nil, not a zero value")
	}
	h := parseHSTS("max-age=31536000; includeSubDomains; preload")
	if h.MaxAge != 31536000 || !h.IncludeSubDomains || !h.Preload {
		t.Fatalf("got %+v", h)
	}
	// A max-age of zero is an instruction to forget the policy, which is not
	// the same as not sending the header.
	if got := parseHSTS("max-age=0"); got == nil || got.MaxAge != 0 {
		t.Fatalf("max-age=0 = %+v", got)
	}
	if got := parseHSTS("nonsense"); got == nil || got.MaxAge != -1 {
		t.Fatalf("a header with no max-age should report -1, got %+v", got)
	}
}

func TestColonHex(t *testing.T) {
	if got := colonHex([]byte{0xde, 0xad, 0x0f}); got != "DE:AD:0F" {
		t.Fatalf("got %q", got)
	}
}

// The grade is the product making a claim about somebody's server, so every
// branch of it is pinned.

func goodScan() *TLSScan {
	return &TLSScan{
		Domain: "example.com", Reachable: true, Trusted: true, NameMatches: true,
		ChainComplete: true, OCSPStapled: true, KeyType: "ECDSA", KeyBits: 256,
		Negotiated: "TLS 1.3",
		Certificate: &Certificate{
			Name: "example.com", Domains: []string{"example.com"},
			DaysLeft: 60, NotBefore: time.Now().Add(-30 * 24 * time.Hour), NotAfter: time.Now().Add(60 * 24 * time.Hour),
		},
		Protocols: []ProtocolResult{
			{Name: "TLS 1.0", Status: "refused"},
			{Name: "TLS 1.1", Status: "refused"},
			{Name: "TLS 1.2", Status: "offered"},
			{Name: "TLS 1.3", Status: "offered"},
		},
		HTTP: &HTTPScan{
			Service: "http", StatusCode: 200, PlainRedirects: true, RedirectVerdict: "same-host",
			HSTS:    &HSTS{MaxAge: hstsStrongMaxAge, IncludeSubDomains: true},
			Headers: []HeaderCheck{{Name: "X-Frame-Options", Present: true, Level: "important"}},
		},
		Findings: []ScanFinding{},
	}
}

func TestGradeAPlus(t *testing.T) {
	scan := goodScan()
	grade(scan)
	if scan.Grade != "A+" {
		t.Fatalf("grade = %q with findings %+v", scan.Grade, scan.Findings)
	}
}

// HSTS and the plain-HTTP redirect are what separate a correct configuration
// from a complete one, and neither is a fault on its own.
func TestGradeADropsToAWithoutHSTS(t *testing.T) {
	scan := goodScan()
	scan.HTTP.HSTS = nil
	grade(scan)
	if scan.Grade != "A" {
		t.Fatalf("grade = %q", scan.Grade)
	}
	if !hasFinding(scan, "tls.no-hsts") {
		t.Error("the reason should be on the page")
	}
}

func TestGradeExpiredIsF(t *testing.T) {
	scan := goodScan()
	scan.Certificate.Expired = true
	grade(scan)
	if scan.Grade != "F" {
		t.Fatalf("grade = %q", scan.Grade)
	}
	if !hasFinding(scan, "tls.expired") {
		t.Error("no expiry finding")
	}
}

func TestGradeUntrustedIsF(t *testing.T) {
	scan := goodScan()
	scan.Trusted = false
	scan.TrustError = "x509: certificate signed by unknown authority"
	grade(scan)
	if scan.Grade != "F" {
		t.Fatalf("grade = %q", scan.Grade)
	}
}

func TestGradeNameMismatchIsF(t *testing.T) {
	scan := goodScan()
	scan.NameMatches = false
	grade(scan)
	if scan.Grade != "F" {
		t.Fatalf("grade = %q", scan.Grade)
	}
}

func TestGradeOldProtocolIsC(t *testing.T) {
	scan := goodScan()
	scan.Protocols[0].Status = "offered"
	grade(scan)
	if scan.Grade != "C" {
		t.Fatalf("grade = %q", scan.Grade)
	}
	if !hasFinding(scan, "tls.old-protocol.TLS 1.0") {
		t.Error("no finding naming the protocol")
	}
}

func TestGradeMissingIntermediateIsB(t *testing.T) {
	scan := goodScan()
	scan.ChainComplete = false
	grade(scan)
	if scan.Grade != "B" {
		t.Fatalf("grade = %q", scan.Grade)
	}
	if !hasFinding(scan, "tls.incomplete-chain") {
		t.Error("no chain finding")
	}
}

func TestGradeNoRedirectIsB(t *testing.T) {
	scan := goodScan()
	scan.HTTP.PlainRedirects = false
	scan.HTTP.RedirectChain = []RedirectHop{{URL: "http://example.com/", Status: 200}}
	grade(scan)
	if scan.Grade != "B" {
		t.Fatalf("grade = %q", scan.Grade)
	}
	if !hasFinding(scan, "tls.no-redirect") {
		t.Error("no redirect finding")
	}
}

// A host that refuses plain HTTP entirely has nothing to redirect, and
// reporting that as a fault would be wrong.
func TestGradeDoesNotPenaliseAClosedPort80(t *testing.T) {
	scan := goodScan()
	scan.HTTP.PlainRedirects = false
	scan.HTTP.PlainError = "connection refused"
	grade(scan)
	if hasFinding(scan, "tls.no-redirect") {
		t.Error("a closed port 80 is not a missing redirect")
	}
}

func TestGradeWeakRSAKeyIsF(t *testing.T) {
	scan := goodScan()
	scan.KeyType, scan.KeyBits = "RSA", 1024
	grade(scan)
	if scan.Grade != "F" {
		t.Fatalf("grade = %q", scan.Grade)
	}
}

// A 256-bit ECDSA key is stronger than a 2048-bit RSA one; comparing the bit
// counts across algorithms is the classic way to get this backwards.
func TestGradeDoesNotPenaliseSmallECDSAKeys(t *testing.T) {
	scan := goodScan()
	scan.KeyType, scan.KeyBits = "ECDSA", 256
	grade(scan)
	if hasFinding(scan, "tls.weak-key") {
		t.Error("a P-256 key flagged as weak")
	}
}

func TestGradeUnreachableIsF(t *testing.T) {
	scan := &TLSScan{Reachable: false, Findings: []ScanFinding{}}
	grade(scan)
	if scan.Grade != "F" {
		t.Fatalf("grade = %q", scan.Grade)
	}
}

func TestFindingsAreSortedWorstFirst(t *testing.T) {
	scan := goodScan()
	scan.Certificate.Expired = true
	scan.HTTP.HSTS = nil
	scan.ChainComplete = false
	grade(scan)
	if len(scan.Findings) < 3 {
		t.Fatalf("expected several findings, got %d", len(scan.Findings))
	}
	if scan.Findings[0].Level != "critical" {
		t.Fatalf("first finding is %q", scan.Findings[0].Level)
	}
}

// A version this client cannot ask for must not be reported as one the server
// does not offer: that is a false reassurance about exactly the versions that
// matter most.
func TestUnknownProtocolStatusIsNotTreatedAsRefused(t *testing.T) {
	scan := goodScan()
	scan.Protocols = []ProtocolResult{{Name: "TLS 1.3", Status: "unknown"}}
	grade(scan)
	if hasFinding(scan, "tls.no-13") {
		t.Error("reported TLS 1.3 as missing when it was never tested")
	}
}

func hasFinding(scan *TLSScan, id string) bool {
	for _, f := range scan.Findings {
		if f.ID == id || strings.HasPrefix(f.ID, id) {
			return true
		}
	}
	return false
}

type errString string

func (e errString) Error() string { return string(e) }
