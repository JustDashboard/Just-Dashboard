package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The diagram's memory: a layout saved against one connection and schema comes
// back on the next read, is isolated from every other schema, and refuses to
// store a document the diagram could not read back.
func TestDiagramLayoutIsRememberedPerConnectionAndSchema(t *testing.T) {
	_, router := dbTestRouter(t)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(http.MethodGet, "/databases/1/diagram?schema=public", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"layout":null`) {
		t.Fatalf("unarranged diagram = %d %s, want 200 with a null layout", rec.Code, rec.Body.String())
	}

	doc := `{"layout":{"version":1,"positions":{"users":{"x":12,"y":48}},"hidden":["audit_log"]}}`
	if rec := call(http.MethodPut, "/databases/1/diagram?schema=public", doc); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"updatedAt"`) {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	rec := call(http.MethodGet, "/databases/1/diagram?schema=public", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"users":{"x":12,"y":48}`) {
		t.Fatalf("read back = %d %s", rec.Code, rec.Body.String())
	}
	// Another schema on the same connection is a different picture.
	if rec := call(http.MethodGet, "/databases/1/diagram?schema=analytics", ""); !strings.Contains(rec.Body.String(), `"layout":null`) {
		t.Fatalf("another schema read the first one's layout: %s", rec.Body.String())
	}
	// Saving again replaces rather than duplicates.
	if rec := call(http.MethodPut, "/databases/1/diagram?schema=public", `{"layout":{"version":1,"positions":{}}}`); rec.Code != http.StatusOK {
		t.Fatalf("second save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(http.MethodGet, "/databases/1/diagram?schema=public", ""); strings.Contains(rec.Body.String(), "users") {
		t.Fatalf("second save did not replace the first: %s", rec.Body.String())
	}

	for _, bad := range []string{`{"layout":[1,2]}`, `{"layout":null}`, `{"layout":"x"}`, `{}`} {
		if rec := call(http.MethodPut, "/databases/1/diagram", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, rec.Code)
		}
	}
	oversized := `{"layout":{"note":"` + strings.Repeat("x", maxDiagramLayoutBytes) + `"}}`
	if rec := call(http.MethodPut, "/databases/1/diagram", oversized); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized layout = %d, want 400", rec.Code)
	}
	if rec := call(http.MethodGet, "/databases/99/diagram", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown connection = %d, want 404", rec.Code)
	}

	if rec := call(http.MethodDelete, "/databases/1/diagram?schema=public", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("reset = %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(http.MethodGet, "/databases/1/diagram?schema=public", ""); !strings.Contains(rec.Body.String(), `"layout":null`) {
		t.Fatalf("reset did not clear the layout: %s", rec.Body.String())
	}
}
