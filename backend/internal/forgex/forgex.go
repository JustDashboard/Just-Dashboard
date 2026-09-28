// Package forgex handles GitLab and Gitea request APIs. Credentials belong to
// one checkout and its Linux owner; they never become Git command arguments.
package forgex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type Service struct {
	store  *store.Store
	sealer *auth.Sealer
	http   *http.Client
}

func New(st *store.Store, sealer *auth.Sealer) *Service {
	return &Service{store: st, sealer: sealer, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type Config struct {
	Configured    bool   `json:"configured"`
	Kind          string `json:"kind"`
	URL           string `json:"url"`
	Project       string `json:"project"`
	Login         string `json:"login"`
	DefaultBranch string `json:"defaultBranch"`
}
type credential struct {
	Config
	Token string `json:"token"`
}
type Setup struct {
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	Project string `json:"project"`
	Token   string `json:"token"`
}

func key(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("could not identify repository owner")
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", owner.Uid, path)))
	return "git.forge." + hex.EncodeToString(h[:]), nil
}
func (s *Service) load(ctx context.Context, path string) (*credential, error) {
	k, err := key(path)
	if err != nil {
		return nil, err
	}
	sealed, ok, err := s.store.Setting(ctx, k)
	if err != nil {
		return nil, err
	}
	if !ok || sealed == "" {
		return nil, nil
	}
	plain, err := s.sealer.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("could not unlock the provider account")
	}
	var c credential
	if err := json.Unmarshal([]byte(plain), &c); err != nil {
		return nil, err
	}
	return &c, nil
}
func (s *Service) Status(ctx context.Context, path string) (*Config, error) {
	c, err := s.load(ctx, path)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return &Config{}, nil
	}
	return &c.Config, nil
}
func validateSetup(req *Setup) error {
	if req.Kind != "gitlab" && req.Kind != "gitea" {
		return fmt.Errorf("choose GitLab or Gitea")
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(req.URL), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("enter the provider's HTTPS base URL")
	}
	local := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && local != nil && local.IsLoopback()) {
		return fmt.Errorf("provider credentials require HTTPS (HTTP is accepted only on loopback)")
	}
	if strings.Contains(u.Path, "..") || strings.ContainsAny(u.Path, "%\\") {
		return fmt.Errorf("invalid provider base path")
	}
	req.URL = u.String()
	req.Project = strings.Trim(strings.TrimSpace(req.Project), "/")
	parts := strings.Split(req.Project, "/")
	if len(parts) < 2 || (req.Kind == "gitea" && len(parts) != 2) {
		return fmt.Errorf("enter the project as owner/name or group/subgroup/name")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#%\\\x00\r\n") {
			return fmt.Errorf("invalid project path")
		}
	}
	if strings.TrimSpace(req.Token) == "" || len(req.Token) > 8192 || strings.ContainsAny(req.Token, "\r\n\x00") {
		return fmt.Errorf("a valid access token is required")
	}
	return nil
}
func (s *Service) Configure(ctx context.Context, path string, req Setup) (*Config, error) {
	if err := validateSetup(&req); err != nil {
		return nil, err
	}
	c := &credential{Config: Config{Configured: true, Kind: req.Kind, URL: req.URL, Project: req.Project}, Token: req.Token}
	var user struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	}
	if err := s.json(ctx, c, "GET", "/user", nil, &user); err != nil {
		return nil, err
	}
	c.Login = user.Login
	if c.Login == "" {
		c.Login = user.Username
	}
	if c.Login == "" {
		return nil, fmt.Errorf("provider did not identify this token's account")
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := s.json(ctx, c, "GET", c.repo(), nil, &repo); err != nil {
		return nil, err
	}
	c.DefaultBranch = repo.DefaultBranch
	plain, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	sealed, err := s.sealer.Seal(string(plain))
	if err != nil {
		return nil, err
	}
	k, err := key(path)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetSetting(ctx, k, sealed); err != nil {
		return nil, err
	}
	return &c.Config, nil
}
func (s *Service) Disconnect(ctx context.Context, path string) error {
	k, err := key(path)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, k, "")
}
func (c *credential) repo() string {
	if c.Kind == "gitlab" {
		return "/projects/" + url.PathEscape(c.Project)
	}
	parts := strings.Split(c.Project, "/")
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}
func (c *credential) requests() string {
	if c.Kind == "gitlab" {
		return c.repo() + "/merge_requests"
	}
	return c.repo() + "/pulls"
}
func (s *Service) connected(ctx context.Context, path string) (*credential, error) {
	c, err := s.load(ctx, path)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("connect a provider account for this repository first")
	}
	return c, nil
}
func (s *Service) raw(ctx context.Context, c *credential, method, endpoint string, body any) ([]byte, http.Header, error) {
	api := "/api/v1"
	if c.Kind == "gitlab" {
		api = "/api/v4"
	}
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+api+endpoint, input)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.HasSuffix(endpoint, ".diff") {
		req.Header.Set("Accept", "text/plain")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Kind == "gitlab" {
		req.Header.Set("PRIVATE-TOKEN", c.Token)
	} else {
		req.Header.Set("Authorization", "token "+c.Token)
	}
	res, err := s.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("provider request failed: %s", strings.ReplaceAll(err.Error(), c.Token, "[redacted]"))
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > 4<<20 {
		return nil, nil, fmt.Errorf("provider response exceeds 4 MiB; open the request on the provider")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var msg struct {
			Message json.RawMessage `json:"message"`
		}
		_ = json.Unmarshal(data, &msg)
		detail := strings.ReplaceAll(string(msg.Message), c.Token, "[redacted]")
		if len(detail) > 400 {
			detail = detail[:400]
		}
		return nil, nil, fmt.Errorf("%s returned HTTP %d: %s", c.Kind, res.StatusCode, detail)
	}
	return data, res.Header, nil
}
func (s *Service) json(ctx context.Context, c *credential, method, endpoint string, body, result any) error {
	data, _, err := s.raw(ctx, c, method, endpoint, body)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(data, result)
}
