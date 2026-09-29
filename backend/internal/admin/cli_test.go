package admin

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestAccountCommandsShareTheStoreAndAuditWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := auth.NewService(st, nil, time.Hour, time.Hour, false)
	password := "Temporary-Password-42$'\\"
	var output, logs bytes.Buffer
	call := func(args []string, input string) error {
		t.Helper()
		if err := validateArgs(args); err != nil {
			return err
		}
		return run(t.Context(), dir, args, strings.NewReader(input), &output, &logs)
	}
	if err := call([]string{"create-user", "Alice"}, password+"\n"); err != nil {
		t.Fatal(err)
	}
	users, err := svc.ListUsers(t.Context())
	if err != nil || len(users) != 1 {
		t.Fatalf("users=%v err=%v", users, err)
	}
	user := users[0]
	if user.Username != "alice" || user.Role != auth.RoleReadOnly || !user.MustChangePW {
		t.Fatalf("created account=%+v", user)
	}
	if !svc.VerifyUserPassword(t.Context(), user.ID, password) {
		t.Fatal("password characters did not survive stdin")
	}
	if err := call([]string{"users"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := call([]string{"create-user", "alice", "admin"}, password); err == nil {
		t.Fatal("duplicate account accepted")
	}
	if err := call([]string{"reset-password", "missing"}, password); err == nil {
		t.Fatal("missing account accepted")
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE users SET disabled = 1 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := call([]string{"reset-password", " ALICE "}, "Replacement-Password-43\n"); err != nil {
		t.Fatal(err)
	}
	updated, err := svc.UserByID(t.Context(), user.ID)
	if err != nil || !updated.Disabled || !updated.MustChangePW {
		t.Fatalf("reset account=%+v err=%v", updated, err)
	}
	if err := call([]string{"revoke-sessions", "alice"}, ""); err != nil {
		t.Fatal(err)
	}
	var total, success int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT count(*), sum(success) FROM audit_log
		WHERE actor = 'cli' AND username = 'root' AND target IN ('alice', 'missing')`).Scan(&total, &success); err != nil {
		t.Fatal(err)
	}
	if total != 5 || success != 3 {
		t.Fatalf("audit total=%d successes=%d", total, success)
	}
	var details string
	if err := st.DB.QueryRowContext(t.Context(), `SELECT group_concat(detail) FROM audit_log`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{output.String(), logs.String(), details} {
		if strings.Contains(text, password) || strings.Contains(text, "Replacement-Password-43") {
			t.Fatal("password leaked into output or audit")
		}
	}
}

func TestCLIHelpAndBadArgumentsNeverOpenAStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	var output bytes.Buffer
	if err := Run(t.Context(), dir, []string{"--help"}, nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "reset-password") {
		t.Fatal("help omitted recovery")
	}
	for _, args := range [][]string{{"unknown"}, {"users", "extra"}, {"create-user"},
		{"create-user", "user", "owner"}, {"reset-password", "user", "secret"}} {
		if err := Run(t.Context(), dir, args, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	if err := run(t.Context(), dir, []string{"users"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("missing database created silently")
	}
}

func TestPasswordInputIsOneBoundedLine(t *testing.T) {
	for _, input := range []string{"weak", "Strong-Password-42\nextra", strings.Repeat("X", 4098)} {
		if _, err := readPassword(strings.NewReader(input)); err == nil {
			t.Fatal("invalid password input accepted")
		}
	}
	for _, input := range []string{"Strong-Password-42", "Strong-Password-42\n", "Strong-Password-42\r\n"} {
		password, err := readPassword(strings.NewReader(input))
		if err != nil || password != "Strong-Password-42" {
			t.Fatalf("password=%q err=%v", password, err)
		}
	}
}
