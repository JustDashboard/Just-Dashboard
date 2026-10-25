package accountlink

import (
	"errors"
	"testing"
	"time"
)

func at24(h, m int) time.Time { return time.Date(2026, 10, 25, h, m, 0, 0, time.UTC) }

func TestAWindowIsReadOnTheServersClockAndMayRunPastMidnight(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		window Window
		now    time.Time
		want   bool
	}{
		{"inside", Window{"03:00", "05:00"}, at24(4, 0), true},
		{"the start is inside", Window{"03:00", "05:00"}, at24(3, 0), true},
		{"the end is outside", Window{"03:00", "05:00"}, at24(5, 0), false},
		{"a minute before the end", Window{"03:00", "05:00"}, at24(4, 59), true},
		{"before", Window{"03:00", "05:00"}, at24(2, 59), false},
		{"after", Window{"03:00", "05:00"}, at24(12, 0), false},
		{"past midnight, evening side", Window{"22:00", "02:00"}, at24(23, 30), true},
		{"past midnight, morning side", Window{"22:00", "02:00"}, at24(1, 59), true},
		{"past midnight, outside", Window{"22:00", "02:00"}, at24(2, 0), false},
		{"past midnight, midday", Window{"22:00", "02:00"}, at24(12, 0), false},
		{"empty window is never open", Window{"03:00", "03:00"}, at24(3, 0), false},
		{"unparseable is never open", Window{"3am", "5am"}, at24(4, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.window.contains(tc.now); got != tc.want {
				t.Fatalf("%+v contains %s = %v", tc.window, tc.now.Format("15:04"), got)
			}
		})
	}
	// The same instant is a different time of day in a different zone, and the
	// window follows the server's.
	berlin := time.FixedZone("CEST", 2*3600)
	instant := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	if (Window{"03:00", "05:00"}).contains(instant.In(berlin)) != true {
		t.Fatal("01:30 UTC is 03:30 in UTC+2")
	}
	if (Window{"03:00", "05:00"}).contains(instant.In(time.UTC)) {
		t.Fatal("01:30 UTC is not inside 03:00-05:00 in UTC")
	}
}

