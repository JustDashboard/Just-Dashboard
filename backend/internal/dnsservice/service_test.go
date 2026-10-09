package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type adGuardFixture struct {
	mu           sync.Mutex
	protection   bool
	upstreams    []string
	allowed      []string
	denied       []string
	blocked      []string
	policyExtra  string
	readbackFail bool
	mutations    atomic.Int32
	password     string
}

func TestDNSServicePiHoleWaitsForNativeRestartWithoutRepeatingMutation(t *testing.T) {
	for _, processKnown := range []bool{true, false} {
		t.Run(map[bool]string{true: "later_boot_same_pid", false: "unknown_process_refused"}[processKnown], func(t *testing.T) {
			s, _, _, _ := newServiceFixture(t, true)
			started := time.Now().Add(-10 * time.Second)
			var restartAt atomic.Int64
			var patches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var value any
				switch r.Method + " " + r.URL.Path {
				case "POST /api/auth":
					value = map[string]any{"session": map[string]any{"valid": true, "sid": "native-session"}}
				case "DELETE /api/auth":
					w.WriteHeader(204)
					return
				case "GET /api/info/version":
					value = map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.7.1"}}}}
				case "GET /api/info/ftl":
					if processKnown {
						boot := started
						if at := restartAt.Load(); at != 0 && time.Now().After(time.Unix(0, at)) {
							boot = time.Unix(0, at)
						}
						value = map[string]any{"ftl": map[string]any{"pid": 146, "uptime": float64(time.Since(boot)) / float64(time.Millisecond)}}
					} else {
						value = map[string]any{"ftl": map[string]any{}}
					}
				case "PATCH /api/config":
					patches.Add(1)
					// FTL accepts and exposes persisted config before the process exits.
					restartAt.Store(time.Now().Add(750 * time.Millisecond).UnixNano())
					value = map[string]any{}
				case "GET /api/config":
					upstream := "192.0.2.53#53"
					if patches.Load() != 0 {
						upstream = "192.0.2.54#5353"
					}
					value = map[string]any{"config": map[string]any{"dns": map[string]any{"upstreams": []string{upstream}, "listeningMode": "ALL", "port": 53, "hosts": []string{}, "cnameRecords": []string{}}}}
				case "GET /api/dns/blocking":
					value = map[string]any{"blocking": "enabled", "timer": nil}
				case "GET /api/clients":
					value = map[string]any{"clients": []any{}}
				case "GET /api/groups":
					value = map[string]any{"groups": []any{}}
				case "GET /api/queries":
					value = map[string]any{"queries": []any{}}
				default:
					t.Error("unexpected native restart request", r.Method, r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(value)
			}))
			defer server.Close()
			view, err := s.Connect(t.Context(), ConnectionRequest{Name: "Native FTL", Engine: PiHole, Endpoint: server.URL, Management: true, Credential: Credential{Password: "fixture-only-password"}})
			if err != nil {
				t.Fatal(err)
			}
			change, err := s.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "upstreams", Upstreams: []string{"192.0.2.54:5353"}})
			if err != nil {
				t.Fatal(err)
			}
			change, err = s.Apply(t.Context(), change.ID)
			if err != nil {
				t.Fatal(err)
			}
			if processKnown {
				if change.State != "verified" || patches.Load() != 1 || time.Now().Before(time.Unix(0, restartAt.Load())) || change.After.Process.PID != change.Before.Process.PID {
					t.Fatal("persisted config was verified before restart, or mutation was repeated", change.State, patches.Load())
				}
			} else if change.State != "refused" || patches.Load() != 0 {
				t.Fatal("unknown restart identity allowed a native mutation", change.State, patches.Load())
			}
		})
	}
}

