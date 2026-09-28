package proxysvc

import (
	"crypto"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// What one certificate file holds, read from the file alone: its names, its
// key, the chain it carries and whether that chain reaches a public root.
// Nothing here dials anything; the TLS report is where the served chain is
// asked for.

// CertificateFacts is one certificate, decoded.
type CertificateFacts struct {
	Subject       string    `json:"subject"`
	Issuer        string    `json:"issuer"`
	DNSNames      []string  `json:"dnsNames"`
	IPAddresses   []string  `json:"ipAddresses"`
	Emails        []string  `json:"emails"`
	URIs          []string  `json:"uris"`
	Serial        string    `json:"serial"`
	SHA256        string    `json:"sha256"`
	SHA1          string    `json:"sha1"`
	NotBefore     time.Time `json:"notBefore"`
	NotAfter      time.Time `json:"notAfter"`
	KeyType       string    `json:"keyType"`
	KeyBits       int       `json:"keyBits"`
	Signature     string    `json:"signature"`
	CA            bool      `json:"ca"`
	SelfSigned    bool      `json:"selfSigned"`
	OCSP          []string  `json:"ocsp"`
	IssuerURLs    []string  `json:"issuerUrls"`
	CRL           []string  `json:"crl"`
	SCTs          int       `json:"scts"`
	SignsPrevious bool      `json:"signsPrevious,omitempty"`
}

// CertificateChain is the certificates a file carries in the order nginx
// sends them, and what a client without the missing pieces makes of them.
type CertificateChain struct {
	// Verdict is "complete", "wrong-order", "missing-intermediate",
	// "private-ca", "self-signed" or "invalid".
	Verdict      string             `json:"verdict"`
	Note         string             `json:"note"`
	Certificates []CertificateFacts `json:"certificates"`
}

// CertificateKey is what is known of the private key beside a certificate,
// never the key itself.
type CertificateKey struct {
	Path string `json:"path"`
	// From says where the path came from: the nginx site that pairs it, the
	// certbot lineage, or the file beside the certificate.
	From          string `json:"from"`
	Mode          string `json:"mode,omitempty"`
	Owner         string `json:"owner,omitempty"`
	Group         string `json:"group,omitempty"`
	GroupReadable bool   `json:"groupReadable,omitempty"`
	WorldReadable bool   `json:"worldReadable,omitempty"`
	// Matches is null when the key could not be read or parsed; Error says
	// why.
	Matches *bool  `json:"matches"`
	Error   string `json:"error,omitempty"`
}

// CertificateDetail is everything the Certificates page shows for one file.
type CertificateDetail struct {
	Path  string           `json:"path"`
	Chain CertificateChain `json:"chain"`
	Key   *CertificateKey  `json:"key"`
}

// sctListOID is the X.509 extension holding embedded signed certificate
// timestamps (RFC 6962 section 3.3).
var sctListOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}

// maxKeyFileBytes bounds the read of a key file for the match check: a key
// is a few kilobytes, and the path comes from a site's config.
const maxKeyFileBytes = 64 << 10

var certKeyRe = regexp.MustCompile(`(?m)^\s*ssl_certificate_key\s+([^;]+);`)

