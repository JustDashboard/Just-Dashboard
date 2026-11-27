package hublink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
)

// fakeFleet is a FleetSources whose readings a test sets and counts.
type fakeFleet struct {
	mu     sync.Mutex
	os     *OSUpdates
	osErr  error
	certs  []CertificateFact
	scans  []ImageScanFact
	counts SignInCounts
	reads  map[string]int
}

func (f *fakeFleet) sources() *FleetSources {
	note := func(k string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.reads == nil {
			f.reads = map[string]int{}
		}
		f.reads[k]++
	}
	return &FleetSources{
		OSUpdates: func(context.Context) (*OSUpdates, error) {
			note("os")
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.os, f.osErr
		},
		Certificates: func(context.Context) ([]CertificateFact, error) {
			note("certificates")
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.certs, nil
		},
		ImageScans: func(context.Context) ([]ImageScanFact, error) {
			note("imageScans")
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.scans, nil
		},
		SignIns: func(context.Context, time.Time) (SignInCounts, error) {
			note("signins")
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.counts, nil
		},
	}
}

func (f *fakeFleet) readsOf(k string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[k]
}

// newFleetLink links a hub with fleet sources whose readings run inline, so
// a heartbeat carries what they read at once.
func newFleetLink(t *testing.T, scope accountlink.Scope) (*env, *fakeFleet) {
	t.Helper()
	security := 2
	f := &fakeFleet{
		os:     &OSUpdates{Pending: 7, Security: &security, RebootRequired: true, CheckedAt: vectorNow - 60},
		certs:  []CertificateFact{{Name: "shop.example.com", DaysLeft: 12, Issuer: "R11"}},
		scans:  []ImageScanFact{{Deployment: "shop", Environment: "production", Critical: 1, High: 3, ScannedAt: vectorNow - 3600}},
		counts: SignInCounts{Successes: 4, Failures: 2, LastFailureAt: vectorNow - 120},
	}
	e := newEnv(t, func(o *Options) { o.Fleet = f.sources() })
	e.svc.fleet.spawn = func(fn func()) { fn() }
	e.link(scope)
	return e, f
}

func lastBeat(t *testing.T, e *env) map[string]json.RawMessage {
	t.Helper()
	if err := e.svc.Sync(context.WithoutCancel(t.Context())); err != nil {
		t.Fatal(err)
	}
	bodies := e.hub.heartbeatBodies()
	var beat map[string]json.RawMessage
	if err := json.Unmarshal(bodies[len(bodies)-1], &beat); err != nil {
		t.Fatal(err)
	}
	return beat
}

// The fleet fields are the hub link's own keys beside the snapshot: os and
// signins at every scope, certificate and deployment names from operate up
// (hub-link-protocol.md 5.4).
func TestTheFleetFieldsFollowTheScope(t *testing.T) {
	e, _ := newFleetLink(t, accountlink.ScopeObserve)
	beat := lastBeat(t, e)
	if got := string(beat["os"]); got != `{"updates":{"pending":7,"security":2,"rebootRequired":true,"checkedAt":1761350340}}` {
		t.Fatalf("os = %s", got)
	}
	if got := string(beat["signins"]); got != `{"successes":4,"failures":2,"lastFailureAt":1761350280}` {
		t.Fatalf("signins = %s", got)
	}
	for _, k := range []string{"certificates", "imageScans"} {
		if _, ok := beat[k]; ok {
			t.Fatalf("observe sent %s: %s", k, beat[k])
		}
	}

	if err := e.svc.SetScope(t.Context(), accountlink.ScopeOperate); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute)
	beat = lastBeat(t, e)
	if got := string(beat["certificates"]); got != `[{"name":"shop.example.com","daysLeft":12,"issuer":"R11"}]` {
		t.Fatalf("certificates = %s", got)
	}
	if got := string(beat["imageScans"]); got != `[{"deployment":"shop","environment":"production","critical":1,"high":3,"scannedAt":1761346800}]` {
		t.Fatalf("imageScans = %s", got)
	}
	if _, ok := beat["os"]; !ok {
		t.Fatal("operate left os out")
	}
	// The snapshot itself is unchanged: the server section is the same
	// account-link shape, with no fleet key inside it.
	var server map[string]any
	if err := json.Unmarshal(beat["server"], &server); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"updates", "certificates", "imageScans", "signins"} {
		if _, ok := server[k]; ok {
			t.Fatalf("the snapshot's server section carries %s", k)
		}
	}
}

