package hublink

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
)

// The published vectors (docs/internal/backend/hub-link-vectors.json, protocol
// section 12) run against the code this dashboard signs and verifies with. The
// hub's tests carry the same file; Ed25519 is deterministic, so a match on both
// sides is two implementations agreeing byte for byte.

type vectors struct {
	Keys []struct {
		Role, SeedHex, PublicKey, Fingerprint, Kid string
	}
	CanonicalJSON []struct {
		Name, Input, Canonical, Sha256 string
	} `json:"canonicalJson"`
	RejectedArgs []struct {
		Name, Input string
	}
	RequestSignatures []struct {
		Name, Method, PathWithQuery, Body, BodySha256, XJdServer, SeedHex, PublicKey, SignedString, Signature string
		UnixTime, Sequence                                                                                    int64
	}
	CommandSignatures []struct {
		Name, Source, ServerID, CommandID, Kind, Scope, Args, CanonicalArgs, ArgsSha256 string
		Kid, SignerSeedHex, SignedString, Signature, VerifyServerID, VerifyPublicKey    string
		Expect                                                                          string
		ExpiresAt                                                                       int64
	}
	HelloBodies map[string]string
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("../../../docs/internal/backend/hub-link-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Keys) != 3 || len(v.RequestSignatures) == 0 || len(v.CommandSignatures) == 0 || len(v.HelloBodies) != 3 {
		t.Fatalf("the vectors file is not the one this test reads: %d keys", len(v.Keys))
	}
	return v
}

func TestVectorKeysAndFingerprints(t *testing.T) {
	for _, k := range loadVectors(t).Keys {
		key := seedKey(t, k.SeedHex)
		if got := pubB64(key); got != k.PublicKey {
			t.Errorf("%s public key = %s, want %s", k.Role, got, k.PublicKey)
		}
		if got := accountlink.Fingerprint(key.Public().(ed25519.PublicKey)); got != k.Fingerprint {
			t.Errorf("%s fingerprint = %s, want %s", k.Role, got, k.Fingerprint)
		}
		if k.Kid != "" && !kidPattern.MatchString(k.Kid) {
			t.Errorf("%s kid %q does not match the hub's kid pattern", k.Role, k.Kid)
		}
	}
}

func TestVectorCanonicalArguments(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.CanonicalJSON {
		got, err := accountlink.CanonicalArgs(json.RawMessage(c.Input))
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		sum := sha256.Sum256(got)
		if string(got) != c.Canonical || hex.EncodeToString(sum[:]) != c.Sha256 {
			t.Errorf("%s: canonical = %s (%x), want %s (%s)", c.Name, got, sum, c.Canonical, c.Sha256)
		}
	}
	for _, c := range v.RejectedArgs {
		if got, err := accountlink.CanonicalArgs(json.RawMessage(c.Input)); err == nil {
			t.Errorf("%s: %q was accepted as %s", c.Name, c.Input, got)
		}
	}
}

func TestVectorRequestSignatures(t *testing.T) {
	for _, c := range loadVectors(t).RequestSignatures {
		key := seedKey(t, c.SeedHex)
		sum := sha256.Sum256([]byte(c.Body))
		if hex.EncodeToString(sum[:]) != c.BodySha256 {
			t.Errorf("%s: body hash differs", c.Name)
		}
		if got := accountlink.RequestMessage(c.Method, c.PathWithQuery, []byte(c.Body), c.UnixTime, c.Sequence); got != c.SignedString {
			t.Errorf("%s: signed string = %q, want %q", c.Name, got, c.SignedString)
		}
		if got := accountlink.SignRequest(key, c.Method, c.PathWithQuery, []byte(c.Body), c.UnixTime, c.Sequence); got != c.Signature {
			t.Errorf("%s: signature = %s, want %s", c.Name, got, c.Signature)
		}
		// The fake hub's own reading of the signed string, which the other
		// tests verify every request with, agrees too.
		if got := signedString(c.Method, c.PathWithQuery, []byte(c.Body), c.UnixTime, c.Sequence); got != c.SignedString {
			t.Errorf("%s: the fake hub's signed string = %q", c.Name, got)
		}
		if strings.HasPrefix(c.PathWithQuery, "/api/hub/v1/") == strings.HasPrefix(c.PathWithQuery, "/api/link/v1/") {
			t.Errorf("%s: %s is under neither link's base path", c.Name, c.PathWithQuery)
		}
	}
}