// CertificateDetail reads the certificate at path, which must be one the
// inventory lists: the path comes from the page, and anything else is
// refused before it is opened.
func (s *Service) CertificateDetail(path string) (*CertificateDetail, error) {
	vhosts := s.nginxVHosts()
	certs := listCertificates(filepath.Join(letsencryptDir, "live"), importedDir, vhosts)
	index := slices.IndexFunc(certs, func(c Certificate) bool { return c.Path == path })
	if index < 0 {
		return nil, ErrCertificateNotListed
	}
	cert := certs[index]
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	chain, err := decodeChain(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	detail := &CertificateDetail{Path: path, Chain: judgeChain(chain)}
	if keyPath, from := certificateKeyPath(cert, vhosts); keyPath != "" {
		detail.Key = inspectKey(keyPath, from, chain[0])
	}
	return detail, nil
}

// certificateKeyPath is the key nginx pairs with the certificate where a site
// names it, since that pairing is the one a handshake uses; otherwise
// certbot's for its lineage, or the privkey.pem an import keeps beside it.
func certificateKeyPath(cert Certificate, vhosts []VHost) (string, string) {
	for _, v := range vhosts {
		if v.CertPath != cert.Path || !slices.Contains(cert.UsedBy, v.Name) {
			continue
		}
		content, err := os.ReadFile(v.Path)
		if err != nil {
			continue
		}
		if m := certKeyRe.FindSubmatch(content); m != nil {
			return strings.Trim(strings.TrimSpace(string(m[1])), `"'`), "nginx:" + v.Name
		}
	}
	if cert.Source == "certbot" {
		confs, _ := readRenewalConfs(letsencryptDir)
		for _, conf := range confs {
			if conf.Name == cert.Name && conf.PrivKey != "" {
				return conf.PrivKey, "certbot"
			}
		}
	}
	sibling := filepath.Join(filepath.Dir(cert.Path), "privkey.pem")
	if _, err := os.Stat(sibling); err == nil {
		return sibling, "beside the certificate"
	}
	return "", ""
}

// inspectKey reports the key file's permissions and whether it is the
// certificate's key. The key is compared in memory and never returned.
func inspectKey(path, from string, leaf *x509.Certificate) *CertificateKey {
	key := &CertificateKey{Path: path, From: from}
	info, err := os.Stat(path)
	if err != nil {
		key.Error = err.Error()
		return key
	}
	perm := info.Mode().Perm()
	key.Mode = fmt.Sprintf("%04o", perm)
	key.GroupReadable = perm&0o040 != 0
	key.WorldReadable = perm&0o004 != 0
	key.Owner, key.Group = fileOwner(info)
	f, err := os.Open(path)
	if err != nil {
		key.Error = err.Error()
		return key
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes))
	if err != nil {
		key.Error = err.Error()
		return key
	}
	public, err := privateKeyPublic(raw)
	if err != nil {
		key.Error = err.Error()
		return key
	}
	matches := false
	if eq, ok := public.(interface{ Equal(crypto.PublicKey) bool }); ok {
		matches = eq.Equal(leaf.PublicKey)
	}
	key.Matches = &matches
	return key
}

// fileOwner names the file's owner and group, by number where the name
// cannot be looked up.
func fileOwner(info os.FileInfo) (string, string) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	owner, group := strconv.FormatUint(uint64(st.Uid), 10), strconv.FormatUint(uint64(st.Gid), 10)
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}
	if g, err := user.LookupGroupId(group); err == nil {
		group = g.Name
	}
	return owner, group
}

// privateKeyPublic is the public half of the first private key in a PEM file.
func privateKeyPublic(raw []byte) (crypto.PublicKey, error) {
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return nil, errors.New("no private key in the file")
		}
		if !strings.Contains(block.Type, "PRIVATE KEY") {
			continue
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || block.Headers["Proc-Type"] != "" {
			return nil, errors.New("the key is encrypted, so whether it matches cannot be read")
		}
		var parsed any
		var err error
		switch block.Type {
		case "RSA PRIVATE KEY":
			parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			parsed, err = x509.ParseECPrivateKey(block.Bytes)
		default:
			parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		}
		if err != nil {
			return nil, fmt.Errorf("the key could not be parsed: %w", err)
		}
		signer, ok := parsed.(crypto.Signer)
		if !ok {
			return nil, errors.New("the key is of a kind that has no public half to compare")
		}
		return signer.Public(), nil
	}
}

// decodeChain is every CERTIFICATE block in a PEM file, in file order.
func decodeChain(raw []byte) ([]*x509.Certificate, error) {
	var chain []*x509.Certificate
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("certificate %d could not be parsed: %w", len(chain)+1, err)
		}
		chain = append(chain, c)
	}
	if len(chain) == 0 {
		return nil, errors.New("no PEM certificate in the file")
	}
	return chain, nil
}

