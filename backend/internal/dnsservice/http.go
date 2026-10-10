package dnsservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxNativeBody = 512 << 10

type nativeClient struct {
	http       *http.Client
	origin     string
	engine     Engine
	version    string
	credential Credential
	sid        string
	transport  Reading
	policy     []json.RawMessage
}

func newNativeClient(req ConnectionRequest) (*nativeClient, error) {
	req, err := ValidateConnection(req)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(req.Endpoint)
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if req.CA != "" && !roots.AppendCertsFromPEM([]byte(req.CA)) {
		return nil, errors.New("custom CA contains no usable certificate")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: req.ServerName},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		DisableCompression:    true,
		MaxConnsPerHost:       2,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != u.Host {
				return nil, errors.New("native management destination changed")
			}
			return dialer.DialContext(ctx, network, u.Host)
		},
	}
	c := &nativeClient{origin: req.Endpoint, engine: req.Engine, credential: req.Credential}
	c.http = &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("native management redirects are refused")
	}}
	c.transport = Reading{"loopback_http", "pinned_destination", "Management uses cleartext HTTP on the explicitly selected loopback endpoint."}
	return c, nil
}

func (c *nativeClient) close() { c.http.CloseIdleConnections() }

func (c *nativeClient) request(ctx context.Context, method, path string, payload any, result any) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n#") {
		return errors.New("unsupported native management path")
	}
	var body []byte
	contentType := "application/json"
	if form, ok := payload.(url.Values); ok {
		body, contentType = []byte(form.Encode()), "application/x-www-form-urlencoded"
	} else if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return errors.New("native request cannot be encoded")
		}
	}
	if len(body) > maxNativeBody {
		return errors.New("native request exceeds its bound")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("native request cannot be constructed")
	}
	req.Header.Set("Content-Type", contentType)
	switch c.engine {
	case AdGuard:
		req.SetBasicAuth(c.credential.Username, c.credential.Password)
	case PiHole:
		if c.sid != "" {
			req.Header.Set("X-FTL-SID", c.sid)
		}
	case Technitium:
		req.Header.Set("Authorization", "Bearer "+c.credential.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("native management request failed; destination, trust, timeout or redirect check did not complete")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A native error body can contain credentials or private configuration.
		return fmt.Errorf("native management returned HTTP %d", resp.StatusCode)
	}
	if resp.TLS != nil && resp.TLS.HandshakeComplete {
		c.transport = Reading{"verified_https", "tls_connection", "Management completed verified TLS against the pinned address and declared server identity."}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxNativeBody+1))
	if err != nil || len(data) > maxNativeBody {
		return errors.New("native management response is unreadable or exceeds its bound")
	}
	if result == nil {
		return nil
	}
	if json.Unmarshal(data, result) != nil {
		return errors.New("native management response does not match the supported JSON contract")
	}
	return nil
}

func (c *nativeClient) login(ctx context.Context) error {
	if c.engine != PiHole {
		return nil
	}
	var response struct {
		Session struct {
			Valid bool   `json:"valid"`
			SID   string `json:"sid"`
		} `json:"session"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/auth", map[string]string{"password": c.credential.Password}, &response); err != nil {
		return err
	}
	if !response.Session.Valid || response.Session.SID == "" || len(response.Session.SID) > 4096 || strings.ContainsAny(response.Session.SID, "\r\n") {
		return errors.New("native Pi-hole did not establish an authenticated session")
	}
	c.sid = response.Session.SID
	return nil
}

func (c *nativeClient) logout() {
	if c.sid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.request(ctx, http.MethodDelete, "/api/auth", nil, nil)
	c.sid = ""
}

func (c *nativeClient) text(s string) string {
	for _, secret := range []string{c.credential.Password, c.credential.Token, c.sid} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if len(s) > 512 {
		s = s[:512]
	}
	return s
}

func (c *nativeClient) retainPolicy(value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxNativeBody {
		return errors.New("native policy exceeds its bound")
	}
	c.policy = append(c.policy, data)
	return nil
}

func (c *nativeClient) fingerprint() string {
	data, _ := json.Marshal(c.policy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
