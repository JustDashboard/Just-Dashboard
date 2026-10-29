package main

import (
	"strings"
	"testing"
)

// jd openapi prints the dashboard's description as it came, JSON or YAML, so
// a generator reads exactly what the server serves.
func TestOpenAPIPrintsTheDashboardsDescriptionAsItCame(t *testing.T) {
	f := newFake(t)
	code, stdout, stderr := jd(t, invocation{env: f.env(t)}, "openapi")
	if code != exitOK {
		t.Fatalf("exit %d %s %s", code, stdout, stderr)
	}
	if stdout != string(fakeOpenAPIJSON) {
		t.Errorf("stdout is not the server's document:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr: %s", stderr)
	}
	code, stdout, _ = jd(t, invocation{env: f.env(t)}, "openapi", "--yaml")
	if code != exitOK || stdout != string(fakeOpenAPIYAML) {
		t.Errorf("--yaml: exit %d\n%s", code, stdout)
	}
	if got := f.requests[len(f.requests)-1]; !strings.HasPrefix(got, "GET /api/v1/deploy/openapi.yaml") {
		t.Errorf("--yaml asked for %s", got)
	}
	if code, _, _ := jd(t, invocation{env: f.env(t)}, "openapi", "extra"); code != exitUsage {
		t.Errorf("a positional word: exit %d", code)
	}
	// A refused key is the same exit as everywhere else: 3, and the key
	// itself is never printed.
	env := f.env(t)
	env["JD_KEY"] = "vpsd_wrongkey"
	code, stdout, stderr = jd(t, invocation{env: env}, "openapi")
	if code != exitNoSession || strings.Contains(stdout+stderr, "vpsd_wrongkey") {
		t.Errorf("a refused key: exit %d %s %s", code, stdout, stderr)
	}
}
