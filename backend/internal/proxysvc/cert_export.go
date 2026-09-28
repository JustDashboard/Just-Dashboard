package proxysvc

import (
	"bytes"
	"context"
	"crypto"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Downloading what a listed certificate file holds, and exporting its key.
// Every part is cut from the file the inventory lists, so the path a caller
// names is only ever a key into that list; the private key leaves only after
// it is proven to be the certificate's own, so a site config naming some
// other file as its key cannot turn the export into a file reader.

// ErrKeyNotFound is a listed certificate whose private key this host does not
// have where a site, certbot or the import put it.
var ErrKeyNotFound = errors.New("no private key is known for this certificate")

// CertificateFile is a file handed to the browser.
type CertificateFile struct {
	// Name is the certificate's name in the inventory, which the export's
	// typed phrase repeats.
	Name     string
	Filename string
	Type     string
	Body     []byte
}

// minPFXPassword is the shortest password a PFX is sealed with: the file is
// the private key, and it travels by mail and chat.
const minPFXPassword = 8

// listedCertificate is the inventory's entry for path, and every certificate
// the file holds in file order.
func (s *Service) listedCertificate(path string) (Certificate, []byte, error) {
	vhosts := s.nginxVHosts()
	certs := listCertificates(filepath.Join(letsencryptDir, "live"), importedDir, vhosts)
	index := slices.IndexFunc(certs, func(c Certificate) bool { return c.Path == path })
	if index < 0 {
		return Certificate{}, nil, ErrCertificateNotListed
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Certificate{}, nil, err
	}
	return certs[index], raw, nil
}

// pemBlocks re-encodes the file's CERTIFICATE blocks, dropping anything else
// a file may carry beside them: a key pasted into a fullchain by hand must
// not leave through the public download.
func pemBlocks(raw []byte) [][]byte {
	var blocks [][]byte
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return blocks
		}
		if block.Type == "CERTIFICATE" {
			blocks = append(blocks, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes}))
		}
	}
}

// CertificatePart is one public part of the listed certificate at path:
// "fullchain" (the leaf and what follows it), "cert" (the leaf alone) or
// "chain" (the intermediates alone).
func (s *Service) CertificatePart(path, part string) (*CertificateFile, error) {
	cert, raw, err := s.listedCertificate(path)
	if err != nil {
		return nil, err
	}
	blocks := pemBlocks(raw)
	if len(blocks) == 0 {
		return nil, fmt.Errorf("%s holds no certificate", path)
	}
	var body [][]byte
	switch part {
	case "fullchain":
		body = blocks
	case "cert":
		body = blocks[:1]
	case "chain":
		if len(blocks) == 1 {
			return nil, errors.New("the file holds the certificate alone, with no chain after it")
		}
		body = blocks[1:]
	default:
		return nil, fmt.Errorf("part must be fullchain, cert or chain, not %q", part)
	}
	return &CertificateFile{
		Name:     cert.Name,
		Filename: exportBase(cert.Name) + "-" + part + ".pem",
		Type:     "application/x-pem-file",
		Body:     bytes.Join(body, nil),
	}, nil
}

// CertificateKeyFile is the PEM private key paired with the listed
// certificate at path, read only once it matches the certificate.
func (s *Service) CertificateKeyFile(path string) (*CertificateFile, []byte, error) {
	cert, raw, err := s.listedCertificate(path)
	if err != nil {
		return nil, nil, err
	}
	key, err := s.matchingKey(cert, raw)
	if err != nil {
		return nil, nil, err
	}
	return &CertificateFile{
		Name:     cert.Name,
		Filename: exportBase(cert.Name) + "-privkey.pem",
		Type:     "application/x-pem-file",
		Body:     key,
	}, raw, nil
}

// matchingKey reads the key nginx, certbot or the import pairs with cert and
// returns it only when its public half is the leaf's.
func (s *Service) matchingKey(cert Certificate, raw []byte) ([]byte, error) {
	chain, err := decodeChain(raw)
	if err != nil {
		return nil, err
	}
	keyPath, _ := certificateKeyPath(cert, s.nginxVHosts())
	if keyPath == "" {
		return nil, ErrKeyNotFound
	}
	f, err := os.Open(keyPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	key, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes))
	if err != nil {
		return nil, err
	}
	public, err := privateKeyPublic(key)
	if err != nil {
		return nil, err
	}
	eq, ok := public.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !eq.Equal(chain[0].PublicKey) {
		return nil, errors.New("the key the certificate is paired with is not its key, so it is not exported")
	}
	return onlyPrivateKey(key), nil
}

// onlyPrivateKey is the file's first unencrypted private key block, so the
// download is the key and not whatever else the key file carries.
func onlyPrivateKey(raw []byte) []byte {
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return nil
		}
		if strings.Contains(block.Type, "PRIVATE KEY") {
			return pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: block.Bytes})
		}
	}
}

// CertificatePFX seals the listed certificate at path, its chain and its key
// into a PKCS#12 file. openssl reads the key and the certificates from pipes
// and the password from its environment, so neither is ever in an argv that
// ps shows or on disk. Legacy is 3DES with a SHA-1 MAC, which Windows before
// Server 2019, older macOS keychains and Java 8 need; the default is
// OpenSSL 3's AES-256 with PBKDF2.
func (s *Service) CertificatePFX(ctx context.Context, path, password string, legacy bool) (*CertificateFile, error) {
	if len(password) < minPFXPassword {
		return nil, fmt.Errorf("the password must be at least %d characters", minPFXPassword)
	}
	keyFile, raw, err := s.CertificateKeyFile(path)
	if err != nil {
		return nil, err
	}
	args := []string{"pkcs12", "-export", "-inkey", "/dev/fd/3", "-in", "/dev/fd/4",
		"-passout", "env:JD_PFX_PASS", "-name", keyFile.Name}
	if legacy {
		args = append(args, "-certpbe", "PBE-SHA1-3DES", "-keypbe", "PBE-SHA1-3DES", "-macalg", "sha1")
	}
	cmd := hostexec.Command(ctx, "openssl", args...)
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "JD_PFX_PASS="+password)
	keyRead, err := feedPipe(keyFile.Body)
	if err != nil {
		return nil, err
	}
	defer keyRead.Close()
	certRead, err := feedPipe(bytes.Join(pemBlocks(raw), nil))
	if err != nil {
		return nil, err
	}
	defer certRead.Close()
	cmd.ExtraFiles = []*os.File{keyRead, certRead}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("openssl pkcs12: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return &CertificateFile{
		Name:     keyFile.Name,
		Filename: exportBase(keyFile.Name) + ".pfx",
		Type:     "application/x-pkcs12",
		Body:     stdout.Bytes(),
	}, nil
}

// feedPipe is the read end of a pipe the content is written into. A key and
// a chain are a few kilobytes, well inside the pipe buffer, but the write
// runs apart so a larger chain cannot block the caller before openssl reads.
func feedPipe(content []byte) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	go func() {
		_, _ = w.Write(content)
		_ = w.Close()
	}()
	return r, nil
}

// exportBase is the certificate's name as a filename: the inventory's names
// are lineage and import names, but one read from a site may be a domain
// with a wildcard.
func exportBase(name string) string {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, name)
	if strings.Trim(base, "._") == "" {
		return "certificate"
	}
	return base
}
