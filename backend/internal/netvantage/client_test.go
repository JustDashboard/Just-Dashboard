package netvantage

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func machineFixture(t *testing.T, s *Service) (*httptest.Server, string, *http.Client) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if r.URL.Path != "/api/v1/probe-agent/poll" {
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				w.WriteHeader(400)
				return
			}
		}
		if r.URL.Path == "/api/v1/probe-agent/enroll" {
			var c Claim
			json.Unmarshal(body, &c)
			v, e := s.Claim(r.Context(), c)
			if e != nil {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(v)
			return
		}
		seq, _ := strconv.ParseInt(r.Header.Get("X-JD-Probe-Sequence"), 10, 64)
		ts, _ := strconv.ParseInt(r.Header.Get("X-JD-Probe-Time"), 10, 64)
		sig := Signature{ServerKey: r.Header.Get("X-JD-Probe-Server"), ID: r.Header.Get("X-JD-Vantage"), Sequence: seq, Timestamp: ts, Value: r.Header.Get("X-JD-Probe-Signature")}
		if e := s.Authenticate(r.Context(), r.Method, r.URL.RequestURI(), body, sig, "127.0.0.1"); e != nil {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/v1/probe-agent/poll" {
			job, e := s.Poll(r.Context(), sig.ID)
			if e != nil {
				w.WriteHeader(500)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"job": job})
			return
		}
		var result Result
		json.Unmarshal(body, &result)
		if e := s.Complete(r.Context(), sig.ID, result); e != nil {
			w.WriteHeader(409)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"recorded": true})
	}))
	t.Cleanup(server.Close)
	sum := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	pin := hex.EncodeToString(sum[:])
	client, e := Transport(pin)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	t.Cleanup(client.CloseIdleConnections)
	return server, pin, client
}
func TestRootlessPollingPinsTransportAndPersistsSequence(t *testing.T) {
	s, _ := testService(t)
	server, pin, client := machineFixture(t, s)
	en, e := s.CreateEnrollment(context.Background(), scopeInput())
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := Enroll(context.Background(), client, server.URL, pin, en.Vantage.ID, en.Token, en.ServerKey)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "private.json")
	if e = SaveConfig(path, cfg); e != nil {
		t.Fatal(e)
	}
	check, e := s.CreateCheck(context.Background(), Request{VantageID: en.Vantage.ID, ScopeID: "service", Family: "inet", Port: 443, TLS: true}, "operator")
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	poller := &Poller{Config: cfg, Path: path, Client: client, Probe: func(ctx context.Context, j Job) *Result { calls++; r := reported(j); return &r }}
	if e = poller.Once(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = poller.Once(context.Background()); e != nil {
		t.Fatal(e)
	}
	saved, e := LoadConfig(path)
	if e != nil || saved.Sequence != 3 || calls != 1 {
		t.Fatalf("probe replay or unsaved sequence %#v calls=%d %v", saved, calls, e)
	}
	checks, e := s.Checks(context.Background())
	if e != nil || checks[0].ID != check.ID || checks[0].Result == nil {
		t.Fatalf("result not durable %#v %v", checks, e)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("private state permissions widened")
	}
}
func TestControlTransportRejectsChangedPinAndRedirect(t *testing.T) {
	s, _ := testService(t)
	server, _, client := machineFixture(t, s)
	client.Transport.(*http.Transport).TLSClientConfig.VerifyConnection = nil
	wrong, e := Transport(strings.Repeat("0", 64))
	if e != nil {
		t.Fatal(e)
	}
	wrong.Transport.(*http.Transport).TLSClientConfig.RootCAs = client.Transport.(*http.Transport).TLSClientConfig.RootCAs
	_, e = wrong.Post(server.URL+"/api/v1/probe-agent/poll", "application/json", nil)
	if e == nil {
		t.Fatal("changed TLS server pin accepted")
	}
	if e = client.CheckRedirect(nil, nil); e == nil {
		t.Fatal("control-plane redirect accepted")
	}
}
func TestAgentStateAndControlURLRefuseUnsafeForms(t *testing.T) {
	for _, url := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/?secret=1", "https://example.com/other"} {
		if e := validateControlURL(url); e == nil {
			t.Fatalf("unsafe control origin %s", url)
		}
	}
	path := filepath.Join(t.TempDir(), "state")
	os.WriteFile(path, []byte(`{}`), 0o644)
	if _, e := LoadConfig(path); e == nil {
		t.Fatal("world-readable agent credentials accepted")
	}
	if e := SaveConfig(path, AgentConfig{}); e == nil {
		t.Fatal("private state replaced an unsafe entry")
	}
}

func TestSignedAgentNativeEvidenceBothFamilies(t *testing.T) {
	for _, family := range []string{"inet", "inet6"} {
		t.Run(family, func(t *testing.T) {
			s, _ := testService(t)
			destination, roots := nativeTLS(t, family)
			target := probeJob(destination, family)
			input := scopeInput()
			input.Scopes = []Scope{target.Scope}
			en, e := s.CreateEnrollment(context.Background(), input)
			if e != nil {
				t.Fatal(e)
			}
			control, pin, client := machineFixture(t, s)
			cfg, e := Enroll(context.Background(), client, control.URL, pin, en.Vantage.ID, en.Token, en.ServerKey)
			if e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(t.TempDir(), "state")
			if e = SaveConfig(path, cfg); e != nil {
				t.Fatal(e)
			}
			request := target.Request
			request.VantageID = en.Vantage.ID
			check, e := s.CreateCheck(context.Background(), request, "operator")
			if e != nil {
				t.Fatal(e)
			}
			resolver := nativeDNS(t, net.ParseIP(target.Scope.Addresses[0]))
			poller := &Poller{Config: cfg, Path: path, Client: client, Probe: func(ctx context.Context, j Job) *Result { return probe(ctx, j, resolver, roots) }}
			if e = poller.Once(context.Background()); e != nil {
				t.Fatal(e)
			}
			checks, e := s.Checks(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if len(checks) != 1 || checks[0].ID != check.ID || checks[0].Status != "completed" || checks[0].Result == nil || checks[0].Result.SourceAddress == "" || checks[0].Result.Stages[2].State != "verified" {
				t.Fatalf("native signed result lost: %#v", checks)
			}
		})
	}
}
