package proxysvc

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Not every certificate comes from Let's Encrypt.
//
// An EV certificate a company bought, one issued by an internal CA, one a
// hosting provider handed over — all of them arrive as two blocks of PEM and
// have nowhere to go on a page that only knows how to run certbot. This is the
// other half of "manage certificates": put an existing one where the proxy can
// find it, having first checked it is what it claims to be.
//
// The checking is the point. A key that does not match its certificate
// produces an nginx that refuses to start, and finding that out at reload time
// on a live server is the expensive way.

// importedDir is where imported certificates live. Deliberately not inside
// /etc/letsencrypt: certbot owns that tree and prunes what it does not
// recognise, and a renewal run should never be able to delete a certificate it
// did not issue.
//
// A variable rather than a constant so a live test running as an ordinary
// user can import into a directory it may write; nothing else assigns it.
var importedDir = "/etc/ssl/just-dashboard"

var importNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ImportResult reports where the files landed, so the site form can be pointed
// at them without the operator retyping a path.
type ImportResult struct {
	Name     string       `json:"name"`
	CertPath string       `json:"certPath"`
	KeyPath  string       `json:"keyPath"`
	Cert     *Certificate `json:"certificate"`
	// ChainComplete reports whether the chain reaches a root: one the bundle
	// carried, or one this server trusts. A leaf on its own works in every
	// desktop browser and fails on exactly the clients nobody tests with, so
	// it is worth saying at import rather than leaving to be discovered by a
	// payment gateway.
	ChainComplete bool `json:"chainComplete"`
	// Replaced reports that an import of the same name was overwritten; the
	// previous pair is kept beside it as .bak, and nginx serves the new one
	// only after its next reload.
	Replaced bool     `json:"replaced"`
	Warnings []string `json:"warnings"`
	// Chain is the common name of each certificate as saved, leaf first.
	Chain []string `json:"chain"`
	// UsedBy is the enabled sites serving a replaced import, each of which
	// keeps the previous pair until nginx reloads.
	UsedBy []string `json:"usedBy,omitempty"`
}

// ExistingImportError refuses to overwrite an import nobody asked to replace.
// Writing over one silently was how a certificate a site still served
// disappeared under a name somebody reused.
type ExistingImportError struct {
	Name string
	// Existing is the certificate there now, nil when it cannot be read.
	Existing *Certificate
}

func (e *ExistingImportError) Error() string {
	if e.Existing == nil {
		return fmt.Sprintf("%s is already imported. Replace it to overwrite that pair.", e.Name)
	}
	return fmt.Sprintf("%s is already imported: it covers %s and expires %s. Replace it to overwrite that pair.",
		e.Name, strings.Join(e.Existing.Domains, ", "), e.Existing.NotAfter.Format("2 Jan 2006"))
}

type importOptions struct {
	// replace overwrites an import of the same name.
	replace bool
	// keepPrevious leaves the pair being replaced beside it as .bak, which
	// is what makes replacing recoverable.
	keepPrevious bool
	// dryRun stops before anything is written, and before an existing
	// import is refused: Replaced then says one would be replaced.
	dryRun bool
}

// ImportCertificate validates a certificate and key and writes them to disk.
// An import of the same name is overwritten only when replace is set.
func ImportCertificate(name, certPEM, keyPEM string, replace bool) (*ImportResult, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if isCaddyEvidence(name) {
		return nil, fmt.Errorf("%s has the shape of Caddy's release copies, which are kept out of this list — choose another name", name)
	}
	return importCertificate(name, certPEM, keyPEM, importOptions{replace: replace, keepPrevious: true})
}

