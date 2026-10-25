package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
)

// ExitError is a command that ended with a particular status. A decline or a
// timeout is exit 2, so a script can tell "the person said no" from "it broke".
type ExitError struct {
	Code int
	// Msg is printed to stderr when it is not empty.
	Msg string
}

func (e *ExitError) Error() string { return e.Msg }

const (
	maxLinkWait     = time.Hour
	defaultLinkWait = 15 * time.Minute
)

func isLinkCommand(name string) bool {
	return name == "link" || name == "unlink" || name == "link-status"
}

// LongRunning says whether a command waits on a person, so the caller does not
// give it the half minute the account commands get.
func LongRunning(args []string) bool { return len(args) > 0 && args[0] == "link" }

// linkOptions are the flags of `link`.
type linkOptions struct {
	scope accountlink.Scope
	wait  time.Duration
}

func parseLinkFlags(args []string) (linkOptions, error) {
	o := linkOptions{scope: accountlink.ScopeObserve, wait: defaultLinkWait}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != "--scope" && name != "--wait" {
			return o, fmt.Errorf("unknown option %q; link takes --scope observe|operate|maintain and --wait 15m", args[i])
		}
		if seen[name] {
			return o, fmt.Errorf("%s was given twice", name)
		}
		seen[name] = true
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

// linkEnvironment is what the compose stack passes into `docker compose run`,
// the same variables the server reads.
type linkEnvironment struct {
	enabled   bool
	siteURL   string
	masterKey string
}

func readLinkEnvironment() (linkEnvironment, error) {
	e := linkEnvironment{enabled: true, masterKey: config.Env("JD_MASTER_KEY")}
	if raw := config.Env("JD_ACCOUNT_LINK"); raw != "" {
		switch strings.ToLower(raw) {
		case "1", "t", "true":
		case "0", "f", "false":
			e.enabled = false
		default:
			return e, fmt.Errorf("JD_ACCOUNT_LINK=%q is not true or false", raw)
		}
	}
	site, err := accountlink.ParseSiteURL(config.Env("JD_ACCOUNT_URL"))
	if err != nil {
		return e, fmt.Errorf("JD_ACCOUNT_URL=%q: %w", config.Env("JD_ACCOUNT_URL"), err)
	}
	e.siteURL = site
	return e, nil
}

// runLink runs link, unlink and link-status against the store, as root at the
// terminal. The commands that change the link build their own sealer from
// JD_MASTER_KEY: account commands never decrypt anything, and these have to,
// because the identity this install signs with is sealed.
func runLink(ctx context.Context, st *store.Store, args []string, out, errOut io.Writer) error {
	env, err := readLinkEnvironment()
	if err != nil {
		return err
	}
	var sealer *auth.Sealer
	if args[0] != "link-status" {
		if env.masterKey == "" {
			return errors.New("linking needs JD_MASTER_KEY, which the Compose stack passes in from .env; run this through scripts/manage.sh from the install directory")
		}
		if sealer, err = auth.NewSealer(env.masterKey); err != nil {
			return fmt.Errorf("JD_MASTER_KEY: %w", err)
		}
	} else if env.masterKey != "" {
		sealer, _ = auth.NewSealer(env.masterKey)
	}
	// The audit rows are written to the store as always. What the account
	// commands also print to stderr for each one is left out here, because the
	// installer shows this command's output to a person who is waiting for a
	// code, and warnings and errors still come through.
	quiet := slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: slog.LevelWarn}))
	logger := audit.New(st, quiet)
	svc := accountlink.New(accountlink.Options{
		Store: st, Sealer: sealer, Audit: logger, Log: quiet,
		SiteURL: env.siteURL, Enabled: env.enabled, Version: version.Version,
	})
	switch args[0] {
	case "link":
		opts, err := parseLinkFlags(args[1:])
		if err != nil {
			return err
		}
		return linkCommand(ctx, svc, logger, opts, out)
	case "unlink":
		return unlinkCommand(ctx, svc, logger, out)
	}
	return statusCommand(ctx, svc, env, out)
}

func cliRecord(ctx context.Context, logger *audit.Logger, action, target string, success bool, detail map[string]any) {
	status := 200
	if !success {
		status = 400
	}
	logger.Record(ctx, audit.Entry{
		Username: "root", Role: "admin", Actor: "cli", Method: "CLI", Action: action, Target: target,
		Status: status, Success: success, Detail: audit.Detail(detail),
	})
}

