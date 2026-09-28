package proxysvc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/youmark/pkcs8"
	"golang.org/x/crypto/pkcs12"
)

// What certificate authorities actually send.
//
// Two tidy PEM blocks is the exception. A Windows export is a .pfx with a
// password, a reseller's zip holds the leaf and a bundle in whichever order
// its tooling liked, a key made with `openssl genpkey -aes256` is encrypted,
// and an authority's intermediate is often only named in the leaf — at the
// address its Authority Information Access extension gives. Everything here
// turns those into the certificate and key PEM the import already checks, so
// there is still one path that writes to disk.

// ImportInput is an import as the page sends it: PEM text, or a PKCS#12 file,
// and whatever opens it.
type ImportInput struct {
	Name        string `json:"name"`
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
	// PFX is a PKCS#12 file (.pfx, .p12); when present it supplies both the
	// certificate and the key, and Certificate adds any chain it lacks.
	PFX []byte `json:"pfx"`
	// Password opens the PFX, or an encrypted private key.
	Password string `json:"password"`
	// FetchIssuer is the operator's consent to fetch a missing intermediate
	// from the address the certificate itself names.
	FetchIssuer bool `json:"fetchIssuer"`
	// Replace overwrites an import of the same name, keeping the previous
	// pair beside it as .bak. Without it an existing name is refused.
	Replace bool `json:"replace"`
}

// ImportInspection is what an import would do, read before anything is
// written: which names it covers, whether its chain holds, and what it would
// replace.
type ImportInspection struct {
	*ImportResult
	// SuggestedName is a name made from the certificate's common name, for a
	// form whose name is still empty.
	SuggestedName string `json:"suggestedName"`
	// Existing is the certificate an import of this name would replace, and
	// UsedBy the enabled sites that serve it.
	Existing *Certificate `json:"existing,omitempty"`
	UsedBy   []string     `json:"usedBy"`
	// IssuerURL is where the certificate says its missing issuer can be
	// fetched; it is fetched only once the operator agrees.
	IssuerURL string `json:"issuerURL,omitempty"`
}

// Import resolves what the page sent and imports it. A replaced import says
// which sites serve it, since each of them keeps the old pair until nginx
// reloads.
func (s *Service) Import(ctx context.Context, in ImportInput) (*ImportResult, error) {
	material, err := resolveImport(ctx, in)
	if err != nil {
		return nil, err
	}
	res, err := ImportCertificate(in.Name, material.certPEM, material.keyPEM, in.Replace)
	if err != nil {
		return nil, err
	}
	res.Warnings = append(res.Warnings, material.notes...)
	if res.Replaced {
		res.UsedBy = s.sitesUsing(res.CertPath)
	}
	return res, nil
}

// InspectImport runs every check an import makes and writes nothing.
func (s *Service) InspectImport(ctx context.Context, in ImportInput) (*ImportInspection, error) {
	material, err := resolveImport(ctx, in)
	if err != nil {
		return nil, err
	}
	leaf, err := material.leaf()
	if err != nil {
		return nil, err
	}
	out := &ImportInspection{SuggestedName: suggestImportName(leaf), UsedBy: []string{}}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if name == "" {
		name = out.SuggestedName
	}
	if isCaddyEvidence(name) {
		return nil, fmt.Errorf("%s has the shape of Caddy's release copies, which are kept out of this list — choose another name", name)
	}
	res, err := importCertificate(name, material.certPEM, material.keyPEM, importOptions{dryRun: true})
	if err != nil {
		return nil, err
	}
	res.Warnings = append(res.Warnings, material.notes...)
	out.ImportResult = res
	if res.Replaced {
		out.Existing, _ = readCertificate(res.CertPath)
		out.UsedBy = s.sitesUsing(res.CertPath)
	}
	if !res.ChainComplete && !res.Cert.Staging {
		out.IssuerURL = material.issuerURL
	}
	return out, nil
}

// sitesUsing names the enabled sites whose configuration points at path.
func (s *Service) sitesUsing(path string) []string {
	vhosts := s.nginxVHosts()
	enabled := map[string]bool{}
	for _, v := range vhosts {
		enabled[v.Name] = v.Enabled
	}
	out := []string{}
	for _, c := range listCertificates(filepath.Join(letsencryptDir, "live"), importedDir, vhosts) {
		if c.Path != path {
			continue
		}
		for _, site := range c.UsedBy {
			if enabled[site] {
				out = append(out, site)
			}
		}
	}
	return out
}

// importMaterial is the certificate and key as PEM the import can read, with
// what was done to get there.
type importMaterial struct {
	certPEM, keyPEM string
	// issuerURL is the first fetchable issuer address of the chain's last
	// certificate, when the chain stops short of a root.
	issuerURL string
	notes     []string
}

