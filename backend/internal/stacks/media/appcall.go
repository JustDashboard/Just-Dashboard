package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// appCall is one request to an application of the stack. A secret goes in a
// header or a body, never in URL: a transport error names the URL it failed
// on, and that error reaches the step's record.
type appCall struct {
	Method  string
	URL     string
	Header  map[string]string
	JSON    any
	Form    url.Values
	Cookies []*http.Cookie
}

// appReply is what came back, read whole. The body is bounded: the largest
// answer a step reads is a manager's quality profiles through Seerr.
type appReply struct {
	Status  int
	Body    []byte
	Cookies []*http.Cookie
}

const appReplyLimit = 16 << 20

func callApp(ctx context.Context, env stacks.Env, call appCall) (appReply, error) {
	var body io.Reader
	contentType := ""
	switch {
	case call.JSON != nil:
		encoded, err := json.Marshal(call.JSON)
		if err != nil {
			return appReply{}, stacks.Permanent(err)
		}
		body, contentType = bytes.NewReader(encoded), "application/json"
	case call.Form != nil:
		body, contentType = strings.NewReader(call.Form.Encode()), "application/x-www-form-urlencoded"
	}
	request, err := http.NewRequestWithContext(ctx, call.Method, call.URL, body)
	if err != nil {
		return appReply{}, stacks.Permanent(err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.1")
	for name, value := range call.Header {
		request.Header.Set(name, value)
	}
	for _, cookie := range call.Cookies {
		request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	response, err := env.HTTP().Do(request)
	if err != nil {
		return appReply{}, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, appReplyLimit+1))
	if err != nil {
		return appReply{}, err
	}
	if len(content) > appReplyLimit {
		return appReply{}, fmt.Errorf("%s %s answered with more than %d MiB", call.Method, request.URL.Path, appReplyLimit>>20)
	}
	return appReply{Status: response.StatusCode, Body: content, Cookies: response.Cookies()}, nil
}

// decode reads the reply's JSON into target.
func (r appReply) decode(target any) error {
	if err := json.Unmarshal(r.Body, target); err != nil {
		return fmt.Errorf("an answer was not the JSON expected: %w", err)
	}
	return nil
}

// excerpt is the start of a reply's body for an error message, on one line,
// with every secret the caller sent scrubbed in case the application echoed
// one back.
func (r appReply) excerpt(secrets ...string) string {
	text := strings.Join(strings.Fields(string(r.Body)), " ")
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[hidden]")
		}
	}
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

// unexpected is the error for a status a step has no reading for.
func (r appReply) unexpected(what string, secrets ...string) error {
	if detail := r.excerpt(secrets...); detail != "" {
		return fmt.Errorf("%s: HTTP %d: %s", what, r.Status, detail)
	}
	return fmt.Errorf("%s: HTTP %d", what, r.Status)
}

// samePath compares two folder paths as the applications report them: with or
// without a trailing slash.
func samePath(a, b string) bool {
	return a != "" && b != "" && path.Clean(a) == path.Clean(b)
}

// sameValue compares two values as JSON would hold them, so a number read
// back as float64 equals the int it was written from.
func sameValue(a, b any) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(left, right)
}
