package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRequiresJSONAndOneValue(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "json", contentType: "application/json; charset=utf-8", body: `{"name":"ok"}`},
		{name: "simple cross origin type", contentType: "text/plain", body: `{"name":"no"}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "trailing value", contentType: "application/json", body: `{"name":"one"} {"name":"two"}`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			var dst struct {
				Name string `json:"name"`
			}
			err := DecodeJSON(r, &dst)
			if tt.wantStatus == 0 {
				if err != nil || dst.Name != "ok" {
					t.Fatalf("DecodeJSON = (%q, %v), want (ok, nil)", dst.Name, err)
				}
				return
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tt.wantStatus {
				t.Fatalf("DecodeJSON error = %v, want status %d", err, tt.wantStatus)
			}
		})
	}
}

// decodeInto runs DecodeJSON over body and returns its refusal.
func decodeInto(t *testing.T, body string, dst any) *APIError {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	err := DecodeJSON(r, dst)
	if err == nil {
		return nil
	}
	var refusal *APIError
	if !errors.As(err, &refusal) {
		t.Fatalf("DecodeJSON error %v is not an APIError", err)
	}
	return refusal
}

// A decode refusal is in the request's terms: the field a form can mark, and
// no Go type. encoding/json's own sentence for the first case below is "cannot
// unmarshal number into Go struct field updateProfileRequest.displayName of
// type string".
func TestADecodeRefusalNamesTheFieldAndNoGoType(t *testing.T) {
	type runtime struct {
		InternalPort int `json:"internalPort"`
	}
	type request struct {
		DisplayName string   `json:"displayName"`
		Enabled     bool     `json:"enabled"`
		Tags        []string `json:"tags"`
		Small       int8     `json:"small"`
		Ratio       float64  `json:"ratio"`
		Runtime     runtime  `json:"runtime"`
		Optional    *int     `json:"optional"`
	}
	for body, want := range map[string]struct{ field, message string }{
		`{"displayName":5}`:                  {"displayName", "displayName must be a string, not a number"},
		`{"enabled":"yes"}`:                  {"enabled", "enabled must be true or false, not a string"},
		`{"tags":"a"}`:                       {"tags", "tags must be a list, not a string"},
		`{"small":300}`:                      {"small", "small must be a whole number, not the number 300"},
		`{"ratio":true}`:                     {"ratio", "ratio must be a number, not true or false"},
		`{"runtime":{"internalPort":"80"}}`:  {"runtime.internalPort", "runtime.internalPort must be a whole number, not a string"},
		`{"runtime":[1]}`:                    {"runtime", "runtime must be an object, not a list"},
		`{"optional":"1"}`:                   {"optional", "optional must be a whole number, not a string"},
		`{"nope":1}`:                         {"nope", `unknown field "nope"`},
		`{"runtime":{"internalPorts":8080}}`: {"internalPorts", `unknown field "internalPorts"`},
		`[]`:                                 {"", "the request body must be an object, not a list"},
		``:                                   {"", "a JSON body is required"},
		`{"displayName":"x"`:                 {"", "malformed request body: it ends before the JSON value does"},
	} {
		var dst request
		refusal := decodeInto(t, body, &dst)
		if refusal == nil || refusal.Status != http.StatusBadRequest || refusal.Code != "bad_request" ||
			refusal.Field != want.field || refusal.Message != want.message {
			t.Errorf("%s refused as %+v, want field %q and %q", body, refusal, want.field, want.message)
		}
		if refusal != nil && (strings.Contains(refusal.Message, "Go ") || strings.Contains(refusal.Message, "request.") ||
			strings.Contains(refusal.Message, "json:")) {
			t.Errorf("%s: the refusal shows the decoder's internals: %s", body, refusal.Message)
		}
	}
	// A syntax error keeps the decoder's words, which name the character.
	var dst request
	if refusal := decodeInto(t, `{"displayName":x}`, &dst); refusal == nil || refusal.Status != http.StatusBadRequest ||
		!strings.Contains(refusal.Message, "invalid character 'x'") {
		t.Errorf("a syntax error refused as %+v", refusal)
	}
}

// A body past the limit is 413, not a malformed request: the client can tell
// "send less" from "send something else".
func TestABodyPastTheLimitIs413(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	big := `{"name":"` + strings.Repeat("a", maxJSONBody) + `"}`
	refusal := decodeInto(t, big, &dst)
	if refusal == nil || refusal.Status != http.StatusRequestEntityTooLarge || refusal.Code != "body_too_large" ||
		refusal.Message != "the request body is larger than the 4 MB this route accepts" {
		t.Errorf("an oversized body refused as %+v", refusal)
	}
	// The value fits and what follows it does not: still too large, not "two values".
	trailing := `{"name":"a"}` + strings.Repeat(" ", maxJSONBody)
	if refusal := decodeInto(t, trailing, &dst); refusal == nil || refusal.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("a body whose padding passes the limit refused as %+v", refusal)
	}
	// A route with a larger limit names its own.
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"`+strings.Repeat("a", 8<<20)+`"}`))
	r.Header.Set("Content-Type", "application/json")
	var within *APIError
	if err := DecodeJSONWithin(r, &dst, 8<<20); !errors.As(err, &within) || within.Status != http.StatusRequestEntityTooLarge ||
		!strings.Contains(within.Message, "8 MB") {
		t.Errorf("a body past a route's own limit refused as %v", err)
	}
}
