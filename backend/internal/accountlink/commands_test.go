package accountlink

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// vectorState is a link made with the protocol document's server id and site
// key, so the document's own signed commands can be run through verify.
func vectorState(t *testing.T, scope Scope) State {
	t.Helper()
	return State{
		URL: "https://just-dashboard.com", ServerID: vectorServerID, Scope: scope, Owner: "ion@example.com",
		SiteKeys: []SiteKey{{Kid: vectorKid, PublicKey: pubB64(seedKey(t, vectorSiteSeed))}},
	}
}

func TestTheDocumentsSignedCommandsVerifyAndTheirArgumentsParse(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	now := time.Unix(vectorNow, 0)
	policy := Command{
		ID: "01JB0000000000000000000000", Kind: KindUpdatePolicy, ExpiresAt: 1761351000, Kid: vectorKid,
		Args:      json.RawMessage(`{"window":{"start":"03:00","end":"05:00"},"auto":"patch"}`),
		Signature: "f9kQt1qTReHEQuwIp7W9fevwIaGnLvWXNo5YE1FgZih//V7rSvjUA5s7ItA4ARjh1JEB6NAA007un7+iBf58CQ==",
	}
	args, _, err := e.svc.verify(t.Context(), vectorState(t, ScopeMaintain), policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if args.Policy != (UpdatePolicy{Auto: "patch", Window: Window{"03:00", "05:00"}}) {
		t.Fatalf("policy = %+v", args.Policy)
	}

	restart := Command{
		ID: "01JB0000000000000000000001", Kind: KindContainerReboot, ExpiresAt: 1761351000, Kid: vectorKid,
		Args:      json.RawMessage(`{"container":"shop-web"}`),
		Signature: "bc1AyygpESzhigTdYoMZD9c9Rntn5HR2bXgrc3SRhDJNUO58CXrpUltv8SNou11NQbO5wDGRQnY2gXm5D1/1Cw==",
	}
	args, _, err = e.svc.verify(t.Context(), vectorState(t, ScopeOperate), restart, now)
	if err != nil || args.Container != "shop-web" {
		t.Fatalf("restart = %+v, %v", args, err)
	}
	// The same signed command, against another server, does not verify: the
	// server id is part of what the site signed.
	other := vectorState(t, ScopeOperate)
	other.ServerID = "7a0d3c1e-0000-4000-8000-000000000002"
	restart.ID = "01JB0000000000000000000002"
	if _, _, err := e.svc.verify(t.Context(), other, restart, now); err == nil {
		t.Fatal("a command signed for one server verified on another")
	}
}

// commandCase is a command to hand to process, and what must come of it.
type commandCase struct {
	name   string
	build  func(e *testEnv, st State) Command
	status string
	reason string // substring of the refusal or failure
}

func TestCommandsThatShouldNotRunAreRefusedReportedAndAudited(t *testing.T) {
	t.Parallel()
	good := func(n int, kind string, args map[string]any) func(*testEnv, State) Command {
		return func(e *testEnv, st State) Command {
			return e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(n), kind, args, e.clock().Add(10*time.Minute))
		}
	}
	other := seedKey(t, vectorServerSeed) // a key the dashboard never pinned
	for _, tc := range []struct {
		scope Scope
		commandCase
	}{
		{ScopeOperate, commandCase{name: "a key it never pinned", build: func(e *testEnv, st State) Command {
			return e.site.signedCommand(other, "2026-11", st.ServerID, ulid(1), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
		}, status: resultRefused, reason: "has not pinned"}},
		{ScopeOperate, commandCase{name: "a key it pinned with the wrong signature", build: func(e *testEnv, st State) Command {
			return e.site.signedCommand(other, vectorKid, st.ServerID, ulid(2), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
		}, status: resultRefused, reason: "does not verify"}},
		{ScopeOperate, commandCase{name: "arguments changed after signing", build: func(e *testEnv, st State) Command {
			c := good(3, KindContainerReboot, map[string]any{"container": "web"})(e, st)
			c.Args = json.RawMessage(`{"container":"database"}`)
			return c
		}, status: resultRefused, reason: "does not verify"}},
		{ScopeOperate, commandCase{name: "kind changed after signing", build: func(e *testEnv, st State) Command {
			c := good(4, KindContainerReboot, map[string]any{"container": "web"})(e, st)
			c.Kind = KindContainerStop
			return c
		}, status: resultRefused, reason: "does not verify"}},
		{ScopeOperate, commandCase{name: "signed for another server", build: func(e *testEnv, st State) Command {
			return e.site.signedCommand(e.site.key, vectorKid, "server-9999-0000", ulid(5), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
		}, status: resultRefused, reason: "does not verify"}},
		{ScopeOperate, commandCase{name: "already expired", build: func(e *testEnv, st State) Command {
			return e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(6), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(-time.Second))
		}, status: resultRefused, reason: "expired"}},
		{ScopeObserve, commandCase{name: "outside the observe scope", build: good(7, KindContainerReboot, map[string]any{"container": "web"}),
			status: resultRefused, reason: "container.restart needs scope operate; this server is linked at observe"}},
		{ScopeObserve, commandCase{name: "a maintain command at observe", build: good(8, KindUpdateInstall, map[string]any{"version": "0.8.3"}),
			status: resultRefused, reason: "dashboard.update.install needs scope maintain; this server is linked at observe"}},
		{ScopeOperate, commandCase{name: "a maintain command at operate", build: good(9, KindRestart, map[string]any{"rebuild": false}),
			status: resultRefused, reason: "dashboard.restart needs scope maintain; this server is linked at operate"}},
		{ScopeMaintain, commandCase{name: "a kind it does not run", build: good(10, "container.exec", map[string]any{"container": "web"}),
			status: resultRefused, reason: "not one this dashboard runs"}},
		{ScopeMaintain, commandCase{name: "an id that is not a ULID", build: func(e *testEnv, st State) Command {
			return e.site.signedCommand(e.site.key, vectorKid, st.ServerID, "not-a-ulid", KindSnapshotNow, map[string]any{}, e.clock().Add(time.Minute))
		}, status: resultRefused, reason: "ULID"}},
		{ScopeMaintain, commandCase{name: "an extra argument", build: good(11, KindContainerReboot, map[string]any{"container": "web", "force": true}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a missing argument", build: good(12, KindContainerReboot, map[string]any{}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a container name with a path in it", build: good(13, KindContainerStop, map[string]any{"container": "../etc"}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a container name that is an option", build: good(14, KindContainerStop, map[string]any{"container": "-f"}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a stack name that is too long", build: good(15, KindStackRestart, map[string]any{"name": strings.Repeat("a", 129)}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a job id of zero", build: good(16, KindBackupRun, map[string]any{"jobId": 0}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a job id that is a string", build: good(17, KindBackupRun, map[string]any{"jobId": "3"}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a version with a fourth part", build: good(18, KindUpdateInstall, map[string]any{"version": "0.8.2.1"}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a version with text", build: good(19, KindUpdateInstall, map[string]any{"version": "latest"}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a policy that is not off, patch or all", build: good(20, KindUpdatePolicy,
			map[string]any{"auto": "always", "window": map[string]any{"start": "03:00", "end": "05:00"}}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "a window with a bad clock time", build: good(21, KindUpdatePolicy,
			map[string]any{"auto": "all", "window": map[string]any{"start": "25:00", "end": "05:00"}}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "an empty window for a policy that updates", build: good(22, KindUpdatePolicy,
			map[string]any{"auto": "all", "window": map[string]any{"start": "03:00", "end": "03:00"}}),
			status: resultRefused, reason: "must differ"}},
		{ScopeMaintain, commandCase{name: "a window with an extra key", build: good(23, KindUpdatePolicy,
			map[string]any{"auto": "all", "window": map[string]any{"start": "03:00", "end": "04:00", "tz": "UTC"}}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "rebuild that is not a boolean", build: good(24, KindRestart, map[string]any{"rebuild": "yes"}),
			status: resultRefused, reason: "invalid arguments"}},
		{ScopeMaintain, commandCase{name: "arguments on a command that takes none", build: good(25, KindSnapshotNow, map[string]any{"now": true}),
			status: resultRefused, reason: "invalid arguments"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			st := e.link(tc.scope)
			cmd := tc.build(e, st)
			e.svc.process(t.Context(), st, cmd)

			if calls := e.host.called(); len(calls) != 0 {
				t.Fatalf("a refused command reached the machine: %+v", calls)
			}
			res, ok := e.site.result(cmd.ID)
			if !ok || res.Status != tc.status || !strings.Contains(res.Error, tc.reason) {
				t.Fatalf("site was told %+v (reported %v), want %s containing %q", res, ok, tc.status, tc.reason)
			}
			rows := e.auditRows("account.command.")
			if len(rows) != 1 || rows[0].Success || rows[0].Actor != "account:ion@example.com" || !strings.Contains(rows[0].Detail, "refused") ||
				!strings.Contains(rows[0].Detail, "args") {
				t.Fatalf("audit = %+v", rows)
			}
		})
	}
}

func TestEveryKindRunsThroughItsServiceAndIsAuditedAsTheSiteAccount(t *testing.T) {
	t.Parallel()
	future := func(e *testEnv) time.Time { return e.clock().Add(10 * time.Minute) }
	for _, tc := range []struct {
		kind      string
		args      map[string]any
		wantCall  hostCall
		wantOut   string
		wantAudit string
	}{
		{KindContainerStart, map[string]any{"container": "shop-web"}, hostCall{"Container", "account:ion@example.com", []any{"shop-web", "start"}}, "shop-web start", "shop-web"},
		{KindContainerStop, map[string]any{"container": "shop-web"}, hostCall{"Container", "account:ion@example.com", []any{"shop-web", "stop"}}, "shop-web stop", "shop-web"},
		{KindContainerReboot, map[string]any{"container": "shop-web"}, hostCall{"Container", "account:ion@example.com", []any{"shop-web", "restart"}}, "shop-web restart", "shop-web"},
		{KindStackRestart, map[string]any{"name": "shop"}, hostCall{"RestartStack", "account:ion@example.com", []any{"shop"}}, "restarted shop", "shop"},
		{KindBackupRun, map[string]any{"jobId": 7}, hostCall{"RunBackup", "account:ion@example.com", []any{int64(7)}}, "backup started", "job:7"},
		{KindUpdateInstall, map[string]any{"version": "0.8.3"}, hostCall{"InstallUpdate", "account:ion@example.com", []any{"0.8.3"}}, "installing 0.8.3", "0.8.3"},
		{KindRestart, map[string]any{"rebuild": true}, hostCall{"RestartDashboard", "account:ion@example.com", []any{true}}, "restarting", ""},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			e := newEnv(t)
			st := e.link(ScopeMaintain)
			cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), tc.kind, tc.args, future(e))
			e.svc.process(t.Context(), st, cmd)

			calls := e.host.called()
			if len(calls) != 1 || fmt.Sprint(calls[0]) != fmt.Sprint(tc.wantCall) {
				t.Fatalf("calls = %+v, want %+v", calls, tc.wantCall)
			}
			res, ok := e.site.result(cmd.ID)
			if !ok || res.Status != resultSucceeded || res.Output != tc.wantOut || res.Error != "" ||
				res.StartedAt != vectorNow || res.FinishedAt < res.StartedAt {
				t.Fatalf("result = %+v (reported %v)", res, ok)
			}
			rows := e.auditRows("account.command.")
			if len(rows) != 1 {
				t.Fatalf("audit = %+v", rows)
			}
			row := rows[0]
			if row.Action != "account.command."+tc.kind || row.Actor != "account:ion@example.com" || row.Username != "account:ion@example.com" ||
				!row.Success || row.Target != tc.wantAudit || !strings.Contains(row.Detail, cmd.ID) {
				t.Fatalf("audit row = %+v", row)
			}
		})
	}
}

func TestSnapshotNowSendsAHeartbeatAndThePolicyCommandStoresThePolicy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeMaintain)
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindSnapshotNow, map[string]any{}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd)
	if res, _ := e.site.result(cmd.ID); res.Status != resultSucceeded {
		t.Fatalf("snapshot.now = %+v", res)
	}
	select {
	case <-e.svc.kick:
	default:
		t.Fatal("snapshot.now did not ask for a heartbeat")
	}

	policy := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(2), KindUpdatePolicy,
		map[string]any{"auto": "patch", "window": map[string]any{"start": "03:00", "end": "05:00"}}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, policy)
	got, ok := e.svc.policy(t.Context())
	if !ok || got != (UpdatePolicy{Auto: "patch", Window: Window{"03:00", "05:00"}}) {
		t.Fatalf("stored policy = %+v (%v)", got, ok)
	}
	if calls := e.host.called(); len(calls) != 0 {
		t.Fatalf("policy and snapshot commands touched the machine: %+v", calls)
	}
	status, _ := e.svc.Status(t.Context())
	if status.Link.UpdatePolicy == nil || status.Link.UpdatePolicy.Auto != "patch" || status.Link.LastCommand == nil ||
		status.Link.LastCommand.Kind != KindUpdatePolicy || status.Link.LastCommand.Status != resultSucceeded ||
		status.Link.LastCommand.IssuedBy != "ion@example.com" {
		t.Fatalf("status = %+v", status.Link)
	}
}

func TestACommandIsRunOnceWhateverTheSiteSendsAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(10*time.Minute))
	e.svc.process(t.Context(), st, cmd)
	e.svc.process(t.Context(), st, cmd)

	if calls := e.host.called(); len(calls) != 1 {
		t.Fatalf("a replayed command ran %d times", len(calls))
	}
	rows := e.auditRows("account.command.")
	if len(rows) != 2 {
		t.Fatalf("audit = %+v", rows)
	}
	var refused int
	for _, r := range rows {
		if !r.Success && strings.Contains(r.Detail, "already received") {
			refused++
		}
	}
	if refused != 1 {
		t.Fatalf("the replay was not refused as one: %+v", rows)
	}
	// The replay is audited and refused, and says nothing to the site about
	// that id: what the site holds for it is the first delivery's result.
	if res, _ := e.site.result(cmd.ID); res.Status != resultSucceeded {
		t.Fatalf("the replay overwrote the site's result: %+v", res)
	}
	if posts := e.site.resultPosts(cmd.ID); posts != 1 {
		t.Fatalf("the site was told about the id %d times", posts)
	}
	var status string
	if err := e.st.DB.QueryRow(`SELECT status FROM account_link_commands WHERE id = ?`, cmd.ID).Scan(&status); err != nil || status != resultSucceeded {
		t.Fatalf("the replay rewrote the command's record: %q (%v)", status, err)
	}
	var n int
	if err := e.st.DB.QueryRow(`SELECT count(*) FROM account_link_commands WHERE id = ?`, cmd.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("replay table has %d rows (%v)", n, err)
	}
}

