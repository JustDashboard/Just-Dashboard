package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Invariant 3 rests entirely on this function: an irreversible action runs
// only if the caller echoed the exact phrase. The phrase travels as its own
// field so the client never has to parse it back out of the message.
func TestRequireTypedConfirmation(t *testing.T) {
	cases := []struct {
		name   string
		header string
		phrase string
		status int
		code   string
	}{
		{"missing", "", "prod-db", http.StatusPreconditionRequired, "confirmation_required"},
		{"wrong", "prod", "prod-db", http.StatusPreconditionFailed, "confirmation_mismatch"},
		{"case differs", "PROD-DB", "prod-db", http.StatusPreconditionFailed, "confirmation_mismatch"},
		{"exact", "prod-db", "prod-db", 0, ""},
		{"surrounding space is forgiven", "  prod-db\t", "prod-db", 0, ""},
		// The phrase is a file or object name, so it can contain anything a
		// name can contain — including the quotes the message wraps it in.
		{"quotes in the phrase", `my"file`, `my"file`, 0, ""},
		{"quotes truncated", "my", `my"file`, http.StatusPreconditionFailed, "confirmation_mismatch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodDelete, "/api/v1/files", nil)
			if c.header != "" {
				r.Header.Set(ConfirmHeader, c.header)
			}
			err := RequireTypedConfirmation(httptest.NewRecorder(), r, c.phrase)
			if c.status == 0 {
				if err != nil {
					t.Fatalf("expected the confirmation to pass, got %v", err)
				}
				return
			}
			apiErr, ok := err.(*APIError)
			if !ok {
				t.Fatalf("expected an *APIError, got %T", err)
			}
			if apiErr.Status != c.status || apiErr.Code != c.code {
				t.Fatalf("got %d/%s, want %d/%s", apiErr.Status, apiErr.Code, c.status, c.code)
			}
			if apiErr.Phrase != c.phrase {
				t.Fatalf("the client cannot ask for the right text: phrase = %q, want %q",
					apiErr.Phrase, c.phrase)
			}
		})
	}
}

func TestEncodedConfirmationPreservesObjectIdentity(t *testing.T) {
	for _, phrase := range []string{"目录", " folder ", "😀", "name+percent%", "line\nbreak"} {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/files", nil)
		r.Header.Set(ConfirmHeader, url.PathEscape(phrase))
		r.Header.Set(ConfirmEncodingHeader, "uri")
		if err := RequireTypedConfirmation(httptest.NewRecorder(), r, phrase); err != nil {
			t.Errorf("encoded %q rejected: %v", phrase, err)
		}
		if err := RequireTypedConfirmation(httptest.NewRecorder(), r, phrase+"different"); err == nil {
			t.Errorf("encoded %q accepted for a different object", phrase)
		}
		ws := httptest.NewRequest(http.MethodGet, "/socket?confirm="+url.QueryEscape(phrase), nil)
		if err := RequireTypedConfirmationWS(httptest.NewRecorder(), ws, phrase); err != nil {
			t.Errorf("WS %q rejected: %v", phrase, err)
		}
		if err := RequireTypedConfirmation(httptest.NewRecorder(), ws, phrase); err == nil {
			t.Fatal("ordinary request accepted query confirmation")
		}
	}
	for _, value := range []string{"%zz", "%FF"} {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/files", nil)
		r.Header.Set(ConfirmHeader, value)
		r.Header.Set(ConfirmEncodingHeader, "uri")
		if err := RequireTypedConfirmation(httptest.NewRecorder(), r, value); err == nil {
			t.Errorf("invalid encoding %q accepted", value)
		}
	}
}

// A browser cannot set a header on a WebSocket handshake, so the streaming
// compose runner accepts the phrase as a query parameter instead. These pin
// that the relaxation is exactly that and no wider: the query form works only
// where a route opted into it, and a wrong phrase is still refused.
func TestRequireTypedConfirmationWS(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/docker/stacks/shop/run?action=down&confirm=shop", nil)
	if err := RequireTypedConfirmationWS(rec, r, "shop"); err != nil {
		t.Fatalf("a matching phrase in the query must be accepted: %v", err)
	}

	wrong := httptest.NewRequest(http.MethodGet, "/api/v1/docker/stacks/shop/run?confirm=nope", nil)
	if err := RequireTypedConfirmationWS(rec, wrong, "shop"); err == nil {
		t.Fatal("a mismatched phrase must still be refused")
	}

	// The ordinary guard must not have widened: a query parameter is not a
	// confirmation on a route that did not ask for one.
	plain := httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers/1/stop?confirm=shop", nil)
	if err := RequireTypedConfirmation(rec, plain, "shop"); err == nil {
		t.Fatal("the header-only guard must ignore the query parameter")
	}
}
