package jd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request builder against a test server: what each kind of call puts on
// the wire (method, path with its parameters escaped, query, headers, body)
// and how an answer and a refusal come back. Written by hand, against the
// generated methods; regenerating sdk/ leaves this file alone.

type seen struct {
	method, path, rawQuery, contentType, accept, authorization, idempotency string
	body                                                                    map[string]any
}

func server(t *testing.T, status int, contentType, answer string) (*httptest.Server, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.rawQuery = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		got.contentType, got.accept = r.Header.Get("Content-Type"), r.Header.Get("Accept")
		got.authorization, got.idempotency = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &got.body); err != nil {
				t.Errorf("the body is not JSON: %s", raw)
			}
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestAWriteSendsItsPathBodyAndKey(t *testing.T) {
	srv, got := server(t, http.StatusAccepted, "application/json", `{"id":91,"runNumber":4,"state":"queued","operation":"deploy"}`)
	c := NewClient(srv.URL, "vpsd_example")
	run, err := c.DeploymentRunCreate(context.Background(), 12, 34,
		DeploymentRunCreateRequest{Operation: DeployOperationDeploy, Ref: "main"},
		WithHeader("Idempotency-Key", "ci-1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/api/v1/deploy/12/environments/34/runs" {
		t.Errorf("sent %s %s", got.method, got.path)
	}
	if got.authorization != "Bearer vpsd_example" || got.contentType != "application/json" || got.accept != "application/json" || got.idempotency != "ci-1" {
		t.Errorf("headers: %+v", got)
	}
	if got.body["operation"] != "deploy" || got.body["ref"] != "main" {
		t.Errorf("body: %v", got.body)
	}
	// A field left at its zero value is not sent; the API reads it as that.
	if _, sent := got.body["sourceRevision"]; sent {
		t.Errorf("an empty field was sent: %v", got.body)
	}
	if run.ID != 91 || run.RunNumber != 4 || run.State != RunStateQueued {
		t.Errorf("answer: %+v", run)
	}
}

func TestAPathParameterIsEscapedAndTheBaseIsTakenEitherWay(t *testing.T) {
	srv, got := server(t, http.StatusOK, "application/json", `{"variables":[]}`)
	value := "s3cret"
	for _, base := range []string{srv.URL, srv.URL + "/", srv.URL + "/api/v1", srv.URL + "/api/v1/"} {
		c := NewClient(base, "k")
		if _, err := c.DeploymentVariablePut(context.Background(), 1, 2, "A B/C", VariableWriteRequest{Value: &value}); err != nil {
			t.Fatal(err)
		}
		if got.method != http.MethodPut || got.path != "/api/v1/deploy/1/environments/2/variables/A%20B%2FC" {
			t.Errorf("base %q sent %s %s", base, got.method, got.path)
		}
		if got.body["value"] != "s3cret" {
			t.Errorf("body: %v", got.body)
		}
	}
}

func TestAQueryLeavesOutWhatIsNotSet(t *testing.T) {
	srv, got := server(t, http.StatusOK, "application/json", `{"runs":[],"running":false}`)
	c := NewClient(srv.URL, "k")
	raw, err := c.DeployRuns(context.Background(), 7, &DeployRunsParams{Limit: 5, View: "engine", State: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.path != "/api/v1/deploy/7/runs" || got.rawQuery != "limit=5&state=failed&view=engine" {
		t.Errorf("sent %s %s?%s", got.method, got.path, got.rawQuery)
	}
	if !strings.Contains(string(raw), `"running":false`) {
		t.Errorf("answer: %s", raw)
	}
	if got.contentType != "" || got.body != nil {
		t.Errorf("a read sent a body: %q %v", got.contentType, got.body)
	}
	if _, err := c.DeployRuns(context.Background(), 7, nil); err != nil || got.rawQuery != "" {
		t.Errorf("no parameters sent %q (%v)", got.rawQuery, err)
	}
}

func TestNoContentAndTextAnswers(t *testing.T) {
	srv, got := server(t, http.StatusNoContent, "", "")
	if err := NewClient(srv.URL, "k").DeploymentTriggerDelete(context.Background(), 3, 4, 5); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodDelete || got.path != "/api/v1/deploy/3/environments/4/triggers/5" {
		t.Errorf("sent %s %s", got.method, got.path)
	}
	csv, got := server(t, http.StatusOK, "text/csv", "time,status\n1,200\n")
	body, err := NewClient(csv.URL, "k").DeploymentRequestExport(context.Background(), 3, &DeploymentRequestExportParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "time,status\n1,200\n" || got.accept != "text/csv" || got.rawQuery != "limit=10" {
		t.Errorf("export: %q accept %q query %q", body, got.accept, got.rawQuery)
	}
}

func TestARefusalIsAnErrorWithTheEnvelope(t *testing.T) {
	srv, _ := server(t, http.StatusForbidden, "application/json",
		`{"error":{"code":"key_scope","message":"this deploy key was not given this deployment","field":"deployment","retryable":false}}`)
	_, err := NewClient(srv.URL, "k").DeployGet(context.Background(), 9)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %T %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden || apiErr.Code != "key_scope" || apiErr.Field != "deployment" ||
		!strings.Contains(apiErr.Message, "not given") || !IsCode(err, "key_scope") {
		t.Errorf("error: %+v", apiErr)
	}
	if !strings.Contains(err.Error(), "403 key_scope: this deploy key") {
		t.Errorf("Error() = %q", err.Error())
	}

	// A proxy's page in front of the dashboard is not the envelope: its
	// status and text are kept.
	proxy, _ := server(t, http.StatusBadGateway, "text/html", "<h1>Bad gateway</h1>")
	_, err = NewClient(proxy.URL, "k").DeployGet(context.Background(), 9)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway || apiErr.Code != "" || apiErr.Message != "<h1>Bad gateway</h1>" {
		t.Errorf("a page that is not the envelope: %+v", apiErr)
	}
}

func TestAStreamIsGivenAsItsAddress(t *testing.T) {
	if got := NewClient("https://dash.example.com/", "k").DeploymentRunStreamURL(12, 99); got != "wss://dash.example.com/api/v1/deploy/12/runs/99/stream" {
		t.Errorf("stream address %q", got)
	}
	if got := NewClient("http://127.0.0.1:8080", "k").DeploymentRunStreamURL(1, 2); got != "ws://127.0.0.1:8080/api/v1/deploy/1/runs/2/stream" {
		t.Errorf("stream address %q", got)
	}
}
