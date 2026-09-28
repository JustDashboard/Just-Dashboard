package selfcfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A preview environment is reachable on the tailnet and nowhere else.
//
// The dashboard's own invariant is that nothing but Caddy binds a routable
// address, and a pull request's code is the last thing that should get an
// exception: it was written by somebody else and has not been merged. So the
// preview's container stays on loopback, and `tailscale serve` — which
// tailscaled runs on the host's tailnet address, with the tailnet's own
// certificates — is what carries https://<node>.<tailnet>.ts.net:<port> to it.
// This file is the dashboard's side of that arrangement: a fixed port range it
// calls its own, a reader for what tailscaled is currently serving so nothing
// of the operator's is ever overwritten, and the two commands that add and
// remove one mapping.

const (
	// TailnetPortMin and TailnetPortMax bound the ports the dashboard will
	// publish on. A range rather than "any free port" so that the start-up
	// sweep can tell a mapping the dashboard left behind from one the
	// operator made by hand.
	TailnetPortMin = 21000
	TailnetPortMax = 21999
	// serveTimeout bounds every `tailscale serve` call. Adding a mapping is a
	// local-socket round trip to tailscaled; anything slower is a daemon that
	// is not answering, and a deployment step should say so rather than hang.
	serveTimeout = 30 * time.Second
)

// ErrTailnetPortTaken is returned when a port in the dashboard's range is
// already served with something the dashboard did not put there, or is
// funnelled to the public internet, which a preview must never be.
var ErrTailnetPortTaken = errors.New("that tailnet port is already served by something the dashboard does not own")

// TailnetServe adds and removes `tailscale serve` mappings on the host.
type TailnetServe struct {
	// run executes one tailscale invocation and returns its combined output.
	// It is the seam that keeps the tests off the host's tailnet: every
	// command, including the status reads, goes through it.
	run func(ctx context.Context, args ...string) ([]byte, error)
	// detect answers what this machine is on its tailnet, for the same reason.
	detect func(ctx context.Context) Identity
}

func NewTailnetServe() *TailnetServe {
	return &TailnetServe{run: runTailscale, detect: DetectTailscale}
}

// runTailscale is the host-side executor: on the host, not in this container,
// because tailscaled's socket is the host's and this image carries no client.
func runTailscale(ctx context.Context, args ...string) ([]byte, error) {
	if !hostexec.Available("tailscale") {
		return nil, errors.New("tailscale is not installed on this host")
	}
	ctx, cancel := context.WithTimeout(ctx, serveTimeout)
	defer cancel()

	out, err := hostexec.CommandOnHost(ctx, "tailscale", args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return out, fmt.Errorf("tailscale %s: %s", strings.Join(args, " "), detail)
	}
	return out, nil
}

// ServeEntry is one port of tailscaled's serve configuration as the dashboard
// reads it: where it goes, over which scheme, and whether it is funnelled.
type ServeEntry struct {
	Port int
	// Target is the "/" handler's proxy target, http://127.0.0.1:<port> for
	// anything the dashboard published. A file, text or raw TCP mapping is
	// reported here too, so that it reads as foreign rather than as free.
	Target string
	HTTPS  bool
	Funnel bool
}

// LoopbackUpstream is the loopback port a plain proxy mapping points at, or 0
// for anything the dashboard would never have published: a funnelled port, a
// directory, a raw forwarder, a proxy to some other address. It is read next
// to loopbackTarget, the writer, so the two cannot drift apart; the deploy
// package withdraws only a mapping whose upstream this vouches for.
func (e ServeEntry) LoopbackUpstream() int {
	if e.Funnel {
		return 0
	}
	rest, ok := strings.CutPrefix(e.Target, "http://127.0.0.1:")
	if !ok {
		return 0
	}
	port, err := strconv.Atoi(rest)
	if err != nil || port < 1 || port > 65535 || loopbackTarget(port) != e.Target {
		return 0
	}
	return port
}

