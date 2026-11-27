package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
	"github.com/pquerna/otp/totp"
)

// Invariant 2: a temporary password grants nothing. The factors an account
// owes come first, then the password change, and until the change is made a
// session reaches its status, sign-out and the change itself. These go
// through the real router with a signed-in session of their own.

const (
	temporaryPassword   = "Temporary-Credential-9"
	replacementPassword = "Replacement-Credential-9"
)

// required2FAServer is testServer with JD_REQUIRE_2FA on, so an account
// without an authenticator signs in to a session that owes its enrolment.
func required2FAServer(t *testing.T) *Server {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("ef", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	cfg := &config.Config{
		Addr: "127.0.0.1:8080", DataDir: t.TempDir(), AllowedCIDRs: []*net.IPNet{loopback},
		SessionTTL: time.Hour, IdleTTL: time.Minute,
		FileRoots: []string{t.TempDir()}, LogRoots: []string{t.TempDir()},
		Require2FA: true,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.NewService(st, sealer, cfg.SessionTTL, cfg.IdleTTL, cfg.Require2FA)
	s := New(cfg, log, st, svc, sealer, audit.New(st, log), nil)
	t.Cleanup(s.Shutdown)
	return s
}

// signInThroughRouter signs in with POST /auth/login and returns a client
// holding the session it set, with the status the sign-in answered.
func signInThroughRouter(t *testing.T, s *Server, username, password string) (*client, authStatus) {
	t.Helper()
	c := &client{t: t, h: s.Routes()}
	w := c.do(http.MethodPost, "/api/v1/auth/login", `{"username":"`+username+`","password":"`+password+`"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == httpx.SessionCookie {
			c.cookie = httpx.SessionCookie + "=" + cookie.Value
		}
	}
	if c.cookie == "" {
		t.Fatalf("sign-in set no %s cookie", httpx.SessionCookie)
	}
	var st authStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return c, st
}

func sessionStatus(t *testing.T, c *client) authStatus {
	t.Helper()
	w := c.do(http.MethodGet, "/api/v1/auth/session", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /auth/session = %d %s", w.Code, w.Body.String())
	}
	var st authStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// refusedFor asserts the answer is the password change the session owes,
// and not some other 403 (a hub session's, a key scope's).
func refusedFor(t *testing.T, c *client, method, path, body string) {
	t.Helper()
	w := c.do(method, path, body, nil)
	if code, _ := errorCode(t, w.Body.Bytes()); w.Code != http.StatusForbidden || code != "password_change_required" {
		t.Fatalf("%s %s with a password change owed = %d %s, want 403 password_change_required", method, path, w.Code, w.Body.String())
	}
}

func TestACompleteSessionOwingAPasswordChangeCannotEnrolOrVerify(t *testing.T) {
	s := testServer(t)
	made, err := s.Auth.CreateUser(t.Context(), "temporary", temporaryPassword, auth.RoleAdmin, true)
	if err != nil {
		t.Fatal(err)
	}
	// JD_REQUIRE_2FA off and no authenticator: the password is every factor
	// the account owes, so the session is complete from the start.
	c, signedIn := signInThroughRouter(t, s, "temporary", temporaryPassword)
	if signedIn.Authenticated || !signedIn.NeedsPasswordChange || signedIn.NeedsTOTP || signedIn.NeedsEnroll {
		t.Fatalf("sign-in answered %+v, want only the password change", signedIn)
	}
	if st := sessionStatus(t, c); !st.NeedsPasswordChange || st.Authenticated {
		t.Fatalf("status answered %+v, want the password change", st)
	}

	// The second factor's routes and a passkey's enrolment, three rounds: the
	// account's guess budget has a burst of five, so had any refusal spent a
	// guess the password change below would be refused for it.
	for range 3 {
		refusedFor(t, c, http.MethodPost, "/api/v1/auth/2fa/setup", "{}")
		refusedFor(t, c, http.MethodPost, "/api/v1/auth/2fa/enable", `{"code":"000000"}`)
		refusedFor(t, c, http.MethodPost, "/api/v1/auth/2fa/verify", `{"code":"000000"}`)
	}
	refusedFor(t, c, http.MethodPost, "/api/v1/account/passkeys/register/begin", `{"password":"`+temporaryPassword+`"}`)
	refusedFor(t, c, http.MethodGet, "/api/v1/account/sessions", "")
	user, err := s.Auth.UserByID(t.Context(), made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.TOTPEnabled {
		t.Fatal("an authenticator was enrolled while a password change was owed")
	}

	if w := c.do(http.MethodPost, "/api/v1/account/password", `{"currentPassword":"`+temporaryPassword+`","newPassword":"`+replacementPassword+`"}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("password change = %d %s", w.Code, w.Body.String())
	}

	// Signed in again, the account can enrol from its settings like any other.
	c, signedIn = signInThroughRouter(t, s, "temporary", replacementPassword)
	if !signedIn.Authenticated || signedIn.NeedsPasswordChange {
		t.Fatalf("sign-in after the change answered %+v", signedIn)
	}
	if w := c.do(http.MethodPost, "/api/v1/auth/2fa/setup", "{}", nil); w.Code != http.StatusOK {
		t.Fatalf("enrolment after the change = %d %s", w.Code, w.Body.String())
	}
}

func TestUnderRequired2FAATemporaryPasswordEnrolsFirstAndThenChanges(t *testing.T) {
	s := required2FAServer(t)
	if _, err := s.Auth.CreateUser(t.Context(), "temporary", temporaryPassword, auth.RoleAdmin, true); err != nil {
		t.Fatal(err)
	}
	c, signedIn := signInThroughRouter(t, s, "temporary", temporaryPassword)
	if !signedIn.NeedsEnroll || signedIn.NeedsPasswordChange || signedIn.Authenticated {
		t.Fatalf("sign-in answered %+v, want the enrolment first", signedIn)
	}
	// Owing its factor, the session reaches neither the change nor a feature.
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/account/password", `{"currentPassword":"` + temporaryPassword + `","newPassword":"` + replacementPassword + `"}`},
		{http.MethodGet, "/api/v1/account/sessions", ""},
	} {
		w := c.do(request.method, request.path, request.body, nil)
		if code, _ := errorCode(t, w.Body.Bytes()); w.Code != http.StatusUnauthorized || code != "totp_enrollment_required" {
			t.Fatalf("%s %s before enrolment = %d %s", request.method, request.path, w.Code, w.Body.String())
		}
	}

	setup := c.do(http.MethodPost, "/api/v1/auth/2fa/setup", "{}", nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("enrolment setup = %d %s", setup.Code, setup.Body.String())
	}
	var secret auth.EnrollmentSecret
	if err := json.Unmarshal(setup.Body.Bytes(), &secret); err != nil || secret.Secret == "" {
		t.Fatalf("setup answered %s (%v)", setup.Body.String(), err)
	}
	code, err := totp.GenerateCode(secret.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w := c.do(http.MethodPost, "/api/v1/auth/2fa/enable", `{"code":"`+code+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("enrolment = %d %s", w.Code, w.Body.String())
	}

	// Enrolled, the session is complete and owes the change, and nothing but.
	if st := sessionStatus(t, c); !st.NeedsPasswordChange || st.Authenticated || st.NeedsTOTP || st.NeedsEnroll {
		t.Fatalf("status after enrolment answered %+v, want the password change", st)
	}
	refusedFor(t, c, http.MethodPost, "/api/v1/auth/2fa/setup", "{}")
	refusedFor(t, c, http.MethodPost, "/api/v1/auth/2fa/verify", `{"code":"`+code+`"}`)
	refusedFor(t, c, http.MethodGet, "/api/v1/account/sessions", "")
	if w := c.do(http.MethodPost, "/api/v1/account/password", `{"currentPassword":"`+temporaryPassword+`","newPassword":"`+replacementPassword+`"}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("password change after enrolment = %d %s", w.Code, w.Body.String())
	}

	// The new password signs in to the code the account now has, no change owed.
	_, signedIn = signInThroughRouter(t, s, "temporary", replacementPassword)
	if !signedIn.NeedsTOTP || signedIn.NeedsPasswordChange || signedIn.NeedsEnroll {
		t.Fatalf("sign-in after enrolment and change answered %+v, want the code", signedIn)
	}
}

func TestSignOutStaysOpenToASessionOwingAPasswordChange(t *testing.T) {
	s := testServer(t)
	if _, err := s.Auth.CreateUser(t.Context(), "temporary", temporaryPassword, auth.RoleAdmin, true); err != nil {
		t.Fatal(err)
	}
	c, _ := signInThroughRouter(t, s, "temporary", temporaryPassword)
	if w := c.do(http.MethodPost, "/api/v1/auth/logout", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("sign-out = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, "/api/v1/auth/2fa/setup", "{}", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("a signed-out session reached setup: %d %s", w.Code, w.Body.String())
	}
}
