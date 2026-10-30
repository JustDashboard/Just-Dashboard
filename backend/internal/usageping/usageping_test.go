package usageping

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// fakeSite records every request it is sent and answers with status.
type fakeSite struct {
	*httptest.Server
	mu       sync.Mutex
	status   int
	bodies   []string
	requests []*http.Request
}

func newFakeSite(t *testing.T) *fakeSite {
	t.Helper()
	f := &fakeSite{status: http.StatusNoContent}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body.Write(buf[:n])
			if err != nil {
				break
			}
		}
		f.bodies = append(f.bodies, body.String())
		f.requests = append(f.requests, r)
		w.WriteHeader(f.status)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSite) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeSite) answer(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

type env struct {
	t    *testing.T
	st   *store.Store
	site *fakeSite
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &env{t: t, st: st, site: newFakeSite(t), now: time.Date(2026, 10, 30, 0, 20, 0, 0, time.UTC)}
}

// service is a new Service over the same store, which is what a restart is.
func (e *env) service(offBy string) *Service {
	return New(Options{
		Store: e.st, SiteURL: e.site.URL, Version: "0.8.2", OffBy: offBy,
		Now: func() time.Time { return e.now },
	})
}

func (e *env) status(s *Service) Status {
	e.t.Helper()
	st, err := s.Status(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func TestNothingIsSentUntilSomebodySaysYes(t *testing.T) {
	e := newEnv(t)
	s := e.service("")

	s.sendIfDue(t.Context())
	if st := e.status(s); st.Choice != ChoiceUndecided || !st.Enabled {
		t.Fatalf("a fresh install reads as %+v", st)
	}

	if err := s.SetChoice(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	s.sendIfDue(t.Context())
	if st := e.status(s); st.Choice != ChoiceOff {
		t.Fatalf("a no reads as %+v", st)
	}

	if got := e.site.count(); got != 0 {
		t.Fatalf("the site was sent %d reports by a server that never said yes", got)
	}
}

func TestASwitchInTheEnvironmentOverridesAnEarlierYes(t *testing.T) {
	e := newEnv(t)
	if err := e.service("").SetChoice(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	before := e.site.count()

	s := e.service("JD_USAGE_PING")
	e.now = e.now.Add(48 * time.Hour)
	s.sendIfDue(t.Context())
	if got := e.site.count(); got != before {
		t.Fatalf("a switched-off server sent %d more reports", got-before)
	}
	if err := s.SetChoice(t.Context(), true); err != ErrDisabled {
		t.Fatalf("answering while switched off: %v", err)
	}
	if st := e.status(s); st.Enabled || st.OffBy != "JD_USAGE_PING" {
		t.Fatalf("switched off reads as %+v", st)
	}
}

// One report per UTC day is the whole of how the count works: there is no ID to
// de-duplicate on, so a second one is a second server.
func TestAServerIsCountedOnceADayHoweverOftenItRestarts(t *testing.T) {
	e := newEnv(t)
	s := e.service("")
	if err := s.SetChoice(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if got := e.site.count(); got != 1 {
		t.Fatalf("a yes sent %d reports, want the first at once", got)
	}

	s.sendIfDue(t.Context())
	e.service("").sendIfDue(t.Context())
	e.now = e.now.Add(23 * time.Hour)
	e.service("").sendIfDue(t.Context())
	if got := e.site.count(); got != 1 {
		t.Fatalf("%d reports in one UTC day", got)
	}

	e.now = e.now.Add(time.Hour)
	e.service("").sendIfDue(t.Context())
	if got := e.site.count(); got != 2 {
		t.Fatalf("the next UTC day sent %d reports in total, want 2", got)
	}
}

func TestSwitchingOffAndOnInADayIsNotASecondServer(t *testing.T) {
	e := newEnv(t)
	s := e.service("")
	for _, on := range []bool{true, false, true} {
		if err := s.SetChoice(t.Context(), on); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.site.count(); got != 1 {
		t.Fatalf("%d reports after on, off, on", got)
	}
}

// The promise on the page is that it shows the report rather than describing
// it, so the bytes and the headers are what is pinned here.
func TestTheRequestCarriesExactlyWhatThePageShows(t *testing.T) {
	e := newEnv(t)
	s := e.service("")
	shown := e.status(s).NextBody
	if want := `{"version":"0.8.2","usedToday":false,"usedThisWeek":false}`; shown != want {
		t.Fatalf("the page would show %s, want %s", shown, want)
	}
	if err := s.SetChoice(t.Context(), true); err != nil {
		t.Fatal(err)
	}

	if e.site.bodies[0] != shown {
		t.Fatalf("sent %s, the page showed %s", e.site.bodies[0], shown)
	}
	req := e.site.requests[0]
	if req.Method != http.MethodPost || req.URL.Path != Path || req.URL.RawQuery != "" {
		t.Fatalf("sent %s %s", req.Method, req.URL)
	}
	allowed := map[string]bool{"User-Agent": true, "Content-Type": true, "Content-Length": true, "Accept-Encoding": true, "Connection": true}
	for name := range req.Header {
		if !allowed[name] {
			t.Errorf("the request carries a %s header", name)
		}
	}
	if ua := req.UserAgent(); ua != "just-dashboard" {
		t.Errorf("user agent %q says more than the name", ua)
	}

	st := e.status(s)
	if st.LastBody != shown || st.LastSentAt == nil || !st.LastSentAt.Equal(e.now) || st.LastError != "" {
		t.Fatalf("after a report: %+v", st)
	}
}

// A new field here means telling people the project collects something new,
// and changing the site's receiver, which refuses anything else.
func TestReportIsExactlyThreeFields(t *testing.T) {
	typ := reflect.TypeFor[Report]()
	var names []string
	for i := range typ.NumField() {
		names = append(names, typ.Field(i).Tag.Get("json"))
	}
	if got, want := strings.Join(names, ","), "version,usedToday,usedThisWeek"; got != want {
		t.Fatalf("Report has the fields %s, the published contract is %s", got, want)
	}
}

func TestAFailedReportIsExplainedAndTriedAgain(t *testing.T) {
	e := newEnv(t)
	e.site.answer(http.StatusInternalServerError)
	s := e.service("")
	if err := s.SetChoice(t.Context(), true); err != nil {
		t.Fatalf("a site that is down failed the answer: %v", err)
	}
	st := e.status(s)
	if st.Choice != ChoiceOn || !strings.Contains(st.LastError, "500") || st.LastSentAt != nil {
		t.Fatalf("after a refusal: %+v", st)
	}

	e.site.answer(http.StatusNoContent)
	s.sendIfDue(t.Context())
	st = e.status(s)
	if st.LastError != "" || st.LastSentAt == nil {
		t.Fatalf("after the retry: %+v", st)
	}
	if got := e.site.count(); got != 2 {
		t.Fatalf("%d attempts, want the refusal and the retry", got)
	}
	s.sendIfDue(t.Context())
	if got := e.site.count(); got != 2 {
		t.Fatalf("%d attempts after a success the same day", got)
	}
}

func TestAFailureKeepsTheLastReportThatWent(t *testing.T) {
	e := newEnv(t)
	s := e.service("")
	if err := s.SetChoice(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	sent := e.now
	e.now = e.now.Add(24 * time.Hour)
	e.site.answer(http.StatusBadGateway)
	s.sendIfDue(t.Context())
	st := e.status(s)
	if st.LastSentAt == nil || !st.LastSentAt.Equal(sent) || st.LastError == "" {
		t.Fatalf("a failure the next day reads as %+v", st)
	}
}

func TestARedirectIsNeverFollowed(t *testing.T) {
	e := newEnv(t)
	elsewhere := newFakeSite(t)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	s := New(Options{
		Store: e.st, SiteURL: redirector.URL, Version: "0.8.2", Now: func() time.Time { return e.now },
	})
	if err := s.SetChoice(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if got := elsewhere.count(); got != 0 {
		t.Fatalf("the report followed a redirect %d times", got)
	}
	if st := e.status(s); st.LastError == "" || st.LastSentAt != nil {
		t.Fatalf("a redirect reads as %+v", st)
	}
}

func TestUsedIsSomebodySignedIn(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 20, 0, 0, time.UTC)
	hoursAgo := func(h int) int64 { return now.Add(-time.Duration(h) * time.Hour).Unix() }
	cases := []struct {
		name         string
		session      int64
		auditAction  string
		auditAt      int64
		auditSuccess int
		today, week  bool
	}{
		{name: "nobody has ever signed in"},
		{name: "a session seen an hour ago", session: hoursAgo(1), today: true, week: true},
		{name: "a session seen three days ago", session: hoursAgo(72), week: true},
		{name: "a session seen eight days ago", session: hoursAgo(192)},
		// The session row is gone by then, which is why the audit log is read.
		{name: "signed in and out earlier today", auditAction: "auth.login", auditAt: hoursAgo(2), auditSuccess: 1, today: true, week: true},
		{name: "signed out two days ago", auditAction: "auth.logout", auditAt: hoursAgo(48), auditSuccess: 1, week: true},
		{name: "a bot guessing passwords", auditAction: "auth.login", auditAt: hoursAgo(1), auditSuccess: 0},
		{name: "something else in the audit log", auditAction: "docker.restart", auditAt: hoursAgo(1), auditSuccess: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.now = now
			if tc.session != 0 {
				if _, err := e.st.DB.Exec(`INSERT INTO users(id, username, password_hash, role, created_at) VALUES(1, 'a', 'x', 'admin', 1)`); err != nil {
					t.Fatal(err)
				}
				if _, err := e.st.DB.Exec(`INSERT INTO sessions(id, user_id, token_hash, created_at, last_seen_at, expires_at) VALUES('s', 1, 'h', 1, ?, ?)`, tc.session, now.Add(time.Hour).Unix()); err != nil {
					t.Fatal(err)
				}
			}
			if tc.auditAction != "" {
				if _, err := e.st.DB.Exec(`INSERT INTO audit_log(ts, action, success) VALUES(?, ?, ?)`, tc.auditAt, tc.auditAction, tc.auditSuccess); err != nil {
					t.Fatal(err)
				}
			}
			var report Report
			if err := json.Unmarshal([]byte(e.status(e.service("")).NextBody), &report); err != nil {
				t.Fatal(err)
			}
			if report.UsedToday != tc.today || report.UsedThisWeek != tc.week {
				t.Fatalf("today=%v week=%v, want today=%v week=%v", report.UsedToday, report.UsedThisWeek, tc.today, tc.week)
			}
		})
	}
}

func TestStartAndStop(t *testing.T) {
	e := newEnv(t)
	s := e.service("")
	s.Start(t.Context())
	s.Start(t.Context())
	s.Stop()
	s.Stop()

	var none *Service
	none.Start(t.Context())
	none.Stop()
}