// serveConfig is the part of `tailscale serve status --json` this reads. The
// keys are strings because that is how tailscaled writes them: ports under TCP,
// "<host>:<port>" under Web and AllowFunnel.
type serveConfig struct {
	TCP map[string]struct {
		HTTPS        bool
		HTTP         bool
		TCPForward   string
		TerminateTLS string
	}
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string
			Path  string
			Text  string
		}
	}
	AllowFunnel map[string]bool
}

// Served reads what tailscaled is currently serving, by port.
func (t *TailnetServe) Served(ctx context.Context) (map[int]ServeEntry, error) {
	out, err := t.run(ctx, "serve", "status", "--json")
	if err != nil {
		return nil, err
	}
	return parseServeStatus(out)
}

// parseServeStatus is Served without the subprocess, so the part that reads
// somebody else's JSON is testable against a fixture.
//
// It is tolerant about the empty cases — "{}", nothing at all, or the plain
// sentence the CLI prints when no configuration exists — and strict about
// everything else: a status that cannot be read is a reason to refuse to
// publish, not a licence to treat every port as free.
func parseServeStatus(raw []byte) (map[int]ServeEntry, error) {
	text := strings.TrimSpace(string(raw))
	entries := map[int]ServeEntry{}
	if text == "" {
		return entries, nil
	}
	if !strings.HasPrefix(text, "{") {
		if strings.Contains(strings.ToLower(text), "no serve config") {
			return entries, nil
		}
		return nil, errors.New("tailscale serve status could not be read: " + text)
	}
	var cfg serveConfig
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, errors.New("tailscale serve status could not be read: " + err.Error())
	}

	entry := func(port int) ServeEntry {
		if e, ok := entries[port]; ok {
			return e
		}
		return ServeEntry{Port: port}
	}
	for key, tcp := range cfg.TCP {
		port, ok := servePort(key)
		if !ok {
			continue
		}
		e := entry(port)
		e.HTTPS = tcp.HTTPS
		// A raw forwarder has no web handler to name a target, and it is
		// certainly not the dashboard's, so its destination stands in.
		if tcp.TCPForward != "" {
			e.Target = tcp.TCPForward
		}
		entries[port] = e
	}
	for key, web := range cfg.Web {
		port, ok := serveHostPort(key)
		if !ok {
			continue
		}
		e := entry(port)
		if h, ok := web.Handlers["/"]; ok {
			switch {
			case h.Proxy != "":
				e.Target = h.Proxy
			case h.Path != "":
				e.Target = "file:" + h.Path
			case h.Text != "":
				e.Target = "text"
			}
		} else if len(web.Handlers) > 0 {
			// Only sub-paths are served: nothing the dashboard would write,
			// and a target it must not compare equal to its own.
			e.Target = "paths"
		}
		entries[port] = e
	}
	for key, allowed := range cfg.AllowFunnel {
		if !allowed {
			continue
		}
		port, ok := serveHostPort(key)
		if !ok {
			continue
		}
		e := entry(port)
		e.Funnel = true
		entries[port] = e
	}
	return entries, nil
}

