package netvantage

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type lockTransport struct{ calls int }

func (r *lockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"job":null}`)), Header: make(http.Header)}, nil
}
func TestOnceRunAndEnrollmentShareStateLockBeforeTransmission(t *testing.T) {
	s, _ := testService(t)
	en, _ := enrolled(t, s)
	path := filepath.Join(t.TempDir(), "state")
	cfg := AgentConfig{URL: "https://example.test", TLSPin: strings.Repeat("a", 64), Manifest: Manifest{Vantage: en.Vantage, ServerKey: en.ServerKey}, PrivateKey: encode(s.key), Sequence: 7}
	if e := SaveConfig(path, cfg); e != nil {
		t.Fatal(e)
	}
	transport := &lockTransport{}
	client := &http.Client{Transport: transport}
	poller := &Poller{Path: path, Client: client}
	e := WithStateLock(path, func() error {
		if e := poller.Once(context.Background()); e == nil {
			t.Fatal("once entered competing state")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := poller.Run(ctx, nil); e == nil {
			t.Fatal("daemon entered competing state")
		}
		if _, e := EnrollState(context.Background(), client, cfg.URL, cfg.TLSPin, en.Vantage.ID, en.Token, en.ServerKey, path); e == nil {
			t.Fatal("enrollment entered competing state")
		}
		saved, e := LoadConfig(path)
		if e != nil || saved.Sequence != 7 || transport.calls != 0 {
			t.Fatal("competing operation transmitted or replaced state", saved.Sequence, transport.calls, e)
		}
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.Command(exe, "-test.run=^TestStateLockRejectsCompetingProcess$")
		cmd.Env = append(os.Environ(), "JD_VANTAGE_LOCK_CHILD="+path)
		_, e = hostexec.RunGroup(ctx, cmd, time.Second)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	// The instance's stale in-memory sequence is ignored after acquiring the lock.
	if e = poller.Once(context.Background()); e != nil {
		t.Fatal(e)
	}
	saved, e := LoadConfig(path)
	if e != nil || saved.Sequence != 8 || transport.calls != 1 {
		t.Fatal("once did not reload durable state", saved.Sequence, transport.calls, e)
	}
	if _, e = EnrollState(context.Background(), client, cfg.URL, cfg.TLSPin, en.Vantage.ID, en.Token, en.ServerKey, path); e == nil || transport.calls != 1 {
		t.Fatal("enrollment overwrote existing identity or sent a claim")
	}
}
func TestStateLockRejectsCompetingProcess(t *testing.T) {
	path := os.Getenv("JD_VANTAGE_LOCK_CHILD")
	if path == "" {
		t.Skip("helper invoked by parent lock fixture")
	}
	transport := &lockTransport{}
	poller := &Poller{Path: path, Client: &http.Client{Transport: transport}}
	if e := poller.Once(context.Background()); e == nil || !strings.Contains(e.Error(), "another poller") || transport.calls != 0 {
		t.Fatal("competing process transmitted", e, transport.calls)
	}
}
func TestEnrollmentClaimsAndFirstSaveAreLockedTogether(t *testing.T) {
	s, _ := testService(t)
	server, pin, client := machineFixture(t, s)
	en, e := s.CreateEnrollment(context.Background(), scopeInput())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "identity")
	cfg, e := EnrollState(context.Background(), client, server.URL, pin, en.Vantage.ID, en.Token, en.ServerKey, path)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := LoadConfig(path)
	if e != nil || saved.PrivateKey != cfg.PrivateKey || saved.Manifest.Vantage.ID != en.Vantage.ID {
		t.Fatal("first identity was not saved under lock", e)
	}
	if _, e = EnrollState(context.Background(), client, server.URL, pin, en.Vantage.ID, en.Token, en.ServerKey, path); e == nil {
		t.Fatal("second enrollment replaced existing identity")
	}
}
