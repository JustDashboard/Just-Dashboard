package api

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestAdvisoryRoutesAreRemoved(t *testing.T) {
	s := testServer(t)
	retired := map[string]bool{
		"/api/v1/docker/health":                        true,
		"/api/v1/docker/containers/{id}/failure":       true,
		"/api/v1/docker/containers/{id}/anomalies":     true,
		"/api/v1/security/posture":                     true,
		"/api/v1/databases/{id}/advisor":               true,
		"/api/v1/deploy/{id}/environments/{env}/check": true,
		"/api/v1/certificates/findings":                true,
		"/api/v1/proxy/findings/snoozes":               true,
	}
	if err := chi.Walk(s.Routes().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if retired[route] {
			t.Errorf("retired advisory route still registered: %s %s", method, route)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for _, path := range []string{"/api/v1/docker/health", "/api/v1/security/posture", "/api/v1/certificates/findings", "/api/v1/proxy/findings/snoozes"} {
		if w := c.do(http.MethodGet, path, "", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s returned %d, want 404: %s", path, w.Code, w.Body.String())
		}
	}
}
