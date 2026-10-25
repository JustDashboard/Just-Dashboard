package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink/linkstub"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// linkFixture is a data directory with a store in it, a site to link to, and
// the environment the Compose stack hands `docker compose run`.
type linkFixture struct {
	t    *testing.T
	dir  string
	site *linkstub.Site
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	f := &linkFixture{t: t, dir: dir, site: linkstub.New(t)}
	t.Setenv("JD_MASTER_KEY", strings.Repeat("ab", 32))
	t.Setenv("JD_ACCOUNT_URL", f.site.URL)
	t.Setenv("JD_ACCOUNT_LINK", "")
	return f
}

func (f *linkFixture) run(args ...string) (string, string, error) {
	f.t.Helper()
	if err := validateArgs(args); err != nil {
		return "", "", err
	}
	var out, errOut bytes.Buffer
	err := run(f.t.Context(), f.dir, args, strings.NewReader(""), &out, &errOut)
	return out.String(), errOut.String(), err
}

func (f *linkFixture) audit(action string) (rows []string) {
	f.t.Helper()
	st, err := store.Open(f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	defer st.Close()
	q, err := st.DB.Query(`SELECT username || '/' || actor || '/' || method || '/' || success || '/' || target FROM audit_log WHERE action = ?`, action)
	if err != nil {
		f.t.Fatal(err)
	}
	defer q.Close()
	for q.Next() {
		var row string
		if err := q.Scan(&row); err != nil {
			f.t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func exitCode(err error) int {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	if err != nil {
		return 1
	}
	return 0
}

func TestLinkPrintsTheCodeWaitsForApprovalAndReportsTheLink(t *testing.T) {
	f := newLinkFixture(t)
	go func() {
		f.site.WaitForRequest()
		f.site.Approve("ion@example.com")
	}()
	out, _, err := f.run("link", "--scope", "operate", "--wait", "30s")
	if err != nil {
		t.Fatalf("link = %v\n%s", err, out)
	}
	for _, want := range []string{
		"To link this server, open   " + f.site.URL + "/link\n",
		"and enter the code          ABCD-2345\n",
		f.site.URL + "/link?code=ABCD-2345",
		"operate scope", "good for 15 minutes", "Waiting for your approval",
		`Linked to ion@example.com as "`,
		`(operate).`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if rows := f.audit("account.link"); len(rows) != 1 || !strings.HasPrefix(rows[0], "root/cli/CLI/1/ion@example.com") {
		t.Fatalf("audit = %v", rows)
	}

	status, _, err := f.run("link-status")
	if err != nil || !strings.HasPrefix(status, "Linked to ion@example.com as ") || !strings.Contains(status, "(operate); no heartbeat yet.") {
		t.Fatalf("link-status = %q, %v", status, err)
	}
	if _, _, err := f.run("link"); err == nil || !strings.Contains(err.Error(), "already linked to ion@example.com") {
		t.Fatalf("second link = %v", err)
	}

	out, _, err = f.run("unlink")
	if err != nil || !strings.Contains(out, "Unlinked from ion@example.com.") || strings.Contains(out, "could not be told") {
		t.Fatalf("unlink = %q, %v", out, err)
	}
	if _, _, _, unlinks, _ := f.site.Count(); unlinks != 1 {
		t.Fatalf("site was told %d times", unlinks)
	}
	if _, _, err := f.run("unlink"); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("unlink twice = %v", err)
	}
	if rows := f.audit("account.unlink"); len(rows) != 1 || !strings.HasPrefix(rows[0], "root/cli/CLI/1/ion@example.com") {
		t.Fatalf("unlink audit = %v", rows)
	}
	if status, _, _ := f.run("link-status"); !strings.HasPrefix(status, "Not linked.") {
		t.Fatalf("link-status = %q", status)
	}
}

func TestADeclinedOrUnansweredRequestExitsTwoAndLeavesNothingBehind(t *testing.T) {
	f := newLinkFixture(t)
	go func() {
		f.site.WaitForRequest()
		f.site.Deny()
	}()
	out, _, err := f.run("link", "--wait", "30s")
	if exitCode(err) != 2 || !strings.Contains(out, "declined") {
		t.Fatalf("declined: exit %d, %v\n%s", exitCode(err), err, out)
	}
	if status, _, _ := f.run("link-status"); !strings.HasPrefix(status, "Not linked.") || !strings.Contains(status, "denied") {
		t.Fatalf("link-status = %q", status)
	}

	// Nobody answers: the wait ends, and the request is not left pending.
	f2 := newLinkFixture(t)
	out, _, err = f2.run("link", "--wait", "1s")
	if exitCode(err) != 2 || !strings.Contains(out, "not approved in time") {
		t.Fatalf("timeout: exit %d, %v\n%s", exitCode(err), err, out)
	}
	if status, _, _ := f2.run("link-status"); status != "Not linked.\n" {
		t.Fatalf("link-status after a timeout = %q", status)
	}
	if rows := f2.audit("account.link"); len(rows) != 1 || !strings.Contains(rows[0], "/0/") {
		t.Fatalf("a failed attempt must be audited as failed: %v", rows)
	}
}

func TestLinkingIsRefusedWhenSwitchedOffAndNeverReachesTheSite(t *testing.T) {
	f := newLinkFixture(t)
	t.Setenv("JD_ACCOUNT_LINK", "false")
	_, _, err := f.run("link")
	if err == nil || !strings.Contains(err.Error(), "JD_ACCOUNT_LINK=false") || exitCode(err) != 1 {
		t.Fatalf("link = %v", err)
	}
	if _, _, _, _, hits := f.site.Count(); hits != 0 {
		t.Fatalf("a switched-off install called the site %d times", hits)
	}
	if status, _, _ := f.run("link-status"); !strings.Contains(status, "switched off by JD_ACCOUNT_LINK=false") {
		t.Fatalf("link-status = %q", status)
	}
}

func TestLinkingNeedsTheMasterKeyAndSaysSo(t *testing.T) {
	f := newLinkFixture(t)
	t.Setenv("JD_MASTER_KEY", "")
	for _, command := range []string{"link", "unlink"} {
		if _, _, err := f.run(command); err == nil || !strings.Contains(err.Error(), "JD_MASTER_KEY") {
			t.Fatalf("%s without a key = %v", command, err)
		}
	}
	// Reading the state needs no key.
	if status, _, err := f.run("link-status"); err != nil || !strings.HasPrefix(status, "Not linked.") {
		t.Fatalf("link-status = %q, %v", status, err)
	}
	t.Setenv("JD_MASTER_KEY", "not-hex")
	if _, _, err := f.run("link"); err == nil || !strings.Contains(err.Error(), "JD_MASTER_KEY") {
		t.Fatalf("link with a bad key = %v", err)
	}
}

func TestASiteThatCannotBeReachedIsAnErrorNotAHang(t *testing.T) {
	f := newLinkFixture(t)
	t.Setenv("JD_ACCOUNT_URL", "http://127.0.0.1:1")
	start := time.Now()
	_, _, err := f.run("link")
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Fatalf("link = %v", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("an unreachable site took too long to report")
	}
}

func TestAnInterruptedLinkCleansUpItsRequest(t *testing.T) {
	f := newLinkFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, f.dir, []string{"link", "--wait", "30s"}, strings.NewReader(""), io.Discard, io.Discard)
	}()
	f.site.WaitForRequest()
	// Interrupted once the request is kept here, not while it is being made.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if status, _, _ := f.run("link-status"); strings.HasPrefix(status, "Link requested") {
			break
		}
	}
	cancel()
	if err := <-done; exitCode(err) != 2 {
		t.Fatalf("interrupted link = %v", err)
	}
	// Ctrl-C cleans up after itself: nothing is pending and linking again works.
	if status, _, _ := f.run("link-status"); status != "Not linked.\n" {
		t.Fatalf("an interrupted link left %q", status)
	}
}

