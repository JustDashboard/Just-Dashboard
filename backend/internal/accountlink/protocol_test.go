package accountlink

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// These are the protocol document's test vectors, section 8. The site's tests
// carry the same ones; Ed25519 is deterministic, so a match here and there is
// two implementations agreeing byte for byte.

func TestProtocolVectorsFingerprintAndKeys(t *testing.T) {
	t.Parallel()
	server := seedKey(t, vectorServerSeed)
	if got := pubB64(server); got != "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=" {
		t.Fatalf("server public key = %s", got)
	}
	if got := Fingerprint(server.Public().(ed25519.PublicKey)); got != "21fe:31df:a154:a261" {
		t.Fatalf("fingerprint = %s", got)
	}
	if got := pubB64(seedKey(t, vectorSiteSeed)); got != "PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw=" {
		t.Fatalf("site public key = %s", got)
	}
}

func TestProtocolVectorsSignedRequests(t *testing.T) {
	t.Parallel()
	server := seedKey(t, vectorServerSeed)
	for _, tc := range []struct {
		name, method, path, body string
		seq                      int64
		signed, signature        string
	}{
		{
			name: "heartbeat", method: "POST", path: "/api/link/v1/heartbeat", body: `{"protocol":1,"sentAt":1761350400}`, seq: 42,
			signed:    "POST\n/api/link/v1/heartbeat\nee341c8e79b968efdc9c6624e4082ef76da73cb863eea0d9254865a9af8b0d69\n1761350400\n42",
			signature: "XDJtjqocqTgEY+2SwVbtdNvVMldQuQal9SfhVwlPraV6hm03G6XEGTyDN4WcDWfWExtMN6cn5hcqkHvvyKVaCA==",
		},
		{
			name: "command poll", method: "GET", path: "/api/link/v1/commands?wait=25", seq: 43,
			signed:    "GET\n/api/link/v1/commands?wait=25\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n1761350400\n43",
			signature: "GdFDiTSKbDEAba2E3QgpItGSZvaMWWuoZypCz3jAdHtUqN9Rtj+gTKJ+37tSREqS9z7JM2nRpvpqfzg35/PKCA==",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestMessage(tc.method, tc.path, []byte(tc.body), vectorNow, tc.seq); got != tc.signed {
				t.Fatalf("signed string = %q\nwant %q", got, tc.signed)
			}
			if got := signRequest(server, tc.method, tc.path, []byte(tc.body), vectorNow, tc.seq); got != tc.signature {
				t.Fatalf("signature = %s\nwant %s", got, tc.signature)
			}
		})
	}
}

