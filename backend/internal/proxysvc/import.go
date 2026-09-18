package proxysvc

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	// ChainComplete reports whether intermediates were included. A leaf on its
	// own works in every desktop browser and fails on exactly the clients
	// nobody tests with, so it is worth saying at import rather than leaving
	// to be discovered by a payment gateway.
	ChainComplete bool     `json:"chainComplete"`
	Warnings      []string `json:"warnings"`
}

// ImportCertificate validates a certificate and key and writes them to disk.
func ImportCertificate(name, certPEM, keyPEM string) (*ImportResult, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !importNameRe.MatchString(name) {
		return nil, fmt.Errorf("name must be lowercase letters, digits, dots, dashes or underscores")
	}
	leaf, chainLen, err := parseCertChain(certPEM)
	if err != nil {
		return nil, err
	}
	if err := keyMatchesCertificate(keyPEM, leaf); err != nil {
		return nil, err
	}

	res := &ImportResult{
		Name:          name,
		CertPath:      filepath.Join(importedDir, name, "fullchain.pem"),
		KeyPath:       filepath.Join(importedDir, name, "privkey.pem"),
		Cert:          summarise(leaf, name, filepath.Join(importedDir, name, "fullchain.pem")),
		ChainComplete: chainLen > 1,
		Warnings:      []string{},
	}
	res.Cert.Source = "imported"
	if chainLen == 1 {
		res.Warnings = append(res.Warnings,
			"Only the leaf certificate was supplied. Desktop browsers usually paper over a missing intermediate from cache; phones, curl and payment gateways do not. Paste the full chain if your authority provided one.")
	}
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

	dir := filepath.Join(importedDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(res.CertPath, []byte(normalisePEM(certPEM)), 0o644); err != nil {
		return nil, err
	}
	// The key is 0600 and the directory it sits in is not, matching how
	// certbot arranges live/: the certificate is public and the key is not.
	if err := os.WriteFile(res.KeyPath, []byte(normalisePEM(keyPEM)), 0o600); err != nil {
		return nil, err
	}
	if err := os.Chmod(res.KeyPath, 0o600); err != nil {
		return nil, err
	}
	return res, nil
}

// parseCertChain reads every certificate in the block and returns the leaf.
func parseCertChain(certPEM string) (*x509.Certificate, int, error) {
	rest := []byte(certPEM)
	var leaf *x509.Certificate
	count := 0
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
			return nil, 0, fmt.Errorf("that is not a readable certificate: %v", err)
		}
		count++
		if leaf == nil {
			leaf = parsed
		}
	}
	if leaf == nil {
		return nil, 0, fmt.Errorf("no certificate found — paste the block beginning with -----BEGIN CERTIFICATE-----")
	}
	if leaf.NotAfter.Before(leaf.NotBefore) {
		return nil, 0, fmt.Errorf("the certificate's validity dates are the wrong way round")
	}
	return leaf, count, nil
}

// keyMatchesCertificate is the check worth having.
//
// A mismatched pair is accepted by every text editor and rejected by nginx at
// reload, which on a live server means finding out during an outage. Comparing
// the public halves catches it here instead.
func keyMatchesCertificate(keyPEM string, cert *x509.Certificate) error {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return fmt.Errorf("no private key found — paste the block beginning with -----BEGIN PRIVATE KEY-----")
	}
	key, err := parsePrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("that private key could not be read: %v", err)
	}
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		priv, ok := key.(*rsa.PrivateKey)
		if !ok || !priv.PublicKey.Equal(pub) {
			return fmt.Errorf("the private key does not belong to this certificate")
		}
	case *ecdsa.PublicKey:
		priv, ok := key.(*ecdsa.PrivateKey)
		if !ok || !priv.PublicKey.Equal(pub) {
			return fmt.Errorf("the private key does not belong to this certificate")
		}
	case ed25519.PublicKey:
		priv, ok := key.(ed25519.PrivateKey)
		if !ok || !priv.Public().(ed25519.PublicKey).Equal(pub) {
			return fmt.Errorf("the private key does not belong to this certificate")
		}
	default:
		return fmt.Errorf("unsupported key type in the certificate")
	}
	return nil
}

// parsePrivateKey accepts the three encodings a certificate authority might
// hand back: PKCS#8, PKCS#1 and SEC 1.
func parsePrivateKey(der []byte) (any, error) {
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

// normalisePEM fixes the two things that survive a copy and paste: Windows
// line endings, and a missing final newline. Both make openssl and nginx
// complain about a file that is otherwise correct.
func normalisePEM(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.TrimSpace(content) + "\n"
}
