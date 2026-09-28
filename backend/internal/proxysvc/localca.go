package proxysvc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Certificates for names no public authority will sign.
//
// A NAS on the LAN, a printer's admin page, 10.0.0.5, grafana.internal: Let's
// Encrypt cannot reach any of them, so they end up served over plain HTTP or
// behind a certificate every browser warns about until somebody has clicked
// through the warning a hundred times. A small authority of the host's own
// ends that for every device that trusts it: install its root once, and each
// certificate it signs is trusted there, renewed here before it expires.
//
// Nothing it signs is trusted anywhere else, and every surface that shows one
// says so. Its key is the one secret that matters — whoever reads it can sign
// for any name, and every device that installed the root believes them — so it
// is written 0600 in a directory only root can enter, and no route reads it
// back or exports it.

// privateDir holds what these certificates need beyond the pair a site names:
// the local CA's root and key, and the key of each signing request still
// waiting for its certificate. Not inside importedDir, where every directory
// is listed as a certificate and neither of these is one; and root-only,
// which importedDir is not.
//
// A variable for the same reason as importedDir.
var privateDir = "/etc/ssl/just-dashboard-private"

// privateMu keeps two writers — an operator's issuance and the daily renewal
// — from staging over the same pair at once. It guards files, not nginx's
// configuration, so it is never the service lock.
var privateMu sync.Mutex

const (
	// leafLifetime is the longest any client accepts from a private root:
	// Safari refuses more than 825 days even there, and 397 keeps inside
	// every public rule too, should one ever be applied to it.
	leafLifetime = 397 * 24 * time.Hour
	// localCARenewBefore is well ahead of the thirty days at which the
	// page starts warning, so a renewal that works never shows as one due.
	localCARenewBefore = 45 * 24 * time.Hour
	localCALifetime    = 10 * 365 * 24 * time.Hour
)

var (
	// ErrNoLocalCA is a request for the local CA on a host that has none.
	ErrNoLocalCA = errors.New("there is no local CA on this server yet — create it first")
	// ErrLocalCAExists refuses a second root: every device that installed
	// the first would stop trusting what it signed.
	ErrLocalCAExists = errors.New("this server already has a local CA")
)

// CertificateInputError is a request the operator can correct: a name that
// cannot be carried, a key type nobody offers. It is told apart from a failure
// to write, which is the server's.
type CertificateInputError struct{ msg string }

func (e *CertificateInputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error {
	return &CertificateInputError{msg: fmt.Sprintf(format, a...)}
}

// KeyType is the kind of key a certificate made here is given.
type KeyType string

const (
	KeyECDSAP256 KeyType = "ecdsa-p256"
	KeyECDSAP384 KeyType = "ecdsa-p384"
	KeyRSA2048   KeyType = "rsa-2048"
	KeyRSA3072   KeyType = "rsa-3072"
	KeyRSA4096   KeyType = "rsa-4096"
)

// generateKey makes a key of the kind asked for; ECDSA P-256 when none is,
// which every client of the last decade accepts and costs a handshake least.
func generateKey(kind KeyType) (crypto.Signer, error) {
	switch kind {
	case "", KeyECDSAP256:
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case KeyECDSAP384:
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case KeyRSA2048:
		return rsa.GenerateKey(rand.Reader, 2048)
	case KeyRSA3072:
		return rsa.GenerateKey(rand.Reader, 3072)
	case KeyRSA4096:
		return rsa.GenerateKey(rand.Reader, 4096)
	}
	return nil, invalidf("%q is not a key type offered here: ecdsa-p256, ecdsa-p384, rsa-2048, rsa-3072 or rsa-4096", kind)
}

// keyTypeOf names a public key the way the request did, empty for one no
// request here could have asked for.
func keyTypeOf(pub crypto.PublicKey) KeyType {
	switch key := pub.(type) {
	case *ecdsa.PublicKey:
		switch key.Curve {
		case elliptic.P256():
			return KeyECDSAP256
		case elliptic.P384():
			return KeyECDSAP384
		}
	case *rsa.PublicKey:
		switch key.N.BitLen() {
		case 2048:
			return KeyRSA2048
		case 3072:
			return KeyRSA3072
		case 4096:
			return KeyRSA4096
		}
	}
	return ""
}

var (
	dnsNameRe = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	slugRe    = regexp.MustCompile(`[^a-z0-9]+`)
)

// certNames is what a certificate is asked to cover, split the way a
// certificate carries it: host names in one field, addresses in another.
type certNames struct {
	dns []string
	ips []net.IP
	// first is the name given first, which becomes the common name.
	first string
}

func (n certNames) all() []string {
	out := slices.Clone(n.dns)
	for _, ip := range n.ips {
		out = append(out, ip.String())
	}
	return out
}

// parseCertNames reads the names a certificate is asked to cover: host names,
// a wildcard, or addresses. A dotted quad that is not an address is refused
// rather than taken for a host name, since that is always a typo.
func parseCertNames(names []string) (certNames, error) {
	var out certNames
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if name == "" {
			continue
		}
		if addr, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")); err == nil {
			if addr.Zone() != "" {
				return out, invalidf("%s names an interface; a certificate carries the address alone", raw)
			}
			addr = addr.Unmap()
			if seen[addr.String()] {
				continue
			}
			seen[addr.String()] = true
			out.ips = append(out.ips, net.IP(addr.AsSlice()))
			if out.first == "" {
				out.first = addr.String()
			}
			continue
		}
		labels := strings.Split(name, ".")
		if len(name) > 253 || !dnsNameRe.MatchString(name) || strings.Trim(labels[len(labels)-1], "0123456789") == "" {
			return out, invalidf("%s is not a name a certificate can carry: a host name of letters, digits, dashes and dots, a *. wildcard, or an address", strings.TrimSpace(raw))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out.dns = append(out.dns, name)
		if out.first == "" {
			out.first = name
		}
	}
	switch total := len(out.dns) + len(out.ips); {
	case total == 0:
		return out, invalidf("name at least one host name or address for the certificate to cover")
	case total > 100:
		return out, invalidf("%d names is more than one certificate should carry; 100 at most", total)
	}
	return out, nil
}