func TestProtocolVectorsSignedCommands(t *testing.T) {
	t.Parallel()
	site := seedKey(t, vectorSiteSeed)
	for _, tc := range []struct {
		name, id, kind, args, canonical, sum, signature string
	}{
		{
			name: "update policy", id: "01JB0000000000000000000000", kind: "dashboard.update.policy",
			args:      `{"window":{"start":"03:00","end":"05:00"},"auto":"patch"}`,
			canonical: `{"auto":"patch","window":{"end":"05:00","start":"03:00"}}`,
			sum:       "b8c9c210e4ebc80fffa9dfb908b23ac8c8136fc0d4d01219dac5344eb31581e1",
			signature: "f9kQt1qTReHEQuwIp7W9fevwIaGnLvWXNo5YE1FgZih//V7rSvjUA5s7ItA4ARjh1JEB6NAA007un7+iBf58CQ==",
		},
		{
			name: "container restart", id: "01JB0000000000000000000001", kind: "container.restart",
			args:      `{"container":"shop-web"}`,
			canonical: `{"container":"shop-web"}`,
			sum:       "0b9f5b229e2ef3629c47700b7b4e220ba1bef8fa61bd2a1c2c9433c1a9528a36",
			signature: "bc1AyygpESzhigTdYoMZD9c9Rntn5HR2bXgrc3SRhDJNUO58CXrpUltv8SNou11NQbO5wDGRQnY2gXm5D1/1Cw==",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := decodeArgs(json.RawMessage(tc.args))
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := canonicalJSON(decoded)
			if err != nil || string(canonical) != tc.canonical {
				t.Fatalf("canonical = %s, %v\nwant %s", canonical, err, tc.canonical)
			}
			if sum := sha256.Sum256(canonical); hex.EncodeToString(sum[:]) != tc.sum {
				t.Fatalf("args sha256 = %x", sum)
			}
			msg := commandMessage(vectorServerID, tc.id, tc.kind, canonical, 1761351000)
			want := vectorServerID + "\n" + tc.id + "\n" + tc.kind + "\n" + tc.sum + "\n1761351000"
			if msg != want {
				t.Fatalf("signed string = %q\nwant %q", msg, want)
			}
			if got := base64.StdEncoding.EncodeToString(ed25519.Sign(site, []byte(msg))); got != tc.signature {
				t.Fatalf("signature = %s\nwant %s", got, tc.signature)
			}
			sig, _ := base64.StdEncoding.DecodeString(tc.signature)
			if !ed25519.Verify(site.Public().(ed25519.PublicKey), []byte(msg), sig) {
				t.Fatal("the document's signature does not verify against the document's site key")
			}
		})
	}
}

func TestCanonicalJSONIsTheSameBytesEveryImplementationWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"empty", `{}`, `{}`},
		{"sorted by code point", `{"b":1,"a":2,"B":3,"é":4}`, `{"B":3,"a":2,"b":1,"é":4}`},
		{"nested and whitespace", `{ "z" : { "y" : true , "x" : false } , "a" : "s" }`, `{"a":"s","z":{"x":false,"y":true}}`},
		{"no html escaping", `{"a":"<b>&amp;"}`, `{"a":"<b>&amp;"}`},
		{"quote backslash and controls", `{"a":"q\"b\\\n\t\u0001"}`, `{"a":"q\"b\\\n\t\u0001"}`},
		{"unicode is left alone", `{"a":"é "}`, "{\"a\":\"é \"}"},
		{"integers in plain decimal", `{"n":1000000,"m":-5,"z":0}`, `{"m":-5,"n":1000000,"z":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := decodeArgs(json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			got, err := canonicalJSON(v)
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %s, %v\nwant %s", got, err, tc.want)
			}
		})
	}
}

func TestArgumentsOutsideTheRestrictedValueSetAreRejected(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"array":         `{"a":[1]}`,
		"null":          `{"a":null}`,
		"fraction":      `{"a":1.5}`,
		"exponent":      `{"a":1e3}`,
		"trailing zero": `{"a":1.0}`,
		"too large":     `{"a":9007199254740993}`,
		"not an object": `"x"`,
		"top-level num": `1`,
		"trailing":      `{"a":1} {"b":2}`,
		"empty":         ``,
		"too deep":      `{"a":{"b":{"c":{"d":{"e":{"f":1}}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeArgs(json.RawMessage(in)); err == nil {
				t.Fatalf("%s accepted", in)
			}
		})
	}
}

func TestScopesNest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		have, need Scope
		want       bool
	}{
		{ScopeObserve, ScopeObserve, true}, {ScopeObserve, ScopeOperate, false}, {ScopeObserve, ScopeMaintain, false},
		{ScopeOperate, ScopeObserve, true}, {ScopeOperate, ScopeOperate, true}, {ScopeOperate, ScopeMaintain, false},
		{ScopeMaintain, ScopeObserve, true}, {ScopeMaintain, ScopeOperate, true}, {ScopeMaintain, ScopeMaintain, true},
		{"", ScopeObserve, false}, {"root", ScopeObserve, false},
	} {
		if got := tc.have.Includes(tc.need); got != tc.want {
			t.Errorf("%q includes %q = %v, want %v", tc.have, tc.need, got, tc.want)
		}
	}
	for kind, need := range map[string]Scope{
		"snapshot.now": ScopeObserve, "container.start": ScopeOperate, "container.stop": ScopeOperate,
		"container.restart": ScopeOperate, "stack.restart": ScopeOperate, "backup.run": ScopeOperate,
		"dashboard.update.install": ScopeMaintain, "dashboard.update.policy": ScopeMaintain, "dashboard.restart": ScopeMaintain,
	} {
		if got, ok := RequiredScope(kind); !ok || got != need {
			t.Errorf("%s needs %q (known %v), want %q", kind, got, ok, need)
		}
	}
	if _, ok := RequiredScope("container.exec"); ok {
		t.Error("an unlisted kind is known")
	}
}

