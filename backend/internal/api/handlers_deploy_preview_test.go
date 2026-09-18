package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestDeploymentPreviewURLValidation(t *testing.T) {
	for _, raw := range []string{"", "https://*.example.com", "javascript:alert(1)", "https://user:secret@example.com", "https://example.com;frame-src *", "https://example.com\n", "//example.com", "file:///etc/passwd"} {
		if _, err := deploymentPreviewURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"example.test", "https://example.test", "http://127.0.0.1:8080"} {
		if _, err := deploymentPreviewURL(raw); err != nil {
			t.Errorf("refused %q: %v", raw, err)
		}
	}
}

func TestDeploymentPreviewFramesOnlyRecordedOrigin(t *testing.T) {
	s := testServer(t)
	id, env, _ := insertDeploymentConfigurationAPI(t, s)
	_, err := s.Store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id, kind, ownership, resource_kind, resource_id, created_at) VALUES(?, 'domain', 'managed', 'proxy_site', 'https://web.example.test', 1)`, env)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deploy/%d/preview-frame", id)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "preview-reader", auth.RoleReadOnly)}
	res := reader.do(http.MethodGet, path+"?url=https://attacker.example", "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", res.Code, res.Body.String())
	}
	policy := res.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "frame-src https://web.example.test;") || !strings.Contains(policy, "frame-ancestors 'self'") {
		t.Fatalf("policy: %s", policy)
	}
	if strings.Contains(res.Body.String(), "attacker.example") || !strings.Contains(res.Body.String(), `sandbox="allow-scripts allow-same-origin allow-forms"`) {
		t.Fatalf("wrapper: %s", res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("preview can be cached")
	}
	anon := &client{t: t, h: s.Routes()}
	if res := anon.do(http.MethodGet, path, "", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", res.Code)
	}
}