func TestAForgedCommandCannotBurnTheIdOfARealOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	real := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(10*time.Minute))
	forged := real
	forged.Signature = real.Signature[:len(real.Signature)-4] + "AAAA"
	e.svc.process(t.Context(), st, forged)
	if calls := e.host.called(); len(calls) != 0 {
		t.Fatal("a forged command ran")
	}
	e.svc.process(t.Context(), st, real)
	if calls := e.host.called(); len(calls) != 1 {
		t.Fatalf("the real command was refused after a forgery with its id: %+v", e.auditRows("account."))
	}
}

func TestAKeyThePolicyRetiredStopsBeingTrustedAtItsEndDate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	st.SiteKeys[0].NotAfter = e.clock().Add(-time.Second).Unix()
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd)
	if res, _ := e.site.result(cmd.ID); res.Status != resultRefused || !strings.Contains(res.Error, "has not pinned") {
		t.Fatalf("result = %+v", res)
	}
}

func TestAFailingServiceIsReportedAsFailedNotRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	e.host.fail["Container"] = errors.New("no such container: web")
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd)
	res, _ := e.site.result(cmd.ID)
	if res.Status != resultFailed || !strings.Contains(res.Error, "no such container") {
		t.Fatalf("result = %+v", res)
	}
	rows := e.auditRows("account.command.")
	if len(rows) != 1 || rows[0].Success || rows[0].Status != 500 {
		t.Fatalf("audit = %+v", rows)
	}
	if status, _ := e.svc.Status(t.Context()); status.Link.LastCommand.Status != resultFailed {
		t.Fatalf("lastCommand = %+v", status.Link.LastCommand)
	}
}

