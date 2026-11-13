// Package agent holds the identity a server presents when it is managed by a
// hub instead of being driven directly by a person.
//
// The trust model is deliberately small. There is no certificate authority and
// no chain to validate: the agent pins exactly one hub certificate, the hub
// pins exactly one agent certificate, and each side compares a SHA-256
// fingerprint. Pinning a single known key is both simpler to reason about and
// stricter than accepting anything a CA happened to sign.
//
// Enrolment is the one moment the agent will talk to a caller it does not yet
// trust, so it is bounded on every axis: the token is single-use, short-lived,
// compared in constant time, and refused outright once an enrolment already
// exists. A stolen token cannot re-point a live agent at somebody else's hub.
package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// How long a freshly minted enrolment token stays usable. Long enough to paste
// into the hub, short enough that one left in a scrollback is not a standing
// invitation.
const TokenTTL = 15 * time.Minute

// Certificates are long-lived because rotation is an enrolment, not a renewal:
// the pin has to change on both sides at once either way.
const certValidity = 10 * 365 * 24 * time.Hour

var (
	ErrNotEnrolled     = errors.New("agent is not enrolled with a hub")
	ErrAlreadyEnrolled = errors.New("agent is already enrolled; reset it before enrolling again")
	ErrTokenExpired    = errors.New("enrolment token has expired")
	ErrTokenMismatch   = errors.New("enrolment token is not valid")
)

// state is what survives a restart. The private key lives beside it in its own
// file so its permissions can be checked independently.
type state struct {
	AgentID string `json:"agentId"`

	// TokenHash is the SHA-256 of the pending enrolment token, hex encoded.
	// The token itself is printed once at startup and never stored.
	TokenHash   string    `json:"tokenHash,omitempty"`
	TokenExpiry time.Time `json:"tokenExpiry,omitzero"`

	HubFingerprint string    `json:"hubFingerprint,omitempty"`
	EnrolledAt     time.Time `json:"enrolledAt,omitzero"`
}

// Identity is the agent's keypair, its self-signed certificate, and whatever
// hub it has been enrolled with. It is safe for concurrent use.
type Identity struct {
	dir string

	mu    sync.RWMutex
	st    state
	key   *ecdsa.PrivateKey
	cert  []byte // DER
	certP []byte // PEM
	keyP  []byte // PEM
}

// Load reads the identity under dir, creating a keypair and certificate the
// first time. dir is created 0700 — the private key inside it is equivalent to
// root on this machine.
func Load(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent dir: %w", err)
	}
	id := &Identity{dir: dir}

	keyPath := filepath.Join(dir, "identity.key")
	certPath := filepath.Join(dir, "identity.crt")

	keyPEM, keyErr := os.ReadFile(keyPath)
	certPEM, certErr := os.ReadFile(certPath)
	if keyErr == nil && certErr == nil {
		if err := id.adopt(keyPEM, certPEM); err != nil {
			return nil, fmt.Errorf("agent identity is unreadable (delete %s to regenerate): %w", dir, err)
		}
	} else {
		if err := id.generate(); err != nil {
			return nil, err
		}
		if err := writeFile(keyPath, id.keyP, 0o600); err != nil {
			return nil, err
		}
		if err := writeFile(certPath, id.certP, 0o644); err != nil {
			return nil, err
		}
	}

	if raw, err := os.ReadFile(filepath.Join(dir, "enrolment.json")); err == nil {
		if err := json.Unmarshal(raw, &id.st); err != nil {
			return nil, fmt.Errorf("agent enrolment state is corrupt: %w", err)
		}
	}
	if id.st.AgentID == "" {
		id.st.AgentID = randomHex(16)
		if err := id.persist(); err != nil {
			return nil, err
		}
	}
	return id, nil
}

func (i *Identity) generate() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "just-dashboard-agent"
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"just-dashboard agent"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{host, "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	i.key = key
	i.cert = der
	i.certP = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	i.keyP = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return nil
}