func TestVectorCommandSignaturesAsTheDashboardChecksThem(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.CommandSignatures {
		canonical, err := accountlink.CanonicalArgs(json.RawMessage(c.Args))
		if err != nil || string(canonical) != c.CanonicalArgs {
			t.Errorf("%s: canonical args = %s, %v", c.Name, canonical, err)
			continue
		}
		if got := accountlink.CommandMessage(c.ServerID, c.CommandID, c.Kind, canonical, c.ExpiresAt); got != c.SignedString {
			t.Errorf("%s: signed string = %q, want %q", c.Name, got, c.SignedString)
		}
		sig := ed25519.Sign(seedKey(t, c.SignerSeedHex), []byte(c.SignedString))
		if got := base64.StdEncoding.EncodeToString(sig); got != c.Signature {
			t.Errorf("%s: signature = %s, want %s", c.Name, got, c.Signature)
		}
	}
}

// Every hub-link command case, delivered through the real connector to a
// server linked as the vectors' server with the vectors' hub key: the valid
// ones run, and the ones signed for another server or by a key that was never
// pinned are refused, recorded in the audit log and never reach the host.
func TestVectorCommandsThroughTheConnector(t *testing.T) {
	v := loadVectors(t)
	e := newEnv(t)
	st := e.link(accountlink.ScopeMaintain)
	if st.ServerID != vectorServerID || st.SiteKeys[0].PublicKey != pubB64(seedKey(t, vectorHubSeed)) {
		t.Fatalf("not linked as the vectors' server: %+v", st)
	}
	e.svc.Start(t.Context())
	want := map[string]string{}
	for _, c := range v.CommandSignatures {
		if c.Source != "hub-link" {
			continue
		}
		e.hub.queueCommands(accountlink.Command{
			ID: c.CommandID, Kind: c.Kind, Args: json.RawMessage(c.Args), IssuedBy: "owner", IssuedAt: c.ExpiresAt - 600,
			ExpiresAt: c.ExpiresAt, Kid: c.Kid, Signature: c.Signature,
		})
		want[c.CommandID] = map[string]string{"valid": "succeeded", "refused": "refused"}[c.Expect]
	}
	for id, status := range want {
		waitFor(t, "the result of "+id, func() bool { _, ok := e.hub.result(id); return ok })
		res, _ := e.hub.result(id)
		if res.Status != status {
			t.Errorf("%s: %s (%s), want %s", id, res.Status, res.Error, status)
		}
		if status == "refused" && !strings.Contains(res.Error, "does not verify") {
			t.Errorf("%s was refused for another reason: %s", id, res.Error)
		}
	}
	calls := e.host.called()
	if len(calls) != 5 {
		t.Errorf("host calls = %+v, want the five valid commands that reach it", calls)
	}
	for _, call := range calls {
		if call.Actor != "hub:hub" {
			t.Errorf("%s ran as %q", call.Method, call.Actor)
		}
	}
	refusedAudits := 0
	for _, row := range e.auditRows("hub.command.") {
		if !row.Success && strings.Contains(row.Detail, "does not verify") {
			refusedAudits++
		}
	}
	if refusedAudits != 2 {
		t.Fatalf("refused commands audited = %d, want 2", refusedAudits)
	}
}

func TestVectorHelloBodies(t *testing.T) {
	for state, body := range loadVectors(t).HelloBodies {
		if got := string(HelloBody(state)); got != body {
			t.Errorf("hello %s = %s, want %s", state, got, body)
		}
	}
}