func TestAnUpdateForAVersionThatIsNoLongerNewestIsRefusedAsVersionMoved(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeMaintain)
	e.host.fail["InstallUpdate"] = fmt.Errorf("%w: the newest version is now 0.8.4, not 0.8.3", ErrVersionMoved)
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindUpdateInstall, map[string]any{"version": "0.8.3"}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd)
	res, _ := e.site.result(cmd.ID)
	if res.Status != resultRefused || !strings.Contains(res.Error, "version_moved") {
		t.Fatalf("result = %+v", res)
	}
}

func TestLongOutputAndErrorsAreBoundedBeforeTheyLeave(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	e.host.output = strings.Repeat("x", 100<<10)
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindContainerStart, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd)
	if res, _ := e.site.result(cmd.ID); len(res.Output) > 8<<10 {
		t.Fatalf("output of %d bytes sent", len(res.Output))
	}
	e.host.output = ""
	e.host.fail["Container"] = errors.New(strings.Repeat("e", 10<<10))
	cmd2 := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(2), KindContainerStart, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd2)
	if res, _ := e.site.result(cmd2.ID); len(res.Error) > 1000 {
		t.Fatalf("error of %d bytes sent", len(res.Error))
	}
}

func TestACommandArrivingAfterUnlinkIsNotRunBecauseTheLoopReReadsTheState(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	if _, err := e.svc.Unlink(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.svc.currentLink(t.Context(), st); ok {
		t.Fatal("an unlinked server still counts as linked")
	}
}

func TestAnEarlierLinksCommandIsNotTheNewLinksLastCommand(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeObserve)
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindSnapshotNow, map[string]any{}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd)
	if status, _ := e.svc.Status(t.Context()); status.Link.LastCommand == nil {
		t.Fatal("the command did not show as the last one")
	}
	if _, err := e.svc.Unlink(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute)
	e.link(ScopeObserve)
	if status, _ := e.svc.Status(t.Context()); status.Link.LastCommand != nil {
		t.Fatalf("lastCommand after relinking = %+v", status.Link.LastCommand)
	}
}