func certificateFacts(c *x509.Certificate) CertificateFacts {
	keyType, bits := keyInfo(c)
	sum256 := sha256.Sum256(c.Raw)
	sum1 := sha1.Sum(c.Raw)
	facts := CertificateFacts{
		Subject:     c.Subject.String(),
		Issuer:      c.Issuer.String(),
		DNSNames:    append([]string{}, c.DNSNames...),
		IPAddresses: []string{},
		Emails:      append([]string{}, c.EmailAddresses...),
		URIs:        []string{},
		Serial:      colonHex(c.SerialNumber.Bytes()),
		SHA256:      colonHex(sum256[:]),
		SHA1:        colonHex(sum1[:]),
		NotBefore:   c.NotBefore.UTC(),
		NotAfter:    c.NotAfter.UTC(),
		KeyType:     keyType,
		KeyBits:     bits,
		Signature:   c.SignatureAlgorithm.String(),
		CA:          c.IsCA,
		SelfSigned:  c.Issuer.String() == c.Subject.String() && c.CheckSignatureFrom(c) == nil,
		OCSP:        append([]string{}, c.OCSPServer...),
		IssuerURLs:  append([]string{}, c.IssuingCertificateURL...),
		CRL:         append([]string{}, c.CRLDistributionPoints...),
		SCTs:        embeddedSCTs(c),
	}
	for _, ip := range c.IPAddresses {
		facts.IPAddresses = append(facts.IPAddresses, ip.String())
	}
	for _, u := range c.URIs {
		facts.URIs = append(facts.URIs, u.String())
	}
	return facts
}

// embeddedSCTs counts the signed certificate timestamps in the certificate:
// an OCTET STRING wrapping a TLS-encoded list, two-byte length prefixed, of
// two-byte length prefixed entries. A malformed list counts what it could.
func embeddedSCTs(c *x509.Certificate) int {
	for _, ext := range c.Extensions {
		if !ext.Id.Equal(sctListOID) {
			continue
		}
		var list []byte
		if _, err := asn1.Unmarshal(ext.Value, &list); err != nil || len(list) < 2 {
			return 0
		}
		body := list[2:]
		if total := int(binary.BigEndian.Uint16(list)); total < len(body) {
			body = body[:total]
		}
		count := 0
		for len(body) >= 2 {
			size := int(binary.BigEndian.Uint16(body))
			if size == 0 || 2+size > len(body) {
				break
			}
			count++
			body = body[2+size:]
		}
		return count
	}
	return 0
}

