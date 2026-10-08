package netvantage

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type AgentConfig struct {
	URL        string   `json:"url"`
	TLSPin     string   `json:"tlsSpkiSha256"`
	Manifest   Manifest `json:"manifest"`
	PrivateKey string   `json:"privateKey"`
	Sequence   int64    `json:"sequence"`
}

func Transport(pin string) (*http.Client, error) {
	if raw, e := hex.DecodeString(pin); e != nil || len(raw) != 32 {
		return nil, fmt.Errorf("provide the control-plane TLS SPKI SHA256 pin as 64 hexadecimal characters")
	}
	t := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return fmt.Errorf("control-plane TLS certificate is missing")
		}
		sum := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), pin) {
			return fmt.Errorf("control-plane TLS pin changed; operator review and new enrollment are required")
		}
		return nil
	}}}
	return &http.Client{Transport: t, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("probe control-plane redirects are refused")
	}}, nil
}
func validateControlURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("the control plane must be an HTTPS origin without credentials, path, query or fragment")
	}
	return nil
}
func DecodeResponse(body []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("the machine response contains extra content")
	}
	return nil
}
func exchange(ctx context.Context, client *http.Client, origin, path string, body []byte, signature *Signature, out any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(origin, "/")+path, bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != nil {
		req.Header.Set("X-JD-Vantage", signature.ID)
		req.Header.Set("X-JD-Probe-Server", signature.ServerKey)
		req.Header.Set("X-JD-Probe-Sequence", strconv.FormatInt(signature.Sequence, 10))
		req.Header.Set("X-JD-Probe-Time", strconv.FormatInt(signature.Timestamp, 10))
		req.Header.Set("X-JD-Probe-Signature", signature.Value)
	}
	response, e := client.Do(req)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, MaxBody+1))
	if e != nil || len(raw) > MaxBody {
		return fmt.Errorf("control-plane response exceeds the fixed bound")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("control plane refused the request (HTTP %d); no probe is replayed", response.StatusCode)
	}
	return DecodeResponse(raw, out)
}
func Enroll(ctx context.Context, client *http.Client, origin, pin, id, token, serverKey string) (AgentConfig, error) {
	if e := validateControlURL(origin); e != nil {
		return AgentConfig{}, e
	}
	serverIdentity, err := decode(serverKey)
	if err != nil || len(serverIdentity) != 32 || !identityPattern.MatchString(id) || len(strings.TrimSpace(token)) != 64 {
		return AgentConfig{}, fmt.Errorf("provide the operator-approved server signing key, vantage ID and one-use enrollment token")
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return AgentConfig{}, e
	}
	claim := SignClaim(key, Claim{ServerKey: serverKey, ID: id, Token: strings.TrimSpace(token), PublicKey: encode(pub)})
	var manifest Manifest
	if e = exchange(ctx, client, origin, "/api/v1/probe-agent/enroll", Marshal(claim), nil, &manifest); e != nil {
		return AgentConfig{}, e
	}
	if manifest.ServerKey != serverKey || manifest.Vantage.ID != id {
		return AgentConfig{}, fmt.Errorf("the enrolled server signing identity does not match the operator's pin")
	}
	_, e = ValidateEnrollment(EnrollmentRequest{Name: manifest.Vantage.Name, Location: manifest.Vantage.Location, Placement: manifest.Vantage.Placement, Scopes: manifest.Vantage.Scopes})
	if e != nil {
		return AgentConfig{}, e
	}
	return AgentConfig{URL: origin, TLSPin: pin, Manifest: manifest, PrivateKey: encode(key)}, nil
}
func LoadConfig(path string) (AgentConfig, error) {
	var cfg AgentConfig
	st, e := os.Lstat(path)
	if e != nil {
		return cfg, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 || st.Size() > MaxBody {
		return cfg, fmt.Errorf("agent state must be a regular private file of at most 32 KiB")
	}
	raw, e := os.ReadFile(path)
	if e == nil {
		e = DecodeResponse(raw, &cfg)
	}
	if e != nil {
		return cfg, e
	}
	if e = validateControlURL(cfg.URL); e != nil {
		return cfg, e
	}
	serverKey, er := decode(cfg.Manifest.ServerKey)
	_, scopeErr := ValidateEnrollment(EnrollmentRequest{Name: cfg.Manifest.Vantage.Name, Location: cfg.Manifest.Vantage.Location, Placement: cfg.Manifest.Vantage.Placement, Scopes: cfg.Manifest.Vantage.Scopes})
	key, e := decode(cfg.PrivateKey)
	if er != nil || len(serverKey) != 32 || scopeErr != nil || e != nil || len(key) != 64 || cfg.Sequence < 0 || !identityPattern.MatchString(cfg.Manifest.Vantage.ID) {
		return cfg, fmt.Errorf("agent state identity is invalid")
	}
	return cfg, nil
}
func SaveConfig(path string, cfg AgentConfig) error {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0o700); e != nil {
		return e
	}
	if info, e := os.Lstat(path); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		return fmt.Errorf("refusing to replace non-private agent state")
	}
	f, e := os.CreateTemp(dir, ".probe-state-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0o600); e == nil {
		_, e = f.Write(Marshal(cfg))
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(name, path)
	}
	if e != nil {
		return e
	}
	parent, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer parent.Close()
	return parent.Sync()
}

