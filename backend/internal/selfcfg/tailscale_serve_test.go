package selfcfg

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The shape of a real `tailscale serve status --json` (1.102) with three
// mappings: one the dashboard could have made, one plain-HTTP one, and one the
// operator funnelled to the internet — the state that must never read as free.
const serveStatusFixture = `{
  "TCP": {
    "21003": {"HTTPS": true},
    "21004": {"HTTP": true},
    "8443": {"HTTPS": true},
    "9000": {"TCPForward": "127.0.0.1:9001"}
  },
  "Web": {
    "node.tailnet.ts.net:21003": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:41337"}}},
    "node.tailnet.ts.net:21004": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:3000"}}},
    "node.tailnet.ts.net:8443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:8080"}}}
  },
  "AllowFunnel": {
    "node.tailnet.ts.net:8443": true
  }
}`

// serveFake stands in for the tailscale CLI. Each status read answers with the
// next fixture in status (the last one repeats), every other invocation is
// recorded and answered from fail.
type serveFake struct {
	calls  [][]string
	status []string
	reads  int
	fail   map[string]error
}

func (f *serveFake) run(_ context.Context, args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	if line == "serve status --json" {
		i := min(f.reads, len(f.status)-1)
		f.reads++
		return []byte(f.status[i]), nil
	}
	f.calls = append(f.calls, args)
	if err, ok := f.fail[line]; ok {
		return []byte(err.Error()), err
	}
	return nil, nil
}

func (f *serveFake) lines() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func newServeFake(status ...string) (*serveFake, *TailnetServe) {
	if len(status) == 0 {
		status = []string{"{}"}
	}
	f := &serveFake{status: status, fail: map[string]error{}}
	return f, &TailnetServe{
		run: f.run,
		detect: func(context.Context) Identity {
			return Identity{Available: true, Running: true, Hostname: "node.tailnet.ts.net", HTTPSEnabled: true}
		},
	}
}

func TestParseServeStatusReadsEveryMapping(t *testing.T) {
	got, err := parseServeStatus([]byte(serveStatusFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("served = %+v; want four ports", got)
	}
	if e := got[21003]; e.Target != "http://127.0.0.1:41337" || !e.HTTPS || e.Funnel {
		t.Fatalf("21003 = %+v", e)
	}
	if e := got[21004]; e.Target != "http://127.0.0.1:3000" || e.HTTPS || e.Funnel {
		t.Fatalf("21004 = %+v", e)
	}
	if e := got[8443]; !e.Funnel || !e.HTTPS {
		t.Fatalf("the funnelled port was not read as funnelled: %+v", e)
	}
	// A raw TCP forwarder has no web handler; it still has to read as
	// somebody else's rather than as a free port.
	if e := got[9000]; e.Target != "127.0.0.1:9001" {
		t.Fatalf("9000 = %+v", e)
	}
}

func TestParseServeStatusTreatsNothingServedAsEmpty(t *testing.T) {
	for _, raw := range []string{"{}", "", "  \n", "No serve config\n"} {
		got, err := parseServeStatus([]byte(raw))
		if err != nil || len(got) != 0 {
			t.Fatalf("parseServeStatus(%q) = %v, %v; want empty and no error", raw, got, err)
		}
	}
	// Anything else is not "nothing is served", and a publisher that treated
	// it that way would overwrite whatever it could not read.
	if _, err := parseServeStatus([]byte("{not json")); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	if _, err := parseServeStatus([]byte("some other error")); err == nil {
		t.Fatal("an unexpected sentence was read as an empty configuration")
	}
}

// The whole feature is one flag: --https when the tailnet issues certificates,
// --http when it does not, --bg and --yes always, and the loopback target last.
func TestPublishArgvFollowsTheTailnetsHTTPS(t *testing.T) {
	f, serve := newServeFake("{}", serveStatusFixture)
	url, err := serve.Publish(context.Background(), 21003, 41337, 0)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://node.tailnet.ts.net:21003" {
		t.Fatalf("url = %q", url)
	}
	if got := f.lines(); len(got) != 1 || got[0] != "serve --bg --yes --https=21003 http://127.0.0.1:41337" {
		t.Fatalf("argv = %q", got)
	}

	f, serve = newServeFake("{}", serveStatusFixture)
	serve.detect = func(context.Context) Identity {
		return Identity{Available: true, Running: true, Hostname: "node.tailnet.ts.net"}
	}
	url, err = serve.Publish(context.Background(), 21004, 3000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://node.tailnet.ts.net:21004" {
		t.Fatalf("url = %q; a tailnet without certificates gets a plain address, not a broken padlock", url)
	}
	if got := f.lines(); len(got) != 1 || got[0] != "serve --bg --yes --http=21004 http://127.0.0.1:3000" {
		t.Fatalf("argv = %q", got)
	}
}

