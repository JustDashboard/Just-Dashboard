package tailnetgate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gateBench struct {
	handler  *Handler
	asked    *atomic.Int32
	refusals []Refusal
	mu       sync.Mutex
	now      time.Time
	down     atomic.Bool
}

// newGateBench is the route over the real local API client and a fake
// tailscaled: environment 7 admits alice and tag:ci, environment 8 is
// tailnet-only with no list, and asking about environment 9 fails.
func newGateBench(t *testing.T) *gateBench {
	t.Helper()
	b := &gateBench{asked: &atomic.Int32{}, now: time.Unix(1_800_000_000, 0)}
	answers := whoisAnswers(b.asked)
	socket := fakeTailscaled(t, func(w http.ResponseWriter, r *http.Request) {
		if b.down.Load() {
			http.Error(w, "tailscaled is starting", http.StatusServiceUnavailable)
			return
		}
		answers(w, r)
	})
	b.handler = &Handler{
		WhoIs: NewLocalAPI(nil, socket),
		Access: func(_ context.Context, environmentID int64) ([]string, error) {
			switch environmentID {
			case 7:
				return []string{"alice@example.com", "tag:ci"}, nil
			case 8:
				return nil, nil
			}
			return nil, errors.New("the store is locked")
		},
		Refused: func(_ context.Context, refusal Refusal) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.refusals = append(b.refusals, refusal)
		},
		Now: func() time.Time { return b.now },
	}
	return b
}

func (b *gateBench) ask(environment, peer string, header ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, Path+"?"+EnvironmentParam+"="+environment, nil)
	request.RemoteAddr = "127.0.0.1:41000"
	if peer != "" {
		request.Header.Set(PeerHeader, peer)
	}
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	return recorder
}

func (b *gateBench) lastRefusal(t *testing.T) Refusal {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.refusals) == 0 {
		t.Fatal("nothing was recorded")
	}
	return b.refusals[len(b.refusals)-1]
}

func TestWhoisAdmitsAListedLoginAndTellsTheApplication(t *testing.T) {
	b := newGateBench(t)
	w := b.ask("7", "100.101.102.103")
	if w.Code != http.StatusNoContent || w.Header().Get(LoginHeader) != "Alice@Example.com" ||
		w.Header().Get(UserHeader) != "Alice Example" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("alice = %d %v", w.Code, w.Header())
	}
	if len(b.refusals) != 0 {
		t.Fatalf("an admitted request was recorded as refused: %+v", b.refusals)
	}
}

func TestWhoisRefusesALoginNotOnTheList(t *testing.T) {
	b := newGateBench(t)
	b.handler.Access = func(context.Context, int64) ([]string, error) { return []string{"bob@example.com"}, nil }
	w := b.ask("7", "100.101.102.103")
	if w.Code != http.StatusForbidden || w.Header().Get(LoginHeader) != "" {
		t.Fatalf("alice on bob's list = %d %v", w.Code, w.Header())
	}
	if refusal := b.lastRefusal(t); refusal.Reason != "not_listed" || refusal.Login != "Alice@Example.com" ||
		refusal.EnvironmentID != 7 || refusal.Address != "100.101.102.103" {
		t.Fatalf("recorded %+v", refusal)
	}
}

func TestWhoisAdmitsATaggedMachineByItsTag(t *testing.T) {
	b := newGateBench(t)
	w := b.ask("7", "100.64.0.9")
	if w.Code != http.StatusNoContent || w.Header().Get(LoginHeader) != "tag:ci,tag:build" || w.Header().Get(UserHeader) != "ci-runner" {
		t.Fatalf("the CI machine = %d %v", w.Code, w.Header())
	}
}

func TestWhoisRefusesAnAddressNobodyOnTheTailnetHolds(t *testing.T) {
	b := newGateBench(t)
	if w := b.ask("7", "100.70.0.1"); w.Code != http.StatusForbidden {
		t.Fatalf("an unknown address = %d", w.Code)
	}
	if refusal := b.lastRefusal(t); refusal.Reason != "not_on_tailnet" {
		t.Fatalf("recorded %+v", refusal)
	}
	// No peer at all is a request that did not come through tailscaled.
	if w := b.ask("7", ""); w.Code != http.StatusForbidden {
		t.Fatalf("no peer = %d", w.Code)
	}
	if w := b.ask("7", "not-an-address"); w.Code != http.StatusForbidden || b.lastRefusal(t).Reason != "no_peer" {
		t.Fatalf("a malformed peer = %d %+v", w.Code, b.lastRefusal(t))
	}
}

// tailscaled not answering is an outage, never a refusal, and it is not
// remembered: the next request after it is back is answered.
func TestWhoisAnswers503NotA403WhenTailscaledIsDown(t *testing.T) {
	b := newGateBench(t)
	b.down.Store(true)
	if w := b.ask("7", "100.101.102.103"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("with tailscaled down = %d", w.Code)
	}
	if refusal := b.lastRefusal(t); refusal.Reason != "unavailable" || refusal.Status != http.StatusServiceUnavailable {
		t.Fatalf("recorded %+v", refusal)
	}
	b.down.Store(false)
	if w := b.ask("7", "100.101.102.103"); w.Code != http.StatusNoContent {
		t.Fatalf("with tailscaled back = %d", w.Code)
	}
	gone := &Handler{WhoIs: NewLocalAPI(nil, filepath.Join(t.TempDir(), "none.sock")), Access: b.handler.Access}
	request := httptest.NewRequest(http.MethodGet, Path+"?environment=7", nil)
	request.RemoteAddr = "127.0.0.1:41000"
	request.Header.Set(PeerHeader, "100.101.102.103")
	recorder := httptest.NewRecorder()
	gone.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("with no tailscaled socket = %d", recorder.Code)
	}
}