func importCertificate(name, certPEM, keyPEM string, opts importOptions) (*ImportResult, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !importNameRe.MatchString(name) {
		return nil, fmt.Errorf("name must be lowercase letters, digits, dots, dashes or underscores")
	}
	certs, err := parseCertificates(certPEM)
	if err != nil {
		return nil, err
	}
	key, keyBlock, err := decodePrivateKey(keyPEM)
	if err != nil {
		return nil, err
	}
	// The leaf is the certificate the key belongs to, wherever it sits: an
	// authority that sends the intermediates first is not rare, and reading
	// the first block as the leaf refused those as a mismatched key.
	leaf, err := findLeaf(key, certs)
	if err != nil {
		return nil, err
	}
	if leaf.NotAfter.Before(leaf.NotBefore) {
		return nil, fmt.Errorf("the certificate's validity dates are the wrong way round")
	}
	chain := orderChain(leaf, certs)

	dir := filepath.Join(importedDir, name)
	res := &ImportResult{
		Name:     name,
		CertPath: filepath.Join(dir, "fullchain.pem"),
		KeyPath:  filepath.Join(dir, "privkey.pem"),
		Warnings: []string{},
	}
	res.Cert = summarise(leaf, name, res.CertPath)
	res.Cert.Source = "imported"
	res.ChainComplete = chain.complete
	for _, c := range chain.certs {
		res.Chain = append(res.Chain, c.Subject.CommonName)
	}
	if res.Cert.Staging {
		res.Warnings = append(res.Warnings,
			"A staging authority signed this certificate: it is a test certificate, and every browser refuses it. Issue a real one for these names instead.")
	}
	res.Warnings = append(res.Warnings, chain.warnings(len(certs), res.Cert.Staging)...)
	if res.Cert.Expired {
		res.Warnings = append(res.Warnings, "This certificate has already expired.")
	} else if res.Cert.DaysLeft <= expiryWarningDays {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("This certificate expires in %d days, and nothing here will renew it automatically.", res.Cert.DaysLeft))
	}
	if res.Cert.SelfSigned {
		res.Warnings = append(res.Warnings,
			"This certificate is self-signed, so every browser will show a full-page warning before the site.")
	}

	_, certErr := os.Lstat(res.CertPath)
	_, keyErr := os.Lstat(res.KeyPath)
	res.Replaced = certErr == nil || keyErr == nil
	if opts.dryRun {
		return res, nil
	}
	if res.Replaced && !opts.replace {
		existing, _ := readCertificate(res.CertPath)
		return nil, &ExistingImportError{Name: name, Existing: existing}
	}

	var bundle bytes.Buffer
	for _, c := range chain.certs {
		pem.Encode(&bundle, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// The key is 0600 and the directory it sits in is not, matching how
	// certbot arranges live/: the certificate is public and the key is not.
	// Both are staged first and renamed into place, so a reload between the
	// two writes never pairs the new certificate with the old key for longer
	// than the gap between two renames.
	certTmp, err := stageFile(dir, bundle.Bytes(), 0o644)
	if err != nil {
		return nil, err
	}
	defer os.Remove(certTmp)
	keyTmp, err := stageFile(dir, pem.EncodeToMemory(keyBlock), 0o600)
	if err != nil {
		return nil, err
	}
	defer os.Remove(keyTmp)
	if res.Replaced && opts.keepPrevious {
		for _, path := range []string{res.CertPath, res.KeyPath} {
			if err := keepImportBackup(path); err != nil {
				return nil, err
			}
		}
	}
	if err := os.Rename(keyTmp, res.KeyPath); err != nil {
		return nil, err
	}
	if err := os.Rename(certTmp, res.CertPath); err != nil {
		return nil, err
	}
	return res, nil
}

// DeleteImportedCertificate removes an import: its certificate, its key and
// the copies a replacement kept. An enabled site naming it refuses the
// delete unless force. Caddy's release copies share the directory and are
// pruned from their own list, which checks the releases that name them.
func (s *Service) DeleteImportedCertificate(name string, force bool) error {
	if !importNameRe.MatchString(name) {
		return fmt.Errorf("invalid certificate name")
	}
	if isCaddyEvidence(name) {
		return fmt.Errorf("%s is a Caddy release copy: remove it from the release copies no route names", name)
	}
	dir := filepath.Join(importedDir, name)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrImportNotFound
	}
	if err != nil {
		return err
	}
	// A link here leads outside the directory this route may remove from.
	if !info.IsDir() {
		return fmt.Errorf("%s is not an imported certificate's directory", dir)
	}
	if sites := s.enabledCertSites().within(dir); len(sites) > 0 && !force {
		return &CertificateInUseError{Name: name, Sites: sites}
	}
	return os.RemoveAll(dir)
}

