package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// A path parameter reaches its handler decoded.
//
// chi routes on r.URL.RawPath when the request carried one and slices the
// parameter out of the same string, so chi.URLParam hands back the escape
// sequences the client sent. The Docker image routes are where that surfaced:
// an image id is escaped by the browser as sha256%3Aab…, and the daemon
// answered "invalid reference format: repository name (library/sha256%3aab…)
// must be lowercase" — %3A is not a character a reference may contain.
func TestURLParamDecodesAnEscapedSegment(t *testing.T) {
	const id = "sha256:ae3f6ee6a602e788634d3b8729d6165c72551176dd2fdf1b8e6e6f2c11d9ae3e"

	got := ""
	r := chi.NewRouter()
	r.Get("/images/{id}", func(w http.ResponseWriter, req *http.Request) {
		got = URLParam(req, "id")
	})

	req := httptest.NewRequest(http.MethodGet,
		"/images/sha256%3Aae3f6ee6a602e788634d3b8729d6165c72551176dd2fdf1b8e6e6f2c11d9ae3e", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if got != id {
		t.Fatalf("URLParam = %q, want %q", got, id)
	}
	// The behaviour being worked around, asserted so the day chi changes it
	// this test says so rather than silently passing for a new reason.
	if raw := rawChiParam(); raw == id {
		t.Log("chi now decodes path parameters itself; URLParam is a no-op")
	}
}

// A segment that never needed escaping must not be decoded a second time — a
// literal percent in a name would otherwise turn into a different character.
func TestURLParamLeavesAnUnescapedSegmentAlone(t *testing.T) {
	got := ""
	r := chi.NewRouter()
	r.Get("/volumes/{name}", func(w http.ResponseWriter, req *http.Request) {
		got = URLParam(req, "name")
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/volumes/app_data-1.2", nil))

	if got != "app_data-1.2" {
		t.Fatalf("URLParam = %q, want %q", got, "app_data-1.2")
	}
}

// rawChiParam reads the same parameter through chi directly, so the assertion
// above documents which of the two is doing the decoding.
func rawChiParam() string {
	raw := ""
	router := chi.NewRouter()
	router.Get("/images/{id}", func(w http.ResponseWriter, req *http.Request) {
		raw = chi.URLParam(req, "id")
		io.WriteString(w, raw)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet,
		"/images/sha256%3Aae3f6ee6a602e788634d3b8729d6165c72551176dd2fdf1b8e6e6f2c11d9ae3e", nil))
	return raw
}
