package netx

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// WireGuard keys are made here rather than by `wg genkey`: the private key
// then never exists as the output of a subprocess, and the pair can be made
// on a host whose `wg` is the thing being installed next. A key is 32 bytes,
// written in standard base64 (44 characters).
const wgKeyLen = 32

// newWGPrivateKey is 32 random bytes clamped as RFC 7748 §5 asks of an X25519
// scalar. X25519 clamps internally, so an unclamped key would derive the same
// public key, but `wg` prints and stores the clamped form and so do we: a key
// that is the same on disk as in memory is one nobody has to reason about.
func newWGPrivateKey() (string, error) {
	var k [wgKeyLen]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", fmt.Errorf("random bytes for a key: %w", err)
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

// newWGPresharedKey is 32 random bytes with no structure: a symmetric key
// mixed into the handshake, so a peer stays safe against a future computer
// that can break the curve.
func newWGPresharedKey() (string, error) {
	var k [wgKeyLen]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", fmt.Errorf("random bytes for a key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

// wgPublicKey derives the public key of a private key, the X25519 product of
// the scalar and the curve's base point.
func wgPublicKey(private string) (string, error) {
	raw, err := decodeWGKey(private)
	if err != nil {
		return "", err
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("deriving a public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// newWGKeyPair is a private key and its public key.
func newWGKeyPair() (private, public string, err error) {
	private, err = newWGPrivateKey()
	if err != nil {
		return "", "", err
	}
	public, err = wgPublicKey(private)
	if err != nil {
		return "", "", err
	}
	return private, public, nil
}

// decodeWGKey reads a base64 key and checks its length, the only validation a
// key has: any 32 bytes is a valid private, public or preshared key.
func decodeWGKey(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != wgKeyLen {
		return nil, fmt.Errorf("not a WireGuard key (32 bytes in base64)")
	}
	return raw, nil
}

// validWGKey reports whether s is shaped like a key, for values read from a
// file the dashboard did not write.
func validWGKey(s string) bool {
	_, err := decodeWGKey(s)
	return err == nil
}
