package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// mapTestRouter mounts the map's routes behind a principal the test names by
// header, so two accounts and a key can be told apart in one test.
func mapTestRouter(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	for _, u := range []struct {
		id   int64
		role auth.Role
	}{{1, auth.RoleAdmin}, {2, auth.RoleReadOnly}} {
		if _, err := s.Store.DB.Exec(`INSERT INTO users(id, username, password_hash, role, created_at) VALUES(?, ?, 'x', ?, 1)`,
			u.id, "user"+string(rune('0'+u.id)), string(u.role)); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "user1"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			switch req.Header.Get("X-Test-As") {
			case "reader":
				p = &httpx.Principal{User: &auth.User{ID: 2, Username: "user2"}, Role: auth.RoleReadOnly, Kind: "session", IP: "127.0.0.1"}
			case "key":
				p = &httpx.Principal{User: &auth.User{ID: 1, Username: "user1"}, Role: auth.RoleAdmin, Kind: "token", TokenID: 9, IP: "127.0.0.1"}
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountMapRoutes(r)
	return s, r
}

func mapCall(t *testing.T, h http.Handler, method, path, as, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if as != "" {
		req.Header.Set("X-Test-As", as)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Each account keeps its own arrangement, an API key keeps none, and a
// document the map could not read back is refused.
func TestMapLayoutIsEachAccountsOwn(t *testing.T) {
	_, h := mapTestRouter(t)
	if rec := mapCall(t, h, http.MethodGet, "/map/layout", "", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"layout":null`) {
		t.Fatalf("an unarranged map = %d %s", rec.Code, rec.Body.String())
	}
	doc := `{"layout":{"version":1,"positions":{"server":{"x":4,"y":8}},"collapsed":["branch:jobs"]}}`
	if rec := mapCall(t, h, http.MethodPut, "/map/layout", "", doc); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"updatedAt"`) {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	if rec := mapCall(t, h, http.MethodGet, "/map/layout", "", ""); !strings.Contains(rec.Body.String(), `"branch:jobs"`) {
		t.Fatalf("read back = %s", rec.Body.String())
	}
	// Another account's map is its own, and the readonly role keeps one too.
	if rec := mapCall(t, h, http.MethodGet, "/map/layout", "reader", ""); !strings.Contains(rec.Body.String(), `"layout":null`) {
		t.Fatalf("a second account read the first one's arrangement: %s", rec.Body.String())
	}
	if rec := mapCall(t, h, http.MethodPut, "/map/layout", "reader", `{"layout":{"version":1}}`); rec.Code != http.StatusOK {
		t.Fatalf("a readonly account could not keep its arrangement: %d %s", rec.Code, rec.Body.String())
	}
	if rec := mapCall(t, h, http.MethodPut, "/map/layout", "key", doc); rec.Code != http.StatusForbidden {
		t.Errorf("an API key saved an arrangement: %d", rec.Code)
	}
	for _, bad := range []string{`{"layout":[1]}`, `{"layout":null}`, `{"layout":"x"}`, `{}`} {
		if rec := mapCall(t, h, http.MethodPut, "/map/layout", "", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, rec.Code)
		}
	}
	oversized := `{"layout":{"note":"` + strings.Repeat("x", maxMapLayoutBytes) + `"}}`
	if rec := mapCall(t, h, http.MethodPut, "/map/layout", "", oversized); rec.Code != http.StatusBadRequest {
		t.Errorf("an oversized arrangement = %d, want 400", rec.Code)
	}
	if rec := mapCall(t, h, http.MethodDelete, "/map/layout", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("forget = %d %s", rec.Code, rec.Body.String())
	}
	if rec := mapCall(t, h, http.MethodGet, "/map/layout", "", ""); !strings.Contains(rec.Body.String(), `"layout":null`) {
		t.Fatalf("forgetting left the arrangement: %s", rec.Body.String())
	}
	if rec := mapCall(t, h, http.MethodGet, "/map/layout", "reader", ""); strings.Contains(rec.Body.String(), `"layout":null`) {
		t.Fatal("forgetting one account's arrangement took another's")
	}
}

// The map answers on a server with none of its modules running — the test
// server has no Docker, nginx or systemd — every kind it cannot read saying
// so rather than drawing nothing.
func TestServerMapAnswersWhereNothingIsRunning(t *testing.T) {
	_, h := mapTestRouter(t)
	rec := mapCall(t, h, http.MethodGet, "/map/", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("map = %d %s", rec.Code, rec.Body.String())
	}
	var m struct {
		Kinds []mapKind `json:"kinds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Kinds) == 0 {
		t.Fatal("the map sent no kinds")
	}
	for _, k := range m.Kinds {
		if k.State == "read" && k.Items == nil {
			t.Errorf("%s was read and sent a null list", k.Key)
		}
		if k.State == "unread" && k.Reason == "" {
			t.Errorf("%s is unread and does not say why", k.Key)
		}
	}
	reader := mapCall(t, h, http.MethodGet, "/map/", "reader", "")
	if strings.Contains(reader.Body.String(), `"key":"accounts"`) {
		t.Error("a readonly account was sent the dashboard's accounts")
	}
}