// A second delivery of a real command's id, however it fails, is not the real
// command: it must not rewrite the real command's record, and must not report
// anything to the site under an id it never owned.
func TestAnythingReusingARealCommandsIdLeavesItsRecordAndItsResultAlone(t *testing.T) {
	t.Parallel()
	other := seedKey(t, vectorServerSeed)
	for _, tc := range []struct {
		name   string
		reuse  func(e *testEnv, st State, real Command) Command
		reason string
	}{
		{"a forgery", func(e *testEnv, st State, real Command) Command {
			f := real
			f.Signature = real.Signature[:len(real.Signature)-4] + "AAAA"
			return f
		}, "signature"},
		{"another key's signature", func(e *testEnv, st State, real Command) Command {
			return e.site.signedCommand(other, vectorKid, st.ServerID, real.ID, real.Kind, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
		}, "does not verify"},
		{"a replay of the same bytes", func(e *testEnv, st State, real Command) Command { return real }, "already received"},
		{"the same id signed again with other arguments", func(e *testEnv, st State, real Command) Command {
			return e.site.signedCommand(e.site.key, vectorKid, st.ServerID, real.ID, real.Kind, map[string]any{"container": "database"}, e.clock().Add(5*time.Minute))
		}, "already received"},
		{"an expired copy", func(e *testEnv, st State, real Command) Command {
			return e.site.signedCommand(e.site.key, vectorKid, st.ServerID, real.ID, real.Kind, map[string]any{"container": "web"}, e.clock().Add(-time.Second))
		}, "expired"},
		{"a copy asking for more than the scope", func(e *testEnv, st State, real Command) Command {
			return e.site.signedCommand(e.site.key, vectorKid, st.ServerID, real.ID, KindRestart, map[string]any{"rebuild": false}, e.clock().Add(5*time.Minute))
		}, "already received"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			st := e.link(ScopeOperate)
			real := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindContainerReboot, map[string]any{"container": "web"}, e.clock().Add(10*time.Minute))
			e.svc.process(t.Context(), st, real)
			if res, _ := e.site.result(real.ID); res.Status != resultSucceeded {
				t.Fatalf("the real command did not succeed: %+v", res)
			}

			e.svc.process(t.Context(), st, tc.reuse(e, st, real))

			if calls := e.host.called(); len(calls) != 1 {
				t.Fatalf("the second delivery ran: %+v", calls)
			}
			var status, errText string
			if err := e.st.DB.QueryRow(`SELECT status, error FROM account_link_commands WHERE id = ?`, real.ID).Scan(&status, &errText); err != nil ||
				status != resultSucceeded || errText != "" {
				t.Fatalf("the real command's record became %q %q (%v)", status, errText, err)
			}
			if res, _ := e.site.result(real.ID); res.Status != resultSucceeded || res.Error != "" {
				t.Fatalf("the site's result for the real command became %+v", res)
			}
			// Unless the copy was refused before it was recorded, in which
			// case it is the site's to hear about; here the id is a real one,
			// and the site is told once.
			if posts := e.site.resultPosts(real.ID); posts != 1 {
				t.Fatalf("the site heard %d results under the real command's id", posts)
			}
			rows := e.auditRows("account.command.")
			if len(rows) != 2 || rows[0].Success && rows[1].Success {
				t.Fatalf("the refused copy must still be audited: %+v", rows)
			}
			found := false
			for _, r := range rows {
				found = found || (!r.Success && strings.Contains(r.Detail, tc.reason))
			}
			if !found {
				t.Fatalf("no audit entry says %q: %+v", tc.reason, rows)
			}
		})
	}
}