// privateName checks the directory name a certificate or request is kept
// under, by the rule imports follow.
func privateName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !importNameRe.MatchString(name) {
		return "", invalidf("the name must be lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit")
	}
	if isCaddyEvidence(name) {
		return "", invalidf("%s has the shape of Caddy's release copies, which are kept out of the list — choose another name", name)
	}
	return name, nil
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// leafTemplate is a server certificate for names, valid from notBefore for
// leafLifetime. The hour's backdating is for a device whose clock runs slow,
// which would otherwise refuse a certificate issued a minute ago.
func leafTemplate(names certNames, pub crypto.PublicKey, now time.Time) (*x509.Certificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	usage := x509.KeyUsageDigitalSignature
	if _, ok := pub.(*rsa.PublicKey); ok {
		// A TLS 1.2 client doing RSA key exchange encrypts to the key.
		usage |= x509.KeyUsageKeyEncipherment
	}
	notBefore := now.Add(-time.Hour).UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		DNSNames:              names.dns,
		IPAddresses:           names.ips,
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(leafLifetime),
		KeyUsage:              usage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	// A common name longer than 64 characters is outside what X.509 allows,
	// and the names themselves are what clients check.
	if len(names.first) <= 64 {
		tmpl.Subject.CommonName = names.first
	}
	return tmpl, nil
}

func localCAPaths() (certPath, keyPath string) {
	dir := filepath.Join(privateDir, "ca")
	return filepath.Join(dir, "root.pem"), filepath.Join(dir, "root-key.pem")
}

// readLocalCARoot is the local CA's root certificate, or ErrNoLocalCA.
func readLocalCARoot() (*x509.Certificate, error) {
	certPath, _ := localCAPaths()
	root, err := readLeaf(certPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoLocalCA
	}
	if err != nil {
		return nil, fmt.Errorf("the local CA's root could not be read: %w", err)
	}
	return root, nil
}

// readLocalCAKey is the key that signs for root, checked against it: a key
// that is not the root's would sign certificates no device can verify.
func readLocalCAKey(root *x509.Certificate) (crypto.Signer, error) {
	_, keyPath := localCAPaths()
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("the local CA's key could not be read: %w", err)
	}
	key, _, err := decodePrivateKey(string(raw))
	if err != nil {
		return nil, fmt.Errorf("the local CA's key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok || !publicKeyMatches(key, root) {
		return nil, fmt.Errorf("the key beside the local CA's root is not its key, so it can sign nothing")
	}
	return signer, nil
}

