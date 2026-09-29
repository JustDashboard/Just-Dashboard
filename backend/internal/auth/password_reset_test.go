package auth

import (
	"errors"
	"testing"
	"time"
)

func TestResetPasswordRevokesCredentialsAndPreservesEnrollment(t *testing.T) {
	svc, user := newTestService(t, false)
	ctx := t.Context()
	login, err := svc.Login(ctx, user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.DB.ExecContext(ctx, `INSERT INTO api_tokens
		(user_id, name, prefix, token_hash, role, created_at) VALUES (?, 'test', 'test', 'hash', 'admin', 1)`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.DB.ExecContext(ctx, `UPDATE users SET totp_enabled = 1, totp_secret = 'sealed',
		totp_last_step = 123, failed_count = 4, locked_until = ? WHERE id = ?`, time.Now().Add(time.Hour).Unix(), user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.DB.ExecContext(ctx, `INSERT INTO recovery_codes(user_id, code_hash) VALUES (?, 'recovery')`, user.ID); err != nil {
		t.Fatal(err)
	}
	newPassword := "New-Temporary-Password-42"
	if err := svc.ResetPassword(ctx, user.ID, newPassword); err != nil {
		t.Fatal(err)
	}
	if svc.VerifyUserPassword(ctx, user.ID, testPassword) || !svc.VerifyUserPassword(ctx, user.ID, newPassword) {
		t.Fatal("reset did not replace the password")
	}
	if _, _, err := svc.ResolveSession(ctx, login.Token); err == nil {
		t.Fatal("old session survived password reset")
	}
	var secret string
	var enabled, mustChange, failed, locked, step, revoked, codes int
	if err := svc.st.DB.QueryRowContext(ctx, `SELECT totp_secret, totp_enabled, must_change_pw,
		failed_count, locked_until, totp_last_step FROM users WHERE id = ?`, user.ID).
		Scan(&secret, &enabled, &mustChange, &failed, &locked, &step); err != nil {
		t.Fatal(err)
	}
	if secret != "sealed" || enabled != 1 || step != 123 || mustChange != 1 || failed != 0 || locked != 0 {
		t.Fatalf("unexpected recovery state: secret=%q 2fa=%d step=%d change=%d failed=%d locked=%d",
			secret, enabled, step, mustChange, failed, locked)
	}
	if err := svc.st.DB.QueryRowContext(ctx, `SELECT revoked FROM api_tokens WHERE user_id = ?`, user.ID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if err := svc.st.DB.QueryRowContext(ctx, `SELECT count(*) FROM recovery_codes WHERE user_id = ?`, user.ID).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if revoked != 1 || codes != 1 {
		t.Fatalf("revoked=%d recovery codes=%d", revoked, codes)
	}
	result, err := svc.Login(ctx, user.Username, newPassword, "127.0.0.1", "test")
	if err != nil || !result.NeedsTOTP {
		t.Fatalf("reset bypassed enrolled two-factor: result=%+v err=%v", result, err)
	}
}

func TestResetPasswordRollsBackIfRevocationFails(t *testing.T) {
	svc, user := newTestService(t, false)
	ctx := t.Context()
	login, err := svc.Login(ctx, user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.DB.ExecContext(ctx, `CREATE TRIGGER refuse_revoke BEFORE UPDATE ON api_tokens
		BEGIN SELECT RAISE(ABORT, 'fixture failure'); END;
		INSERT INTO api_tokens(user_id, name, prefix, token_hash, role, created_at)
		VALUES (1, 'test', 'test', 'hash', 'admin', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResetPassword(ctx, user.ID, "New-Temporary-Password-42"); err == nil {
		t.Fatal("reset unexpectedly succeeded")
	}
	if !svc.VerifyUserPassword(ctx, user.ID, testPassword) {
		t.Fatal("password changed despite failed transaction")
	}
	if _, _, err := svc.ResolveSession(ctx, login.Token); err != nil {
		t.Fatalf("session was removed despite failed transaction: %v", err)
	}
}

func TestResetPasswordRefusesMissingAndWeakCredentials(t *testing.T) {
	svc, user := newTestService(t, false)
	if err := svc.ResetPassword(t.Context(), user.ID, "weak"); err == nil {
		t.Fatal("weak password accepted")
	}
	if err := svc.ResetPassword(t.Context(), user.ID+1, testPassword); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account: %v", err)
	}
	if !svc.VerifyUserPassword(t.Context(), user.ID, testPassword) {
		t.Fatal("invalid reset changed the account")
	}
}
