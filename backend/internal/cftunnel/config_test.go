package cftunnel

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderRoutesEachHostnameOnceToTheProxy(t *testing.T) {
	origin := Origin{Service: "http://just-dashboard-ingress:80"}
	configuration, err := Render([]string{"shop.example.com", " API.example.com ", "shop.example.com"}, origin)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(configuration)
	want := `{"ingress":[{"hostname":"api.example.com","service":"http://just-dashboard-ingress:80"},{"hostname":"shop.example.com","service":"http://just-dashboard-ingress:80"},{"service":"http_status:404"}]}`
	if string(raw) != want {
		t.Fatalf("rendered\n%s\nwant\n%s", raw, want)
	}
	again, _ := Render([]string{"api.example.com", "shop.example.com"}, origin)
	if again.Digest() != configuration.Digest() {
		t.Fatal("the same names rendered different bytes")
	}
	if strings.Join(configuration.Hostnames(), ",") != "api.example.com,shop.example.com" {
		t.Fatalf("hostnames = %v", configuration.Hostnames())
	}
	// Nothing routed is still a configuration: the catch-all alone.
	empty, err := Render(nil, origin)
	if err != nil || len(empty.Ingress) != 1 || empty.Ingress[0].Service != "http_status:404" {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
	// A proxy that only speaks HTTPS is asked with its own name, its
	// internal certificate accepted.
	secure, _ := Render([]string{"shop.example.com"}, Origin{Service: "https://caddy:443"})
	if rule := secure.Ingress[0]; rule.OriginRequest == nil || !rule.OriginRequest.NoTLSVerify || rule.OriginRequest.OriginServerName != "shop.example.com" {
		t.Fatalf("https origin = %+v", rule)
	}
	for _, bad := range []string{"*.example.com", "-bad.example.com", "localhost", "a..example.com"} {
		if _, err := Render([]string{bad}, origin); err == nil {
			t.Errorf("%s was routed", bad)
		}
	}
	if _, err := Render([]string{"shop.example.com"}, Origin{}); err == nil {
		t.Error("a configuration without an origin was rendered")
	}
}

func fakeToken(t *testing.T, fields map[string]string) string {
	t.Helper()
	raw, _ := json.Marshal(fields)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestParseTokenReadsTheAccountAndTunnel(t *testing.T) {
	token := fakeToken(t, map[string]string{"a": "acc123", "t": "11111111-2222-4333-8444-555555555555", "s": "c2VjcmV0"})
	fields, err := ParseToken("  " + token + "\n")
	if err != nil || fields.AccountID != "acc123" || fields.TunnelID != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("parsed = %+v, %v", fields, err)
	}
	for name, bad := range map[string]string{
		"empty":      "",
		"not base64": "not a token!",
		"no secret":  fakeToken(t, map[string]string{"a": "acc", "t": "11111111-2222-4333-8444-555555555555"}),
		"no tunnel":  fakeToken(t, map[string]string{"a": "acc", "t": "x", "s": "c2VjcmV0"}),
		"too long":   strings.Repeat("A", 5000),
	} {
		if _, err := ParseToken(bad); err == nil {
			t.Errorf("%s was read as a token", name)
		}
	}
	if TokenDigest(token) == TokenDigest(token+"x") || len(TokenDigest(token)) != 16 || strings.Contains(token, TokenDigest(token)) {
		t.Fatal("token digest")
	}
}

func TestLastComplaintIsCloudflaredsOwnWords(t *testing.T) {
	log := strings.Join([]string{
		"2026-10-29T10:04:20Z INF Starting tunnel tunnelID=11111111-2222-4333-8444-555555555555",
		"2026-10-29T10:04:23Z ERR Register tunnel error from server side error=\"Unauthorized: Invalid tunnel secret\" connIndex=0",
		"2026-10-29T10:04:23Z INF Retrying connection in up to 2s connIndex=0",
	}, "\n")
	if got := lastComplaint([]byte(log)); got != `Register tunnel error from server side error="Unauthorized: Invalid tunnel secret" connIndex=0` {
		t.Fatalf("complaint = %q", got)
	}
	// An exit before the logger: printed bare, with cloudflared's own hint
	// after it left out.
	exited := "Provided Tunnel token is not valid.\nSee 'cloudflared tunnel run --help'.\n"
	if got := lastComplaint([]byte(exited)); got != "Provided Tunnel token is not valid." {
		t.Fatalf("bare complaint = %q", got)
	}
	long := "2026-10-29T10:04:23Z ERR token " + strings.Repeat("A", 120)
	if got := lastComplaint([]byte(long)); strings.Contains(got, strings.Repeat("A", 80)) {
		t.Fatalf("a token-like string reached the page: %q", got)
	}
}
