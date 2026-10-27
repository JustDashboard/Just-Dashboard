package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth/passkeytest"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
)

var testParty = RelyingParty{ID: "dash.example.test", Origin: "https://dash.example.test"}

// registerPasskey drives a registration from an existing complete session.
func registerPasskey(t *testing.T, svc *Service, user *User, sessionID string) *passkeytest.Authenticator {
	t.Helper()
	creation, challenge, err := svc.BeginPasskeyRegistration(t.Context(), user.ID, sessionID, testParty)
	if err != nil {
		t.Fatal(err)
	}
	if creation.Response.AuthenticatorSelection.UserVerification != "required" ||
		creation.Response.AuthenticatorSelection.ResidentKey != "required" {
		t.Fatalf("a passkey is discoverable and verifies its user: %+v", creation.Response.AuthenticatorSelection)
	}
	key := passkeytest.New()
	body := key.Register(creation.Response.Challenge.String(), []byte(creation.Response.User.ID.(protocol.URLEncodedBase64)), testParty.ID, testParty.Origin)
	if _, err := svc.FinishPasskeyRegistration(t.Context(), user.ID, sessionID, challenge, "Work laptop", testParty, body); err != nil {
		t.Fatal(err)
	}
	return key
}

// signInWithPasskey begins and finishes a discoverable sign-in.
func signInWithPasskey(t *testing.T, svc *Service, key *passkeytest.Authenticator, rp RelyingParty) (*LoginResult, *Passkey, error) {
	t.Helper()
	assertion, challenge, err := svc.BeginPasskeyLogin(t.Context(), testParty)
	if err != nil {
		t.Fatal(err)
	}
	body := key.Assert(assertion.Response.Challenge.String(), rp.ID, rp.Origin)
	return svc.FinishPasskeyLogin(t.Context(), challenge, testParty, body, "127.0.0.1", "test")
}

func passwordSession(t *testing.T, svc *Service, user *User) *LoginResult {
	t.Helper()
	login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	return login
}

