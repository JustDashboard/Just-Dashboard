package admin

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
)

// The hub link's terminal commands: hub-connect, hub-disconnect and
// hub-status, which scripts/manage.sh runs as `hub connect|disconnect|status`.
// They are the link commands' twins for a Just Hub (hub-link-protocol.md
// section 8.4).

func isHubCommand(name string) bool {
	return name == "hub-connect" || name == "hub-disconnect" || name == "hub-status"
}

// hubConnectOptions are the arguments of hub-connect. The token is never one
// of them: it is read from standard input (--token-stdin), so it appears in no
// process list and no shell history (hub-link-protocol.md 8.4).
type hubConnectOptions struct {
	url        string
	scope      accountlink.Scope
	wait       time.Duration
	token      string
	tokenStdin bool
}

// errTokenArgument is a token given as an argument, which is refused.
var errTokenArgument = errors.New("pass the token on standard input with --token-stdin")

func parseHubConnectArgs(args []string) (hubConnectOptions, error) {
	o := hubConnectOptions{scope: accountlink.ScopeMaintain, wait: defaultLinkWait}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return o, errors.New("hub connect needs the hub's address, for example hub.example.ts.net")
	}
	o.url, args = args[0], args[1:]
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		switch name {
		case "--scope", "--wait", "--token-stdin":
		case "--token":
			return o, errTokenArgument
		default:
			return o, fmt.Errorf("unknown option %q; hub connect takes --scope observe|operate|maintain, --token-stdin and --wait 15m", args[i])
		}
		if seen[name] {
			return o, fmt.Errorf("%s was given twice", name)
		}
		seen[name] = true
		if name == "--token-stdin" {
			if hasValue {
				return o, errors.New("--token-stdin takes no value; pipe the token in")
			}
			o.tokenStdin = true
			continue
		}
		if !hasValue {
			i++
			if i >= len(args) {
				return o, fmt.Errorf("%s needs a value", name)
			}
			value = args[i]
		}
		switch name {
		case "--scope":
			scope, ok := accountlink.ParseScope(value)
			if !ok {
				return o, fmt.Errorf("--scope must be observe, operate or maintain, not %q", value)
			}
			o.scope = scope
		case "--wait":
			d, err := time.ParseDuration(value)
			if err != nil || d < time.Second || d > maxLinkWait {
				return o, fmt.Errorf("--wait must be a duration between 1s and 1h, for example 15m, not %q", value)
			}
			o.wait = d
		}
	}
	return o, nil
}

// hubEnvironment is what the Compose stack passes into `docker compose run`,
// the same variables the server reads.
type hubEnvironment struct {
	enabled    bool
	testOrigin string
	masterKey  string
}

func readHubEnvironment() (hubEnvironment, error) {
	e := hubEnvironment{enabled: true, masterKey: config.Env("JD_MASTER_KEY")}
	if raw := config.Env("JD_HUB_LINK"); raw != "" {
		switch strings.ToLower(raw) {
		case "1", "t", "true":
		case "0", "f", "false":
			e.enabled = false
		default:
			return e, fmt.Errorf("JD_HUB_LINK=%q is not true or false", raw)
		}
	}
	origin, err := hublink.ParseTestOrigin(config.Env("JD_HUB_LINK_TEST_ORIGIN"))
	if err != nil {
		return e, fmt.Errorf("JD_HUB_LINK_TEST_ORIGIN=%q: %w", config.Env("JD_HUB_LINK_TEST_ORIGIN"), err)
	}
	e.testOrigin = origin
	return e, nil
}

// readToken takes one line from standard input, which is how the hub's
// installer passes its token so it never appears in a process list.
func readToken(in io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(in, 256)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("--token-stdin read no token; pipe the line the hub printed")
	}
	return line, nil
}

