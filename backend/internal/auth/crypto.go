package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var ErrDecrypt = errors.New("decrypt failed")

// Sealer encrypts at-rest secrets (TOTP seeds, database DSNs, deploy env vars,
// backup provider credentials) with AES-256-GCM under the operator's master
// key. It is supplied via JD_MASTER_KEY, which cmd/server removes from the
// process environment as soon as this type has consumed it — otherwise every
// child process the dashboard spawns would inherit it.
type Sealer struct {
	aead cipher.AEAD
	// hmacKey is derived from the master key, not the master key itself:
	// DeriveHMAC's output reaches request handlers (a suggested hostname), so
	// it must not be computable from the same bytes that decrypt every secret
	// sealed at rest.
	hmacKey []byte
}

func NewSealer(masterKeyHex string) (*Sealer, error) {
	key, err := hex.DecodeString(strings.TrimSpace(masterKeyHex))
	if err != nil {
		return nil, fmt.Errorf("master key is not valid hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must decode to 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	derived := sha256.Sum256(append([]byte("jd-derive-v1"), key...))
	return &Sealer{aead: aead, hmacKey: derived[:]}, nil
}

// DeriveHMAC returns HMAC-SHA256(derivedKey, label || 0x00 || data), where
// derivedKey is sha256("jd-derive-v1" || masterKey): a value stable for the
// life of this install and unique to label+data, without exposing the key
// that seals secrets at rest. Callers use it where a deterministic
// per-install secret is needed — a hostname suggestion, for instance, that
// must propose the same address every time it is asked for the same name.
func (s *Sealer) DeriveHMAC(label string, data []byte) []byte {
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(label))
	mac.Write([]byte{0})
	mac.Write(data)
	return mac.Sum(nil)
}

func (s *Sealer) Seal(plaintext string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (s *Sealer) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", ErrDecrypt
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return "", ErrDecrypt
	}
	pt, err := s.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(pt), nil
}

type argonParams struct {
	time, memory uint32
	threads      uint8
	keyLen       uint32
}

var defaultArgon = argonParams{time: 3, memory: 64 * 1024, threads: 4, keyLen: 32}

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := defaultArgon
	sum := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum)), nil
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	p.keyLen = uint32(len(want))
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RandomToken returns a URL-safe secret with n bytes of entropy.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken is the at-rest representation of bearer secrets. These are already
// high-entropy random values, so a fast digest is appropriate — unlike
// passwords, they are not guessable and do not need a memory-hard KDF.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