func TestAPasskeyReplacesThePasswordAndNothingElse(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	key := registerPasskey(t, svc, user, login.SessionID)

	// No authenticator and no policy: a passkey signs straight in.
	result, used, err := signInWithPasskey(t, svc, key, testParty)
	if err != nil || result.NeedsTOTP || result.NeedsEnroll || used.Name != "Work laptop" {
		t.Fatalf("passkey sign-in = %+v %+v %v", result, used, err)
	}
	session, _, err := svc.ResolveSession(t.Context(), result.Token)
	if err != nil || !session.TwoFAPassed || session.AuthMethod != "passkey" {
		t.Fatalf("session = %+v, %v", session, err)
	}

	// With an authenticator enrolled, the passkey still owes the code: the
	// session is half-authenticated until the code, and says so afterwards.
	enrollment, err := svc.BeginTOTPEnrollment(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(enrollment.Secret, time.Now())
	if _, err := svc.ConfirmTOTPEnrollmentForSession(t.Context(), user.ID, login.SessionID, code); err != nil {
		t.Fatal(err)
	}
	result, _, err = signInWithPasskey(t, svc, key, testParty)
	if err != nil || !result.NeedsTOTP {
		t.Fatalf("an enrolled account's passkey sign-in = %+v, %v; want the code asked for", result, err)
	}
	session, _, err = svc.ResolveSession(t.Context(), result.Token)
	if err != nil || session.TwoFAPassed {
		t.Fatalf("a passkey alone completed an enrolled account's session: %+v, %v", session, err)
	}
	next := time.Now().Add(30 * time.Second)
	code, _ = totp.GenerateCode(enrollment.Secret, next)
	if err := svc.VerifySecondFactor(t.Context(), result.SessionID, user.ID, code); err != nil {
		t.Fatal(err)
	}
	session, _, _ = svc.ResolveSession(t.Context(), result.Token)
	if !session.TwoFAPassed || session.AuthMethod != "passkey+totp" {
		t.Fatalf("after the code = %+v", session)
	}
}

func TestRequire2FAKeepsItsMeaningForAPasskey(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	key := registerPasskey(t, svc, user, login.SessionID)
	svc.require2FA = true
	result, _, err := signInWithPasskey(t, svc, key, testParty)
	if err != nil || !result.NeedsEnroll || result.NeedsTOTP {
		t.Fatalf("an unenrolled account under the policy = %+v, %v; want enrolment owed", result, err)
	}
	session, _, err := svc.ResolveSession(t.Context(), result.Token)
	if err != nil || session.TwoFAPassed {
		t.Fatalf("the policy's enrolment was skipped by a passkey: %+v, %v", session, err)
	}
}

func TestAPasskeyCeremonyIsTakenOnceAndOnlyWhereItWasStarted(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	key := registerPasskey(t, svc, user, login.SessionID)

	assertion, challenge, err := svc.BeginPasskeyLogin(t.Context(), testParty)
	if err != nil {
		t.Fatal(err)
	}
	body := key.Assert(assertion.Response.Challenge.String(), testParty.ID, testParty.Origin)
	if _, _, err := svc.FinishPasskeyLogin(t.Context(), challenge, testParty, body, "127.0.0.1", "test"); err != nil {
		t.Fatal(err)
	}
	// The same response again: the challenge is gone.
	if _, _, err := svc.FinishPasskeyLogin(t.Context(), challenge, testParty, body, "127.0.0.1", "test"); !errors.Is(err, ErrPasskeyChallenge) {
		t.Fatalf("a replayed challenge = %v", err)
	}
	// The old signed response against a new challenge: it signed the old one.
	_, fresh, _ := svc.BeginPasskeyLogin(t.Context(), testParty)
	if _, _, err := svc.FinishPasskeyLogin(t.Context(), fresh, testParty, body, "127.0.0.1", "test"); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("a replayed assertion = %v", err)
	}
	// Finished at another origin than it was begun at.
	_, other, _ := svc.BeginPasskeyLogin(t.Context(), testParty)
	elsewhere := RelyingParty{ID: "other.example.test", Origin: "https://other.example.test"}
	if _, _, err := svc.FinishPasskeyLogin(t.Context(), other, elsewhere, body, "127.0.0.1", "test"); !errors.Is(err, ErrPasskeyChallenge) {
		t.Fatalf("a ceremony finished elsewhere = %v", err)
	}
	// An expired one: the seal holds its expiry, so it is moved back here
	// by sealing an envelope that has already run out.
	assertion, _, _ = svc.BeginPasskeyLogin(t.Context(), testParty)
	data := webauthnSession(assertion.Response.Challenge.String())
	stale, err := svc.sealChallenge("login", 0, "", testParty, data)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := svc.sealer.Open(stale)
	stale, _ = svc.sealer.Seal(strings.Replace(plain, `"expires":`, `"expires":1,"was":`, 1))
	expired := key.Assert(assertion.Response.Challenge.String(), testParty.ID, testParty.Origin)
	if _, _, err := svc.FinishPasskeyLogin(t.Context(), stale, testParty, expired, "", ""); !errors.Is(err, ErrPasskeyChallenge) {
		t.Fatalf("an expired challenge = %v", err)
	}
	// One the browser altered does not open.
	if _, _, err := svc.FinishPasskeyLogin(t.Context(), challenge[:len(challenge)-4]+"AAAA", testParty, body, "", ""); !errors.Is(err, ErrPasskeyChallenge) {
		t.Fatalf("an altered challenge = %v", err)
	}
	// A registration begun by one session cannot be finished by another.
	other2 := passwordSession(t, svc, user)
	creation, registration, err := svc.BeginPasskeyRegistration(t.Context(), user.ID, login.SessionID, testParty)
	if err != nil {
		t.Fatal(err)
	}
	second := passkeytest.New()
	attestation := second.Register(creation.Response.Challenge.String(), []byte(creation.Response.User.ID.(protocol.URLEncodedBase64)), testParty.ID, testParty.Origin)
	if _, err := svc.FinishPasskeyRegistration(t.Context(), user.ID, other2.SessionID, registration, "Phone", testParty, attestation); !errors.Is(err, ErrPasskeyChallenge) {
		t.Fatalf("a registration finished by another session = %v", err)
	}
}

func webauthnSession(challenge string) *webauthn.SessionData {
	return &webauthn.SessionData{Challenge: challenge, RelyingPartyID: testParty.ID, UserVerification: protocol.VerificationRequired,
		Expires: time.Now().Add(time.Minute)}
}

