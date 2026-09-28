package proxysvc

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Buying a certificate starts with a signing request.
//
// The authority signs a public key it is sent, so the private key has to exist
// first, somewhere. Made with openssl on a laptop, it then travels — to the
// server, by scp or by pasting it into a form — which is exactly the journey
// a private key should not take. Made here, it is written once, 0600, in a
// root-only directory, and the request is the only thing that leaves. When
// the authority sends the certificate back, it is matched to the key waiting
// for it, and the pair moves in beside the imports with nothing pasted but
// the certificate.
//
// A request is kept under the name its certificate will have, and that name
// may already hold one: renewing a bought certificate is a new request for
// the same name, completed over the certificate a site is still serving.

var (
	ErrRequestExists   = errors.New("a signing request is already waiting under that name")
	ErrRequestNotFound = errors.New("no signing request is waiting under that name")
)

func requestsDir() string { return filepath.Join(privateDir, "requests") }

// CSRSubject is what an organisation-validated certificate says about its
// owner. A domain-validated one ignores it; the authority checks any of it
// that it prints, whatever the request says.
type CSRSubject struct {
	Organization string `json:"organization,omitempty"`
	Locality     string `json:"locality,omitempty"`
	Province     string `json:"province,omitempty"`
	Country      string `json:"country,omitempty"`
}

// SigningRequestInput asks for a key and a request for names.
type SigningRequestInput struct {
	Name    string     `json:"name"`
	Names   []string   `json:"names"`
	KeyType KeyType    `json:"keyType"`
	Subject CSRSubject `json:"subject"`
}

// SigningRequest is a request whose certificate has not arrived yet.
type SigningRequest struct {
	Name    string     `json:"name"`
	Domains []string   `json:"domains"`
	Subject CSRSubject `json:"subject"`
	KeyType KeyType    `json:"keyType,omitempty"`
	Created time.Time  `json:"created"`
	// CSR is the request itself, which is what the authority is sent.
	CSR string `json:"csr"`
	// KeyPath is where its key waits; the key itself is never sent back.
	KeyPath string `json:"keyPath"`
	// Replaces is the certificate already kept under the same name, which
	// completing the request replaces.
	Replaces *Certificate `json:"replaces,omitempty"`
	// Error is why the request cannot be completed as it stands.
	Error string `json:"error,omitempty"`
}

func (in CSRSubject) clean() (CSRSubject, error) {
	out := CSRSubject{
		Organization: strings.TrimSpace(in.Organization),
		Locality:     strings.TrimSpace(in.Locality),
		Province:     strings.TrimSpace(in.Province),
		Country:      strings.ToUpper(strings.TrimSpace(in.Country)),
	}
	for label, value := range map[string]string{"organisation": out.Organization, "city": out.Locality, "state or region": out.Province} {
		if len(value) > 64 {
			return out, invalidf("the %s is longer than the 64 characters a certificate allows", label)
		}
		if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) }) {
			return out, invalidf("the %s holds a control character", label)
		}
	}
	if out.Country != "" && (len(out.Country) != 2 || strings.Trim(out.Country, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return out, invalidf("the country is its two-letter code, such as DE or US")
	}
	return out, nil
}

func (in CSRSubject) name(commonName string) pkix.Name {
	name := pkix.Name{CommonName: commonName}
	if in.Organization != "" {
		name.Organization = []string{in.Organization}
	}
	if in.Locality != "" {
		name.Locality = []string{in.Locality}
	}
	if in.Province != "" {
		name.Province = []string{in.Province}
	}
	if in.Country != "" {
		name.Country = []string{in.Country}
	}
	return name
}

func subjectOf(name pkix.Name) CSRSubject {
	first := func(values []string) string {
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	return CSRSubject{
		Organization: first(name.Organization),
		Locality:     first(name.Locality),
		Province:     first(name.Province),
		Country:      first(name.Country),
	}
}

// CreateSigningRequest makes a key and a request for the names asked, and
// keeps both until the certificate comes back.
func CreateSigningRequest(in SigningRequestInput) (*SigningRequest, error) {
	name, err := privateName(in.Name)
	if err != nil {
		return nil, err
	}
	names, err := parseCertNames(in.Names)
	if err != nil {
		return nil, err
	}
	subject, err := in.Subject.clean()
	if err != nil {
		return nil, err
	}
	key, err := generateKey(in.KeyType)
	if err != nil {
		return nil, err
	}
	commonName := names.first
	if len(commonName) > 64 {
		commonName = ""
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:     subject.name(commonName),
		DNSNames:    names.dns,
		IPAddresses: names.ips,
	}, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}

	privateMu.Lock()
	defer privateMu.Unlock()
	dir := filepath.Join(requestsDir(), name)
	if err := makePrivateDir(requestsDir()); err != nil {
		return nil, err
	}
	// The directory is the claim on the name: a second request made at the
	// same moment finds it and is refused.
	if err := os.Mkdir(dir, 0o700); errors.Is(err, os.ErrExist) {
		return nil, ErrRequestExists
	} else if err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := writeStaged(dir, keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600, false); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	if err := writeStaged(dir, filepath.Join(dir, "request.csr"), csrPEM, 0o644, false); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return readSigningRequest(name)
}