// ErrImportNotFound is a delete of an import that is not there.
var ErrImportNotFound = errors.New("no imported certificate has that name")

// stageFile writes content to a new file in dir with the given mode and
// returns its path.
func stageFile(dir string, content []byte, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(dir, ".import-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// keepImportBackup leaves path's current content at path.bak, as a hard link so the
// original stays where it is until the new file is renamed over it.
func keepImportBackup(path string) error {
	backup := path + ".bak"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(path, backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// parseCertificates reads every certificate in a bundle, in the order given.
func parseCertificates(certPEM string) ([]*x509.Certificate, error) {
	rest := []byte(certPEM)
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("that is not a readable certificate: %v", err)
		}
		certs = append(certs, parsed)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificate found — paste the block beginning with -----BEGIN CERTIFICATE-----")
	}
	return certs, nil
}

// parseCertChain reads every certificate in the block and returns the first,
// for a bundle whose order is already known — Caddy's own storage.
func parseCertChain(certPEM string) (*x509.Certificate, int, error) {
	certs, err := parseCertificates(certPEM)
	if err != nil {
		return nil, 0, err
	}
	leaf := certs[0]
	if leaf.NotAfter.Before(leaf.NotBefore) {
		return nil, 0, fmt.Errorf("the certificate's validity dates are the wrong way round")
	}
	return leaf, len(certs), nil
}

// importChain is a bundle put in the order nginx sends it.
type importChain struct {
	// certs is the leaf, then each certificate that signed the one before,
	// without the root: clients carry their own, and sending one only adds
	// to every handshake.
	certs []*x509.Certificate
	// root is the self-signed certificate the chain ended in, when the
	// bundle carried it.
	root *x509.Certificate
	// complete is the chain reaching a root, one in the bundle or one this
	// server trusts; reason is why not.
	complete bool
	reason   string
	// reordered and unused say what was changed from the bundle as given,
	// so the result can say it.
	reordered bool
	unused    int
}

// orderChain follows the leaf's issuers through the bundle by signature, not
// by name alone — an intermediate with the right subject and the wrong key is
// the mistake a name comparison waves through.
func orderChain(leaf *x509.Certificate, bundle []*x509.Certificate) importChain {
	chain := importChain{certs: []*x509.Certificate{leaf}}
	path := []*x509.Certificate{leaf}
	current := leaf
	for !isSelfSigned(current) {
		var issuer *x509.Certificate
		for _, c := range bundle {
			if !slices.Contains(path, c) && bytes.Equal(c.RawSubject, current.RawIssuer) && current.CheckSignatureFrom(c) == nil {
				issuer = c
				break
			}
		}
		if issuer == nil {
			break
		}
		path = append(path, issuer)
		if isSelfSigned(issuer) {
			chain.root = issuer
			break
		}
		chain.certs = append(chain.certs, issuer)
		current = issuer
	}
	// The bundle's own order, of the certificates the chain kept.
	given := slices.DeleteFunc(slices.Clone(bundle), func(c *x509.Certificate) bool {
		return !slices.Contains(path, c)
	})
	chain.reordered = !slices.Equal(given, path)
	chain.unused = len(bundle) - len(path)
	switch {
	case chain.root != nil:
		chain.complete = true
	case isSelfSigned(leaf):
		// Nothing is missing from a certificate that signed itself; the
		// self-signed warning is the one worth giving.
		chain.complete = true
	default:
		chain.complete, chain.reason = trustedChain(chain.certs)
	}
	return chain
}

// trustedChain verifies the chain against the system's roots, and against
// the configured ACME authority's where there is one, at a moment inside the
// leaf's validity: an expired import is reported as expired, not as a broken
// chain.
func trustedChain(certs []*x509.Certificate) (bool, string) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if path := acmeDirectory().CARoot; path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			roots.AppendCertsFromPEM(raw)
		}
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	leaf := certs[0]
	at := time.Now()
	if at.After(leaf.NotAfter) {
		at = leaf.NotAfter.Add(-time.Minute)
	} else if at.Before(leaf.NotBefore) {
		at = leaf.NotBefore.Add(time.Minute)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: intermediates, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// warnings says what the reader should know about the chain as saved. A
// staging chain reaches no root anything trusts by design, so it gets no
// verdict here: "an intermediate is missing" would send the reader looking
// for one, when the staging warning is the whole story.
func (c importChain) warnings(supplied int, staging bool) []string {
	var out []string
	switch {
	case c.complete || staging:
	case len(c.certs) == 1:
		out = append(out, "Only the leaf certificate was supplied, or none of the others signed it. Desktop browsers usually paper over a missing intermediate from cache; phones, curl and payment gateways do not. Paste the full chain if your authority provided one.")
	default:
		out = append(out, fmt.Sprintf("The chain does not reach a root this server trusts (%s). That is expected for a private authority; for a public one an intermediate is missing.", c.reason))
	}
	if c.unused > 0 {
		out = append(out, fmt.Sprintf("%d of the %d certificates supplied did not sign this one or its issuers, and were left out.", c.unused, supplied))
	}
	if c.reordered {
		out = append(out, "The certificates were not in order; they were saved leaf first, as nginx sends them.")
	}
	if c.root != nil {
		out = append(out, "The root certificate was left out: clients carry their own, and sending it only adds to every handshake.")
	}
	return out
}

// isSelfSigned is a certificate its own key signed. CheckSignature rather
// than CheckSignatureFrom: a self-signed leaf is usually not a CA, and the
// CA constraint would read it as signed by somebody else.
func isSelfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawSubject, c.RawIssuer) &&
		c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

// keyMatchesCertificate is the check worth having.
//
// A mismatched pair is accepted by every text editor and rejected by nginx at
// reload, which on a live server means finding out during an outage. Comparing
// the public halves catches it here instead.
func keyMatchesCertificate(keyPEM string, cert *x509.Certificate) error {
	key, _, err := decodePrivateKey(keyPEM)
	if err != nil {
		return err
	}
	if !publicKeyMatches(key, cert) {
		return fmt.Errorf("the private key does not belong to this certificate")
	}
	return nil
}

func publicKeyMatches(key crypto.PrivateKey, cert *x509.Certificate) bool {
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		priv, ok := key.(*rsa.PrivateKey)
		return ok && priv.PublicKey.Equal(pub)
	case *ecdsa.PublicKey:
		priv, ok := key.(*ecdsa.PrivateKey)
		return ok && priv.PublicKey.Equal(pub)
	case ed25519.PublicKey:
		priv, ok := key.(ed25519.PrivateKey)
		return ok && priv.Public().(ed25519.PublicKey).Equal(pub)
	}
	return false
}

// errEncryptedKey names the one refusal an operator can act on in a line.
var errEncryptedKey = errors.New("the private key is encrypted — enter the password it was exported with")

// decodePrivateKey finds the key among the blocks pasted. `openssl ecparam
// -genkey` writes an EC PARAMETERS block before the key, and reading only the
// first block refused every key made that way.
func decodePrivateKey(keyPEM string) (crypto.PrivateKey, *pem.Block, error) {
	rest := []byte(keyPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, nil, fmt.Errorf("no private key found — paste the block beginning with -----BEGIN PRIVATE KEY-----")
		}
		switch {
		case block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED"):
			return nil, nil, errEncryptedKey
		case block.Type == "PRIVATE KEY" || block.Type == "RSA PRIVATE KEY" || block.Type == "EC PRIVATE KEY":
			key, err := parsePrivateKey(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("that private key could not be read: %v", err)
			}
			return key, block, nil
		}
	}
}

// parsePrivateKey accepts the three encodings a certificate authority might
// hand back: PKCS#8, PKCS#1 and SEC 1.
func parsePrivateKey(der []byte) (crypto.PrivateKey, error) {
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("not a PKCS#8, PKCS#1 or SEC 1 key")
}
