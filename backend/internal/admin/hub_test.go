package admin

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink/hubstub"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// hubFixture is a data directory with a store in it, a hub at the test origin,
// and the environment the Compose stack hands `docker compose run`.
type hubFixture struct {
	t   *testing.T
	dir string
	hub *hubstub.Hub
}

func newHubFixture(t *testing.T) *hubFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	f := &hubFixture{t: t, dir: dir, hub: hubstub.New(t)}
	t.Setenv("JD_MASTER_KEY", strings.Repeat("ab", 32))
	t.Setenv("JD_HUB_LINK", "")
	t.Setenv("JD_HUB_LINK_TEST_ORIGIN", f.hub.URL)
	return f
}

func (f *hubFixture) runIn(stdin string, args ...string) (string, error) {
	f.t.Helper()
	if err := validateArgs(args); err != nil {
		return "", err
	}
	var out bytes.Buffer
	err := run(f.t.Context(), f.dir, args, strings.NewReader(stdin), &out, io.Discard)
	return out.String(), err
}

func (f *hubFixture) run(args ...string) (string, error) { return f.runIn("", args...) }

func (f *hubFixture) audit(action string) []string {
	return (&linkFixture{t: f.t, dir: f.dir}).audit(action)
}

func TestHubConnectPrintsTheCodeWaitsAndReportsTheLink(t *testing.T) {
	f := newHubFixture(t)
	go func() {
		f.hub.WaitForRequest()
		f.hub.Approve()
	}()
	out, err := f.run("hub-connect", f.hub.URL, "--wait", "30s")
	if err != nil {
		t.Fatalf("hub-connect = %v\n%s", err, out)
	}
	for _, want := range []string{"Connecting to Just Hub at   " + f.hub.URL, "K7QM-29XD", "fingerprint", "maintain scope", `Connected to Just Hub "hub" at ` + f.hub.URL + ` as "`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "(maintain).\n") {
		t.Errorf("output = %q", out)
	}
	if rows := f.audit("hub.link"); len(rows) != 1 || !strings.HasPrefix(rows[0], "root/cli/CLI/1/hub") {
		t.Fatalf("audit = %v", rows)
	}
	status, err := f.run("hub-status")
	if err != nil || !strings.HasPrefix(status, `Connected to Just Hub "hub" at `+f.hub.URL) || !strings.Contains(status, "(maintain); no heartbeat yet.") {
		t.Fatalf("hub-status = %q, %v", status, err)
	}

	// One hub per server.
	if _, err := f.run("hub-connect", f.hub.URL); exitCode(err) != 1 || !strings.Contains(err.Error(), "already connected") {
		t.Fatalf("second connect = %v", err)
	}

	out, err = f.run("hub-disconnect")
	if err != nil || !strings.Contains(out, `Disconnected from Just Hub "hub"`) || strings.Contains(out, "could not be told") {
		t.Fatalf("hub-disconnect = %q, %v", out, err)
	}
	if _, _, _, unlinks := f.hub.Count(); unlinks != 1 {
		t.Fatal("the hub was not told")
	}
	if rows := f.audit("hub.unlink"); len(rows) != 1 || !strings.HasPrefix(rows[0], "root/cli/CLI/1/") {
		t.Fatalf("unlink audit = %v", rows)
	}
	if _, err := f.run("hub-disconnect"); exitCode(err) != 1 {
		t.Fatalf("disconnect when not connected = %v", err)
	}
	if status, _ := f.run("hub-status"); status != "Not connected to a hub.\n" {
		t.Fatalf("hub-status = %q", status)
	}
}

func TestATokenOnStandardInputApprovesWithoutAPerson(t *testing.T) {
	f := newHubFixture(t)
	token := f.hub.Token()
	out, err := f.runIn(token+"\n", "hub-connect", f.hub.URL, "--token-stdin", "--scope", "observe", "--wait", "10s")
	if err != nil || !strings.Contains(out, "token approves it") || !strings.HasSuffix(out, "(observe).\n") {
		t.Fatalf("hub-connect = %q, %v", out, err)
	}
	if f.hub.LastRequest()["token"] != token {
		t.Fatal("the token did not reach the hub")
	}
	// A spent token is refused by the hub; it is a failure, not a fallback
	// to a code.
	if _, err := f.run("hub-disconnect"); err != nil {
		t.Fatal(err)
	}
	out, err = f.run("hub-connect", f.hub.URL, "--token", token, "--wait", "5s")
	if exitCode(err) != 1 || !strings.Contains(err.Error(), "bad_token") {
		t.Fatalf("spent token = %q, %v", out, err)
	}
	if status, _ := f.run("hub-status"); status != "Not connected to a hub.\n" {
		t.Fatalf("a refused token left %q", status)
	}
	if _, err := f.runIn("", "hub-connect", f.hub.URL, "--token-stdin"); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("empty stdin = %v", err)
	}
	if _, err := f.run("hub-connect", f.hub.URL, "--token", "not-a-token"); exitCode(err) != 1 {
		t.Fatalf("malformed token = %v", err)
	}
}

func TestADeclinedOrUnapprovedHubRequestExitsTwoAndLeavesNothing(t *testing.T) {
	f := newHubFixture(t)
	go func() {
		f.hub.WaitForRequest()
		f.hub.Deny()
	}()
	out, err := f.run("hub-connect", f.hub.URL, "--wait", "30s")
	if exitCode(err) != 2 || !strings.Contains(out, "declined in the hub") {
		t.Fatalf("declined = %q, %v", out, err)
	}
	out, err = f.run("hub-connect", f.hub.URL, "--wait", "1s")
	if exitCode(err) != 2 || !strings.Contains(out, "not approved in time") {
		t.Fatalf("unanswered = %q, %v", out, err)
	}
	if status, _ := f.run("hub-status"); status != "Not connected to a hub.\n" {
		t.Fatalf("hub-status = %q", status)
	}
	if rows := f.audit("hub.link"); len(rows) != 2 || strings.Contains(strings.Join(rows, " "), "/1/") {
		t.Fatalf("failed attempts audited = %v", rows)
	}
}