// ListSigningRequests is every request still waiting, newest first. It
// waits for a request being made, so none is read half written.
func ListSigningRequests() ([]SigningRequest, error) {
	privateMu.Lock()
	defer privateMu.Unlock()
	entries, err := os.ReadDir(requestsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []SigningRequest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the waiting signing requests could not be read: %w", err)
	}
	out := []SigningRequest{}
	for _, e := range entries {
		if !e.IsDir() || !importNameRe.MatchString(e.Name()) {
			continue
		}
		request, err := readSigningRequest(e.Name())
		if err != nil {
			request = &SigningRequest{Name: e.Name(), Domains: []string{}, KeyPath: filepath.Join(requestsDir(), e.Name(), "privkey.pem"), Error: err.Error()}
		}
		out = append(out, *request)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func readSigningRequest(name string) (*SigningRequest, error) {
	dir := filepath.Join(requestsDir(), name)
	csrPath := filepath.Join(dir, "request.csr")
	raw, err := os.ReadFile(csrPath)
	if err != nil {
		return nil, fmt.Errorf("its request could not be read: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("its request.csr is not a signing request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("its request could not be read: %w", err)
	}
	info, _ := os.Stat(csrPath)
	request := &SigningRequest{
		Name:    name,
		Domains: append(slices.Clone(csr.DNSNames), addressStrings(csr)...),
		Subject: subjectOf(csr.Subject),
		KeyType: keyTypeOf(csr.PublicKey),
		CSR:     string(pem.EncodeToMemory(block)),
		KeyPath: filepath.Join(dir, "privkey.pem"),
	}
	if info != nil {
		request.Created = info.ModTime().UTC()
	}
	if len(request.Domains) == 0 && csr.Subject.CommonName != "" {
		request.Domains = []string{csr.Subject.CommonName}
	}
	if existing, err := readCertificate(filepath.Join(importedDir, name, "fullchain.pem")); err == nil {
		request.Replaces = existing
	}
	if _, err := waitingKey(name, csr.PublicKey); err != nil {
		request.Error = err.Error()
	}
	return request, nil
}

func addressStrings(csr *x509.CertificateRequest) []string {
	var out []string
	for _, ip := range csr.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

// waitingKey is the key a request was made with, read from where it waits
// and checked against the request's public half.
func waitingKey(name string, public crypto.PublicKey) (string, error) {
	raw, err := os.ReadFile(filepath.Join(requestsDir(), name, "privkey.pem"))
	if err != nil {
		return "", fmt.Errorf("its key could not be read: %w", err)
	}
	key, _, err := decodePrivateKey(string(raw))
	if err != nil {
		return "", fmt.Errorf("its key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok || !publicKeysEqual(signer.Public(), public) {
		return "", errors.New("the key waiting beside it is not the key it was made with")
	}
	return string(raw), nil
}

func publicKeysEqual(a, b crypto.PublicKey) bool {
	key, ok := a.(interface{ Equal(crypto.PublicKey) bool })
	return ok && key.Equal(b)
}

// CompleteSigningRequest imports the certificate an authority signed for a
// waiting request, with the key that has been waiting for it, then forgets
// the request. A certificate kept under the same name is replaced only when
// asked, as an import is, and kept as .bak.
func CompleteSigningRequest(name, certPEM string, replace bool) (*ImportResult, error) {
	name, err := privateName(name)
	if err != nil {
		return nil, err
	}
	privateMu.Lock()
	defer privateMu.Unlock()
	request, err := readSigningRequest(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrRequestNotFound
	}
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(request.CSR))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	keyPEM, err := waitingKey(name, csr.PublicKey)
	if err != nil {
		return nil, err
	}
	certs, err := parseCertificates(certPEM)
	if err != nil {
		return nil, invalidf("%s", err.Error())
	}
	var leaf *x509.Certificate
	for _, c := range certs {
		if publicKeysEqual(csr.PublicKey, c.PublicKey) {
			leaf = c
			break
		}
	}
	if leaf == nil {
		return nil, invalidf("this certificate was not signed for the request %s: its public key is not the one made for it. Paste the certificate the authority issued for this request", name)
	}
	if leaf.NotAfter.Before(leaf.NotBefore) {
		return nil, invalidf("the certificate's validity dates are the wrong way round")
	}
	res, err := importCertificate(name, certPEM, keyPEM, importOptions{replace: replace, keepPrevious: true})
	if err != nil {
		return nil, err
	}
	if missing := uncovered(request.Domains, certificateDomains(leaf)); len(missing) > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The request asked for %s as well, which the certificate does not cover.", strings.Join(missing, ", ")))
	}
	if err := os.RemoveAll(filepath.Join(requestsDir(), name)); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The request itself could not be removed (%v); discard it from the page.", err))
	}
	return res, nil
}

// uncovered is the names asked for that a certificate's names do not cover,
// a wildcard covering one label.
func uncovered(asked, names []string) []string {
	var out []string
	for _, want := range asked {
		covered := slices.ContainsFunc(names, func(have string) bool {
			if strings.EqualFold(have, want) {
				return true
			}
			rest, ok := strings.CutPrefix(have, "*.")
			_, parent, found := strings.Cut(want, ".")
			return ok && found && !strings.HasPrefix(want, "*.") && strings.EqualFold(parent, rest)
		})
		if !covered {
			out = append(out, want)
		}
	}
	return out
}

// DiscardSigningRequest removes a waiting request and its key. An authority
// that has already signed it has signed for a key that no longer exists, so
// that certificate can never be used: the request is made again.
func DiscardSigningRequest(name string) error {
	name, err := privateName(name)
	if err != nil {
		return err
	}
	privateMu.Lock()
	defer privateMu.Unlock()
	dir := filepath.Join(requestsDir(), name)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return ErrRequestNotFound
	}
	return os.RemoveAll(dir)
}