// judgeChain says whether the chain in the file would satisfy a client that
// trusts this host's system roots and fetches nothing, which is what most
// non-browser clients do. Expiry is left to the page's own expiry status, so
// the chain is checked at a moment every certificate in it was valid.
func judgeChain(chain []*x509.Certificate) CertificateChain {
	out := CertificateChain{Certificates: make([]CertificateFacts, len(chain))}
	ordered := true
	for i, c := range chain {
		out.Certificates[i] = certificateFacts(c)
		if i > 0 {
			signs := chain[i-1].CheckSignatureFrom(c) == nil
			out.Certificates[i].SignsPrevious = signs
			ordered = ordered && signs
		}
	}
	leaf := chain[0]
	at := time.Now()
	for _, c := range chain {
		if at.After(c.NotAfter) {
			at = c.NotAfter.Add(-time.Minute)
		}
	}
	if at.Before(leaf.NotBefore) {
		at = leaf.NotBefore.Add(time.Minute)
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: intermediates, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	var unknown x509.UnknownAuthorityError
	switch {
	case !ordered && len(chain) > 1:
		out.Verdict = "wrong-order"
		out.Note = "A certificate in the file does not sign the one before it. nginx sends them in file order: the leaf first, then each issuer. Strict clients refuse a chain out of order, and a certificate that belongs to no chain here only adds bytes."
	case err == nil:
		out.Verdict = "complete"
		out.Note = "The chain reaches a root this host trusts using only what the file carries."
	case out.Certificates[0].SelfSigned && len(chain) == 1:
		out.Verdict = "self-signed"
		out.Note = "The certificate signs itself. Only a client told to trust this exact certificate accepts it."
	case errors.As(err, &unknown):
		out.Verdict, out.Note = unknownAuthority(chain, roots, intermediates, at)
	default:
		out.Verdict = "invalid"
		out.Note = err.Error()
	}
	return out
}

// unknownAuthority tells apart a chain that ends at a root no one trusts from
// one that stops short of a public root. Without the network it cannot see
// an issuer's certificate, so an issuer that publishes where to fetch its
// certificate is read as a public CA whose intermediate is missing.
func unknownAuthority(chain []*x509.Certificate, roots, intermediates *x509.CertPool, at time.Time) (string, string) {
	for _, c := range chain {
		if stagingIssuer(c) {
			return "private-ca", "A test authority signed this chain, and no client trusts it."
		}
	}
	own := roots.Clone()
	for _, c := range chain {
		if c.Issuer.String() == c.Subject.String() && c.CheckSignatureFrom(c) == nil {
			own.AddCert(c)
		}
	}
	if _, err := chain[0].Verify(x509.VerifyOptions{
		Roots: own, Intermediates: intermediates, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err == nil {
		return "private-ca", "The chain ends at a root it carries itself, which no public trust store holds: only clients given that root trust it."
	}
	top := chain[len(chain)-1]
	if len(top.IssuingCertificateURL) > 0 {
		return "missing-intermediate", "The file stops at " + nameOf(top.Issuer.CommonName, top.Issuer.String()) +
			"'s signature without its certificate, and no root here signed it. Its issuer publishes the certificate at " +
			strings.Join(top.IssuingCertificateURL, ", ") + "; browsers fetch it, most other clients refuse the chain. If this is a private CA, its root has to be given to each client instead."
	}
	return "private-ca", "No root this host trusts signed " + nameOf(top.Issuer.CommonName, top.Issuer.String()) +
		", and the certificate says nothing of where its issuer is published: a private CA, or an intermediate the file leaves out."
}

// DecodedPEM is a pasted PEM, read without the network.
type DecodedPEM struct {
	Chain    *CertificateChain `json:"chain,omitempty"`
	Requests []DecodedCSR      `json:"requests"`
	// Refused names blocks left unread: a private key is never decoded or
	// echoed back, and other types are not certificates.
	Refused []string `json:"refused"`
}

// DecodedCSR is a certificate signing request.
type DecodedCSR struct {
	Subject        string   `json:"subject"`
	DNSNames       []string `json:"dnsNames"`
	IPAddresses    []string `json:"ipAddresses"`
	Emails         []string `json:"emails"`
	KeyType        string   `json:"keyType"`
	KeyBits        int      `json:"keyBits"`
	Signature      string   `json:"signature"`
	SignatureValid bool     `json:"signatureValid"`
}

// MaxDecodeBytes bounds a pasted PEM: a long chain is a few tens of
// kilobytes.
const MaxDecodeBytes = 256 << 10

// DecodePEM reads the certificates and signing requests in a pasted PEM.
func DecodePEM(text string) (*DecodedPEM, error) {
	raw := []byte(text)
	out := &DecodedPEM{Requests: []DecodedCSR{}, Refused: []string{}}
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("certificate %d could not be parsed: %w", len(certs)+1, err)
			}
			certs = append(certs, c)
		case "CERTIFICATE REQUEST", "NEW CERTIFICATE REQUEST":
			csr, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("signing request %d could not be parsed: %w", len(out.Requests)+1, err)
			}
			out.Requests = append(out.Requests, decodeCSR(csr))
		default:
			if strings.Contains(block.Type, "PRIVATE KEY") {
				out.Refused = append(out.Refused, "a private key, which is not read: paste only the certificate")
			} else {
				out.Refused = append(out.Refused, strconv.Quote(block.Type))
			}
		}
	}
	if len(certs) == 0 && len(out.Requests) == 0 {
		if len(out.Refused) > 0 {
			return nil, fmt.Errorf("no certificate or signing request: found %s", strings.Join(out.Refused, ", "))
		}
		return nil, errors.New("no PEM block: paste text starting with -----BEGIN CERTIFICATE----- or -----BEGIN CERTIFICATE REQUEST-----")
	}
	if len(certs) > 0 {
		chain := judgeChain(certs)
		out.Chain = &chain
	}
	return out, nil
}