func TestAnInterruptedHubConnectForgetsItsRequest(t *testing.T) {
	f := newHubFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, f.dir, []string{"hub-connect", f.hub.URL, "--wait", "30s"}, strings.NewReader(""), io.Discard, io.Discard)
	}()
	f.hub.WaitForRequest()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if status, _ := f.run("hub-status"); strings.HasPrefix(status, "Connection requested") {
			break
		}
	}
	cancel()
	if err := <-done; exitCode(err) != 2 {
		t.Fatalf("interrupted = %v", err)
	}
	if status, _ := f.run("hub-status"); status != "Not connected to a hub.\n" {
		t.Fatalf("an interrupted connect left %q", status)
	}
}

func TestHubConnectIsRefusedWhenSwitchedOffOrTheAddressIsRefused(t *testing.T) {
	f := newHubFixture(t)
	t.Setenv("JD_HUB_LINK", "false")
	if _, err := f.run("hub-connect", f.hub.URL); exitCode(err) != 1 || !strings.Contains(err.Error(), "JD_HUB_LINK=false") {
		t.Fatalf("switched off = %v", err)
	}
	if status, _ := f.run("hub-status"); !strings.Contains(status, "switched off by JD_HUB_LINK=false") {
		t.Fatalf("hub-status = %q", status)
	}
	t.Setenv("JD_HUB_LINK", "true")
	if _, err := f.run("hub-connect", "http://hub.example.com"); exitCode(err) != 1 || !strings.Contains(err.Error(), "https://") {
		t.Fatalf("refused address = %v", err)
	}
	if r, p, b, u := f.hub.Count(); r+p+b+u != 0 {
		t.Fatal("a refused connect reached the hub")
	}
	t.Setenv("JD_MASTER_KEY", "")
	if _, err := f.run("hub-connect", f.hub.URL); err == nil || !strings.Contains(err.Error(), "JD_MASTER_KEY") {
		t.Fatalf("no master key = %v", err)
	}
	if _, err := f.run("hub-status"); err != nil {
		t.Fatalf("status needs no master key: %v", err)
	}
	t.Setenv("JD_HUB_LINK_TEST_ORIGIN", "http://10.0.0.1:43180")
	if _, err := f.run("hub-status"); err == nil || !strings.Contains(err.Error(), "JD_HUB_LINK_TEST_ORIGIN") {
		t.Fatalf("a bad test origin = %v", err)
	}
}

func TestARequestBegunInSettingsIsNamedByHubConnect(t *testing.T) {
	f := newHubFixture(t)
	st, err := store.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	sealer, _ := auth.NewSealer(strings.Repeat("ab", 32))
	svc := hublink.New(hublink.Options{Store: st, Sealer: sealer, Enabled: true, TestOrigin: f.hub.URL})
	if _, err := svc.Connect(t.Context(), hublink.ConnectRequest{URL: f.hub.URL, Scope: accountlink.ScopeOperate, By: "alice"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if _, err := f.run("hub-connect", f.hub.URL); err == nil || !strings.Contains(err.Error(), "K7QM-29XD") || !strings.Contains(err.Error(), "already waiting") {
		t.Fatalf("hub-connect = %v", err)
	}
	if status, _ := f.run("hub-status"); !strings.HasPrefix(status, "Connection requested (operate) to "+f.hub.URL+": code K7QM-29XD") {
		t.Fatalf("hub-status = %q", status)
	}
}

func TestHubArgumentsAreValidatedBeforeAnythingIsOpened(t *testing.T) {
	for _, args := range [][]string{
		{"hub-connect"}, {"hub-connect", "--scope", "observe"}, {"hub-connect", "hub.x.ts.net", "--scope", "root"},
		{"hub-connect", "hub.x.ts.net", "--wait", "2h"}, {"hub-connect", "hub.x.ts.net", "--token"},
		{"hub-connect", "hub.x.ts.net", "--token-stdin", "--token", "jht_x"}, {"hub-connect", "hub.x.ts.net", "--token-stdin=x"},
		{"hub-connect", "hub.x.ts.net", "--force"}, {"hub-connect", "hub.x.ts.net", "--scope", "observe", "--scope", "operate"},
		{"hub-disconnect", "now"}, {"hub-status", "-v"},
	} {
		if err := validateArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	for _, args := range [][]string{
		{"hub-connect", "hub.x.ts.net"}, {"hub-connect", "https://hub.x.ts.net", "--scope=operate", "--wait=10m"},
		{"hub-connect", "hub.x.ts.net", "--token-stdin"}, {"hub-connect", "hub.x.ts.net", "--token", "jht_x", "--scope", "observe"},
		{"hub-disconnect"}, {"hub-status"},
	} {
		if err := validateArgs(args); err != nil {
			t.Errorf("%v refused: %v", args, err)
		}
	}
	o, err := parseHubConnectArgs([]string{"hub.x.ts.net"})
	if err != nil || o.scope != accountlink.ScopeMaintain || o.wait != 15*time.Minute || o.url != "hub.x.ts.net" {
		t.Fatalf("defaults = %+v, %v", o, err)
	}
	if !LongRunning([]string{"hub-connect", "hub.x.ts.net"}) || LongRunning([]string{"hub-status"}) || LongRunning([]string{"hub-disconnect"}) {
		t.Fatal("LongRunning picks the wrong hub commands")
	}
}