func TestPublishRefusesAPortSomebodyElseServes(t *testing.T) {
	f, serve := newServeFake(serveStatusFixture)
	// 21004 goes to :3000; a preview that wants it for :5000 and has never
	// held it is asking for the operator's mapping.
	if _, err := serve.Publish(context.Background(), 21004, 5000, 0); !errors.Is(err, ErrTailnetPortTaken) {
		t.Fatalf("err = %v; want ErrTailnetPortTaken", err)
	}
	// Even naming a previous upstream does not help when the port serves a
	// third target.
	if _, err := serve.Publish(context.Background(), 21004, 5000, 5001); !errors.Is(err, ErrTailnetPortTaken) {
		t.Fatalf("err = %v; want ErrTailnetPortTaken", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a refused publish still ran %q", f.lines())
	}
}

func TestPublishRefusesAFunnelledPort(t *testing.T) {
	fixture := strings.ReplaceAll(serveStatusFixture, `"8443": {"HTTPS": true}`, `"21003": {"HTTPS": true}`)
	fixture = strings.ReplaceAll(fixture, "node.tailnet.ts.net:8443", "node.tailnet.ts.net:21003")
	f, serve := newServeFake(fixture)
	// Same target, same scheme, and still refused: the port is on the public
	// internet, and a preview must never be.
	if _, err := serve.Publish(context.Background(), 21003, 41337, 0); !errors.Is(err, ErrTailnetPortTaken) {
		t.Fatalf("err = %v; want ErrTailnetPortTaken", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a refused publish still ran %q", f.lines())
	}
}

// A rebuilt preview lands on a new loopback port; the tailnet port it held
// before is its own to re-point.
func TestPublishRepointsItsOwnPreviousUpstream(t *testing.T) {
	after := strings.ReplaceAll(serveStatusFixture, "http://127.0.0.1:41337", "http://127.0.0.1:42000")
	f, serve := newServeFake(serveStatusFixture, after)
	url, err := serve.Publish(context.Background(), 21003, 42000, 41337)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://node.tailnet.ts.net:21003" {
		t.Fatalf("url = %q", url)
	}
	if got := f.lines(); len(got) != 1 || got[0] != "serve --bg --yes --https=21003 http://127.0.0.1:42000" {
		t.Fatalf("argv = %q", got)
	}
}

// tailscaled refuses to switch a port's scheme in place, and the operator
// turning HTTPS on for the tailnet is exactly when a rebuild asks for that.
func TestPublishWithdrawsItsOwnMappingWhenTheSchemeChanged(t *testing.T) {
	plain := strings.ReplaceAll(serveStatusFixture, `"21003": {"HTTPS": true}`, `"21003": {"HTTP": true}`)
	// Publish's check, Withdraw's check, Withdraw's read-back, Publish's read-back.
	f, serve := newServeFake(plain, plain, "{}", serveStatusFixture)
	if _, err := serve.Publish(context.Background(), 21003, 41337, 41337); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"serve --yes --http=21003 off",
		"serve --bg --yes --https=21003 http://127.0.0.1:41337",
	}
	if got := f.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("argv = %q; want %q", got, want)
	}
}

func TestPublishChecksItsNumbersBeforeArgv(t *testing.T) {
	for _, c := range []struct{ port, upstream, previous int }{
		{20999, 3000, 0},
		{22000, 3000, 0},
		{21003, 0, 0},
		{21003, 65536, 0},
		{21003, 3000, -1},
	} {
		f, serve := newServeFake()
		if _, err := serve.Publish(context.Background(), c.port, c.upstream, c.previous); err == nil {
			t.Fatalf("Publish(%d, %d, %d) succeeded", c.port, c.upstream, c.previous)
		}
		if f.reads != 0 || len(f.calls) != 0 {
			t.Fatalf("Publish(%d, %d, %d) ran tailscale with a number it should have refused", c.port, c.upstream, c.previous)
		}
	}
	f, serve := newServeFake()
	if err := serve.Withdraw(context.Background(), 443); err == nil || f.reads != 0 || len(f.calls) != 0 {
		t.Fatalf("Withdraw(443) = %v after %q; a port outside the range is not the dashboard's to touch", err, f.lines())
	}
}

func TestPublishNeedsARunningTailnet(t *testing.T) {
	f, serve := newServeFake()
	serve.detect = func(context.Context) Identity {
		return Identity{Available: true, State: "NeedsLogin", Detail: "This machine is not connected to a tailnet (NeedsLogin). Run `tailscale up` on the host."}
	}
	_, err := serve.Publish(context.Background(), 21003, 41337, 0)
	if err == nil || !strings.Contains(err.Error(), "not running on this host") || !strings.Contains(err.Error(), "tailscale up") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("ran %q against a tailnet that is not up", f.lines())
	}
}