func TestAPasskeyIsRefusedForTheWrongOriginOrRelyingParty(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	key := registerPasskey(t, svc, user, login.SessionID)
	for name, rp := range map[string]RelyingParty{
		"another origin":         {ID: testParty.ID, Origin: "https://evil.example.test"},
		"another relying party":  {ID: "evil.example.test", Origin: testParty.Origin},
		"plain http of the same": {ID: testParty.ID, Origin: "http://dash.example.test"},
	} {
		if _, _, err := signInWithPasskey(t, svc, key, rp); !errors.Is(err, ErrPasskeyRejected) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A credential registered at one name signs in nowhere else.
	other := RelyingParty{ID: "vps.tail0000.ts.net", Origin: "https://vps.tail0000.ts.net"}
	assertion, challenge, _ := svc.BeginPasskeyLogin(t.Context(), other)
	body := key.Assert(assertion.Response.Challenge.String(), other.ID, other.Origin)
	if _, _, err := svc.FinishPasskeyLogin(t.Context(), challenge, other, body, "", ""); !errors.Is(err, ErrPasskeyUnknown) || !strings.Contains(err.Error(), "registered at dash.example.test") {
		t.Fatalf("a passkey used at another name = %v", err)
	}
}

func TestACounterThatGoesBackwardsIsRefused(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	key := registerPasskey(t, svc, user, login.SessionID)
	if _, _, err := signInWithPasskey(t, svc, key, testParty); err != nil {
		t.Fatal(err)
	}
	if _, _, err := signInWithPasskey(t, svc, key, testParty); err != nil {
		t.Fatal(err)
	}
	key.SignCount = 0 // the next assertion reports 1, below the 2 already seen
	if _, _, err := signInWithPasskey(t, svc, key, testParty); !errors.Is(err, ErrPasskeyCloned) {
		t.Fatalf("a regressed counter = %v", err)
	}
}

func TestARemovedPasskeyAndADisabledAccountSignInNowhere(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	key := registerPasskey(t, svc, user, login.SessionID)
	keys, err := svc.ListPasskeys(t.Context(), user.ID)
	if err != nil || len(keys) != 1 || keys[0].RPID != testParty.ID {
		t.Fatalf("list = %+v, %v", keys, err)
	}
	if err := svc.SetDisabled(t.Context(), user.ID, true); err != nil {
		// The only admin cannot be disabled; make a second one first.
		other, cerr := svc.CreateUser(t.Context(), "second", testPassword, RoleAdmin, false)
		if cerr != nil {
			t.Fatal(cerr)
		}
		_ = other
		if err := svc.SetDisabled(t.Context(), user.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := signInWithPasskey(t, svc, key, testParty); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("a disabled account's passkey = %v", err)
	}
	if err := svc.SetDisabled(t.Context(), user.ID, false); err != nil {
		t.Fatal(err)
	}
	renamed, err := svc.RenamePasskey(t.Context(), user.ID, keys[0].ID, "  Old   laptop ")
	if err != nil || renamed.Name != "Old laptop" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	if _, err := svc.RemovePasskey(t.Context(), user.ID, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := signInWithPasskey(t, svc, key, testParty); !errors.Is(err, ErrPasskeyUnknown) {
		t.Fatalf("a removed passkey = %v", err)
	}
}

func TestAnAuthenticatorThatDoesNotVerifyItsUserIsRefused(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	creation, challenge, err := svc.BeginPasskeyRegistration(t.Context(), user.ID, login.SessionID, testParty)
	if err != nil {
		t.Fatal(err)
	}
	key := passkeytest.New()
	key.UserVerified = false
	body := key.Register(creation.Response.Challenge.String(), []byte(creation.Response.User.ID.(protocol.URLEncodedBase64)), testParty.ID, testParty.Origin)
	if _, err := svc.FinishPasskeyRegistration(t.Context(), user.ID, login.SessionID, challenge, "Key", testParty, body); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("a registration without user verification = %v", err)
	}
	verified := registerPasskey(t, svc, user, login.SessionID)
	verified.UserVerified = false
	if _, _, err := signInWithPasskey(t, svc, verified, testParty); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("a sign-in without user verification = %v", err)
	}
}

func TestAResetFromARootShellRemovesTheAccountsPasskeys(t *testing.T) {
	svc, user := newTestService(t, false)
	login := passwordSession(t, svc, user)
	key := registerPasskey(t, svc, user, login.SessionID)
	// Changing one's own password keeps them; the root shell's reset does not.
	if err := svc.SetPassword(t.Context(), user.ID, "Another-Horse-Battery-9"); err != nil {
		t.Fatal(err)
	}
	if count, _ := svc.CountPasskeys(t.Context(), user.ID); count != 1 {
		t.Fatalf("a password change removed passkeys: %d left", count)
	}
	if err := svc.ResetPassword(t.Context(), user.ID, "Recovered-Horse-Battery-9"); err != nil {
		t.Fatal(err)
	}
	if count, _ := svc.CountPasskeys(t.Context(), user.ID); count != 0 {
		t.Fatalf("a reset left %d passkeys", count)
	}
	if _, _, err := signInWithPasskey(t, svc, key, testParty); !errors.Is(err, ErrPasskeyUnknown) {
		t.Fatalf("a passkey after a reset = %v", err)
	}
}

func TestReauthenticationNeedsThePasswordAndAFreshCode(t *testing.T) {
	svc, user := newTestService(t, false)
	if err := svc.VerifyReauthentication(t.Context(), user.ID, "wrong", ""); !errors.Is(err, ErrReauthentication) {
		t.Fatalf("a wrong password = %v", err)
	}
	if err := svc.VerifyReauthentication(t.Context(), user.ID, testPassword, ""); err != nil {
		t.Fatalf("an unenrolled account's password = %v", err)
	}
	login := passwordSession(t, svc, user)
	enrollment, _ := svc.BeginTOTPEnrollment(t.Context(), user.ID)
	code, _ := totp.GenerateCode(enrollment.Secret, time.Now())
	if _, err := svc.ConfirmTOTPEnrollmentForSession(t.Context(), user.ID, login.SessionID, code); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyReauthentication(t.Context(), user.ID, testPassword, ""); !errors.Is(err, ErrReauthentication) {
		t.Fatalf("an enrolled account without its code = %v", err)
	}
	next, _ := totp.GenerateCode(enrollment.Secret, time.Now().Add(30*time.Second))
	if err := svc.VerifyReauthentication(t.Context(), user.ID, testPassword, next); err != nil {
		t.Fatalf("password and code = %v", err)
	}
	if err := svc.VerifyReauthentication(t.Context(), user.ID, testPassword, next); !errors.Is(err, ErrReauthentication) {
		t.Fatalf("the same code twice = %v", err)
	}
}

func TestPasskeyPartyIsTheNameTheDashboardIsServedAt(t *testing.T) {
	for _, tc := range []struct {
		origin, host string
		allowed      []string
		want         RelyingParty
		refused      string
	}{
		{origin: "https://dash.example.test", host: "dash.example.test", want: RelyingParty{ID: "dash.example.test", Origin: "https://dash.example.test"}},
		{origin: "https://Dash.Example.test:8443", host: "dash.example.test:8443", want: RelyingParty{ID: "dash.example.test", Origin: "https://dash.example.test:8443"}},
		{origin: "http://localhost:43252", host: "localhost:43252", want: RelyingParty{ID: "localhost", Origin: "http://localhost:43252"}},
		{origin: "https://vps.tail0000.ts.net", host: "127.0.0.1:8080", allowed: []string{"https://vps.tail0000.ts.net"}, want: RelyingParty{ID: "vps.tail0000.ts.net", Origin: "https://vps.tail0000.ts.net"}},
		{origin: "https://100.64.0.7", host: "100.64.0.7", refused: "IP address 100.64.0.7"},
		{origin: "https://[fd7a::1]", host: "[fd7a::1]", refused: "IP address fd7a::1"},
		{origin: "http://dash.example.test", host: "dash.example.test", refused: "need https"},
		{origin: "https://evil.example.test", host: "dash.example.test", refused: "not where this dashboard is served"},
		{origin: "", host: "dash.example.test", refused: "no origin"},
	} {
		got, err := PasskeyParty(tc.origin, tc.host, tc.allowed)
		if tc.refused != "" {
			if !errors.Is(err, ErrPasskeyOrigin) || !strings.Contains(err.Error(), tc.refused) {
				t.Fatalf("%s at %s = %+v, %v; want %q", tc.origin, tc.host, got, err, tc.refused)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s at %s = %+v, %v; want %+v", tc.origin, tc.host, got, err, tc.want)
		}
	}
}