// localCAName is the root's common name. The host's name is in it because
// a laptop that trusts two servers' roots lists both in one keychain.
func localCAName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "Just Dashboard local CA"
	}
	const prefix, max = "Just Dashboard local CA (", 64
	if len(prefix)+len(host)+1 > max {
		host = host[:max-len(prefix)-1]
	}
	return prefix + host + ")"
}

// CreateLocalCA makes the root: an ECDSA P-256 key and a certificate valid for
// ten years, allowed to sign server certificates and nothing below them. The
// key is written before the certificate, whose presence is what says the CA
// exists, so an interrupted creation is simply made again.
func CreateLocalCA() (*LocalCA, error) {
	privateMu.Lock()
	defer privateMu.Unlock()
	certPath, keyPath := localCAPaths()
	if _, err := os.Lstat(certPath); err == nil {
		return nil, ErrLocalCAExists
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now().Add(-time.Hour).UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: localCAName(), Organization: []string{"Just Dashboard"}},
		NotBefore:             now,
		NotAfter:              now.Add(localCALifetime),
		IsCA:                  true,
		BasicConstraintsValid: true,
		// It signs leaves directly; an intermediate it signed could sign
		// for anything, out of reach of this server.
		MaxPathLen:     0,
		MaxPathLenZero: true,
		KeyUsage:       x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(certPath)
	if err := makePrivateDir(dir); err != nil {
		return nil, err
	}
	if err := writeStaged(dir, keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600, false); err != nil {
		return nil, err
	}
	if err := writeStaged(dir, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644, false); err != nil {
		return nil, err
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return describeRoot(root), nil
}

// makePrivateDir creates dir and privateDir above it, both 0700: the CA's key
// and every waiting request's key live under it.
func makePrivateDir(dir string) error {
	for _, d := range []string{privateDir, dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}

// writeStaged puts content at path through a staged file renamed into place,
// so a reader never finds half of it; backup keeps what was there as .bak.
func writeStaged(dir, path string, content []byte, mode os.FileMode, backup bool) error {
	staged, err := stageFile(dir, content, mode)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if backup {
		if err := keepImportBackup(path); err != nil {
			return err
		}
	}
	return os.Rename(staged, path)
}

// LocalCA is the local CA as the Certificates page reads it.
type LocalCA struct {
	Exists bool `json:"exists"`
	// Name is the root's common name, which a device's trust settings show.
	Name        string    `json:"name,omitempty"`
	NotBefore   time.Time `json:"notBefore,omitzero"`
	NotAfter    time.Time `json:"notAfter,omitzero"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	// Error is why it can issue nothing now: its key missing, or not its own.
	Error  string        `json:"error,omitempty"`
	Leaves []LocalCALeaf `json:"leaves"`
	// RenewBefore is how many days before expiry the daily check renews.
	RenewBefore int `json:"renewBefore"`
}

// LocalCALeaf is a certificate the local CA signed that lives with the
// imports, where the daily check finds and renews it.
type LocalCALeaf struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Domains   []string  `json:"domains"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	DaysLeft  int       `json:"daysLeft"`
	// RenewsAt is the first daily check that finds it due.
	RenewsAt time.Time `json:"renewsAt"`
	UsedBy   []string  `json:"usedBy"`
	// Error is why it cannot be renewed here.
	Error string `json:"error,omitempty"`
}

func describeRoot(root *x509.Certificate) *LocalCA {
	sum := sha256.Sum256(root.Raw)
	return &LocalCA{
		Exists:      true,
		Name:        root.Subject.CommonName,
		NotBefore:   root.NotBefore.UTC(),
		NotAfter:    root.NotAfter.UTC(),
		Fingerprint: colonHex(sum[:]),
		Leaves:      []LocalCALeaf{},
		RenewBefore: int(localCARenewBefore / (24 * time.Hour)),
	}
}

// LocalCAState is the root and every certificate it signed, with the sites
// naming each.
func (s *Service) LocalCAState() (*LocalCA, error) {
	root, err := readLocalCARoot()
	if errors.Is(err, ErrNoLocalCA) {
		return &LocalCA{Leaves: []LocalCALeaf{}, RenewBefore: int(localCARenewBefore / (24 * time.Hour))}, nil
	}
	if err != nil {
		return nil, err
	}
	state := describeRoot(root)
	if _, err := readLocalCAKey(root); err != nil {
		state.Error = err.Error()
	}
	vhosts := s.nginxVHosts()
	for _, leaf := range localCALeaves(root) {
		entry := LocalCALeaf{
			Name:      leaf.name,
			Path:      leaf.certPath,
			Domains:   certificateDomains(leaf.cert),
			NotBefore: leaf.cert.NotBefore.UTC(),
			NotAfter:  leaf.cert.NotAfter.UTC(),
			DaysLeft:  int(time.Until(leaf.cert.NotAfter).Hours() / 24),
			RenewsAt:  leaf.cert.NotAfter.Add(-localCARenewBefore).UTC(),
			UsedBy:    sitesNaming(vhosts, []string{leaf.certPath}, false),
		}
		if leaf.err != nil {
			entry.Error = leaf.err.Error()
		}
		state.Leaves = append(state.Leaves, entry)
	}
	return state, nil
}

// LocalCARoot is the root certificate to install on a device, and the file
// name to offer it under. The root is public by design; only its key is not.
func LocalCARoot() ([]byte, string, error) {
	root, err := readLocalCARoot()
	if err != nil {
		return nil, "", err
	}
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(root.Subject.CommonName), "-"), "-")
	if slug == "" {
		slug = "local-ca"
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}), slug + ".crt", nil
}