func TestARequestBegunInSettingsIsNamedWhenTheCommandIsRun(t *testing.T) {
	f := newLinkFixture(t)
	st, err := store.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	sealer, _ := auth.NewSealer(strings.Repeat("ab", 32))
	svc := accountlink.New(accountlink.Options{Store: st, Sealer: sealer, SiteURL: f.site.URL, Enabled: true})
	if _, err := svc.BeginLink(t.Context(), accountlink.ScopeObserve, "alice"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	_, _, err = f.run("link")
	if err == nil || !strings.Contains(err.Error(), "ABCD-2345") || !strings.Contains(err.Error(), "already waiting") {
		t.Fatalf("link = %v", err)
	}
	if status, _, _ := f.run("link-status"); !strings.HasPrefix(status, "Link requested (observe): code ABCD-2345 on ") {
		t.Fatalf("link-status = %q", status)
	}
}

func TestLinkArgumentsAreValidatedBeforeAnythingIsOpened(t *testing.T) {
	for _, args := range [][]string{
		{"link", "--scope"}, {"link", "--scope", "root"}, {"link", "--scope", "OBSERVE"}, {"link", "--wait", "soon"},
		{"link", "--wait", "0s"}, {"link", "--wait", "2h"}, {"link", "--scope", "observe", "--scope", "operate"},
		{"link", "--force"}, {"link", "observe"}, {"unlink", "now"}, {"link-status", "-v"},
	} {
		if err := validateArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	for _, args := range [][]string{
		{"link"}, {"link", "--scope", "maintain"}, {"link", "--scope=operate", "--wait=10m"}, {"link", "--wait", "90s", "--scope", "observe"},
		{"unlink"}, {"link-status"},
	} {
		if err := validateArgs(args); err != nil {
			t.Errorf("%v refused: %v", args, err)
		}
	}
	o, err := parseLinkFlags(nil)
	if err != nil || o.scope != "observe" || o.wait != 15*time.Minute {
		t.Fatalf("defaults = %+v, %v", o, err)
	}
	dir := t.TempDir() + "/missing"
	if err := Run(t.Context(), dir, []string{"link", "--scope", "root"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("bad arguments reached the store")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("a store was created")
	}
}

func TestOnlyTheLinkCommandWaitsOnAPerson(t *testing.T) {
	if !LongRunning([]string{"link"}) || LongRunning([]string{"unlink"}) || LongRunning([]string{"users"}) || LongRunning(nil) {
		t.Fatal("LongRunning picks the wrong commands")
	}
}
