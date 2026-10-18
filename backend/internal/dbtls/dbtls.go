// Package dbtls makes the certificates a database server speaks TLS with: one
// certificate authority that belongs to this dashboard, and a server
// certificate signed by it for each database the dashboard started.
//
// It is pure. It keeps nothing and touches no file, container or store: the
// caller seals the authority's key, decides where a certificate is mounted and
// when it is renewed, and this package only says what is true of the bytes it
// is given. That is what lets the names, the lifetimes and the renewal window
// be tested without a database.
package dbtls

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strings"
	"time"
)

const (
	// CAValidity is how long the authority lasts. Clients are given its
	// certificate once and keep trusting it, so it has to outlive every server
	// certificate it will sign by a wide margin; nothing replaces it on a
	// schedule.
	CAValidity = 10 * 365 * 24 * time.Hour
	// LeafValidity is how long a server certificate lasts. A year, renewed in
	// place long before it ends: short enough that a leaked key stops being
	// useful, long enough that a dashboard that was off for a month does not
	// find every database with an expired certificate.
	LeafValidity = 365 * 24 * time.Hour
	// RenewWindow is how long before expiry a certificate is renewed.
	RenewWindow = 30 * 24 * time.Hour
	// backdate covers a clock that runs behind the server's.
	backdate = 5 * time.Minute
)

// ErrCAExpired is returned when the authority can no longer sign.
var ErrCAExpired = errors.New("the database certificate authority has expired")

// NewCA creates an authority and returns its certificate and its private key,
// both as PEM. The key is returned to be sealed and is never kept here.
func NewCA(now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Just Dashboard database CA", Organization: []string{"Just Dashboard"}},
		NotBefore:             now.Add(-backdate).UTC(),
		NotAfter:              now.Add(CAValidity).UTC(),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pemBlock("CERTIFICATE", der), pemBlock("PRIVATE KEY", keyDER), nil
}

// CA is an authority loaded to sign with. Hold it only for as long as a
// signing takes.
type CA struct {
	cert *x509.Certificate
	key  crypto.Signer
}

// LoadCA reads an authority's certificate and key and checks they belong
// together.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, errors.New("the stored certificate is not a certificate authority")
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("the stored authority key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the stored authority key could not be read: %w", err)
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errors.New("the stored authority key cannot sign")
	}
	if !publicKeysEqual(cert.PublicKey, signer.Public()) {
		return nil, errors.New("the stored authority key does not match its certificate")
	}
	return &CA{cert: cert, key: signer}, nil
}

// Leaf is a server certificate with the key it was made for.
type Leaf struct {
	CertPEM   []byte
	KeyPEM    []byte
	Serial    string
	NotBefore time.Time
	NotAfter  time.Time
	// Names are the subject alternative names, as Names returns them.
	Names []string
}

// CombinedPEM is the certificate followed by its key in one file, the form
// MongoDB reads as tlsCertificateKeyFile.
func (l *Leaf) CombinedPEM() []byte {
	out := append([]byte(nil), l.CertPEM...)
	return append(out, l.KeyPEM...)
}