// PrivateCertificateRequest asks for a certificate made on this server,
// signed by the local CA or by its own key.
type PrivateCertificateRequest struct {
	// Name is the directory it is kept under, beside the imports.
	Name    string   `json:"name"`
	Names   []string `json:"names"`
	KeyType KeyType  `json:"keyType"`
	// Replace overwrites a certificate already kept under Name, keeping it
	// as .bak; without it a name in use is refused.
	Replace bool `json:"replace"`
}

// IssueFromLocalCA signs a certificate for the names asked with the local CA
// and keeps it with the imports, where sites name it and the daily check
// renews it.
func IssueFromLocalCA(req PrivateCertificateRequest) (*ImportResult, error) {
	name, err := privateName(req.Name)
	if err != nil {
		return nil, err
	}
	names, err := parseCertNames(req.Names)
	if err != nil {
		return nil, err
	}
	privateMu.Lock()
	defer privateMu.Unlock()
	root, err := readLocalCARoot()
	if err != nil {
		return nil, err
	}
	rootKey, err := readLocalCAKey(root)
	if err != nil {
		return nil, err
	}
	if err := refuseInUse(name, req.Replace); err != nil {
		return nil, err
	}
	key, err := generateKey(req.KeyType)
	if err != nil {
		return nil, err
	}
	tmpl, err := leafTemplate(names, key.Public(), time.Now())
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, key.Public(), rootKey)
	if err != nil {
		return nil, err
	}
	res, err := installPair(name, der, key)
	if err != nil {
		return nil, err
	}
	res.Cert.LocalCA = true
	return res, nil
}

// SelfSignCertificate makes a certificate its own key signs: trusted only by
// a device that is told to trust that one certificate, and never renewed
// here, since a renewal is a new certificate every such device would have to
// be told about again.
func SelfSignCertificate(req PrivateCertificateRequest) (*ImportResult, error) {
	name, err := privateName(req.Name)
	if err != nil {
		return nil, err
	}
	names, err := parseCertNames(req.Names)
	if err != nil {
		return nil, err
	}
	privateMu.Lock()
	defer privateMu.Unlock()
	if err := refuseInUse(name, req.Replace); err != nil {
		return nil, err
	}
	key, err := generateKey(req.KeyType)
	if err != nil {
		return nil, err
	}
	tmpl, err := leafTemplate(names, key.Public(), time.Now())
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	res, err := installPair(name, der, key)
	if err != nil {
		return nil, err
	}
	res.Warnings = append(res.Warnings,
		"It is self-signed: every browser warns before the site until the certificate itself is trusted on that device, and it is not renewed here.")
	return res, nil
}

// refuseInUse is ExistingImportError for a name already holding a pair,
// unless the request is to replace it.
func refuseInUse(name string, replace bool) error {
	certPath := filepath.Join(importedDir, name, "fullchain.pem")
	_, certErr := os.Lstat(certPath)
	_, keyErr := os.Lstat(filepath.Join(importedDir, name, "privkey.pem"))
	if (certErr == nil || keyErr == nil) && !replace {
		existing, _ := readCertificate(certPath)
		return &ExistingImportError{Name: name, Existing: existing}
	}
	return nil
}