// The exit code is not the fact; the read-back is. A mapping that did not
// appear, or came back funnelled, is withdrawn and reported, never returned as
// an address.
func TestPublishWithdrawsAMappingThatDidNotTake(t *testing.T) {
	f, serve := newServeFake("{}", "{}")
	_, err := serve.Publish(context.Background(), 21003, 41337, 0)
	if err == nil || !strings.Contains(err.Error(), "did not take") {
		t.Fatalf("err = %v", err)
	}
	// Withdraw reads an empty status, so there is nothing to switch off.
	if got := f.lines(); len(got) != 1 {
		t.Fatalf("argv = %q", got)
	}

	funnelled := strings.ReplaceAll(serveStatusFixture, `"node.tailnet.ts.net:8443": true`,
		`"node.tailnet.ts.net:8443": true, "node.tailnet.ts.net:21003": true`)
	f, serve = newServeFake("{}", funnelled, funnelled, "{}")
	_, err = serve.Publish(context.Background(), 21003, 41337, 0)
	if err == nil || !strings.Contains(err.Error(), "funnelled") {
		t.Fatalf("err = %v", err)
	}
	want := []string{
		"serve --bg --yes --https=21003 http://127.0.0.1:41337",
		"serve --yes --https=21003 off",
	}
	if got := f.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("argv = %q; want %q", got, want)
	}
}

func TestWithdrawSwitchesTheServedSchemeOff(t *testing.T) {
	f, serve := newServeFake(serveStatusFixture, "{}")
	if err := serve.Withdraw(context.Background(), 21003); err != nil {
		t.Fatal(err)
	}
	if got := f.lines(); len(got) != 1 || got[0] != "serve --yes --https=21003 off" {
		t.Fatalf("argv = %q", got)
	}

	f, serve = newServeFake(serveStatusFixture, "{}")
	if err := serve.Withdraw(context.Background(), 21004); err != nil {
		t.Fatal(err)
	}
	if got := f.lines(); len(got) != 1 || got[0] != "serve --yes --http=21004 off" {
		t.Fatalf("argv = %q", got)
	}
}

// The status is the best guess at the scheme, not a promise; when the CLI
// refuses one, the other is tried before giving up.
func TestWithdrawFallsBackToTheOtherScheme(t *testing.T) {
	f, serve := newServeFake(serveStatusFixture, "{}")
	f.fail["serve --yes --https=21003 off"] = errors.New("error: port 21003 is not serving HTTPS")
	if err := serve.Withdraw(context.Background(), 21003); err != nil {
		t.Fatal(err)
	}
	want := []string{"serve --yes --https=21003 off", "serve --yes --http=21003 off"}
	if got := f.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("argv = %q; want %q", got, want)
	}

	// Both refused and the port still listed: the CLI's own words come back.
	f, serve = newServeFake(serveStatusFixture)
	f.fail["serve --yes --https=21003 off"] = errors.New("error: no such mapping")
	f.fail["serve --yes --http=21003 off"] = errors.New("error: no such mapping")
	err := serve.Withdraw(context.Background(), 21003)
	if err == nil || !strings.Contains(err.Error(), "no such mapping") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithdrawIsSatisfiedByAnUnservedPort(t *testing.T) {
	f, serve := newServeFake("{}")
	if err := serve.Withdraw(context.Background(), 21003); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("switched off a port nothing served: %q", f.lines())
	}
}

func TestServedSurfacesAStatusThatCannotBeRead(t *testing.T) {
	f, serve := newServeFake("tailscaled is not running")
	if _, err := serve.Served(context.Background()); err == nil {
		t.Fatal("an unreadable status was reported as an empty configuration")
	}
	if _, err := serve.Publish(context.Background(), 21003, 41337, 0); err == nil {
		t.Fatal("published on top of a configuration that could not be read")
	}
	if len(f.calls) != 0 {
		t.Fatalf("ran %q", f.lines())
	}
}

// The deploy package vouches for a mapping by the upstream this reads: only
// a plain proxy to a loopback port, never a funnelled port, a directory, a
// raw forwarder or a proxy to some other address.
func TestLoopbackUpstreamReadsOnlyAPlainLoopbackProxy(t *testing.T) {
	got, err := parseServeStatus([]byte(serveStatusFixture))
	if err != nil {
		t.Fatal(err)
	}
	for port, want := range map[int]int{21003: 41337, 21004: 3000, 8443: 0, 9000: 0} {
		if upstream := got[port].LoopbackUpstream(); upstream != want {
			t.Fatalf("port %d upstream = %d, want %d (%+v)", port, upstream, want, got[port])
		}
	}
	for target, want := range map[string]int{
		"http://127.0.0.1:3000": 3000, "http://127.0.0.1:65535": 65535,
		"http://localhost:3000": 0, "https://127.0.0.1:3000": 0, "http://127.0.0.1:+3000": 0, "http://127.0.0.1:03000": 0,
		"http://127.0.0.1:0": 0, "http://127.0.0.1:65536": 0, "http://127.0.0.1:3000/": 0, "file:/srv/site": 0, "text": 0, "": 0,
	} {
		if upstream := (ServeEntry{Target: target}).LoopbackUpstream(); upstream != want {
			t.Fatalf("LoopbackUpstream(%q) = %d, want %d", target, upstream, want)
		}
	}
	if upstream := (ServeEntry{Target: "http://127.0.0.1:3000", Funnel: true}).LoopbackUpstream(); upstream != 0 {
		t.Fatalf("a funnelled mapping was vouched for: %d", upstream)
	}
}