func TestOnlyTheThirdComponentDiffersForAPatchUpdate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"0.8.2", "0.8.3", true},
		{"0.8.2", "0.8.10", true},
		{"0.8", "0.8.1", true},
		{"0.8.1", "0.8.2", true},
		{"v0.8.2", "0.8.3", true},
		{"0.8.2", "0.9", false},
		{"0.8.2", "0.9.0", false},
		{"0.8.2", "1.0", false},
		{"0.8.9", "0.9.0", false},
		{"0.8.2", "0.8.2", false},
		{"0.8.3", "0.8.2", false},
		{"0.8", "0.8.0", false},
		{"0.8.2", "garbage", false},
		{"garbage", "0.8.3", false},
		{"", "0.8.3", false},
	} {
		if got := isPatchUpdate(tc.current, tc.latest); got != tc.want {
			t.Errorf("isPatchUpdate(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestAPolicyIsRefusedWhenItsPartsAreNotWhatTheProtocolAllows(t *testing.T) {
	t.Parallel()
	ok := Window{"03:00", "05:00"}
	for name, p := range map[string]UpdatePolicy{
		"off":        {AutoOff, ok},
		"off, empty": {AutoOff, Window{"03:00", "03:00"}},
		"patch":      {AutoPatch, ok},
		"all":        {AutoAll, Window{"23:00", "01:00"}},
	} {
		if err := p.validate(); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	for name, p := range map[string]UpdatePolicy{
		"unknown mode":    {"sometimes", ok},
		"empty mode":      {"", ok},
		"hour 24":         {AutoAll, Window{"24:00", "05:00"}},
		"minute 60":       {AutoAll, Window{"03:60", "05:00"}},
		"no leading zero": {AutoAll, Window{"3:00", "05:00"}},
		"seconds":         {AutoAll, Window{"03:00:00", "05:00"}},
		"empty window":    {AutoAll, Window{"03:00", "03:00"}},
	} {
		if err := p.validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestWhenAnUpdateIsDue(t *testing.T) {
	t.Parallel()
	window := Window{"03:00", "05:00"}
	avail := UpdateStatus{Version: "0.8.2", Latest: "0.8.3", Available: true, Supported: true}
	with := func(f func(*UpdateStatus)) UpdateStatus { s := avail; f(&s); return s }
	for _, tc := range []struct {
		name   string
		policy UpdatePolicy
		status UpdateStatus
		now    time.Time
		want   string
	}{
		{"patch inside the window", UpdatePolicy{AutoPatch, window}, avail, at24(4, 0), "0.8.3"},
		{"all inside the window", UpdatePolicy{AutoAll, window}, avail, at24(4, 0), "0.8.3"},
		{"off", UpdatePolicy{AutoOff, window}, avail, at24(4, 0), ""},
		{"outside the window", UpdatePolicy{AutoAll, window}, avail, at24(12, 0), ""},
		{"patch does not take a minor release", UpdatePolicy{AutoPatch, window}, with(func(s *UpdateStatus) { s.Latest = "0.9" }), at24(4, 0), ""},
		{"all takes a minor release", UpdatePolicy{AutoAll, window}, with(func(s *UpdateStatus) { s.Latest = "0.9" }), at24(4, 0), "0.9"},
		{"nothing newer", UpdatePolicy{AutoAll, window}, with(func(s *UpdateStatus) { s.Available = false }), at24(4, 0), ""},
		{"an install that cannot update itself", UpdatePolicy{AutoAll, window}, with(func(s *UpdateStatus) { s.Supported = false }), at24(4, 0), ""},
		{"an update already running", UpdatePolicy{AutoAll, window}, with(func(s *UpdateStatus) { s.Busy = true }), at24(4, 0), ""},
		{"a release marked breaking is never unattended", UpdatePolicy{AutoAll, window}, with(func(s *UpdateStatus) { s.Breaking = true }), at24(4, 0), ""},
		{"no newest version known", UpdatePolicy{AutoAll, window}, with(func(s *UpdateStatus) { s.Latest = "" }), at24(4, 0), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, due := updateDue(tc.policy, tc.status, tc.now)
			if due != (tc.want != "") || got != tc.want {
				t.Fatalf("updateDue = %q, %v; want %q", got, due, tc.want)
			}
		})
	}
}

func TestThePolicyInstallsOnceAVersionAndOnlyWithinMaintain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeMaintain)
	e.host.mu.Lock()
	e.host.update = UpdateStatus{Version: "0.8.2", Latest: "0.8.3", Available: true, Supported: true}
	e.host.mu.Unlock()
	if _, err := e.svc.setPolicy(t.Context(), UpdatePolicy{AutoPatch, Window{"03:00", "05:00"}}); err != nil {
		t.Fatal(err)
	}

	// Outside the window nothing happens.
	e.advance(2 * time.Hour) // 02:00
	e.svc.evaluatePolicy(t.Context(), st)
	if len(e.host.called()) != 0 {
		t.Fatalf("installed outside the window: %+v", e.host.called())
	}

	// Inside it, once.
	e.advance(90 * time.Minute) // 03:30
	e.svc.evaluatePolicy(t.Context(), st)
	calls := e.host.called()
	if len(calls) != 1 || calls[0].Method != "InstallUpdate" || calls[0].Args[0] != "0.8.3" ||
		calls[0].Actor != "account:ion@example.com (update policy)" {
		t.Fatalf("calls = %+v", calls)
	}
	e.advance(time.Hour) // 04:30, the install failed or is still going: not asked again
	e.svc.evaluatePolicy(t.Context(), st)
	if len(e.host.called()) != 1 {
		t.Fatal("the same version was started twice")
	}
	if rows := e.auditRows("account.update.auto"); len(rows) != 1 || rows[0].Target != "0.8.3" || !rows[0].Success {
		t.Fatalf("audit = %+v", rows)
	}

	// A newer version is a new chance, and so is a fresh policy.
	e.host.mu.Lock()
	e.host.update.Latest = "0.8.4"
	e.host.mu.Unlock()
	e.svc.evaluatePolicy(t.Context(), st)
	if len(e.host.called()) != 2 {
		t.Fatalf("a newer version was not tried: %+v", e.host.called())
	}
	e.svc.setPolicy(t.Context(), UpdatePolicy{AutoPatch, Window{"03:00", "05:00"}})
	e.svc.evaluatePolicy(t.Context(), st)
	if len(e.host.called()) != 3 {
		t.Fatalf("a new policy did not reset the attempt: %+v", e.host.called())
	}

	// Narrowing the scope below maintain switches the policy off, because it
	// is the site's authority to update that the scope grants.
	st.Scope = ScopeOperate
	e.svc.setPolicy(t.Context(), UpdatePolicy{AutoPatch, Window{"03:00", "05:00"}})
	e.svc.evaluatePolicy(t.Context(), st)
	if len(e.host.called()) != 3 {
		t.Fatalf("the policy ran at operate: %+v", e.host.called())
	}
}

func TestAFailedAutomaticInstallIsAuditedAsFailed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeMaintain)
	e.host.mu.Lock()
	e.host.update = UpdateStatus{Version: "0.8.2", Latest: "0.8.3", Available: true, Supported: true}
	e.host.mu.Unlock()
	e.host.fail["InstallUpdate"] = errBoom
	e.svc.setPolicy(t.Context(), UpdatePolicy{AutoAll, Window{"00:00", "23:59"}})
	e.svc.evaluatePolicy(t.Context(), st)
	if rows := e.auditRows("account.update.auto"); len(rows) != 1 || rows[0].Success {
		t.Fatalf("audit = %+v", rows)
	}
}

var errBoom = errors.New("boom")