// installPair keeps a certificate and its key where imports live, the way an
// import does: key 0600, certificate 0644, each staged and renamed into place
// with the key first, and a pair already there kept as .bak.
func installPair(name string, der []byte, key crypto.Signer) (*ImportResult, error) {
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(importedDir, name)
	res := &ImportResult{
		Name:          name,
		CertPath:      filepath.Join(dir, "fullchain.pem"),
		KeyPath:       filepath.Join(dir, "privkey.pem"),
		ChainComplete: true,
		Warnings:      []string{},
	}
	_, certErr := os.Lstat(res.CertPath)
	_, keyErr := os.Lstat(res.KeyPath)
	res.Replaced = certErr == nil || keyErr == nil
	res.Cert = summarise(leaf, name, res.CertPath)
	res.Cert.Source = "imported"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writeStaged(dir, res.KeyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600, res.Replaced); err != nil {
		return nil, err
	}
	if err := writeStaged(dir, res.CertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644, res.Replaced); err != nil {
		return nil, err
	}
	return res, nil
}

// localLeaf is a certificate among the imports that the local CA signed.
type localLeaf struct {
	name, certPath, keyPath string
	cert                    *x509.Certificate
	key                     crypto.Signer
	// err is why it cannot be renewed: without its own key it cannot.
	err error
}