func TestDNSNativeFTLRestartRejectsRequestLatencyAsNewBoot(t *testing.T) {
	mutation := time.Now().UTC()
	before := &Snapshot{Process: &NativeProcess{PID: 146, StartedAt: mutation.Add(-10 * time.Second), StartedBefore: mutation.Add(-9 * time.Second)}}
	delayed := &Snapshot{Process: &NativeProcess{PID: 146, StartedAt: mutation.Add(-10 * time.Second), StartedBefore: mutation.Add(2 * time.Second)}}
	if ftlRestarted(before, delayed, mutation) {
		t.Fatal("a delayed response from the old process was treated as a later boot")
	}
	restarted := &Snapshot{Process: &NativeProcess{PID: 146, StartedAt: mutation.Add(time.Second), StartedBefore: mutation.Add(2 * time.Second)}}
	if !ftlRestarted(before, restarted, mutation) {
		t.Fatal("a bounded later boot with a reused container PID was not recognized")
	}
}

func (f *adGuardFixture) serve(w http.ResponseWriter, r *http.Request) {
	user, password, ok := r.BasicAuth()
	if !ok || user != "fixture-user" || password != f.password {
		http.Error(w, "bad native credentials", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var value any
	switch r.Method + " " + r.URL.Path {
	case "GET /control/status":
		value = map[string]any{"version": "v0.107.71", "dns_addresses": []string{"127.0.0.1", "::1"}, "dns_port": 53, "protection_enabled": f.protection, "running": true}
	case "GET /control/dns_info":
		if f.readbackFail {
			http.Error(w, f.password, http.StatusServiceUnavailable)
			return
		}
		value = map[string]any{"upstream_dns": f.upstreams, "foreign_policy": f.policyExtra}
	case "GET /control/access/list":
		value = map[string]any{"allowed_clients": f.allowed, "disallowed_clients": f.denied, "blocked_hosts": f.blocked}
	case "GET /control/clients":
		value = map[string]any{"clients": []any{map[string]any{"name": "private-client", "ids": []string{"10.0.0.0/24"}, "use_global_settings": false, "filtering_enabled": true}}}
	case "GET /control/querylog":
		if r.URL.Query().Get("limit") != "100" {
			http.Error(w, "unbounded query history", http.StatusBadRequest)
			return
		}
		value = map[string]any{"data": []any{map[string]any{"time": "2026-10-08T12:00:00Z", "client": "10.0.0.2", "reason": "FilteredBlackList", "question": map[string]string{"host": "private.corp.example", "type": "A"}}}}
	case "POST /control/protection":
		var body struct {
			Enabled  *bool `json:"enabled"`
			Duration int   `json:"duration"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Enabled == nil || body.Duration != 0 {
			http.Error(w, "bad native protection request", 400)
			return
		}
		f.protection = *body.Enabled
		f.mutations.Add(1)
		value = map[string]bool{"ok": true}
	case "POST /control/access/set":
		var body struct {
			Allowed []string `json:"allowed_clients"`
			Denied  []string `json:"disallowed_clients"`
			Blocked []string `json:"blocked_hosts"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad native access request", 400)
			return
		}
		f.allowed, f.denied, f.blocked = body.Allowed, body.Denied, body.Blocked
		f.mutations.Add(1)
		value = map[string]bool{"ok": true}
	case "POST /control/dns_config":
		var body struct {
			Upstreams []string `json:"upstream_dns"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad native upstream request", 400)
			return
		}
		f.upstreams = body.Upstreams
		f.mutations.Add(1)
		value = map[string]bool{"ok": true}
	default:
		http.Error(w, "unsupported native operation", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func newServiceFixture(t *testing.T, management bool) (*Service, *adGuardFixture, ConnectionRequest, View) {
	t.Helper()
	f := &adGuardFixture{protection: true, upstreams: []string{"192.0.2.53:53"}, allowed: []string{"10.0.0.0/24"}, denied: []string{}, blocked: []string{"retained.private.example"}, password: "fixture-only-secret-value"}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{DB: db.DB, Seal: sealer.Seal, Open: sealer.Open})
	req := ConnectionRequest{Name: "Test engine", Engine: AdGuard, Endpoint: server.URL, Management: management, Credential: Credential{Username: "fixture-user", Password: f.password}}
	view, err := s.Connect(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return s, f, req, view
}

func TestDNSServiceSealsCredentialsAndKeepsReadOnlyClosed(t *testing.T) {
	s, f, _, view := newServiceFixture(t, false)
	if view.Snapshot == nil || view.Snapshot.Transport.State != "loopback_http" || view.Snapshot.ZoneEvidence.State != "unsupported" || len(view.Snapshot.Clients) != 1 || len(view.Snapshot.Queries) != 1 {
		t.Fatalf("native snapshot lost scope: %+v", view)
	}
	var sealed string
	if err := s.db.QueryRow(`SELECT secret_enc FROM network_dns_services WHERE id=?`, view.Connection.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, f.password) {
		t.Fatal("native password stored in plaintext")
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), f.password) {
		t.Fatal("native password exposed in typed view")
	}
	enabled := false
	if _, err := s.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "protection", Protection: &enabled}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only preview=%v", err)
	}
	if f.mutations.Load() != 0 {
		t.Fatal("read-only connection changed native configuration")
	}
}

func TestDNSServiceRefusesChangedCompleteNativePolicyBeforeMutation(t *testing.T) {
	s, f, _, view := newServiceFixture(t, true)
	enabled := false
	plan, err := s.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "protection", Protection: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.policyExtra = "changed outside the edited field"
	f.mu.Unlock()
	result, err := s.Apply(t.Context(), plan.ID)
	if err != nil || result.State != "refused" || f.mutations.Load() != 0 {
		t.Fatalf("changed complete policy applied: %+v %v", result, err)
	}
	if _, err = s.Apply(t.Context(), plan.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("refused plan reused: %v", err)
	}
}

func TestDNSServiceConsumesConcurrentPlanExactlyOnceAndRetainsReadback(t *testing.T) {
	s, f, _, view := newServiceFixture(t, true)
	enabled := false
	plan, err := s.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "protection", Protection: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Apply(context.Background(), plan.ID); results <- err }()
	}
	wg.Wait()
	close(results)
	conflicts := 0
	for err := range results {
		if errors.Is(err, ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	retained, err := s.Change(t.Context(), plan.ID)
	if err != nil || conflicts != 1 || f.mutations.Load() != 1 || retained.State != "verified" || retained.After == nil || retained.After.Protection || len(retained.Before.Queries) != 0 {
		t.Fatalf("single-use/readback failed: %+v %v conflicts=%d mutations=%d", retained, err, conflicts, f.mutations.Load())
	}
}

func TestDNSServiceAccessChangePreservesNativeBlockedHosts(t *testing.T) {
	s, f, _, view := newServiceFixture(t, true)
	plan, err := s.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "access", AllowedClients: []string{"192.0.2.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Apply(t.Context(), plan.ID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil || result.State != "verified" || len(f.blocked) != 1 || f.blocked[0] != "retained.private.example" {
		t.Fatalf("foreign native access policy lost: %+v %v blocked=%v", result, err, f.blocked)
	}
}

func TestDNSServiceGenerationFenceSurvivesCredentialUpdateAndRestart(t *testing.T) {
	s, _, req, view := newServiceFixture(t, true)
	enabled := false
	plan, err := s.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "protection", Protection: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.Update(t.Context(), view.Connection.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Connection.Generation != 2 {
		t.Fatal("credential revision did not advance identity generation")
	}
	if _, err = s.Apply(t.Context(), plan.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("old generation applied: %v", err)
	}
	if _, err = s.db.Exec(`UPDATE network_dns_service_changes SET state='applying' WHERE id=?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	retained, err := s.Change(t.Context(), plan.ID)
	if err != nil || retained.State != "interrupted" || retained.EndedAt == nil {
		t.Fatalf("restart replayed or lost operation: %+v %v", retained, err)
	}
}
