package proxysvc

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ocsp"
)

// ChainAudit is what the chain as sent says beyond whether it is trusted:
// the faults a verifier forgives, or that only some clients refuse.
type ChainAudit struct {
	// Order is "ordered" (each certificate followed by its issuer),
	// "out-of-order" (every issuer is there, not after what it signed) or
	// "extra" (a certificate that issues nothing in the chain was sent).
	Order string `json:"order"`
	// RootSent is a self-signed root sent after the leaf. Clients use their
	// own copy, so it is bytes on every handshake and nothing else.
	RootSent bool `json:"rootSent"`
	// SHA1 names the links signed with SHA-1. A root's signature on itself is
	// left out: nothing checks it.
	SHA1 []string `json:"sha1,omitempty"`
	// LeafDays is the leaf's validity in whole days; TooLong is more than 398
	// for a publicly trusted leaf issued since 1 September 2020, which Apple's
	// platforms refuse and no public authority may issue.
	LeafDays int  `json:"leafDays"`
	TooLong  bool `json:"tooLong"`
	// MustStaple is the leaf's TLS Feature extension asking for status_request:
	// a client that honours it (Firefox) refuses a handshake without a staple.
	MustStaple bool `json:"mustStaple"`
	// SCTs counts the Certificate Transparency timestamps received, and
	// SCTSources where from: "certificate", "tls" (the handshake extension)
	// or "ocsp" (the stapled answer).
	SCTs       int      `json:"scts"`
	SCTSources []string `json:"sctSources,omitempty"`
	// ServerAuth is false only for a leaf whose extended key usage is set and
	// leaves out serverAuth; no extension at all means any purpose.
	ServerAuth bool `json:"serverAuth"`
}

// TrustLink is one step of the path a verifier built from the leaf to a root
// in this machine's store. Sent says whether the server supplied it or the
// store did.
type TrustLink struct {
	Subject  string    `json:"subject"`
	NotAfter time.Time `json:"notAfter"`
	Sent     bool      `json:"sent"`
	Root     bool      `json:"root"`
}

var (
	oidTLSFeature   = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 24}
	oidSCTList      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
	oidOCSPSCTList  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 5}
	tlsStatusReq    = 5
	longValiditySet = time.Date(2020, time.September, 1, 0, 0, 0, 0, time.UTC)
)

// maxPublicLeafDays is the CA/Browser Forum's ceiling since September 2020.
const maxPublicLeafDays = 398

// auditChain reads the chain as sent. It is run after describeChain, whose
// Trusted it needs: the lifetime and transparency rules are the public
// roots', and say nothing of a private authority.
func auditChain(scan *TLSScan, chain []*x509.Certificate) {
	if len(chain) == 0 {
		return
	}
	leaf := chain[0]
	audit := &ChainAudit{Order: chainOrder(chain), ServerAuth: servesTLS(leaf)}
	last := chain[len(chain)-1]
	audit.RootSent = len(chain) > 1 && last.IsCA && bytes.Equal(last.RawIssuer, last.RawSubject)
	for _, c := range chain {
		selfSigned := bytes.Equal(c.RawIssuer, c.RawSubject)
		if !selfSigned && isSHA1(c.SignatureAlgorithm) {
			audit.SHA1 = append(audit.SHA1, nameOf(c.Subject.CommonName, c.Subject.String()))
		}
	}
	validity := leaf.NotAfter.Sub(leaf.NotBefore)
	audit.LeafDays = int(validity / (24 * time.Hour))
	audit.TooLong = scan.Trusted && !leaf.NotBefore.Before(longValiditySet) &&
		validity > maxPublicLeafDays*24*time.Hour
	audit.MustStaple = mustStaple(leaf)
	if n := sctsIn(leaf.Extensions, oidSCTList); n > 0 {
		audit.SCTs += n
		audit.SCTSources = append(audit.SCTSources, "certificate")
	}
	scan.ChainAudit = audit
}

