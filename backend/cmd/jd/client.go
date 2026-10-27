package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// client calls one dashboard's API with one key.
type client struct {
	base string // scheme://host[:port], no trailing slash
	key  string
	http *http.Client
}

// apiError is the dashboard's refusal, in the shape every route writes:
// {"error":{"code":…,"message":…}}. Raw marks an answer that was not that
// shape — a proxy's page, or a route an older dashboard does not have.
type apiError struct {
	Status  int
	Code    string
	Message string
	Raw     bool
}

// Error says what a refusal means for the person holding the key, then the
// server's own words, which are written to be read.
func (e *apiError) Error() string {
	msg := strings.TrimSpace(e.Message)
	switch e.Code {
	case "session_required":
		// What refuses a deploy key says why in its own words; the generic
		// refusal an ordinary key meets does not, so jd says it.
		if strings.Contains(msg, "dashboard") {
			return msg + " (session_required)"
		}
		return "this needs a person signed in to the dashboard: " + msg + " (session_required)"
	case "key_scope":
		return msg + " (key_scope)"
	case "unauthorized":
		return "the dashboard refused the key: it is invalid, revoked or expired. Run jd login with a new one. (" + msg + ")"
	case "forbidden":
		return "the key's role does not permit this: " + msg
	case "password_change_required":
		return "the key's owner has to change their temporary password in the dashboard first."
	}
	if e.Raw {
		return fmt.Sprintf("the dashboard answered %d %s: %s", e.Status, http.StatusText(e.Status), msg)
	}
	return fmt.Sprintf("%s (%d): %s", e.Code, e.Status, msg)
}

// newClient checks the address before a key is ever sent to it. Plain http
// is refused anywhere but loopback, where an ssh tunnel ends, because the key
// would cross the network readable by anyone on the path.
func (a *app) newClient(raw, key string) (*client, error) {
	base, err := normaliseURL(raw)
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(base)
	if parsed.Scheme == "http" && !loopbackHost(parsed.Hostname()) && !a.opts.insecureHTTP {
		return nil, usagef("refusing to send the key over plain http to %s: anyone on the way could read it. "+
			"Use https, or pass --insecure-http if the network is already private (a tailnet, an ssh tunnel)", parsed.Host)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	caFile := a.opts.caFile
	if caFile == "" {
		caFile = a.env("JD_CA_FILE")
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("reading the certificate authority: %v", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificate", caFile)
		}
		tlsConfig.RootCAs = pool
	}
	if a.opts.skipVerify {
		fmt.Fprintln(a.stderr, "jd: warning: not checking the dashboard's certificate; anyone in the middle could read the key")
		tlsConfig.InsecureSkipVerify = true
	}
	transport.TLSClientConfig = tlsConfig
	return &client{base: base, key: key, http: &http.Client{Timeout: 30 * time.Second, Transport: transport}}, nil
}

// normaliseURL accepts what a person pastes — the dashboard's address, with
// or without a trailing slash or /api/v1 — and keeps the origin.
func normaliseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", usagef("%q is not a dashboard address; give it as https://host[:port]", raw)
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api/v1")
	return u.Scheme + "://" + u.Host + strings.TrimRight(path, "/"), nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func query(pairs ...string) url.Values {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		q.Set(pairs[i], pairs[i+1])
	}
	return q
}

// do sends one request and returns the body of a successful answer. body is
// marshalled unless it is already JSON (json.RawMessage).
func (c *client) do(method, path string, q url.Values, body any) ([]byte, error) {
	target := c.base + "/api/v1" + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, ok := body.(json.RawMessage)
		if !ok {
			var err error
			if raw, err = json.Marshal(body); err != nil {
				return nil, err
			}
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "jd/"+version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		// The transport's error names the URL, never the header.
		return nil, fmt.Errorf("could not reach the dashboard: %v", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("reading the dashboard's answer: %v", err)
	}
	if res.StatusCode >= 400 {
		return nil, decodeError(res.StatusCode, raw)
	}
	return raw, nil
}

func decodeError(status int, raw []byte) *apiError {
	var envelope struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Error != nil && envelope.Error.Code != "" {
		return &apiError{Status: status, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	code := strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
	if status == http.StatusUnauthorized {
		code = "unauthorized"
	}
	return &apiError{Status: status, Code: code, Message: text, Raw: true}
}

// getJSON reads a route into out and also returns the body as the server
// sent it, for --json.
func (c *client) getJSON(path string, q url.Values, out any) ([]byte, error) {
	raw, err := c.do(http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, fmt.Errorf("the dashboard's answer to %s was not what jd expected: %v", path, err)
		}
	}
	return raw, nil
}

// sendJSON writes body and reads the answer into out.
func (c *client) sendJSON(method, path string, q url.Values, body, out any) ([]byte, error) {
	raw, err := c.do(method, path, q, body)
	if err != nil {
		return nil, err
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, fmt.Errorf("the dashboard's answer to %s was not what jd expected: %v", path, err)
		}
	}
	return raw, nil
}

// isCode reports whether err is the dashboard refusing with code.
func isCode(err error, code string) bool {
	var api *apiError
	return errors.As(err, &api) && api.Code == code
}

// printRaw writes a server body as it came, ending in a newline.
func (a *app) printRaw(raw []byte) {
	raw = bytes.TrimRight(raw, "\n")
	a.stdout.Write(append(raw, '\n'))
}
