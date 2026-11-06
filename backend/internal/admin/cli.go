// Package admin provides local account recovery for the host operator. It has
// no HTTP surface and never starts the dashboard's modules or listeners.
package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

const Usage = `Local dashboard account tools:
  --admin users
  --admin create-user USER [readonly|limited|admin]
  --admin reset-password USER
  --admin revoke-sessions USER

Link this server to a just-dashboard.com account:
  --admin link [--scope observe|operate|maintain] [--wait 15m]
  --admin unlink
  --admin link-status

Connect this server to a Just Hub on its tailnet:
  --admin hub-connect URL [--scope observe|operate|maintain] [--token-stdin] [--wait 15m]
  --admin hub-disconnect
  --admin hub-status

Run through scripts/manage.sh on the host. Password commands read one line
from stdin; never supply passwords in command arguments. The password set here
is the one used to sign in. Existing two-factor enrollment is kept.

link prints a code to enter on just-dashboard.com, waits for you to approve it
there, and exits 0 once linked, 2 if it was declined or not approved in time.
The scope (default observe) is what the site may see and do; widen it later in
Settings. Linking needs JD_MASTER_KEY and is refused when JD_ACCOUNT_LINK=false.

hub-connect checks that URL is one machine on this server's tailnet, prints a
code and this server's fingerprint, waits for the hub owner to approve it in the
hub, and exits 0 once connected, 2 if it was declined, not approved within
--wait, or interrupted. A one-time token from the hub approves it in advance;
pipe it in with --token-stdin, never as an argument, where the process list and
shell history would keep it. The scope (default maintain) is what the hub may
see and do. It needs JD_MASTER_KEY and is refused when JD_HUB_LINK=false.
`

func Run(ctx context.Context, dataDir string, args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "help")) {
		_, err := fmt.Fprint(out, Usage)
		return err
	}
	if err := validateArgs(args); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("local account administration requires root; run the host script with sudo")
	}
	return run(ctx, dataDir, args, in, out, errOut)
}

func run(ctx context.Context, dataDir string, args []string, in io.Reader, out, errOut io.Writer) error {
	// A typo in the mount or data directory must not bootstrap a second store.
	info, err := os.Stat(filepath.Join(dataDir, store.DatabaseFile))
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("dashboard database not found; finish installation and check JD_DATA_DIR")
	}
	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if isLinkCommand(args[0]) {
		return runLink(ctx, st, args, out, errOut)
	}
	if isHubCommand(args[0]) {
		return runHub(ctx, st, args, in, out, errOut)
	}
	// These operations never decrypt secrets or evaluate login policy.
	svc := auth.NewService(st, nil, time.Hour, time.Hour, false)
	if args[0] == "users" {
		users, err := svc.ListUsers(ctx)
		if err != nil {
			return err
		}
		table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "USERNAME\tROLE\tDISABLED\t2FA\tCHANGE PASSWORD")
		for _, user := range users {
			fmt.Fprintf(table, "%q\t%s\t%t\t%t\t%t\n", user.Username, user.Role,
				user.Disabled, user.TOTPEnabled, user.MustChangePW)
		}
		return table.Flush()
	}

	logger := audit.New(st, slog.New(slog.NewTextHandler(errOut, nil)))
	// Resetting a password and revoking every session each end the account's
	// hub sign-in grant and revoke its hub sessions inside auth's own
	// transaction; every grant end is audited (hub-link-protocol.md 9.10),
	// here as root at the terminal did it.
	svc.SetHubGrantEnded(func(ctx context.Context, end auth.HubGrantEnd) {
		account := fmt.Sprintf("user %d", end.UserID)
		if u, err := svc.UserByID(ctx, end.UserID); err == nil {
			account = u.Username
		}
		logger.Record(ctx, audit.Entry{
			UserID: end.UserID, Username: "root", Role: "admin", Actor: "cli", Method: "CLI", Status: 200, Success: true,
			Action: "hub.signin.revoke", Target: end.HubUser,
			Detail: audit.Detail(map[string]any{
				"account": account, "hubUser": end.HubUser, "reason": end.Reason, "sessionsRevoked": end.SessionsRevoked,
			}),
		})
	})
	entry := audit.Entry{Username: "root", Role: "admin", Actor: "cli", Method: "CLI",
		Action: "dashboard.user." + args[0], Target: strings.TrimSpace(strings.ToLower(args[1]))}
	var operationErr error
	defer func() {
		entry.Success = operationErr == nil
		entry.Status = 200
		if operationErr != nil {
			entry.Status = 400
		}
		logger.Record(ctx, entry)
	}()
	perform := func() error {
		if args[0] == "create-user" {
			password, err := readPassword(in)
			if err != nil {
				return err
			}
			role := auth.RoleReadOnly
			if len(args) == 3 {
				role = auth.Role(args[2])
			}
			// Typed twice at a root shell, so it is already chosen: the
			// dashboard does not ask for another one at first sign-in.
			user, err := svc.CreateUser(ctx, args[1], password, role, false)
			if err != nil {
				return err
			}
			entry.Target = user.Username
			entry.Detail = audit.Detail(map[string]any{"role": role})
			fmt.Fprintf(out, "Created dashboard user %q (%s). Sign in with the password you just set.\n", user.Username, role)
			return nil
		}
		users, err := svc.ListUsers(ctx)
		if err != nil {
			return err
		}
		var target *auth.User
		for _, user := range users {
			if user.Username == entry.Target {
				target = user
				break
			}
		}
		if target == nil {
			return fmt.Errorf("dashboard user %q does not exist; run the users command to list accounts", entry.Target)
		}
		switch args[0] {
		case "reset-password":
			password, err := readPassword(in)
			if err != nil {
				return err
			}
			if err := svc.ResetPassword(ctx, target.ID, password); err != nil {
				return err
			}
			fmt.Fprintf(out, "Reset password for %q. Sessions and API tokens revoked; two-factor enrollment preserved.\nSign in with the password you just set.\n", target.Username)
			if target.Disabled {
				fmt.Fprintln(out, "This account is disabled and remains unable to sign in.")
			}
		case "revoke-sessions":
			if err := svc.RevokeAllSessions(ctx, target.ID); err != nil {
				return err
			}
			fmt.Fprintf(out, "Signed out all browser sessions for %q. API tokens are unchanged.\n", target.Username)
		}
		return nil
	}
	operationErr = perform()
	return operationErr
}

func validateArgs(args []string) error {
	switch args[0] {
	case "link":
		if _, err := parseLinkFlags(args[1:]); err != nil {
			return err
		}
		return nil
	case "unlink", "link-status":
		if len(args) == 1 {
			return nil
		}
	case "hub-connect":
		if _, err := parseHubConnectArgs(args[1:]); err != nil {
			return err
		}
		return nil
	case "hub-disconnect", "hub-status":
		if len(args) == 1 {
			return nil
		}
	case "users":
		if len(args) == 1 {
			return nil
		}
	case "create-user":
		if len(args) == 2 || (len(args) == 3 && auth.Role(args[2]).Valid()) {
			return nil
		}
	case "reset-password", "revoke-sessions":
		if len(args) == 2 {
			return nil
		}
	}
	return errors.New("invalid account command or arguments; run scripts/manage.sh --help")
}

func readPassword(in io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(in, 4098))
	if err != nil {
		return "", err
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(password) > 4096 || strings.ContainsAny(password, "\r\n") {
		return "", errors.New("supply one password line of at most 4096 bytes on stdin")
	}
	if err := auth.ValidatePasswordStrength(password); err != nil {
		return "", err
	}
	return password, nil
}