// auditHandshake adds what the handshake carried besides the certificates:
// SCTs sent in the TLS extension and inside the stapled OCSP answer.
func auditHandshake(audit *ChainAudit, tlsSCTs [][]byte, staple []byte, leaf, issuer *x509.Certificate) {
	if audit == nil {
		return
	}
	if len(tlsSCTs) > 0 {
		audit.SCTs += len(tlsSCTs)
		audit.SCTSources = append(audit.SCTSources, "tls")
	}
	if len(staple) == 0 || issuer == nil {
		return
	}
	resp, err := ocsp.ParseResponseForCert(staple, leaf, issuer)
	if err != nil {
		return
	}
	if n := sctsIn(resp.Extensions, oidOCSPSCTList); n > 0 {
		audit.SCTs += n
		audit.SCTSources = append(audit.SCTSources, "ocsp")
	}
}

// chainOrder compares each certificate's issuer with the one sent after it.
func chainOrder(chain []*x509.Certificate) string {
	ordered := true
	for i := 0; i < len(chain)-1; i++ {
		if !bytes.Equal(chain[i].RawIssuer, chain[i+1].RawSubject) {
			ordered = false
		}
	}
	if ordered {
		return "ordered"
	}
	for j, c := range chain[1:] {
		issues := false
		for i, other := range chain {
			if i != j+1 && bytes.Equal(other.RawIssuer, c.RawSubject) {
				issues = true
			}
		}
		if !issues {
			return "extra"
		}
	}
	return "out-of-order"
}

// issuerOf finds the certificate that signed the leaf: the verified path's
// next step, or failing a path, one the server sent that checks.
func issuerOf(chain []*x509.Certificate) *x509.Certificate {
	if len(chain) == 0 {
		return nil
	}
	leaf := chain[0]
	if path := verifiedPath(chain); len(path) > 1 {
		return path[1]
	}
	for _, c := range chain[1:] {
		if bytes.Equal(leaf.RawIssuer, c.RawSubject) && leaf.CheckSignatureFrom(c) == nil {
			return c
		}
	}
	return nil
}

// verifiedPath is the first path from the leaf to a root in this machine's
// store, whatever name the leaf is for: a name mismatch is its own finding,
// and should not hide whose chain it is.
func verifiedPath(chain []*x509.Certificate) []*x509.Certificate {
	roots, _ := x509.SystemCertPool()
	paths, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates(chain)})
	if err != nil || len(paths) == 0 {
		return nil
	}
	return paths[0]
}

func trustPath(chain []*x509.Certificate) []TrustLink {
	path := verifiedPath(chain)
	if path == nil {
		return nil
	}
	out := make([]TrustLink, 0, len(path))
	for i, c := range path {
		sent := false
		for _, s := range chain {
			if bytes.Equal(s.Raw, c.Raw) {
				sent = true
			}
		}
		out = append(out, TrustLink{Subject: nameOf(c.Subject.CommonName, c.Subject.String()),
			NotAfter: c.NotAfter.UTC(), Sent: sent, Root: i == len(path)-1})
	}
	return out
}

