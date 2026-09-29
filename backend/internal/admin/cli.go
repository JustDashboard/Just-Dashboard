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

Run through scripts/manage.sh on the host. Password commands read one line
from stdin; never supply passwords in command arguments. New and reset
passwords must be changed at sign-in. Existing two-factor enrollment is kept.
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
			user, err := svc.CreateUser(ctx, args[1], password, role, true)
			if err != nil {
				return err
			}
			entry.Target = user.Username
			entry.Detail = audit.Detail(map[string]any{"role": role, "mustChangePassword": true})
			fmt.Fprintf(out, "Created dashboard user %q (%s). Change the temporary password at sign-in.\n", user.Username, role)
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
			fmt.Fprintf(out, "Reset password for %q. Sessions and API tokens revoked; two-factor enrollment preserved.\nChange the temporary password at sign-in.\n", target.Username)
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
