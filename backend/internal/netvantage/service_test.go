package netvantage

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

func testService(t *testing.T) (*Service, *auth.Sealer) {
	t.Helper()
	st, e := storetest.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	sealer, e := auth.NewSealer(strings.Repeat("ab", 32))
	if e != nil {
		t.Fatal(e)
	}
	s := New(st, sealer)
	if e = s.Ready(); e != nil {
		t.Fatal(e)
	}
	return s, sealer
}
func scopeInput() EnrollmentRequest {
	return EnrollmentRequest{Name: "controlled", Location: "Operator-declared test region", Placement: "controlled_fixture", Scopes: []Scope{{ID: "service", Target: "fixture.private", Addresses: []string{"127.0.0.1", "::1"}, Ports: []int{443}, Families: []string{"inet", "inet6"}}}}
}
func enrolled(t *testing.T, s *Service) (Enrollment, ed25519.PrivateKey) {
	t.Helper()
	en, e := s.CreateEnrollment(context.Background(), scopeInput())
	if e != nil {
		t.Fatal(e)
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Claim(context.Background(), SignClaim(key, Claim{ServerKey: en.ServerKey, ID: en.Vantage.ID, Token: en.Token, PublicKey: encode(pub)}))
	if e != nil {
		t.Fatal(e)
	}
	return en, key
}
func queued(t *testing.T, s *Service, en Enrollment) *SignedJob {
	t.Helper()
	_, e := s.CreateCheck(context.Background(), Request{VantageID: en.Vantage.ID, ScopeID: "service", Family: "inet", Port: 443, TLS: true}, "operator")
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.Poll(context.Background(), en.Vantage.ID)
	if e != nil {
		t.Fatal(e)
	}
	return job
}
func reported(j Job) Result {
	now := time.Now().UTC()
	return Result{CheckID: j.ID, VantageID: j.VantageID, Nonce: j.Nonce, Request: j.Request, Target: j.Scope.Target, Address: "127.0.0.1", SourceAddress: "127.0.0.2", Addresses: []string{"127.0.0.1"}, StartedAt: now, EndedAt: now, Stages: []Stage{{Name: "dns", Basis: "measured", State: "resolved", StartedAt: now, EndedAt: now}, {Name: "tcp", Basis: "measured", State: "connected", StartedAt: now, EndedAt: now}, {Name: "tls", Basis: "measured", State: "verified", StartedAt: now, EndedAt: now}}, Certificate: &Certificate{SHA256: strings.Repeat("a", 64)}, Limitations: []string{"Agent-reported fixture; not an off-host claim."}}
}

func TestEnrollmentUsedOnceAndSecretsExcluded(t *testing.T) {
	s, _ := testService(t)
	en, key := enrolled(t, s)
	claim := SignClaim(key, Claim{ServerKey: en.ServerKey, ID: en.Vantage.ID, Token: en.Token, PublicKey: publicKey(key)})
	if _, e := s.Claim(context.Background(), claim); e == nil {
		t.Fatal("one-time token reused")
	}
	vs, e := s.Vantages(context.Background())
	raw, _ := json.Marshal(vs)
	if e != nil || strings.Contains(string(raw), en.Token) || strings.Contains(string(raw), "enrollment_hash") || strings.Contains(string(raw), "public_key") {
		t.Fatalf("credential in inventory %s %v", raw, e)
	}
	var sealed string
	if e = s.db.QueryRow(`SELECT value FROM settings WHERE key='network.probe.signing-key'`).Scan(&sealed); e != nil || strings.Contains(sealed, encode(s.key)) {
		t.Fatal("server private identity was not sealed")
	}
}
func TestEnrollmentExpiryAndWrongProof(t *testing.T) {
	s, _ := testService(t)
	en, e := s.CreateEnrollment(context.Background(), scopeInput())
	if e != nil {
		t.Fatal(e)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	claim := SignClaim(key, Claim{ServerKey: en.ServerKey, ID: en.Vantage.ID, Token: en.Token, PublicKey: encode(pub)})
	bad := claim
	bad.Token = strings.Repeat("0", 64)
	if _, e = s.Claim(context.Background(), bad); e == nil {
		t.Fatal("wrong proof claimed identity")
	}
	s.now = func() time.Time { return en.ExpiresAt }
	if _, e = s.Claim(context.Background(), claim); e == nil {
		t.Fatal("expired enrollment accepted")
	}
}
func TestReplayCounterSurvivesRestartAndConcurrentRequests(t *testing.T) {
	s, sealer := testService(t)
	en, key := enrolled(t, s)
	signature := SignRequest(key, "POST", "/api/v1/probe-agent/poll", nil, Signature{ServerKey: en.ServerKey, ID: en.Vantage.ID, Sequence: 1, Timestamp: time.Now().Unix()})
	var wg sync.WaitGroup
	accepted := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			accepted <- s.Authenticate(context.Background(), "POST", "/api/v1/probe-agent/poll", nil, signature, "127.0.0.1") == nil
		}()
	}
	wg.Wait()
	close(accepted)
	count := 0
	for yes := range accepted {
		if yes {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("one sequence accepted %d times", count)
	}
	restarted := New(&store.Store{DB: s.db}, sealer)
	if e := restarted.Authenticate(context.Background(), "POST", "/api/v1/probe-agent/poll", nil, signature, "127.0.0.1"); e == nil {
		t.Fatal("restart reopened replay")
	}
	signature.Sequence = 2
	signature.Timestamp = time.Now().Add(-2 * time.Minute).Unix()
	signature = SignRequest(key, "POST", "/api/v1/probe-agent/poll", nil, signature)
	if e := s.Authenticate(context.Background(), "POST", "/api/v1/probe-agent/poll", nil, signature, "127.0.0.1"); e == nil {
		t.Fatal("stale signed request accepted")
	}
}
func TestSignatureBindsBodyPathAndVantage(t *testing.T) {
	s, _ := testService(t)
	en, key := enrolled(t, s)
	sig := SignRequest(key, "POST", "/api/v1/probe-agent/result", []byte("one"), Signature{ServerKey: en.ServerKey, ID: en.Vantage.ID, Sequence: 1, Timestamp: time.Now().Unix()})
	for _, tuple := range [][2]string{{"/api/v1/probe-agent/poll", "one"}, {"/api/v1/probe-agent/result", "two"}} {
		if e := s.Authenticate(context.Background(), "POST", tuple[0], []byte(tuple[1]), sig, "127.0.0.1"); e == nil {
			t.Fatal("signature did not bind request")
		}
	}
}
func TestJobScopeCannotBecomeScannerOrShell(t *testing.T) {
	s, _ := testService(t)
	en, _ := enrolled(t, s)
	for _, req := range []Request{{VantageID: en.Vantage.ID, ScopeID: "unknown", Family: "inet", Port: 443}, {VantageID: en.Vantage.ID, ScopeID: "service", Family: "inet", Port: 22}, {VantageID: en.Vantage.ID, ScopeID: "service", Family: "any", Port: 443}} {
		if _, e := s.CreateCheck(context.Background(), req, "operator"); e == nil {
			t.Fatalf("outside-scope request admitted %#v", req)
		}
	}
	for _, target := range []string{"--help", "$(id)", "192.0.2.0/24", "*.example.com", "host/path"} {
		input := scopeInput()
		input.Scopes[0].Target = target
		if _, e := ValidateEnrollment(input); e == nil {
			t.Fatalf("range/executable target accepted %s", target)
		}
	}
	job := queued(t, s, en)
	manifest := Manifest{Vantage: en.Vantage, ServerKey: en.ServerKey}
	if e := ValidateJob(*job, manifest, time.Now()); e != nil {
		t.Fatal(e)
	}
	job.Job.Scope.Target = "another.host"
	if e := ValidateJob(*job, manifest, time.Now()); e == nil {
		t.Fatal("unsigned altered scope accepted")
	}
	job.Signature = encode(ed25519.Sign(s.key, Marshal(job.Job)))
	if e := ValidateJob(*job, manifest, time.Now()); e == nil {
		t.Fatal("server expanded the agent's local enrollment manifest")
	}
}
func TestResultAcceptedOnceAndTupleLeaseBound(t *testing.T) {
	s, _ := testService(t)
	en, _ := enrolled(t, s)
	job := queued(t, s, en)
	r := reported(job.Job)
	for _, change := range []func(*Result){func(r *Result) { r.Nonce = "wrong" }, func(r *Result) { r.Request.Port = 22 }, func(r *Result) { r.Address = "192.0.2.1" }, func(r *Result) { r.VantageID = strings.Repeat("a", 32) }, func(r *Result) { r.Stages = nil }, func(r *Result) { r.EndedAt = job.Job.ExpiresAt.Add(time.Second) }} {
		copy := r
		copy.Stages = append([]Stage(nil), r.Stages...)
		change(&copy)
		if e := s.Complete(context.Background(), en.Vantage.ID, copy); e == nil {
			t.Fatalf("invalid measurement accepted %#v", copy)
		}
	}
	if e := s.Complete(context.Background(), en.Vantage.ID, r); e != nil {
		t.Fatal(e)
	}
	if e := s.Complete(context.Background(), en.Vantage.ID, r); e == nil {
		t.Fatal("result replay accepted")
	}
	values, e := s.Checks(context.Background())
	if e != nil || len(values) != 1 || values[0].Result == nil || values[0].Status != "completed" {
		t.Fatalf("measurement not retained %#v %v", values, e)
	}
}
func TestRevokedCancelledAndExpiredJobsCannotPromoteMeasurements(t *testing.T) {
	for _, action := range []string{"revoke", "cancel", "expire"} {
		t.Run(action, func(t *testing.T) {
			s, _ := testService(t)
			en, key := enrolled(t, s)
			job := queued(t, s, en)
			r := reported(job.Job)
			switch action {
			case "revoke":
				if e := s.Revoke(context.Background(), en.Vantage.ID); e != nil {
					t.Fatal(e)
				}
			case "cancel":
				if e := s.Cancel(context.Background(), job.Job.ID); e != nil {
					t.Fatal(e)
				}
			case "expire":
				s.now = func() time.Time { return job.Job.ExpiresAt }
			}
			if e := s.Complete(context.Background(), en.Vantage.ID, r); e == nil {
				t.Fatal("late measurement promoted")
			}
			values, e := s.Checks(context.Background())
			if e != nil || values[0].Result != nil {
				t.Fatalf("unknown became measured %#v %v", values, e)
			}
			if action == "revoke" {
				signature := SignRequest(key, "POST", "/api/v1/probe-agent/poll", nil, Signature{ServerKey: en.ServerKey, ID: en.Vantage.ID, Sequence: 1, Timestamp: time.Now().Unix()})
				if e := s.Authenticate(context.Background(), "POST", "/api/v1/probe-agent/poll", nil, signature, "127.0.0.1"); e == nil {
					t.Fatal("revoked identity authenticated")
				}
			}
		})
	}
}
func TestOutstandingAndRetentionBudgets(t *testing.T) {
	s, _ := testService(t)
	en, _ := enrolled(t, s)
	job := queued(t, s, en)
	if _, e := s.CreateCheck(context.Background(), job.Job.Request, "operator"); e == nil {
		t.Fatal("multiple concurrent jobs admitted")
	}
	s.now = func() time.Time { return job.Job.IssuedAt.Add(Retention + time.Hour) }
	checks, e := s.Checks(context.Background())
	if e != nil || len(checks) != 0 {
		t.Fatalf("retention unbounded %#v %v", checks, e)
	}
}

func TestServerSigningIdentityRejectsCrossInstallationReplay(t *testing.T) {
	s, _ := testService(t)
	en, key := enrolled(t, s)
	other, _ := testService(t)
	signature := SignRequest(key, "POST", "/api/v1/probe-agent/poll", nil, Signature{ServerKey: publicKey(other.key), ID: en.Vantage.ID, Sequence: 1, Timestamp: time.Now().Unix()})
	if e := s.Authenticate(context.Background(), "POST", "/api/v1/probe-agent/poll", nil, signature, ""); e == nil {
		t.Fatal("different server identity consumed the request")
	}
	signature.ServerKey = en.ServerKey
	if e := s.Authenticate(context.Background(), "POST", "/api/v1/probe-agent/poll", nil, signature, ""); e == nil {
		t.Fatal("changing the identity header preserved the signature")
	}
	signature = SignRequest(key, "POST", "/api/v1/probe-agent/poll", nil, signature)
	if e := s.Authenticate(context.Background(), "POST", "/api/v1/probe-agent/poll", nil, signature, ""); e != nil {
		t.Fatal("invalid identity consumed the valid sequence", e)
	}
	job := queued(t, s, en)
	if e := VerifyJob(*job, publicKey(other.key)); e == nil {
		t.Fatal("another server accepted the job")
	}
}

func TestEnrollmentAndResultTransactionsAcceptExactlyOnce(t *testing.T) {
	s, _ := testService(t)
	en, e := s.CreateEnrollment(context.Background(), scopeInput())
	if e != nil {
		t.Fatal(e)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	claim := SignClaim(key, Claim{ServerKey: en.ServerKey, ID: en.Vantage.ID, Token: en.Token, PublicKey: publicKey(key)})
	var mu sync.Mutex
	accepted := 0
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, e := s.Claim(context.Background(), claim); e == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted enrollment %d times", accepted)
	}
	job := queued(t, s, en)
	accepted = 0
	result := reported(job.Job)
	for range 8 {
		wg.Go(func() {
			if e := s.Complete(context.Background(), en.Vantage.ID, result); e == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted result %d times", accepted)
	}
}

func TestUnavailableServerKeyFailsWithoutReplacingIdentity(t *testing.T) {
	s, sealer := testService(t)
	if _, e := s.db.Exec(`UPDATE settings SET value='unreadable' WHERE key='network.probe.signing-key'`); e != nil {
		t.Fatal(e)
	}
	reboot := New(&store.Store{DB: s.db}, sealer)
	if e := reboot.Ready(); e == nil {
		t.Fatal("bad key silently replaced")
	}
	var value string
	s.db.QueryRow(`SELECT value FROM settings WHERE key='network.probe.signing-key'`).Scan(&value)
	if value != "unreadable" {
		t.Fatal("failed identity was overwritten")
	}
	if _, e := reboot.CreateEnrollment(context.Background(), scopeInput()); e == nil {
		t.Fatal("admitted enrollment without signing identity")
	}
}

func TestRestartNeverReleasesAnAlreadyLeasedCheck(t *testing.T) {
	s, sealer := testService(t)
	en, _ := enrolled(t, s)
	job := queued(t, s, en)
	reboot := New(&store.Store{DB: s.db}, sealer)
	if repeated, e := reboot.Poll(context.Background(), en.Vantage.ID); e != nil || repeated != nil {
		t.Fatalf("backend restart re-leased a probe: %#v %v", repeated, e)
	}
	reboot.now = func() time.Time { return job.Job.ExpiresAt.Add(time.Second) }
	if repeated, e := reboot.Poll(context.Background(), en.Vantage.ID); e != nil || repeated != nil {
		t.Fatalf("expired job was replayed: %#v %v", repeated, e)
	}
	checks, e := reboot.Checks(context.Background())
	if e != nil || len(checks) != 1 || checks[0].Status != "expired" || checks[0].Result != nil {
		t.Fatalf("transport loss became connectivity evidence: %#v %v", checks, e)
	}
}