// runHub runs hub-connect, hub-disconnect and hub-status against the store, as
// root at the terminal. Connecting and disconnecting open the hub link's
// sealed identity, so they need JD_MASTER_KEY; the status does not.
func runHub(ctx context.Context, st *store.Store, args []string, in io.Reader, out, errOut io.Writer) error {
	env, err := readHubEnvironment()
	if err != nil {
		return err
	}
	var sealer *auth.Sealer
	if args[0] != "hub-status" {
		if env.masterKey == "" {
			return errors.New("connecting to a hub needs JD_MASTER_KEY, which the Compose stack passes in from .env; run this through scripts/manage.sh from the install directory")
		}
		if sealer, err = auth.NewSealer(env.masterKey); err != nil {
			return fmt.Errorf("JD_MASTER_KEY: %w", err)
		}
	} else if env.masterKey != "" {
		sealer, _ = auth.NewSealer(env.masterKey)
	}
	// As with link: the audit rows are written, and only warnings and errors
	// reach a person waiting for a code.
	quiet := slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: slog.LevelWarn}))
	logger := audit.New(st, quiet)
	svc := hublink.New(hublink.Options{
		Store: st, Sealer: sealer, Audit: logger, Log: quiet,
		Enabled: env.enabled, TestOrigin: env.testOrigin, Version: version.Version,
	})
	switch args[0] {
	case "hub-connect":
		opts, err := parseHubConnectArgs(args[1:])
		if err != nil {
			return err
		}
		if opts.tokenStdin {
			if opts.token, err = readToken(in); err != nil {
				return err
			}
		}
		return hubConnectCommand(ctx, svc, logger, opts, out)
	case "hub-disconnect":
		return hubDisconnectCommand(ctx, svc, logger, out)
	}
	return hubStatusCommand(ctx, svc, env, out)
}

func hubConnectCommand(ctx context.Context, svc *hublink.Service, logger *audit.Logger, opts hubConnectOptions, out io.Writer) error {
	// An interrupt ends the wait, not the record of how it ended.
	rec := context.WithoutCancel(ctx)
	fail := func(err error) error {
		cliRecord(rec, logger, "hub.link", string(opts.scope), false, map[string]any{"error": err.Error(), "hub": opts.url})
		return err
	}
	req, err := svc.Connect(ctx, hublink.ConnectRequest{URL: opts.url, Scope: opts.scope, By: "cli", Token: opts.token})
	if err != nil {
		switch {
		case errors.Is(err, hublink.ErrDisabled):
			return fail(errors.New("connecting to Just Hub is switched off on this server (JD_HUB_LINK=false in .env)"))
		case errors.Is(err, hublink.ErrAlreadyLinked):
			if st, ok, _ := svc.Linked(ctx); ok {
				return fail(fmt.Errorf("this server is already connected to Just Hub %q at %s; run hub disconnect first", st.Owner, st.URL))
			}
		case errors.Is(err, hublink.ErrLinkPending):
			if pending, ok, _ := svc.Pending(ctx); ok {
				return fail(fmt.Errorf("a request to connect to %s is already waiting for approval (code %s); approve it in the hub or cancel it in Settings", pending.URL, pending.UserCode))
			}
		}
		return fail(err)
	}

	fmt.Fprintf(out, "\n  Connecting to Just Hub at   %s\n", req.URL)
	fmt.Fprintf(out, "  The hub's machine           %s\n", machineLine(svc, req))
	fmt.Fprintf(out, "  Code                        %s\n", req.UserCode)
	fmt.Fprintf(out, "  This server's fingerprint   %s\n", svc.Engine().Fingerprint(ctx))
	if opts.token != "" {
		fmt.Fprintf(out, "  The hub's token approves it. Waiting for the hub to confirm (%s scope)...\n\n", req.Scope)
	} else {
		fmt.Fprintf(out, "  Approve it in the hub at    %s\n", approvalPage(req.ApproveURL))
		fmt.Fprintf(out, "  It will be connected at the %s scope. The code is good for %d minutes. Waiting for approval...\n\n",
			req.Scope, int((time.Until(req.ExpiresAt)+30*time.Second)/time.Minute))
	}

	waitCtx, cancel := context.WithTimeout(ctx, opts.wait)
	defer cancel()
	res, err := svc.Await(waitCtx)
	if err != nil {
		return fail(err)
	}
	switch res.State {
	case accountlink.PollLinked:
		// The engine has already written the audit entry for the link.
		name := res.Link.ServerName
		if name == "" {
			name = "this server"
		}
		fmt.Fprintf(out, "Connected to Just Hub %q at %s as %q (%s).\n", res.Link.Owner, res.Link.URL, name, res.Link.Scope)
		for _, k := range res.Link.SiteKeys {
			if pub, err := base64.StdEncoding.DecodeString(k.PublicKey); err == nil && len(pub) == ed25519.PublicKeySize {
				fmt.Fprintf(out, "The hub's key fingerprint is %s; compare it with the key fingerprint on the hub's Settings page.\n",
					accountlink.Fingerprint(ed25519.PublicKey(pub)))
			}
		}
		return nil
	case accountlink.PollDenied:
		cliRecord(rec, logger, "hub.link", string(opts.scope), false, map[string]any{"outcome": "denied", "hub": req.URL})
		fmt.Fprintln(out, "The request was declined in the hub. Nothing was connected.")
		return &ExitError{Code: 2}
	case accountlink.PollExpired:
		cliRecord(rec, logger, "hub.link", string(opts.scope), false, map[string]any{"outcome": "not approved in time", "hub": req.URL})
		fmt.Fprintln(out, "The request was not approved in time. Nothing was connected; run the command again for a new code.")
		if res.Message != "" && !strings.HasPrefix(res.Message, "the request was") {
			fmt.Fprintln(out, res.Message)
		}
		return &ExitError{Code: 2}
	}
	cliRecord(rec, logger, "hub.link", string(opts.scope), false, map[string]any{"outcome": res.Message, "hub": req.URL})
	return fmt.Errorf("the request ended without a link: %s", res.Message)
}