func TestACommandThatLivesLongerThanAnyCommandIsRefusedBeforeItIsRecorded(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	far := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindSnapshotNow, map[string]any{}, e.clock().Add(2*time.Hour))
	e.svc.process(t.Context(), st, far)
	res, ok := e.site.result(far.ID)
	if !ok || res.Status != resultRefused || !strings.Contains(res.Error, "more than 1h0m0s") {
		t.Fatalf("a command expiring in two hours: %+v (reported %v)", res, ok)
	}
	// It was never recorded, so the replay table (pruned after a week) holds
	// nothing a long-lived command could outlive.
	var n int
	if err := e.st.DB.QueryRow(`SELECT count(*) FROM account_link_commands WHERE id = ?`, far.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("recorded %d times (%v)", n, err)
	}
	ok59 := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(2), KindSnapshotNow, map[string]any{}, e.clock().Add(59*time.Minute))
	e.svc.process(t.Context(), st, ok59)
	if res, _ := e.site.result(ok59.ID); res.Status != resultSucceeded {
		t.Fatalf("a command expiring in 59 minutes: %+v", res)
	}
}

func TestACommandAHostDeclinesIsRefusedNotFailed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	e.host.fail["Container"] = Refuse("web is one of the dashboard's own containers; restart the dashboard itself with dashboard.restart")
	cmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindContainerStop, map[string]any{"container": "web"}, e.clock().Add(time.Minute))
	e.svc.process(t.Context(), st, cmd)
	res, _ := e.site.result(cmd.ID)
	if res.Status != resultRefused || !strings.Contains(res.Error, "dashboard's own containers") {
		t.Fatalf("result = %+v", res)
	}
	if rows := e.auditRows("account.command."); len(rows) != 1 || rows[0].Success || rows[0].Status != 403 {
		t.Fatalf("audit = %+v", rows)
	}
}

// Only a command still running can be finished: the record of one that has
// ended is what the id means from then on.
func TestAFinishedCommandsRecordCannotBeFinishedAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := ulid(7)
	if _, err := e.st.DB.Exec(`INSERT INTO account_link_commands(id, kind, received_at, status, finished_at) VALUES(?, 'snapshot.now', 1, 'succeeded', 2)`, id); err != nil {
		t.Fatal(err)
	}
	e.svc.finishRecord(t.Context(), id, resultRefused, "overwritten", e.clock())
	var status, errText string
	if err := e.st.DB.QueryRow(`SELECT status, error FROM account_link_commands WHERE id = ?`, id).Scan(&status, &errText); err != nil || status != resultSucceeded || errText != "" {
		t.Fatalf("record = %q %q (%v)", status, errText, err)
	}
}