// A field that is not known is left out, never sent as empty: no package
// manager, a failed listing, a security count the manager cannot tell.
func TestAFleetFieldThatIsNotKnownIsLeftOut(t *testing.T) {
	e, f := newFleetLink(t, accountlink.ScopeOperate)
	f.mu.Lock()
	f.os = nil
	f.mu.Unlock()
	e.advance(7 * time.Hour)
	if beat := lastBeat(t, e); beat["os"] != nil {
		t.Fatalf("a host with no package manager sent os = %s", beat["os"])
	}

	f.mu.Lock()
	f.os, f.osErr = &OSUpdates{Pending: 1}, errors.New("apt list failed")
	f.mu.Unlock()
	e.advance(7 * time.Hour)
	if beat := lastBeat(t, e); beat["os"] != nil {
		t.Fatalf("a failed listing sent os = %s", beat["os"])
	}

	f.mu.Lock()
	f.os, f.osErr = &OSUpdates{Pending: 3, CheckedAt: vectorNow}, nil
	f.mu.Unlock()
	// A failed reading is tried again sooner than the six-hour interval.
	e.advance(readRetry)
	if got := string(lastBeat(t, e)["os"]); got != `{"updates":{"pending":3,"rebootRequired":false,"checkedAt":1761350400}}` {
		t.Fatalf("os without a security count = %s", got)
	}

	// An empty list is "none", which is known, and never null.
	f.mu.Lock()
	f.certs, f.scans = nil, []ImageScanFact{}
	f.mu.Unlock()
	e.advance(certificatesEvery)
	beat := lastBeat(t, e)
	if got := string(beat["certificates"]); got != `[]` {
		t.Fatalf("no certificates = %s", got)
	}
	if got := string(beat["imageScans"]); got != `[]` {
		t.Fatalf("no scans = %s", got)
	}
}

// Each field is read on its own interval, not on every heartbeat: the
// package list every six hours.
func TestTheFleetFieldsAreReadOnTheirOwnInterval(t *testing.T) {
	e, f := newFleetLink(t, accountlink.ScopeOperate)
	lastBeat(t, e)
	before := f.readsOf("os")
	for range 3 {
		e.advance(time.Minute)
		lastBeat(t, e)
	}
	if got := f.readsOf("os"); got != before {
		t.Fatalf("the package list was read %d more times within six hours", got-before)
	}
	if got := f.readsOf("signins"); got < before+3 {
		t.Fatalf("sign-ins were read %d times in three minutes", got)
	}
	e.advance(osUpdatesEvery)
	lastBeat(t, e)
	if got := f.readsOf("os"); got != before+1 {
		t.Fatalf("after six hours the package list was read %d more times, want 1", got-before)
	}
}

// A heartbeat never waits for a reading: the first one carries what is known,
// and the reading's answer rides the next.
func TestAHeartbeatDoesNotWaitForAReading(t *testing.T) {
	c := &cached[int]{every: time.Hour, read: func(context.Context) (int, error) { return 5, nil }}
	var pending []func()
	spawn := func(fn func()) { pending = append(pending, fn) }
	now := time.Unix(vectorNow, 0)
	if _, ok := c.get(now, spawn); ok {
		t.Fatal("a value before any reading finished")
	}
	if _, ok := c.get(now, spawn); ok || len(pending) != 1 {
		t.Fatalf("a second heartbeat started another reading: %d", len(pending))
	}
	pending[0]()
	if v, ok := c.get(now, spawn); !ok || v != 5 {
		t.Fatalf("after the reading: %d %v", v, ok)
	}
	// A clock that went back reads again at once.
	c.get(now.Add(-time.Hour), spawn)
	if len(pending) != 2 {
		t.Fatalf("a clock that went back started %d readings", len(pending)-1)
	}
}

// A reading runs on a goroutine of its own, outside the connector's
// supervision, and its sources parse what the host prints: one that panics is
// a failed reading, retried like any other, not the end of the dashboard.
func TestAFleetReadingThatPanicsIsAFailedReading(t *testing.T) {
	calls := 0
	c := &cached[int]{every: time.Hour, read: func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			panic("an unusual line")
		}
		return 5, nil
	}}
	inline := func(fn func()) { fn() }
	now := time.Unix(vectorNow, 0)
	if _, ok := c.get(now, inline); ok {
		t.Fatal("a reading that panicked gave a value")
	}
	if _, ok := c.get(now.Add(time.Minute), inline); ok || calls != 1 {
		t.Fatalf("read %d times before the retry", calls)
	}
	if v, ok := c.get(now.Add(readRetry), inline); !ok || v != 5 || calls != 2 {
		t.Fatalf("after the retry: %d %v, read %d times", v, ok, calls)
	}
}

