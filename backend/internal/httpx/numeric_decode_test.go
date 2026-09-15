package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONNumbersPreservesMutationKeysAndDecimals(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/rows", strings.NewReader(`{
		"key":{"id":9007199254740993},
		"values":{"amount":12345678901234567890.12345678901234567890,"large":1e10000}
	}`))
	r.Header.Set("Content-Type", "application/json")
	var body struct {
		Key    map[string]any `json:"key"`
		Values map[string]any `json:"values"`
	}
	if err := DecodeJSONNumbers(r, &body); err != nil {
		t.Fatal(err)
	}
	if body.Key["id"] != json.Number("9007199254740993") ||
		body.Values["amount"] != json.Number("12345678901234567890.12345678901234567890") ||
		body.Values["large"] != json.Number("1e10000") {
		t.Fatalf("numeric request was rounded: %#v", body)
	}
}