func (m importMaterial) leaf() (*x509.Certificate, error) {
	certs, err := parseCertificates(m.certPEM)
	if err != nil {
		return nil, err
	}
	key, _, err := decodePrivateKey(m.keyPEM)
	if err != nil {
		return nil, err
	}
	return findLeaf(key, certs)
}

func resolveImport(ctx context.Context, in ImportInput) (importMaterial, error) {
	m := importMaterial{certPEM: in.Certificate, keyPEM: in.Key}
	if len(in.PFX) > 0 {
		certPEM, keyPEM, err := decodePFX(ctx, in.PFX, in.Password)
		if err != nil {
			return m, err
		}
		// Chain certificates pasted beside a PFX are added to its own; the
		// key always comes from the PFX.
		m.certPEM, m.keyPEM = certPEM+in.Certificate, keyPEM
	} else {
		keyPEM, err := decryptKeyPEM(in.Key, in.Password)
		if err != nil {
			return m, err
		}
		m.keyPEM = keyPEM
	}
	leaf, err := m.leaf()
	if err != nil {
		return m, err
	}
	certs, _ := parseCertificates(m.certPEM)
	chain := orderChain(leaf, certs)
	for hops := 0; !chain.complete && hops < 3; hops++ {
		top := chain.certs[len(chain.certs)-1]
		m.issuerURL = issuerURL(top)
		if !in.FetchIssuer || m.issuerURL == "" {
			break
		}
		issuer, err := fetchIssuer(ctx, m.issuerURL)
		if err != nil {
			m.notes = append(m.notes, fmt.Sprintf("The missing intermediate could not be fetched from %s: %v", m.issuerURL, err))
			break
		}
		if top.CheckSignatureFrom(issuer) != nil {
			m.notes = append(m.notes, fmt.Sprintf("%s did not return the certificate that signed %s, so it was not added.", m.issuerURL, top.Subject.CommonName))
			break
		}
		m.certPEM += "\n" + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.Raw}))
		m.notes = append(m.notes, fmt.Sprintf("%s was fetched from %s, where the certificate names its issuer.", issuer.Subject.CommonName, m.issuerURL))
		m.issuerURL = ""
		certs = append(certs, issuer)
		chain = orderChain(leaf, certs)
	}
	return m, nil
}

// findLeaf is the certificate the key belongs to, wherever it sits in the
// bundle.
func findLeaf(key crypto.PrivateKey, certs []*x509.Certificate) (*x509.Certificate, error) {
	for _, c := range certs {
		if publicKeyMatches(key, c) {
			return c, nil
		}
	}
	if len(certs) == 1 {
		return nil, fmt.Errorf("the private key does not belong to this certificate")
	}
	return nil, fmt.Errorf("the private key belongs to none of the %d certificates supplied", len(certs))
}

var errWrongPassword = errors.New("that password does not open it")