func linkCommand(ctx context.Context, svc *accountlink.Service, logger *audit.Logger, opts linkOptions, out io.Writer) error {
	req, err := svc.BeginLink(ctx, opts.scope, "cli")
	if err != nil {
		cliRecord(ctx, logger, "account.link", string(opts.scope), false, map[string]any{"error": err.Error()})
		switch {
		case errors.Is(err, accountlink.ErrDisabled):
			return errors.New("linking is switched off on this server (JD_ACCOUNT_LINK=false in .env)")
		case errors.Is(err, accountlink.ErrAlreadyLinked):
			if st, ok, _ := svc.Linked(ctx); ok {
				return fmt.Errorf("this server is already linked to %s; run unlink first to link it to another account", st.Owner)
			}
		case errors.Is(err, accountlink.ErrLinkPending):
			if pending, ok, _ := svc.Pending(ctx); ok {
				return fmt.Errorf("a link request is already waiting for approval (code %s); approve it on %s or cancel it in Settings", pending.UserCode, svc.SiteURL())
			}
		}
		return err
	}

	fmt.Fprintf(out, "\n  To link this server, open   %s\n", approvalPage(req.ApproveURL))
	fmt.Fprintf(out, "  and enter the code          %s\n", req.UserCode)
	fmt.Fprintf(out, "  or open this address        %s\n", req.ApproveURL)
	fmt.Fprintf(out, "  It will be linked at the %s scope. The code is good for %d minutes. Waiting for your approval...\n\n",
		req.Scope, int((time.Until(req.ExpiresAt)+30*time.Second)/time.Minute))

	waitCtx, cancel := context.WithTimeout(ctx, opts.wait)
	defer cancel()
	res, err := svc.AwaitLink(waitCtx)
	if err != nil {
		return err
	}
	switch res.State {
	case accountlink.PollLinked:
		// The service has already written the audit entry for a link.
		name := res.Link.ServerName
		if name == "" {
			name = "this server"
		}
		fmt.Fprintf(out, "Linked to %s as %q (%s).\n", res.Link.Owner, name, res.Link.Scope)
		return nil
	case accountlink.PollDenied:
		cliRecord(ctx, logger, "account.link", string(opts.scope), false, map[string]any{"outcome": "denied"})
		fmt.Fprintln(out, "The request was declined on the site. Nothing was linked.")
		return &ExitError{Code: 2}
	case accountlink.PollExpired:
		cliRecord(ctx, logger, "account.link", string(opts.scope), false, map[string]any{"outcome": "not approved in time"})
		fmt.Fprintln(out, "The request was not approved in time. Nothing was linked; run the command again for a new code.")
		if res.Message != "" && !strings.HasPrefix(res.Message, "the request was") {
			fmt.Fprintln(out, res.Message)
		}
		return &ExitError{Code: 2}
	}
	cliRecord(ctx, logger, "account.link", string(opts.scope), false, map[string]any{"outcome": res.Message})
	return fmt.Errorf("the request ended without a link: %s", res.Message)
}

// approvalPage is the address a person types a code at: the approval address
// without its code.
func approvalPage(approveURL string) string {
	u, err := url.Parse(approveURL)
	if err != nil {
		return approveURL
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func unlinkCommand(ctx context.Context, svc *accountlink.Service, logger *audit.Logger, out io.Writer) error {
	st, linked, err := svc.Linked(ctx)
	if err != nil {
		return err
	}
	if !linked {
		return errors.New("this server is not linked to a just-dashboard.com account")
	}
	notified, err := svc.Unlink(ctx)
	if err != nil {
		cliRecord(ctx, logger, "account.unlink", st.Owner, false, map[string]any{"error": err.Error()})
		return err
	}
	cliRecord(ctx, logger, "account.unlink", st.Owner, true, map[string]any{"siteNotified": notified})
	fmt.Fprintf(out, "Unlinked from %s.\n", st.Owner)
	if !notified {
		fmt.Fprintln(out, "The site could not be told; remove this server from your account on just-dashboard.com as well.")
	}
	return nil
}

// statusCommand prints one line: unlinked, pending, or linked.
func statusCommand(ctx context.Context, svc *accountlink.Service, env linkEnvironment, out io.Writer) error {
	status, err := svc.Status(ctx)
	if err != nil {
		return err
	}
	off := ""
	if !env.enabled {
		off = " Linking is switched off by JD_ACCOUNT_LINK=false, so nothing is being sent."
	}
	switch status.State {
	case accountlink.StateLinked:
		l := status.Link
		name := "this server"
		if st, ok, _ := svc.Linked(ctx); ok && st.ServerName != "" {
			name = fmt.Sprintf("%q", st.ServerName)
		}
		beat := "no heartbeat yet"
		if l.LastHeartbeatAt != nil {
			beat = "last heartbeat " + ago(time.Since(*l.LastHeartbeatAt))
		}
		fmt.Fprintf(out, "Linked to %s as %s (%s); %s.", l.Owner, name, l.Scope, beat)
		if l.LastError != "" {
			fmt.Fprintf(out, " Last error: %s", l.LastError)
		}
		fmt.Fprintln(out, off)
	case accountlink.StatePending:
		r := status.Request
		fmt.Fprintf(out, "Link requested (%s): code %s on %s, expires in %s.%s\n", r.Scope, r.UserCode,
			approvalPage(r.ApproveURL), (time.Until(r.ExpiresAt) + 30*time.Second).Truncate(time.Minute), off)
	default:
		msg := "Not linked."
		if status.LastOutcome != nil {
			msg += " The last request was " + status.LastOutcome.Result + "."
		}
		fmt.Fprintln(out, msg+off)
	}
	return nil
}

func ago(d time.Duration) string {
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}
