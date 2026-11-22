package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// hookBackend is the dashboard's hook route as the hook sees it: it checks
// the token and records what it was asked.
type hookBackend struct {
	mu     sync.Mutex
	calls  []map[string]string
	status int
	reply  string
}

func (h *hookBackend) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/app-domain/hook/challenge" {
		http.Error(w, `{"error":{"code":"not_found","message":"no route"}}`, http.StatusNotFound)
		return
	}
	if r.Header.Get("Authorization") != "Bearer jdh_secret" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthorized","message":"the hook's credential is not this server's"}}`)
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	h.calls = append(h.calls, body)
	if h.status != 0 {
		w.WriteHeader(h.status)
		_, _ = io.WriteString(w, h.reply)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeHookCredential(t *testing.T, url, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app-domain-hook.json")
	raw, _ := json.Marshal(hookCredential{URL: url, Token: token})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheCertbotHookAsksTheDashboardToPublishAndWithdraw(t *testing.T) {
	backend := &hookBackend{}
	srv := httptest.NewServer(http.HandlerFunc(backend.serve))
	defer srv.Close()
	cred := writeHookCredential(t, srv.URL, "jdh_secret")
	env := map[string]string{"CERTBOT_VALIDATION": "Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZmdoaWo", "CERTBOT_DOMAIN": "acme.just-dashboard.com"}

	for _, action := range []string{"add", "remove"} {
		code, _, stderr := jd(t, invocation{env: env}, "app-domain", "challenge", action, "--credentials", cred)
		if code != exitOK {
			t.Fatalf("%s: exit %d %s", action, code, stderr)
		}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.calls) != 2 || backend.calls[0]["action"] != "add" || backend.calls[1]["action"] != "remove" ||
		backend.calls[0]["domain"] != "acme.just-dashboard.com" || backend.calls[0]["value"] != env["CERTBOT_VALIDATION"] {
		t.Fatalf("calls = %v", backend.calls)
	}
}

func TestTheCertbotHookFailsClearly(t *testing.T) {
	backend := &hookBackend{status: http.StatusBadGateway, reply: `{"error":{"code":"challenge_failed","message":"the site published the challenge, but just-dashboard.com's nameservers ada.ns.example.net did not answer the challenge at _acme-challenge.acme.just-dashboard.com in time"}}`}
	srv := httptest.NewServer(http.HandlerFunc(backend.serve))
	defer srv.Close()
	env := map[string]string{"CERTBOT_VALIDATION": "v", "CERTBOT_DOMAIN": "acme.just-dashboard.com"}

	code, _, stderr := jd(t, invocation{env: env}, "app-domain", "challenge", "add", "--credentials", writeHookCredential(t, srv.URL, "jdh_secret"))
	if code != exitRefused || !strings.Contains(stderr, "did not answer") {
		t.Fatalf("slow zone: exit %d %s", code, stderr)
	}
	code, _, stderr = jd(t, invocation{env: env}, "app-domain", "challenge", "add", "--credentials", writeHookCredential(t, srv.URL, "jdh_wrong"))
	if code != exitNoSession || !strings.Contains(stderr, "credential") {
		t.Fatalf("wrong token: exit %d %s", code, stderr)
	}
	code, _, stderr = jd(t, invocation{env: env}, "app-domain", "challenge", "add", "--credentials", filepath.Join(t.TempDir(), "missing.json"))
	if code != exitRefused || !strings.Contains(stderr, "is missing") {
		t.Fatalf("no credential: exit %d %s", code, stderr)
	}
	code, _, stderr = jd(t, invocation{env: map[string]string{}}, "app-domain", "challenge", "add", "--credentials", writeHookCredential(t, srv.URL, "jdh_secret"))
	if code != exitUsage || !strings.Contains(stderr, "CERTBOT_VALIDATION") {
		t.Fatalf("outside certbot: exit %d %s", code, stderr)
	}
	if code, _, _ := jd(t, invocation{env: env}, "app-domain", "challenge", "replace"); code != exitUsage {
		t.Fatalf("an unknown action: exit %d", code)
	}
	if code, _, _ := jd(t, invocation{env: env}, "app-domain", "status"); code != exitUsage {
		t.Fatalf("an unknown verb: exit %d", code)
	}
	// A credential naming another machine over plain http is refused before
	// the token is sent, as every jd command refuses it.
	code, _, stderr = jd(t, invocation{env: env}, "app-domain", "challenge", "add", "--credentials", writeHookCredential(t, "http://198.51.100.7:8080", "jdh_secret"))
	if code != exitUsage || !strings.Contains(stderr, "plain http") {
		t.Fatalf("remote http: exit %d %s", code, stderr)
	}
}