// The two lists are cut to their counts and then to a share of the body,
// keeping the soonest to expire and the worst scans.
func TestTheFleetListsFitTheirShareOfTheBody(t *testing.T) {
	var certs []CertificateFact
	var scans []ImageScanFact
	for i := range 150 {
		certs = append(certs, CertificateFact{Name: strings.Repeat("a", 200) + fmt.Sprint(i), DaysLeft: 150 - i, Issuer: strings.Repeat("i", 100)})
		scans = append(scans, ImageScanFact{Deployment: strings.Repeat("d", 120) + fmt.Sprint(i), Environment: "production", Critical: i, ScannedAt: 1})
	}
	certs, scans = SortCertificates(certs), SortImageScans(scans)
	gotCerts, gotScans := fitFleetLists(certs, scans)
	a, _ := json.Marshal(gotCerts)
	b, _ := json.Marshal(gotScans)
	if len(a)+len(b) > maxFleetBytes {
		t.Fatalf("the fleet lists take %d bytes", len(a)+len(b))
	}
	if len(gotCerts) == 0 || len(gotScans) == 0 || len(gotCerts) > maxCertificates || len(gotScans) > maxImageScans {
		t.Fatalf("kept %d certificates and %d scans", len(gotCerts), len(gotScans))
	}
	if gotCerts[0].DaysLeft != 1 || gotScans[0].Critical != 149 {
		t.Fatalf("kept %+v first and %+v", gotCerts[0], gotScans[0])
	}
	// A heartbeat with the most of both still fits the protocol's limit.
	e, f := newFleetLink(t, accountlink.ScopeOperate)
	f.mu.Lock()
	f.certs, f.scans = certs, scans
	f.mu.Unlock()
	e.advance(certificatesEvery)
	lastBeat(t, e)
	bodies := e.hub.heartbeatBodies()
	if n := len(bodies[len(bodies)-1]); n > 63<<10 {
		t.Fatalf("the heartbeat is %d bytes", n)
	}
}

// Names keep the text rule: a control or format character is never sent,
// and a name that is nothing else is not a name.
func TestFleetNamesKeepTheTextRule(t *testing.T) {
	got := SortCertificates([]CertificateFact{
		{Name: "shop‮.example.com\n", DaysLeft: 3, Issuer: "R\u000011"},
		{Name: "​", DaysLeft: 1},
	})
	if len(got) != 1 || got[0].Name != "shop.example.com" || got[0].Issuer != "R11" {
		t.Fatalf("got %+v", got)
	}
	scans := SortImageScans([]ImageScanFact{
		{Deployment: "shop", Environment: "production", ScannedAt: 0},
		{Deployment: "shop", Environment: "staging", High: 2, ScannedAt: 5},
	})
	if len(scans) != 1 || scans[0].Environment != "staging" {
		t.Fatalf("a scan with no time was kept: %+v", scans)
	}
}

// A success is a completed sign-in, and a failure a refused password, code or
// passkey, over the last day; the last failure is looked for over 30 days.
func TestSignInCountsCountCompletedSignIns(t *testing.T) {
	e := newEnv(t)
	now := time.Unix(vectorNow, 0)
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }
	write := func(ts time.Time, action string, success bool, detail any) {
		e.audit.Record(t.Context(), audit.Entry{TS: ts, Action: action, Success: success, Detail: audit.Detail(detail)})
	}
	owed := map[string]any{"needsTotp": true, "needsEnrollment": false}
	none := map[string]any{"needsTotp": false, "needsEnrollment": false}
	write(at(time.Hour), "auth.login", true, owed)                                              // a code is owed: not yet
	write(at(59*time.Minute), "auth.2fa.verify", true, nil)                                     // that code: one
	write(at(2*time.Hour), "auth.login", true, none)                                            // two
	write(at(3*time.Hour), "auth.passkey.login", true, none)                                    // three
	write(at(3*time.Hour), "auth.passkey.login", true, map[string]any{"needsEnrollment": true}) // owed: not yet
	write(at(4*time.Hour), "hub.signin", true, nil)                                             // four
	write(at(5*time.Hour), "auth.login", true, nil)                                             // an older entry with no detail: five
	write(at(10*time.Minute), "auth.login", false, nil)                                         // failure
	write(at(20*time.Minute), "auth.2fa.verify", false, map[string]any{"result": "rejected"})   // failure
	write(at(30*time.Minute), "auth.passkey.login", false, nil)                                 // failure
	write(at(5*time.Minute), "hub.signin", false, nil)                                          // the hub's own to count
	write(at(6*time.Minute), "auth.logout", true, nil)                                          // not a sign-in
	write(at(25*time.Hour), "auth.login", true, none)                                           // outside the day
	write(at(48*time.Hour), "auth.login", false, nil)                                           // outside the day
	write(at(40*24*time.Hour), "auth.login", false, nil)                                        // outside 30 days
	write(now.Add(time.Hour), "auth.login", false, nil)                                         // in the future
	got, err := AuditSignIns(e.st.DB)(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if want := (SignInCounts{Successes: 5, Failures: 3, LastFailureAt: at(10 * time.Minute).Unix()}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// With no failure in the day, the last one is still the newest of the
	// 30 days (by then the one that was ahead of the clock); with none in
	// them it is left out.
	got, err = AuditSignIns(e.st.DB)(t.Context(), now.Add(30*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Failures != 0 || got.LastFailureAt != now.Add(time.Hour).Unix() {
		t.Fatalf("a day later: %+v", got)
	}
	got, err = AuditSignIns(e.st.DB)(t.Context(), now.Add(60*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got != (SignInCounts{}) {
		t.Fatalf("two months later: %+v", got)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != `{"successes":0,"failures":0}` {
		t.Fatalf("encoded %s", raw)
	}
}