// Only the proxy asks: from loopback, and not forwarded by another proxy.
func TestWhoisAnswersOnlyTheProxy(t *testing.T) {
	b := newGateBench(t)
	request := httptest.NewRequest(http.MethodGet, Path+"?environment=7", nil)
	request.RemoteAddr = "100.101.102.103:51000"
	request.Header.Set(PeerHeader, "100.101.102.103")
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || b.lastRefusal(t).Reason != "caller" || !strings.Contains(recorder.Body.String(), `"network_denied"`) {
		t.Fatalf("a tailnet caller = %d %+v", recorder.Code, b.lastRefusal(t))
	}
	// The dashboard's own Caddy forwards a browser from loopback, and says so.
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if w := b.ask("7", "100.101.102.103", header, "203.0.113.9"); w.Code != http.StatusForbidden {
			t.Fatalf("a request forwarded with %s = %d", header, w.Code)
		}
	}
	if b.asked.Load() != 0 {
		t.Fatalf("tailscaled was asked %d times for callers that are not the proxy", b.asked.Load())
	}
	post := httptest.NewRequest(http.MethodPost, Path+"?environment=7", nil)
	post.RemoteAddr = "127.0.0.1:41000"
	recorder = httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, post)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", recorder.Code)
	}
	if w := b.ask("x", "100.101.102.103"); w.Code != http.StatusBadRequest {
		t.Fatalf("no environment = %d", w.Code)
	}
}

func TestWhoisRefusesAnEnvironmentWithoutAList(t *testing.T) {
	b := newGateBench(t)
	if w := b.ask("8", "100.101.102.103"); w.Code != http.StatusForbidden || b.lastRefusal(t).Reason != "not_gated" {
		t.Fatalf("an environment with no list = %d", w.Code)
	}
	// Not remembered: the release that brings the list goes live a moment
	// after its gate is written, and its first visitor is let in at once.
	access := b.handler.Access
	b.handler.Access = func(ctx context.Context, environmentID int64) ([]string, error) {
		if environmentID == 8 {
			return []string{"alice@example.com"}, nil
		}
		return access(ctx, environmentID)
	}
	if w := b.ask("8", "100.101.102.103"); w.Code != http.StatusNoContent {
		t.Fatalf("once the list is live = %d", w.Code)
	}
	if w := b.ask("9", "100.101.102.103"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an environment whose list could not be read = %d", w.Code)
	}
}

// A page and its assets are one question to tailscaled, not forty, and an
// answer is asked again once it is a few seconds old.
func TestWhoisKeepsAnswersAFewSeconds(t *testing.T) {
	b := newGateBench(t)
	for range 5 {
		if w := b.ask("7", "100.101.102.103"); w.Code != http.StatusNoContent {
			t.Fatalf("= %d", w.Code)
		}
	}
	if b.asked.Load() != 1 {
		t.Fatalf("tailscaled was asked %d times within the window", b.asked.Load())
	}
	b.now = b.now.Add(6 * time.Second)
	b.ask("7", "100.101.102.103")
	if b.asked.Load() != 2 {
		t.Fatalf("tailscaled was asked %d times after the window", b.asked.Load())
	}
}

// One address refused a hundred times a second is one record a minute,
// carrying how many it stands for.
func TestWhoisRecordsRepeatedRefusalsOnceAMinute(t *testing.T) {
	b := newGateBench(t)
	for range 10 {
		b.ask("7", "100.70.0.1")
	}
	if len(b.refusals) != 1 {
		t.Fatalf("%d records for ten refusals in a second", len(b.refusals))
	}
	b.now = b.now.Add(61 * time.Second)
	b.ask("7", "100.70.0.1")
	if len(b.refusals) != 2 || b.refusals[1].Repeated != 9 {
		t.Fatalf("records = %+v", b.refusals)
	}
}

func TestHeaderValueEncodesWhatIsNotPrintableASCII(t *testing.T) {
	if got := headerValue("Zoë Ä\r\n"); got != "=?utf-8?q?Zo=C3=AB_=C3=84?=" {
		t.Fatalf("encoded %q", got)
	}
	if got := headerValue("Alice Example"); got != "Alice Example" {
		t.Fatalf("plain %q", got)
	}
	if _, ok := peerAddress("100.64.0.1, 100.101.102.103"); !ok {
		t.Fatal("a list was not read")
	}
	if address, _ := peerAddress("[fd7a:115c:a1e0::1]"); address != netip.MustParseAddr("fd7a:115c:a1e0::1") {
		t.Fatalf("a bracketed IPv6 = %v", address)
	}
}
