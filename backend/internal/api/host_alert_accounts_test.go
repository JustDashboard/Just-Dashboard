package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
)

func accountSnapshot() *linuxusers.Snapshot {
	return &linuxusers.Snapshot{Accounts: []string{"root", "ubuntu"}, Keys: []string{}, SudoRules: []string{}}
}

func TestAFirstReadingOfTheAccountsSetsTheLineAndTellsNothing(t *testing.T) {
	now := hostAlertClock
	if got := accountChangeFindings(nil, accountSnapshot(), now); len(got.Firing) != 0 || !got.Judged {
		t.Fatalf("first reading = %+v", got)
	}
	before := &accountBaseline{At: now.Add(-time.Minute), Snapshot: *accountSnapshot()}
	after := accountSnapshot()
	after.Accounts = append(after.Accounts, "mallory")
	got := accountChangeFindings(before, after, now)
	if len(got.Firing) != 1 || got.Firing[0].Subject != "account:mallory" || got.Firing[0].Label != "mallory" {
		t.Fatalf("got %+v", got)
	}
	// A rule that was off for a week has not seen a week of changes.
	stale := &accountBaseline{At: now.Add(-accountBaselineMaxAge - time.Minute), Snapshot: *accountSnapshot()}
	if got := accountChangeFindings(stale, after, now); len(got.Firing) != 0 {
		t.Fatalf("stale = %+v", got)
	}
}

func TestAReadThatFailsIsNotJudgedAndKeepsTheLine(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	fails := func(context.Context) (*linuxusers.Snapshot, error) { return nil, errors.New("no") }
	if got := s.accountChangeReading(ctx, hostAlertClock, fails); got.Judged || got.Why == "" {
		t.Fatalf("got %+v", got)
	}
	if _, ok, _ := s.Store.Setting(ctx, accountBaselineSetting); ok {
		t.Fatal("a failed read stored a line")
	}
	reads := func(context.Context) (*linuxusers.Snapshot, error) { return accountSnapshot(), nil }
	if got := s.accountChangeReading(ctx, hostAlertClock, reads); !got.Judged || len(got.Firing) != 0 {
		t.Fatalf("got %+v", got)
	}
	raw, ok, _ := s.Store.Setting(ctx, accountBaselineSetting)
	if !ok || !strings.Contains(raw, "ubuntu") {
		t.Fatalf("stored %q", raw)
	}
	// A line that does not parse sets a new one rather than failing every pass.
	if err := s.Store.SetSetting(ctx, accountBaselineSetting, "{not json"); err != nil {
		t.Fatal(err)
	}
	if got := s.accountChangeReading(ctx, hostAlertClock, reads); !got.Judged || len(got.Firing) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// A change is told the once, however many passes follow it, and nobody is told it
// has recovered: a key that was added has nothing to come back from.
func TestAnAccountChangeIsToldOnceWithNoRecovery(t *testing.T) {
	h := newHostAlertHarness(t)
	current := accountSnapshot()
	h.a.observe = func(ctx context.Context, rules []hostAlertRule, now time.Time) map[int64]hostAlertReading {
		reading := h.s.accountChangeReading(ctx, now, func(context.Context) (*linuxusers.Snapshot, error) {
			return current, nil
		})
		out := map[int64]hostAlertReading{}
		for _, rule := range rules {
			out[rule.ID] = reading
		}
		return out
	}
	w := h.admin.do(http.MethodPost, "/api/v1/system/alerts/rules", `{"kind":"account_changed"}`, nil)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	h.evaluate(t)
	if len(h.sent) != 0 {
		t.Fatalf("the first pass told %d", len(h.sent))
	}
	h.now = h.now.Add(time.Minute)
	h.evaluate(t)
	if len(h.sent) != 0 {
		t.Fatalf("nothing changed and %d were told", len(h.sent))
	}

	current = accountSnapshot()
	current.Keys = []string{"ubuntu SHA256:new laptop"}
	h.now = h.now.Add(time.Minute)
	h.evaluate(t)
	if len(h.sent) != 1 {
		t.Fatalf("told %d", len(h.sent))
	}
	host := h.sent[0].Host
	if host == nil || host.Kind != hostAlertAccountChanged || !strings.Contains(host.Detail, "laptop") || host.Label != "ubuntu" {
		t.Fatalf("envelope = %+v", host)
	}
	if !strings.HasSuffix(h.sent[0].URL, "/system-users") {
		t.Fatalf("url = %q", h.sent[0].URL)
	}

	for range 3 {
		h.now = h.now.Add(time.Minute)
		h.evaluate(t)
	}
	if len(h.sent) != 1 {
		t.Fatalf("after the change: %d told, want still 1", len(h.sent))
	}
}

func TestTheAccountKindIsARuleLikeAnyOther(t *testing.T) {
	in, err := validateHostAlert(hostAlertWrite{Kind: "ACCOUNT_CHANGED", Params: hostAlertParams{Threshold: 50, Minutes: 3}})
	if err != nil || in.Kind != hostAlertAccountChanged || in.Params != (hostAlertParams{}) {
		t.Fatalf("got %+v, %v", in, err)
	}
	if got := hostAlertPage(hostAlertAccountChanged, "account:mallory"); got != "/system-users" {
		t.Fatalf("page = %q", got)
	}
	if hostAlertTestDetail[hostAlertAccountChanged] == "" || hostAlertTestLabel[hostAlertAccountChanged] == "" {
		t.Fatal("no test message")
	}
	if alertName(hostAlertAccountChanged) == hostAlertAccountChanged {
		t.Fatal("the map has no label for it")
	}
}