// machineLine is the hub's machine and its owner as step 8 of the URL rule
// found them, or why it was not checked.
func machineLine(svc *hublink.Service, req accountlink.Request) string {
	if req.HubNode != nil {
		return hublink.DescribeMachine(*req.HubNode)
	}
	if svc.TestOrigin() != "" && req.URL == svc.TestOrigin() {
		return "not checked (the test origin)"
	}
	return "not checked (tailscaled could not be asked)"
}

func hubDisconnectCommand(ctx context.Context, svc *hublink.Service, logger *audit.Logger, out io.Writer) error {
	st, linked, err := svc.Linked(ctx)
	if err != nil {
		return err
	}
	if !linked {
		return errors.New("this server is not connected to a hub")
	}
	notified, err := svc.Disconnect(ctx)
	if err != nil {
		cliRecord(ctx, logger, "hub.unlink", st.Owner, false, map[string]any{"error": err.Error()})
		return err
	}
	cliRecord(ctx, logger, "hub.unlink", st.Owner, true, map[string]any{"hubNotified": notified})
	fmt.Fprintf(out, "Disconnected from Just Hub %q at %s.\n", st.Owner, st.URL)
	if !notified {
		fmt.Fprintln(out, "The hub could not be told; remove this server in the hub as well.")
	}
	return nil
}

// hubStatusCommand prints one line: not connected, the pending code, or
// connected to whom.
func hubStatusCommand(ctx context.Context, svc *hublink.Service, env hubEnvironment, out io.Writer) error {
	off := ""
	if !env.enabled {
		off = " Connecting to Just Hub is switched off by JD_HUB_LINK=false, so nothing is being sent."
	}
	st, linked, err := svc.Linked(ctx)
	if err != nil {
		return err
	}
	if linked {
		h := svc.Engine().Health(ctx)
		name := "this server"
		if st.ServerName != "" {
			name = fmt.Sprintf("%q", st.ServerName)
		}
		beat := "no heartbeat yet"
		if h.LastHeartbeatAt != nil {
			beat = "last heartbeat " + ago(time.Since(*h.LastHeartbeatAt))
		}
		fmt.Fprintf(out, "Connected to Just Hub %q at %s as %s (%s); %s.", st.Owner, st.URL, name, st.Scope, beat)
		if st.Idle != nil {
			// Nothing is sent on an idle link until it is disconnected.
			fmt.Fprintf(out, " The link is idle, and nothing is sent: %s. Run hub disconnect, then hub connect again.", st.Idle.Reason)
		} else if e := firstNonEmpty(h.HeartbeatError, h.PollError); e != "" {
			fmt.Fprintf(out, " Last error: %s", e)
		}
		fmt.Fprintln(out, off)
		return nil
	}
	if req, pending, err := svc.Pending(ctx); err != nil {
		return err
	} else if pending {
		fmt.Fprintf(out, "Connection requested (%s) to %s: code %s, expires in %s.%s\n", req.Scope, req.URL, req.UserCode,
			(time.Until(req.ExpiresAt) + 30*time.Second).Truncate(time.Minute), off)
		return nil
	}
	msg := "Not connected to a hub."
	if es, err := svc.Engine().Status(ctx); err == nil && es.LastOutcome != nil {
		msg += " The last request was " + es.LastOutcome.Result + "."
	}
	fmt.Fprintln(out, msg+off)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