func servesTLS(leaf *x509.Certificate) bool {
	if len(leaf.ExtKeyUsage) == 0 && len(leaf.UnknownExtKeyUsage) == 0 {
		return true
	}
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func isSHA1(alg x509.SignatureAlgorithm) bool {
	return alg == x509.SHA1WithRSA || alg == x509.DSAWithSHA1 || alg == x509.ECDSAWithSHA1
}

// mustStaple reads the TLS Feature extension (RFC 7633), a sequence of TLS
// extension numbers the certificate requires; 5 is status_request.
func mustStaple(c *x509.Certificate) bool {
	for _, ext := range c.Extensions {
		if !ext.Id.Equal(oidTLSFeature) {
			continue
		}
		var features []int
		if _, err := asn1.Unmarshal(ext.Value, &features); err != nil {
			return false
		}
		for _, f := range features {
			if f == tlsStatusReq {
				return true
			}
		}
	}
	return false
}

// sctsIn counts the timestamps in an SCT list extension: an OCTET STRING
// around RFC 6962's SignedCertificateTimestampList, a two-byte length and
// then each SCT with a two-byte length of its own. A malformed list counts
// what it held before it went wrong.
func sctsIn(exts []pkix.Extension, id asn1.ObjectIdentifier) int {
	for _, ext := range exts {
		if !ext.Id.Equal(id) {
			continue
		}
		var list []byte
		if _, err := asn1.Unmarshal(ext.Value, &list); err != nil || len(list) < 2 {
			return 0
		}
		body := list[2:]
		if total := int(list[0])<<8 | int(list[1]); total < len(body) {
			body = body[:total]
		}
		n := 0
		for len(body) >= 2 {
			size := int(body[0])<<8 | int(body[1])
			if size == 0 || len(body) < 2+size {
				break
			}
			n++
			body = body[2+size:]
		}
		return n
	}
	return 0
}

var extKeyUsageLabels = map[x509.ExtKeyUsage]string{
	x509.ExtKeyUsageAny:             "any",
	x509.ExtKeyUsageServerAuth:      "serverAuth",
	x509.ExtKeyUsageClientAuth:      "clientAuth",
	x509.ExtKeyUsageCodeSigning:     "codeSigning",
	x509.ExtKeyUsageEmailProtection: "emailProtection",
	x509.ExtKeyUsageTimeStamping:    "timeStamping",
	x509.ExtKeyUsageOCSPSigning:     "OCSPSigning",
}

// extKeyUsageNames spells the extended key usages as openssl's short names,
// an unnamed one by its OID.
func extKeyUsageNames(c *x509.Certificate) []string {
	var out []string
	for _, u := range c.ExtKeyUsage {
		if name, ok := extKeyUsageLabels[u]; ok {
			out = append(out, name)
		} else {
			out = append(out, "other")
		}
	}
	for _, oid := range c.UnknownExtKeyUsage {
		out = append(out, oid.String())
	}
	return out
}

// gradeChainAudit applies the chain rules. A scan without an audit (no
// certificate) judges none of them.
func gradeChainAudit(scan *TLSScan, demote func(int, ScanFinding), note func(ScanFinding),
	check func(id, category, title, cap string, passed, na bool)) {
	audit := scan.ChainAudit
	na := audit == nil
	if na {
		audit = &ChainAudit{Order: "ordered", ServerAuth: true}
	}
	check("tls.no-server-auth", "certificate", "The certificate may be used by a TLS server", "F", audit.ServerAuth, na)
	check("tls.sha1", "certificate", "No certificate in the chain is signed with SHA-1", "F", len(audit.SHA1) == 0, na)
	check("tls.must-staple", "certificate", "A must-staple certificate is stapled", "F",
		!audit.MustStaple || scan.OCSPStapled, na || !audit.MustStaple)
	// The lifetime and transparency rules are the public roots'; a chain
	// that is not trusted here cannot be judged by them.
	check("tls.long-validity", "certificate", "The certificate is valid for 398 days or fewer", "B", !audit.TooLong, na || !scan.Trusted)
	check("tls.no-sct", "certificate", "Certificate Transparency timestamps are presented", "B", audit.SCTs > 0, na || !scan.Trusted)
	if na {
		return
	}
	if !audit.ServerAuth {
		demote(gradeF, ScanFinding{ID: "tls.no-server-auth", Level: "critical", Fix: FixIssue,
			Title:  "The certificate is not for TLS servers",
			Detail: "Its extended key usage lists " + strings.Join(scan.Chain[0].ExtKeyUsage, ", ") + " and not serverAuth.",
			Advice: "Every browser refuses it for a website. Issue a certificate for server authentication; any public authority's TLS certificate is one."})
	}
	if len(audit.SHA1) > 0 {
		demote(gradeF, ScanFinding{ID: "tls.sha1", Level: "critical", Fix: FixIssue,
			Title:  "The chain is signed with SHA-1",
			Detail: "Signed with SHA-1: " + strings.Join(audit.SHA1, ", ") + ".",
			Advice: "SHA-1 signatures have been forgeable since 2017, and browsers refuse them. Reissue with SHA-256."})
	}
	if audit.MustStaple && !scan.OCSPStapled {
		demote(gradeF, ScanFinding{ID: "tls.must-staple", Level: "critical",
			Title:  "The certificate demands a stapled OCSP answer and none was sent",
			Detail: "The leaf carries the TLS Feature extension for status_request (OCSP must-staple), and the handshake had no staple.",
			Advice: "Firefox refuses the connection. In nginx set ssl_stapling on and ssl_stapling_verify on with a resolver, and check that nginx can reach the responder — the first handshakes after a reload go out before nginx has fetched an answer. Or reissue without must-staple."})
	}
	if audit.TooLong {
		demote(gradeB, ScanFinding{ID: "tls.long-validity", Level: "warning",
			Title:  fmt.Sprintf("The certificate is valid for %d days", audit.LeafDays),
			Detail: "Longer than the 398 days allowed of a publicly trusted certificate issued since 1 September 2020.",
			Advice: "Safari and Apple's other platforms refuse such a certificate when it chains to a public root; a root added to a device's own store is exempt. Reissue with a shorter term."})
	}
	if scan.Trusted && audit.SCTs == 0 {
		demote(gradeB, ScanFinding{ID: "tls.no-sct", Level: "warning",
			Title:  "No Certificate Transparency timestamps were presented",
			Detail: "None in the certificate, the TLS handshake or a stapled OCSP answer.",
			Advice: "Chrome and Safari refuse a certificate from a public authority without them, and every public authority embeds them. Expected only for a private authority whose root was added to this machine's store; otherwise reissue."})
	}
	switch audit.Order {
	case "out-of-order":
		note(ScanFinding{ID: "tls.chain-order", Level: "notice",
			Title:  "The chain is sent out of order",
			Detail: "Not every certificate is followed by the one that issued it.",
			Advice: "Browsers rebuild the path; some older and embedded TLS stacks take the chain as sent and fail. Send the leaf, then each issuer in turn, as certbot's fullchain.pem does."})
	case "extra":
		note(ScanFinding{ID: "tls.chain-order", Level: "notice",
			Title:  "The chain includes a certificate that issues nothing in it",
			Detail: "A certificate was sent that is not the issuer of any other certificate sent.",
			Advice: "Remove it from the bundle ssl_certificate names. It costs every handshake bytes and can lead a strict client to build the wrong path."})
	}
	if audit.RootSent {
		note(ScanFinding{ID: "tls.root-sent", Level: "notice",
			Title:  "The root certificate is sent",
			Detail: "The chain ends with a self-signed root.",
			Advice: "Clients trust their own copy of a root and ignore this one, so it only adds bytes to every handshake. Leave it out of the bundle."})
	}
}

// gradeRevocation fails the grade on a revoked leaf. An answer that could not
// be had is a notice: revocation is checked on the issuer's word, and its
// silence is not the server's fault.
func gradeRevocation(scan *TLSScan, demote func(int, ScanFinding), note func(ScanFinding),
	check func(id, category, title, cap string, passed, na bool)) {
	rev := scan.Revocation
	known := rev != nil && (rev.Status == "good" || rev.Status == "revoked")
	check("tls.revoked", "certificate", "The certificate has not been revoked", "F", known && rev.Status == "good", !known)
	if rev == nil {
		return
	}
	switch rev.Status {
	case "revoked":
		detail := "Its issuer's " + revocationMethodName(rev.Method) + " lists it as revoked"
		if rev.RevokedAt != nil {
			detail += " since " + rev.RevokedAt.Format("2 January 2006")
		}
		if rev.Reason != "" {
			detail += " (" + rev.Reason + ")"
		}
		demote(gradeF, ScanFinding{ID: "tls.revoked", Level: "critical", Fix: FixIssue,
			Title: "The certificate has been revoked", Detail: detail + ".",
			Advice: "Clients that check revocation refuse it, and it cannot be un-revoked. Issue a new certificate and reload."})
	case "unknown":
		note(ScanFinding{ID: "tls.revocation-unchecked", Level: "notice",
			Title:  "The issuer's OCSP responder does not know this certificate",
			Detail: "It answered, signed, that the certificate is unknown to it.",
			Advice: "Usual for a certificate issued moments ago. Otherwise the responder the certificate names does not answer for its issuer, and clients that check get no answer either."})
	case "unchecked":
		if len(scan.OCSPServers) == 0 && len(scan.CRLURLs) == 0 {
			return
		}
		note(ScanFinding{ID: "tls.revocation-unchecked", Level: "notice",
			Title:  "Revocation could not be checked",
			Detail: rev.Detail,
			Advice: "Nothing to fix on this server unless the responder or CRL is yours. Only answers signed by the issuer are believed, and requests go to public addresses only."})
	}
}

func revocationMethodName(method string) string {
	switch method {
	case "stapled":
		return "stapled OCSP answer"
	case "ocsp":
		return "OCSP responder"
	}
	return "CRL"
}
