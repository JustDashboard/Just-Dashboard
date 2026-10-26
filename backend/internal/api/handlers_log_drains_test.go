package api

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logdrain"
)

// Log drains through the API: an administrator's to change and anybody's to
// read, a header value never read back, a test record delivered with the
// drain's own headers, and every change audited.
func TestLogDrainRoutesAreAdministeredAuditedAndNeverEchoAHeader(t *testing.T) {
	s := testServer(t)
	s.startLogDrains(t.Context())
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "drain-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "drain-reader", auth.RoleReadOnly)}

	var mu sync.Mutex
	var got []string
	var authorization string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := gzip.NewReader(r.Body)
		raw, _ := io.ReadAll(body)
		mu.Lock()
		got = append(got, string(raw))
		authorization = r.Header.Get("Authorization")
		mu.Unlock()
	}))
	defer receiver.Close()

	create := `{"name":"Collector","kind":"http","endpoint":"` + receiver.URL + `","enabled":true,"streams":["output","requests"],
		"headers":[{"name":"Authorization","value":"Bearer fixture-drain-secret"}]}`
	if response := reader.do(http.MethodPost, "/api/v1/deploy/log-drains", create, nil); response.Code != http.StatusForbidden {
		t.Fatalf("a reader created a drain: %d %s", response.Code, response.Body.String())
	}
	response := admin.do(http.MethodPost, "/api/v1/deploy/log-drains", create, nil)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "fixture-drain-secret") {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	var drain logdrain.Drain
	if err := json.Unmarshal(response.Body.Bytes(), &drain); err != nil {
		t.Fatal(err)
	}
	listed := reader.do(http.MethodGet, "/api/v1/deploy/log-drains", "", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"headerNames":["Authorization"]`) || strings.Contains(listed.Body.String(), "fixture-drain-secret") {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}

	path := fmt.Sprintf("/api/v1/deploy/log-drains/%d", drain.ID)
	if response := admin.do(http.MethodPost, path+"/test", "", nil); response.Code != http.StatusOK {
		t.Fatalf("test = %d %s", response.Code, response.Body.String())
	}
	mu.Lock()
	if len(got) != 1 || !strings.Contains(got[0], "Test record from Just Dashboard") || authorization != "Bearer fixture-drain-secret" {
		t.Fatalf("receiver got %q with %q", got, authorization)
	}
	mu.Unlock()

	// A refusal comes back as the destination's own answer.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	defer broken.Close()
	update := `{"name":"Collector","kind":"http","endpoint":"` + broken.URL + `","enabled":true,"streams":["output"]}`
	if response := admin.do(http.MethodPut, path, update, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"headerNames":["Authorization"]`) {
		t.Fatalf("an edit without headers lost them: %d %s", response.Code, response.Body.String())
	}
	response = admin.do(http.MethodPost, path+"/test", "", nil)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "refused the credentials (401)") {
		t.Fatalf("refused test = %d %s", response.Code, response.Body.String())
	}

	if response := admin.do(http.MethodPut, path+"/enabled", `{"enabled":false}`, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("pause = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPost, "/api/v1/deploy/log-drains", `{"name":"Plain","kind":"loki","endpoint":"http://logs.example.test:3100","streams":["output"],"headers":[{"name":"Authorization","value":"Basic x"}]}`, nil); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "plain http") {
		t.Fatalf("a header over plain http = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodDelete, path, "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", response.Code, response.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	want := []string{"deploy.log_drain.create", "deploy.log_drain.test", "deploy.log_drain.update", "deploy.log_drain.pause", "deploy.log_drain.delete"}
	for _, action := range want {
		for {
			entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: action})
			if err == nil && len(entries) > 0 && entries[0].Target == "Collector" {
				if strings.Contains(fmt.Sprint(entries[0].Detail), "fixture-drain-secret") {
					t.Fatalf("%s audited the header value", action)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s was not audited: %+v %v", action, entries, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