type Poller struct {
	Config AgentConfig
	Path   string
	Client *http.Client
	Probe  func(context.Context, Job) *Result
}

func (p *Poller) signed(ctx context.Context, path string, body []byte, out any) error {
	key, e := decode(p.Config.PrivateKey)
	if e != nil || len(key) != 64 {
		return fmt.Errorf("agent private identity is unavailable")
	}
	p.Config.Sequence++
	if e = SaveConfig(p.Path, p.Config); e != nil {
		return e
	}
	signature := SignRequest(key, "POST", path, body, Signature{ServerKey: p.Config.Manifest.ServerKey, ID: p.Config.Manifest.Vantage.ID, Sequence: p.Config.Sequence, Timestamp: time.Now().Unix()})
	return exchange(ctx, p.Client, p.Config.URL, path, body, &signature, out)
}
func (p *Poller) Once(ctx context.Context) error {
	var response struct {
		Job        *SignedJob `json:"job"`
		ServerTime time.Time  `json:"serverTime"`
	}
	if e := p.signed(ctx, "/api/v1/probe-agent/poll", nil, &response); e != nil {
		return e
	}
	if response.Job == nil {
		return nil
	}
	if e := ValidateJob(*response.Job, p.Config.Manifest, time.Now().UTC()); e != nil {
		return e
	}
	run := p.Probe
	if run == nil {
		run = Probe
	}
	result := run(ctx, response.Job.Job)
	// A lost upload can be retried with a fresh transport sequence. The probe
	// itself is run once, and the server will never re-lease this running job.
	for attempt := 0; attempt < 3; attempt++ {
		if !response.Job.Job.ExpiresAt.After(time.Now()) {
			return fmt.Errorf("the result lease expired; no measurement was promoted")
		}
		var recorded struct {
			Recorded bool `json:"recorded"`
		}
		e := p.signed(ctx, "/api/v1/probe-agent/result", Marshal(result), &recorded)
		if e == nil && recorded.Recorded {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == 2 {
			if e == nil {
				e = fmt.Errorf("the control plane did not record the measurement")
			}
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil
}
func (p *Poller) Run(ctx context.Context, notice func(error)) error {
	f, e := os.OpenFile(p.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("another poller owns this private agent state")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	current, e := LoadConfig(p.Path)
	if e != nil {
		return e
	}
	p.Config = current
	for {
		if e = p.Once(ctx); e != nil && notice != nil {
			notice(e)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