// Issue signs a server certificate for the names. It lasts LeafValidity, or
// until the authority ends if that comes first.
func (ca *CA) Issue(names []string, now time.Time) (*Leaf, error) {
	dns, ips, err := splitNames(names)
	if err != nil {
		return nil, err
	}
	if !now.Before(ca.cert.NotAfter) {
		return nil, ErrCAExpired
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	notAfter := now.Add(LeafValidity)
	if notAfter.After(ca.cert.NotAfter) {
		notAfter = ca.cert.NotAfter
	}
	common := ""
	switch {
	case len(dns) > 0:
		common = dns[0]
	case len(ips) > 0:
		common = ips[0].String()
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: common, Organization: []string{"Just Dashboard"}},
		NotBefore:    now.Add(-backdate).UTC(),
		NotAfter:     notAfter.UTC(),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, key.Public(), ca.key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	leaf := &Leaf{
		CertPEM:   pemBlock("CERTIFICATE", der),
		KeyPEM:    pemBlock("PRIVATE KEY", keyDER),
		Serial:    serialHex(serial),
		NotBefore: template.NotBefore,
		NotAfter:  template.NotAfter,
	}
	leaf.Names = namesOf(dns, ips)
	return leaf, nil
}

// Info is what is true of a certificate, for showing and for deciding.
type Info struct {
	Subject     string
	Serial      string
	NotBefore   time.Time
	NotAfter    time.Time
	Fingerprint string
	Names       []string
}

// Inspect reads a PEM certificate.
func Inspect(certPEM []byte) (*Info, error) {
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(cert.Raw)
	return &Info{
		Subject:     cert.Subject.CommonName,
		Serial:      serialHex(cert.SerialNumber),
		NotBefore:   cert.NotBefore.UTC(),
		NotAfter:    cert.NotAfter.UTC(),
		Fingerprint: hex.EncodeToString(sum[:]),
		Names:       namesOf(cert.DNSNames, cert.IPAddresses),
	}, nil
}

// NeedsRenewal reports a certificate inside its renewal window or past its
// end.
func NeedsRenewal(notAfter, now time.Time) bool {
	return now.Add(RenewWindow).After(notAfter)
}

// Names normalises the names a certificate is to carry: trimmed, lower-cased,
// each an address or a host name, no duplicates, addresses first and then
// names in order. The same set in any order and spelling gives the same list,
// which is what lets a stored certificate be compared with the names wanted
// now.
func Names(values []string) ([]string, error) {
	dns, ips, err := splitNames(values)
	if err != nil {
		return nil, err
	}
	return namesOf(dns, ips), nil
}

// SameNames reports two lists naming the same set.
func SameNames(a, b []string) bool {
	left, err := Names(a)
	if err != nil {
		return false
	}
	right, err := Names(b)
	if err != nil {
		return false
	}
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// Verifies reports whether a server certificate chains to the authority and is
// valid for the name at the moment given: what a verifying client decides.
func Verifies(caPEM, leafPEM []byte, name string, at time.Time) error {
	ca, err := parseCertificate(caPEM)
	if err != nil {
		return err
	}
	leaf, err := parseCertificate(leafPEM)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots: pool, DNSName: name, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

func splitNames(values []string) ([]string, []net.IP, error) {
	seenDNS := map[string]bool{}
	seenIP := map[string]bool{}
	var dns []string
	var ips []net.IP
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
		if value == "" {
			continue
		}
		if ip := net.ParseIP(value); ip != nil {
			if !seenIP[ip.String()] {
				seenIP[ip.String()] = true
				ips = append(ips, ip)
			}
			continue
		}
		if !IsHostName(value) {
			return nil, nil, fmt.Errorf("%q is neither an address nor a host name", raw)
		}
		if !seenDNS[value] {
			seenDNS[value] = true
			dns = append(dns, value)
		}
	}
	if len(dns) == 0 && len(ips) == 0 {
		return nil, nil, errors.New("a server certificate needs at least one name or address")
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].String() < ips[j].String() })
	sort.Strings(dns)
	return dns, ips, nil
}

func namesOf(dns []string, ips []net.IP) []string {
	out := make([]string, 0, len(dns)+len(ips))
	sorted := append([]net.IP(nil), ips...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	for _, ip := range sorted {
		out = append(out, ip.String())
	}
	names := append([]string(nil), dns...)
	sort.Strings(names)
	return append(out, names...)
}

// IsHostName reports what a DNS name may be: dot-separated labels of letters,
// digits and hyphens, none starting or ending with a hyphen. A container name
// is usually one, and an underscore in it is the one thing a certificate
// cannot carry, so a caller offers a name only when this says it can.
func IsHostName(name string) bool {
	if len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func parseCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func publicKeysEqual(a, b crypto.PublicKey) bool {
	type equaler interface{ Equal(crypto.PublicKey) bool }
	if key, ok := a.(equaler); ok {
		return key.Equal(b)
	}
	return false
}

func pemBlock(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func serialHex(serial *big.Int) string { return strings.ToUpper(serial.Text(16)) }