// decryptKeyPEM returns the key blocks with any encrypted one decrypted, so
// the saved key is one nginx can read without a passphrase prompt at start.
// Other blocks — an EC PARAMETERS block, a certificate pasted alongside — are
// left for decodePrivateKey to skip.
func decryptKeyPEM(keyPEM, password string) (string, error) {
	rest := []byte(keyPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return keyPEM, nil
		}
		legacy := strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED")
		if block.Type != "ENCRYPTED PRIVATE KEY" && !legacy {
			continue
		}
		if password == "" {
			return "", errEncryptedKey
		}
		if legacy {
			// Deprecated because the format has no integrity check, which is
			// exactly why the key is compared with the certificate next: a
			// wrong password that happens to decrypt yields garbage, not a
			// key that matches.
			der, err := x509.DecryptPEMBlock(block, []byte(password)) //nolint:staticcheck
			if err != nil {
				return "", fmt.Errorf("the private key could not be decrypted: %w", errWrongPassword)
			}
			return string(pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: der})), nil
		}
		key, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, []byte(password))
		if err != nil {
			return "", fmt.Errorf("the private key could not be decrypted: %w (%v)", errWrongPassword, err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return "", fmt.Errorf("the private key could not be re-encoded: %v", err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
	}
}

// decodePFX turns a PKCS#12 file into certificate and key PEM.
//
// x/crypto reads the files Windows and older OpenSSL write (3DES and RC2);
// OpenSSL 3 and current Windows default to AES under PBES2, which it does not
// implement, so those go to the host's openssl. The password reaches openssl
// through its environment, never the argument vector a `ps` shows.
func decodePFX(ctx context.Context, data []byte, password string) (string, string, error) {
	blocks, err := pkcs12.ToPEM(data, password)
	switch {
	case errors.Is(err, pkcs12.ErrIncorrectPassword) || errors.Is(err, pkcs12.ErrDecryption):
		return "", "", fmt.Errorf("the PFX file could not be opened: %w", errWrongPassword)
	case err != nil:
		// An algorithm x/crypto lacks surfaces in several shapes, some of
		// them wrapped as plain strings, so any other failure is asked of
		// openssl, which reads every PFX a real exporter writes.
		blocks, err = decodePFXWithOpenSSL(ctx, data, password)
	}
	if err != nil {
		return "", "", err
	}
	var certs, keys bytes.Buffer
	for _, b := range blocks {
		// Bag attributes (friendlyName, localKeyId) travel as PEM headers,
		// and a header on an unencrypted key is something OpenSSL refuses.
		clean := &pem.Block{Type: b.Type, Bytes: b.Bytes}
		switch {
		case b.Type == "CERTIFICATE":
			pem.Encode(&certs, clean)
		case strings.HasSuffix(b.Type, "PRIVATE KEY") && keys.Len() == 0:
			pem.Encode(&keys, clean)
		}
	}
	if certs.Len() == 0 {
		return "", "", fmt.Errorf("the PFX file holds no certificate")
	}
	if keys.Len() == 0 {
		return "", "", fmt.Errorf("the PFX file holds no private key — export it again with the key included")
	}
	return certs.String(), keys.String(), nil
}

func decodePFXWithOpenSSL(ctx context.Context, data []byte, password string) ([]*pem.Block, error) {
	if !hostexec.Available("openssl") {
		return nil, fmt.Errorf("this PFX file uses an encryption only openssl reads (AES, as OpenSSL 3 and current Windows write), and openssl is not on this server — install it, or paste the certificate and key as PEM")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := hostexec.Command(ctx, "openssl", "pkcs12", "-nodes", "-passin", "env:JD_PFX_PASSWORD")
	cmd.Env = append(os.Environ(), "JD_PFX_PASSWORD="+password)
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.ToLower(stderr.String())
		if strings.Contains(msg, "mac verify") || strings.Contains(msg, "invalid password") {
			return nil, fmt.Errorf("the PFX file could not be opened: %w", errWrongPassword)
		}
		return nil, fmt.Errorf("openssl could not read the PFX file: %s", strings.TrimSpace(stderr.String()))
	}
	var blocks []*pem.Block
	rest := stdout.Bytes()
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return blocks, nil
		}
		blocks = append(blocks, block)
	}
}

// issuerURL is the first http(s) address the certificate gives for its
// issuer. ldap:// ones are skipped: nothing here speaks LDAP.
func issuerURL(c *x509.Certificate) string {
	for _, raw := range c.IssuingCertificateURL {
		if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return raw
		}
	}
	return ""
}

const issuerFetchLimit = 64 << 10

// issuerClient fetches from an address a certificate supplied, and a
// certificate is text anybody can write: every connection, redirects
// included, is refused unless it goes to a public address, so an AIA field
// cannot point this server at its own loopback or the LAN behind it.
var issuerClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if !IsPublicAddress(net.ParseIP(host)) {
					return fmt.Errorf("%s is not a public address", host)
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirected to %s", req.URL.Scheme)
		}
		return nil
	},
}

// fetchIssuer downloads the certificate an AIA address serves, DER or PEM.
func fetchIssuer(ctx context.Context, address string) (*x509.Certificate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := issuerClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("it answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, issuerFetchLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > issuerFetchLimit {
		return nil, errors.New("it sent more than a certificate's worth")
	}
	if block, _ := pem.Decode(body); block != nil && block.Type == "CERTIFICATE" {
		body = block.Bytes
	}
	cert, err := x509.ParseCertificate(body)
	if err != nil {
		// A .p7c bundle is the other shape AIA addresses serve.
		return nil, fmt.Errorf("it did not send a single certificate (%v)", err)
	}
	return cert, nil
}

var nameUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// suggestImportName makes a directory name from the common name, or the
// first DNS name: *.example.com becomes wildcard.example.com.
func suggestImportName(c *x509.Certificate) string {
	source := c.Subject.CommonName
	if source == "" && len(c.DNSNames) > 0 {
		source = c.DNSNames[0]
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if rest, ok := strings.CutPrefix(source, "*."); ok {
		source = "wildcard." + rest
	}
	name := strings.Trim(nameUnsafe.ReplaceAllString(source, "-"), "-._")
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-._")
	}
	if !importNameRe.MatchString(name) || isCaddyEvidence(name) {
		return ""
	}
	return name
}