func servePort(s string) (int, bool) {
	port, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// serveHostPort reads the port out of a "<host>:<port>" key. The host is not
// checked against this node's name: the port is what the dashboard allocates,
// and a mapping under any name on the same port is still in the way.
func serveHostPort(s string) (int, bool) {
	_, port, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return servePort(port)
}

func loopbackTarget(upstreamPort int) string {
	return "http://127.0.0.1:" + strconv.Itoa(upstreamPort)
}

func tailnetPortValid(port int) error {
	if port < TailnetPortMin || port > TailnetPortMax {
		return fmt.Errorf("tailnet port %d is outside the dashboard's range %d-%d", port, TailnetPortMin, TailnetPortMax)
	}
	return nil
}

// Publish maps <scheme>://<hostname>:<port> on the tailnet to
// http://127.0.0.1:<upstreamPort> on this host and returns the address.
//
// previousUpstream is the loopback port this dashboard last published on that
// tailnet port, 0 when it must be unused. Publish refuses to touch a port that
// is served with any other target, or that is funnelled: the first is the
// operator's, and the second would put a pull request's code on the public
// internet. It also never leaves a funnelled port behind — a mapping that came
// back funnelled is withdrawn, not reported as an address.
func (t *TailnetServe) Publish(ctx context.Context, port, upstreamPort, previousUpstream int) (string, error) {
	// Validated before anything reaches argv: the numbers are the only
	// request-derived values in these commands.
	if err := tailnetPortValid(port); err != nil {
		return "", err
	}
	if upstreamPort < 1 || upstreamPort > 65535 {
		return "", fmt.Errorf("upstream port %d is not a port", upstreamPort)
	}
	if previousUpstream < 0 || previousUpstream > 65535 {
		return "", fmt.Errorf("previous upstream port %d is not a port", previousUpstream)
	}
	id := t.detect(ctx)
	if !id.Usable() {
		return "", errors.New("Tailscale is not running on this host: " + id.Detail)
	}

	target := loopbackTarget(upstreamPort)
	entries, err := t.Served(ctx)
	if err != nil {
		return "", err
	}
	if e, ok := entries[port]; ok {
		if e.Funnel {
			return "", ErrTailnetPortTaken
		}
		ours := e.Target == target || (previousUpstream != 0 && e.Target == loopbackTarget(previousUpstream))
		if !ours {
			return "", ErrTailnetPortTaken
		}
		// tailscaled will not switch a port between HTTP and HTTPS in place,
		// which is exactly what happens the first time a preview is rebuilt
		// after the operator turns certificates on for the tailnet.
		if e.HTTPS != id.HTTPSEnabled {
			if err := t.Withdraw(ctx, port); err != nil {
				return "", err
			}
		}
	}

	scheme, flag := "https", "--https="
	if !id.HTTPSEnabled {
		scheme, flag = "http", "--http="
	}
	portText := strconv.Itoa(port)
	if _, err := t.run(ctx, "serve", "--bg", "--yes", flag+portText, target); err != nil {
		return "", err
	}

	// Read back rather than trust the exit code: the address is about to be
	// shown to an operator as a fact, and "funnelled" is the one state that
	// must never be handed over.
	entries, err = t.Served(ctx)
	if err != nil {
		_ = t.Withdraw(ctx, port)
		return "", err
	}
	e, ok := entries[port]
	if !ok || e.Target != target || e.Funnel {
		_ = t.Withdraw(ctx, port)
		if e.Funnel {
			return "", fmt.Errorf("tailscale serve left port %d funnelled, so the mapping was withdrawn", port)
		}
		return "", fmt.Errorf("tailscale serve did not take the mapping for port %d, so it was withdrawn", port)
	}
	return scheme + "://" + id.Hostname + ":" + portText, nil
}

// Withdraw removes the mapping on port, whichever scheme it was published
// with. A port that is not served is already withdrawn.
func (t *TailnetServe) Withdraw(ctx context.Context, port int) error {
	if err := tailnetPortValid(port); err != nil {
		return err
	}
	entries, err := t.Served(ctx)
	if err != nil {
		return err
	}
	e, ok := entries[port]
	if !ok {
		return nil
	}

	// The scheme has to match the mapping or the CLI refuses, and what the
	// status reported is the best guess; the other one is tried when it
	// was wrong, so a mapping is never stranded on a guess.
	portText := strconv.Itoa(port)
	first, second := "--https="+portText, "--http="+portText
	if !e.HTTPS {
		first, second = second, first
	}
	_, err = t.run(ctx, "serve", "--yes", first, "off")
	if err != nil {
		if _, retry := t.run(ctx, "serve", "--yes", second, "off"); retry == nil {
			err = nil
		}
	}

	entries, readErr := t.Served(ctx)
	if readErr != nil {
		if err != nil {
			return err
		}
		return readErr
	}
	if _, still := entries[port]; still {
		if err != nil {
			return err
		}
		return fmt.Errorf("tailscale serve still lists port %d after it was switched off", port)
	}
	return nil
}