func decodeCSR(csr *x509.CertificateRequest) DecodedCSR {
	keyType, bits := keyInfo(&x509.Certificate{PublicKey: csr.PublicKey, PublicKeyAlgorithm: csr.PublicKeyAlgorithm})
	out := DecodedCSR{
		Subject:        csr.Subject.String(),
		DNSNames:       append([]string{}, csr.DNSNames...),
		IPAddresses:    []string{},
		Emails:         append([]string{}, csr.EmailAddresses...),
		KeyType:        keyType,
		KeyBits:        bits,
		Signature:      csr.SignatureAlgorithm.String(),
		SignatureValid: csr.CheckSignature() == nil,
	}
	for _, ip := range csr.IPAddresses {
		out.IPAddresses = append(out.IPAddresses, ip.String())
	}
	return out
}

// CertificateEvent is one entry in a certificate's timeline.
type CertificateEvent struct {
	Time time.Time `json:"time"`
	// Kind is "version" for a certificate certbot archived, "failure" for a
	// renewal certbot logged as failed; the API adds "audit".
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Serial and SHA256 identify an archived version.
	Serial string `json:"serial,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

var archiveCertRe = regexp.MustCompile(`^cert(\d+)\.pem$`)

// CertbotLineageExists reports a name certbot has a renewal configuration
// for, which is the only name whose archive the history reads.
func CertbotLineageExists(name string) bool {
	confs, _ := readRenewalConfs(letsencryptDir)
	return slices.ContainsFunc(confs, func(c renewalConf) bool { return c.Name == name })
}

// ArchivedVersions is every certificate certbot kept for the lineage, oldest
// first: certbot writes certN.pem to the archive on each issuance and
// renewal and never removes one.
func ArchivedVersions(name string) []CertificateEvent {
	if !CertbotLineageExists(name) {
		return nil
	}
	dir := filepath.Join(letsencryptDir, "archive", name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type version struct {
		n    int
		cert *x509.Certificate
	}
	var versions []version
	for _, e := range entries {
		m := archiveCertRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if c, err := readLeaf(filepath.Join(dir, e.Name())); err == nil {
			versions = append(versions, version{n, c})
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].n < versions[j].n })
	out := make([]CertificateEvent, 0, len(versions))
	for i, v := range versions {
		title := "Renewed"
		if i == 0 {
			title = "Issued"
		}
		sum := sha256.Sum256(v.cert.Raw)
		out = append(out, CertificateEvent{
			Time: v.cert.NotBefore.UTC(), Kind: "version",
			Title:  fmt.Sprintf("%s (version %d)", title, v.n),
			Detail: "Valid until " + v.cert.NotAfter.UTC().Format("2 Jan 2006"),
			Serial: colonHex(v.cert.SerialNumber.Bytes()), SHA256: colonHex(sum[:]),
		})
	}
	return out
}

// RenewalFailuresFor is each logged renewal run that failed the lineage, as
// far back as the renewal record reaches.
func RenewalFailuresFor(log *RenewalLog, name string) []CertificateEvent {
	out := []CertificateEvent{}
	if log == nil {
		return out
	}
	for _, run := range log.Runs {
		failures, _ := runFailures(run.Lines)
		for _, f := range failures {
			if f.Lineage == name {
				out = append(out, CertificateEvent{
					Time: run.Start, Kind: "failure", Title: "Renewal failed", Detail: f.Reason,
				})
			}
		}
	}
	return out
}
