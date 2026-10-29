package dnsrecords

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// rest is the HTTP half every JSON provider shares: one request, a bounded
// answer, and the provider's refusal decoded by its own reader.
type rest struct {
	name   string
	base   string
	client *http.Client
	// authorize sets the request's credentials; body is what is sent.
	authorize func(request *http.Request, body []byte) error
	// refusal reads the provider's error document into a sentence.
	refusal func(status int, raw []byte) string
}

// call sends one request and decodes a successful answer into out. A
// status of 300 or more is the provider's refusal in its own words.
func (r rest) call(ctx context.Context, operation, method, path string, query url.Values, body, out any) error {
	_, err := r.send(ctx, operation, method, path, query, body, out)
	return err
}

// send is call, answering the status too, for the callers that read a 404
// as "none".
func (r rest) send(ctx context.Context, operation, method, path string, query url.Values, body, out any) (int, error) {
	target := r.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		payload = raw
	}
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	if payload == nil {
		request.Body, request.ContentLength = http.NoBody, 0
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if r.authorize != nil {
		if err := r.authorize(request, payload); err != nil {
			return 0, err
		}
	}
	response, err := r.client.Do(request)
	if err != nil {
		// The transport's error names the URL, never a credential.
		return 0, fmt.Errorf("%s could not be reached to %s: %w", r.name, operation, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode >= 300 {
		message := ""
		if r.refusal != nil {
			message = r.refusal(response.StatusCode, raw)
		}
		return response.StatusCode, &Error{Provider: r.name, Operation: operation, Status: response.StatusCode, Message: message}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return response.StatusCode, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return response.StatusCode, fmt.Errorf("%s's answer to %s could not be read: %w", r.name, operation, err)
	}
	return response.StatusCode, nil
}

// iniValues reads a credentials file of `key = value` lines, as certbot's
// plugins do, ignoring comments and section headers.
func iniValues(content string) map[string]string {
	values := map[string]string{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		values[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return values
}

// plainRefusal is a refusal whose body is not a document worth reading:
// the status, and the body's first line when it is short text.
func plainRefusal(status int, raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" || len(text) > 300 || strings.ContainsAny(text, "<{") {
		return fmt.Sprintf("%d %s", status, http.StatusText(status))
	}
	return text
}