func (i *Identity) adopt(keyPEM, certPEM []byte) error {
	kb, _ := pem.Decode(keyPEM)
	cb, _ := pem.Decode(certPEM)
	if kb == nil || cb == nil {
		return errors.New("key or certificate is not PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return err
	}
	if _, err := x509.ParseCertificate(cb.Bytes); err != nil {
		return err
	}
	i.key, i.cert, i.certP, i.keyP = key, cb.Bytes, certPEM, keyPEM
	return nil
}

func (i *Identity) persist() error {
	raw, err := json.MarshalIndent(i.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(i.dir, "enrolment.json"), append(raw, '\n'), 0o600)
}

// writeFile writes atomically so a crash mid-write cannot leave an identity
// half-replaced, and sets the mode before any content lands.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("agent: no entropy: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Fingerprint is the SHA-256 of a DER certificate, hex encoded — the value
// both sides compare.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func (i *Identity) ID() string { return i.st.AgentID }

func (i *Identity) CertPEM() []byte { return i.certP }

func (i *Identity) KeyPEM() []byte { return i.keyP }

func (i *Identity) Fingerprint() string { return Fingerprint(i.cert) }

// TLSCertificate is the pair handed to crypto/tls.
func (i *Identity) TLSCertificate() (certPEM, keyPEM []byte) { return i.certP, i.keyP }

func (i *Identity) Enrolled() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.st.HubFingerprint != ""
}

func (i *Identity) HubFingerprint() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.st.HubFingerprint
}

func (i *Identity) EnrolledAt() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.st.EnrolledAt
}

// NewEnrolmentToken mints the one-time secret an operator carries to the hub.
// It replaces any outstanding token, and returns an error once the agent is
// enrolled — a live agent is re-pointed by resetting it, deliberately, on the
// box itself.
func (i *Identity) NewEnrolmentToken() (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.st.HubFingerprint != "" {
		return "", ErrAlreadyEnrolled
	}
	token := randomHex(24)
	sum := sha256.Sum256([]byte(token))
	i.st.TokenHash = hex.EncodeToString(sum[:])
	i.st.TokenExpiry = time.Now().Add(TokenTTL)
	if err := i.persist(); err != nil {
		return "", err
	}
	return token, nil
}

// Enrol pins the hub that presented a valid token. The token is burned whether
// or not the caller ever comes back, so a replay finds nothing to use.
func (i *Identity) Enrol(token string, hubCertDER []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.st.HubFingerprint != "" {
		return ErrAlreadyEnrolled
	}
	if i.st.TokenHash == "" {
		return ErrTokenMismatch
	}
	if time.Now().After(i.st.TokenExpiry) {
		i.st.TokenHash, i.st.TokenExpiry = "", time.Time{}
		_ = i.persist()
		return ErrTokenExpired
	}

	sum := sha256.Sum256([]byte(token))
	presented := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(presented), []byte(i.st.TokenHash)) != 1 {
		return ErrTokenMismatch
	}
	if _, err := x509.ParseCertificate(hubCertDER); err != nil {
		return fmt.Errorf("hub certificate is unusable: %w", err)
	}

	i.st.HubFingerprint = Fingerprint(hubCertDER)
	i.st.EnrolledAt = time.Now().UTC()
	i.st.TokenHash, i.st.TokenExpiry = "", time.Time{}
	return i.persist()
}

// Reset forgets the enrolled hub so the agent can be handed to another one.
// It keeps the keypair, so the agent's own identity is stable across a move.
func (i *Identity) Reset() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.st.HubFingerprint = ""
	i.st.EnrolledAt = time.Time{}
	i.st.TokenHash, i.st.TokenExpiry = "", time.Time{}
	return i.persist()
}

// TrustsCert reports whether a presented client certificate is the enrolled
// hub. It is the whole authorisation decision for a hub-mode request.
func (i *Identity) TrustsCert(der []byte) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.st.HubFingerprint == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(Fingerprint(der)), []byte(i.st.HubFingerprint)) == 1
}