func TestSiteURLIsAnOriginAndPlainHTTPOnlyOnLoopback(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"":                           "https://just-dashboard.com",
		"https://just-dashboard.com": "https://just-dashboard.com",
		"https://example.test:8443/": "https://example.test:8443",
		"http://localhost:3000":      "http://localhost:3000",
		"http://127.0.0.1:3000":      "http://127.0.0.1:3000",
		"http://[::1]:3000":          "http://[::1]:3000",
		// One spelling of an origin, whatever the site is written as.
		"https://Just-Dashboard.COM":     "https://just-dashboard.com",
		"https://just-dashboard.com:443": "https://just-dashboard.com",
		"HTTPS://Example.Test:8443":      "https://example.test:8443",
		"http://LOCALHOST:80/":           "http://localhost",
	} {
		if got, err := ParseSiteURL(in); err != nil || got != want {
			t.Errorf("ParseSiteURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"http://just-dashboard.com", "http://192.168.1.5", "ftp://just-dashboard.com", "just-dashboard.com",
		"https://user:pw@just-dashboard.com", "https://just-dashboard.com/path", "https://just-dashboard.com/?q=1",
		"https://just-dashboard.com/#frag", "https://", "http://localhost.evil.test",
	} {
		if got, err := ParseSiteURL(in); err == nil {
			t.Errorf("ParseSiteURL(%q) = %q, want an error", in, got)
		}
	}
	if !strings.HasPrefix(DefaultSiteURL, "https://") {
		t.Fatal("the default site is not https")
	}
}

func TestTheSitesOwnApprovalAddressIsOnItsOriginHoweverItIsSpelled(t *testing.T) {
	t.Parallel()
	origin, err := ParseSiteURL("https://Just-Dashboard.com:443")
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]bool{
		"https://just-dashboard.com/link?code=ABCD-2345":     true,
		"https://just-dashboard.com:443/link?code=ABCD-2345": true,
		"HTTPS://JUST-DASHBOARD.COM/link":                    true,
		"https://just-dashboard.com":                         true,
		"https://just-dashboard.com:8443/link":               false,
		"http://just-dashboard.com/link":                     false,
		"https://just-dashboard.com.evil.example/link":       false,
		"https://evil.example/link":                          false,
		"https://user:pw@just-dashboard.com/link":            false,
		"//just-dashboard.com/link":                          false,
		"/link":                                              false,
		"":                                                   false,
		"https://just-dashboard.com@evil.example/link":       false,
	} {
		if got := sameOrigin(origin, raw); got != want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", origin, raw, got, want)
		}
	}
	// And the origin need not have come through ParseSiteURL.
	if !sameOrigin("https://Just-Dashboard.com:443/", "https://just-dashboard.com/link") {
		t.Error("an unnormalised configured origin was not compared by what it means")
	}
	if !sameOrigin("http://127.0.0.1:3000", "http://127.0.0.1:3000/link") || sameOrigin("http://127.0.0.1:3000", "http://127.0.0.1:3001/link") {
		t.Error("a loopback development site is not compared by its port")
	}
}