// localCALeaves finds what root signed among the imports by signature, not
// by a note written beside it: a certificate is the local CA's exactly when
// its root verifies it, whoever put the files there.
func localCALeaves(root *x509.Certificate) []localLeaf {
	entries, err := os.ReadDir(importedDir)
	if err != nil {
		return nil
	}
	var out []localLeaf
	for _, e := range entries {
		if !e.IsDir() || isCaddyEvidence(e.Name()) {
			continue
		}
		dir := filepath.Join(importedDir, e.Name())
		leaf := localLeaf{name: e.Name(), certPath: filepath.Join(dir, "fullchain.pem"), keyPath: filepath.Join(dir, "privkey.pem")}
		cert, err := readLeaf(leaf.certPath)
		if err != nil || !signedBy(cert, root) {
			continue
		}
		leaf.cert = cert
		raw, err := os.ReadFile(leaf.keyPath)
		if err != nil {
			leaf.err = fmt.Errorf("its key could not be read, so it cannot be renewed: %w", err)
			out = append(out, leaf)
			continue
		}
		key, _, err := decodePrivateKey(string(raw))
		signer, ok := key.(crypto.Signer)
		if err != nil || !ok || !publicKeyMatches(key, cert) {
			leaf.err = errors.New("the key beside it is not its key, so it cannot be renewed")
		} else {
			leaf.key = signer
		}
		out = append(out, leaf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func signedBy(cert, root *x509.Certificate) bool {
	return bytes.Equal(cert.RawIssuer, root.RawSubject) && cert.CheckSignatureFrom(root) == nil
}

// certificateDomains is every name a certificate covers, addresses included:
// a certificate for 10.0.0.5 covers nothing else, and listing only its host
// names showed it covering nothing at all.
func certificateDomains(c *x509.Certificate) []string {
	out := append([]string{}, c.DNSNames...)
	return append(out, certificateAddresses(c)...)
}

func certificateAddresses(c *x509.Certificate) []string {
	var out []string
	for _, ip := range c.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

// markLocalCALeaves flags the certificates the local CA signed, which are
// trusted only where its root is installed.
func markLocalCALeaves(certs []Certificate) []Certificate {
	root, err := readLocalCARoot()
	if err != nil {
		return certs
	}
	for i, cert := range certs {
		if cert.Error != "" || cert.Issuer != root.Subject.CommonName {
			continue
		}
		if leaf, err := readLeaf(cert.Path); err == nil && signedBy(leaf, root) {
			certs[i].LocalCA = true
		}
	}
	return certs
}

// sitesNaming is the nginx sites whose ssl_certificate is one of paths,
// through symlinks either way; enabledOnly for the ones nginx serves.
func sitesNaming(vhosts []VHost, paths []string, enabledOnly bool) []string {
	resolve := func(path string) string {
		path = filepath.Clean(path)
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return path
	}
	targets := map[string]bool{}
	for _, path := range paths {
		targets[resolve(path)] = true
	}
	sites := []string{}
	for _, v := range vhosts {
		if v.CertPath == "" || (enabledOnly && !v.Enabled) {
			continue
		}
		if targets[resolve(v.CertPath)] {
			sites = appendOnce(sites, v.Name)
		}
	}
	sort.Strings(sites)
	return sites
}

// LocalCACheck is what one pass of the daily renewal did.
type LocalCACheck struct {
	At      time.Time `json:"at"`
	Renewed []string  `json:"renewed"`
	// Reloaded names the enabled sites nginx was reloaded for.
	Reloaded []string `json:"reloaded"`
	// Failed says, for each certificate due that could not be renewed, why.
	Failed []string `json:"failed"`
	// Error is why the pass could not finish: the local CA's key unreadable,
	// or nginx refusing the reload the renewals needed.
	Error string `json:"error,omitempty"`
}

// RenewLocalCALeaves renews every certificate the local CA signed that is
// within localCARenewBefore of expiring at now, for another leafLifetime with
// the key it already has — so the key a site names never changes under it —
// and reloads nginx once when an enabled site names one of them: nginx keeps
// serving the certificate it read at its last reload.
func (s *Service) RenewLocalCALeaves(ctx context.Context, now time.Time) *LocalCACheck {
	check := &LocalCACheck{At: now.UTC(), Renewed: []string{}, Reloaded: []string{}, Failed: []string{}}
	renewed, err := renewDueLeaves(now, check)
	if err != nil {
		check.Error = err.Error()
	}
	sites := sitesNaming(s.nginxVHosts(), renewed, true)
	if len(sites) == 0 {
		return check
	}
	res, err := s.Reload(ctx, KindNginx)
	if err != nil {
		why := err.Error()
		if errors.Is(err, ErrInvalidConf) {
			why = "its configuration test failed"
			if res != nil && res.Validation != nil {
				if output := strings.TrimSpace(res.Validation.Output); output != "" {
					why += " (" + output + ")"
				}
			}
		}
		check.Error = NotReloadedFor(sites, why).Error()
		return check
	}
	check.Reloaded = sites
	return check
}

// renewDueLeaves renews what is due under privateMu and answers with the
// files it replaced.
func renewDueLeaves(now time.Time, check *LocalCACheck) ([]string, error) {
	privateMu.Lock()
	defer privateMu.Unlock()
	root, err := readLocalCARoot()
	if errors.Is(err, ErrNoLocalCA) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var due []localLeaf
	for _, leaf := range localCALeaves(root) {
		if !now.Before(leaf.cert.NotAfter.Add(-localCARenewBefore)) {
			due = append(due, leaf)
		}
	}
	if len(due) == 0 {
		return nil, nil
	}
	rootKey, err := readLocalCAKey(root)
	if err != nil {
		for _, leaf := range due {
			check.Failed = append(check.Failed, leaf.name+": the local CA cannot sign")
		}
		return nil, err
	}
	var renewed []string
	for _, leaf := range due {
		if err := renewLeaf(leaf, root, rootKey, now); err != nil {
			check.Failed = append(check.Failed, leaf.name+": "+err.Error())
			continue
		}
		check.Renewed = append(check.Renewed, leaf.name)
		renewed = append(renewed, leaf.certPath)
	}
	return renewed, nil
}

// renewLeaf signs leaf's names again for its own key and replaces its
// certificate, keeping the previous one as .bak.
func renewLeaf(leaf localLeaf, root *x509.Certificate, rootKey crypto.Signer, now time.Time) error {
	if leaf.err != nil {
		return leaf.err
	}
	names := certNames{dns: leaf.cert.DNSNames, ips: leaf.cert.IPAddresses, first: leaf.cert.Subject.CommonName}
	tmpl, err := leafTemplate(names, leaf.key.Public(), now)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, leaf.key.Public(), rootKey)
	if err != nil {
		return err
	}
	return writeStaged(filepath.Dir(leaf.certPath), leaf.certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644, true)
}

// UsePrivateDirForTest points the local CA and the waiting requests at a
// test's own directory. Production never calls this.
func UsePrivateDirForTest(dir string) (restore func()) {
	previous := privateDir
	privateDir = dir
	return func() { privateDir = previous }
}
