package auth

import "testing"

func TestUpdateUserRollsBackAllFieldsWhenSessionRevocationFails(t *testing.T) {
	svc, user := newTestService(t, false)
	if _, err := svc.CreateUser(t.Context(), "other-admin", testPassword, RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := svc.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.DB.ExecContext(t.Context(), `CREATE TRIGGER refuse_session_delete BEFORE DELETE ON sessions
		BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	username, display, password := "renamed", "New Name", "Replacement-Password-9"
	role, disabled := RoleReadOnly, true
	err = svc.UpdateUser(t.Context(), user.ID, UserUpdate{
		Profile: Profile{Username: &username, DisplayName: &display},
		Role:    &role, Disabled: &disabled, Password: &password,
	})
	if err == nil {
		t.Fatal("update succeeded despite failing revocation")
	}
	after, err := svc.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before || !svc.VerifyUserPassword(t.Context(), user.ID, testPassword) {
		t.Fatalf("failed revocation left a partial edit: before=%+v after=%+v", before, after)
	}
	if _, _, err := svc.ResolveSession(t.Context(), login.Token); err != nil {
		t.Fatalf("failed revocation removed the session: %v", err)
	}
}

func TestUpdateUserCommitsFieldsAndRevokesSessions(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(map[bool]string{false: "password", true: "disable"}[disable], func(t *testing.T) {
			svc, _ := newTestService(t, false)
			user, err := svc.CreateUser(t.Context(), "target", testPassword, RoleAdmin, true)
			if err != nil {
				t.Fatal(err)
			}
			login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
			if err != nil {
				t.Fatal(err)
			}
			username, display, password := "  RENAMED  ", "  New Name  ", "Replacement-Password-9"
			role := RoleReadOnly
			update := UserUpdate{
				Profile: Profile{Username: &username, DisplayName: &display}, Role: &role, Disabled: &disable,
			}
			if !disable {
				update.Password = &password
			}
			if err := svc.UpdateUser(t.Context(), user.ID, update); err != nil {
				t.Fatal(err)
			}
			after, err := svc.UserByID(t.Context(), user.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Username != "renamed" || after.DisplayName != "New Name" || after.Role != role ||
				after.Disabled != disable || after.MustChangePW != disable {
				t.Fatalf("edit did not commit every field: %+v", after)
			}
			if disable {
				password = testPassword
			}
			if !svc.VerifyUserPassword(t.Context(), user.ID, password) {
				t.Fatal("edit did not preserve the requested password")
			}
			var sessions int
			if err := svc.st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, user.ID).Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if sessions != 0 {
				t.Fatalf("old sessions survived: %d", sessions)
			}
			if _, _, err := svc.ResolveSession(t.Context(), login.Token); err == nil {
				t.Fatal("old session still resolves")
			}
		})
	}
}
