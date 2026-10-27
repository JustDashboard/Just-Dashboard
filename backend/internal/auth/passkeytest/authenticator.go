// Package passkeytest is a software WebAuthn authenticator for tests: it
// makes a P-256 credential, answers a registration with "none" attestation
// and signs assertions over exactly the bytes a browser and a real
// authenticator would, so a test can drive the server's ceremonies end to end
// and break them in the ways an attacker would — another origin, another
// relying party, a replayed challenge, a counter that goes backwards.
package passkeytest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// Authenticator holds one credential.
type Authenticator struct {
	key          *ecdsa.PrivateKey
	CredentialID []byte
	// UserHandle is what the server named the account at registration, and
	// what an assertion returns.
	UserHandle []byte
	// SignCount is the counter the next assertion reports minus one; a test
	// sets it lower to make the counter go backwards.
	SignCount uint32
	// UserVerified is the UV flag's value; off, the authenticator says it
	// did not verify its user.
	UserVerified bool
}

func New() *Authenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		panic(err)
	}
	return &Authenticator{key: key, CredentialID: id, UserVerified: true}
}

func b64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func clientData(ceremony, challenge, origin string) []byte {
	data, _ := json.Marshal(map[string]any{"type": ceremony, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return data
}

func (a *Authenticator) flags(attested bool) protocol.AuthenticatorFlags {
	flags := protocol.FlagUserPresent
	if a.UserVerified {
		flags |= protocol.FlagUserVerified
	}
	if attested {
		flags |= protocol.FlagAttestedCredentialData
	}
	return flags
}

func authenticatorData(rpID string, flags protocol.AuthenticatorFlags, count uint32, attested []byte) []byte {
	hash := sha256.Sum256([]byte(rpID))
	data := append([]byte{}, hash[:]...)
	data = append(data, byte(flags))
	data = binary.BigEndian.AppendUint32(data, count)
	return append(data, attested...)
}

func (a *Authenticator) publicKey() []byte {
	data, err := webauthncbor.Marshal(map[int64]any{
		1:  int64(webauthncose.EllipticKey),
		3:  int64(webauthncose.AlgES256),
		-1: int64(webauthncose.P256),
		-2: a.key.PublicKey.X.FillBytes(make([]byte, 32)),
		-3: a.key.PublicKey.Y.FillBytes(make([]byte, 32)),
	})
	if err != nil {
		panic(err)
	}
	return data
}

// Register answers navigator.credentials.create for the challenge the server
// gave, as a browser at origin would, for relying party rpID.
func (a *Authenticator) Register(challenge string, userHandle []byte, rpID, origin string) []byte {
	a.UserHandle = append([]byte{}, userHandle...)
	attested := make([]byte, 16)
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.CredentialID)))
	attested = append(attested, a.CredentialID...)
	attested = append(attested, a.publicKey()...)
	object, err := webauthncbor.Marshal(map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": authenticatorData(rpID, a.flags(true), a.SignCount, attested),
	})
	if err != nil {
		panic(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64(a.CredentialID), "rawId": b64(a.CredentialID), "type": "public-key",
		"response": map[string]any{
			"attestationObject": b64(object),
			"clientDataJSON":    b64(clientData("webauthn.create", challenge, origin)),
			"transports":        []string{"internal", "hybrid"},
		},
	})
	return body
}

// Assert answers navigator.credentials.get: the counter moves forward one,
// and the signature covers the authenticator data and the client data's hash.
func (a *Authenticator) Assert(challenge, rpID, origin string) []byte {
	a.SignCount++
	data := authenticatorData(rpID, a.flags(false), a.SignCount, nil)
	client := clientData("webauthn.get", challenge, origin)
	hash := sha256.Sum256(client)
	digest := sha256.Sum256(append(append([]byte{}, data...), hash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		panic(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64(a.CredentialID), "rawId": b64(a.CredentialID), "type": "public-key",
		"response": map[string]any{
			"authenticatorData": b64(data),
			"clientDataJSON":    b64(client),
			"signature":         b64(signature),
			"userHandle":        b64(a.UserHandle),
		},
	})
	return body
}
