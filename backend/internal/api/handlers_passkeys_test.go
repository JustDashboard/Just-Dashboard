package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth/passkeytest"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The test requests are addressed to example.com, so that is the relying
// party a browser there would use.
const passkeyOrigin = "https://example.com"

var originHeader = map[string]string{"Origin": passkeyOrigin}

type beginAnswer struct {
	ChallengeID string `json:"challengeId"`
	PublicKey   struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

func decodeBegin(t *testing.T, body []byte) beginAnswer {
	t.Helper()
	var answer beginAnswer
	if err := json.Unmarshal(body, &answer); err != nil || answer.ChallengeID == "" || answer.PublicKey.Challenge == "" {
		t.Fatalf("begin answered %s (%v)", body, err)
	}
	return answer
}

func finishBody(challenge string, credential []byte, name string) string {
	body, _ := json.Marshal(map[string]any{"challengeId": challenge, "credential": json.RawMessage(credential), "name": name})
	return string(body)
}

// Passkeys through HTTP: registering takes a fresh password and code from a
// complete session; signing in with one on an account with an authenticator
// still asks for the code, and the session reaches nothing until it is given;
// a replayed challenge, another origin, a counter going backwards and a
// removed passkey are each refused; and every registration, sign-in, rename
// and removal is audited.
func TestPasskeysThroughTheAPIKeepTheSecondFactor(t *testing.T) {
	s := testServer(t)
	// Every request here comes from one address, and the journey signs in
	// more often than the sign-in budget allows a minute; that budget is
	// checked on its own below.
	s.loginLim = httpx.NewLimiter(600, 100)
	routes := s.Routes()
	ctx := t.Context()
	user, err := s.Auth.CreateUser(ctx, "keyholder", "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.Auth.Login(ctx, "keyholder", "Correct-Horse-Battery-9", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := s.Auth.BeginTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A code is accepted once and only for a later step than the last, within
	// a step either side of now: three in a row are all this test has.
	step := time.Now().Add(-60 * time.Second)
	nextCode := func() string {
		step = step.Add(30 * time.Second)
		code, _ := totp.GenerateCode(enrollment.Secret, step)
		return code
	}
	if _, err := s.Auth.ConfirmTOTPEnrollmentForSession(ctx, user.ID, login.SessionID, nextCode()); err != nil {
		t.Fatal(err)
	}
	signedIn := &client{t: t, h: routes, cookie: httpx.SessionCookie + "=" + login.Token}

	// Registering needs the password and the code again.
	begin := "/api/v1/account/passkeys/register/begin"
	if response := signedIn.do(http.MethodPost, begin, `{"password":"wrong","code":""}`, originHeader); response.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password = %d %s", response.Code, response.Body.String())
	}
	if response := signedIn.do(http.MethodPost, begin, `{"password":"Correct-Horse-Battery-9","code":""}`, originHeader); response.Code != http.StatusUnauthorized {
		t.Fatalf("no code from an enrolled account = %d %s", response.Code, response.Body.String())
	}
	if response := signedIn.do(http.MethodPost, begin, `{"password":"Correct-Horse-Battery-9","code":"000000"}`,
		map[string]string{"Origin": "https://203.0.113.9"}); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "IP address 203.0.113.9") {
		t.Fatalf("a passkey at an IP address = %d %s", response.Code, response.Body.String())
	}
	started := signedIn.do(http.MethodPost, begin, fmt.Sprintf(`{"password":"Correct-Horse-Battery-9","code":%q}`, nextCode()), originHeader)
	if started.Code != http.StatusOK {
		t.Fatalf("register begin = %d %s", started.Code, started.Body.String())
	}
	registration := decodeBegin(t, started.Body.Bytes())
	key := passkeytest.New()
	handle, _ := decodeURLBase64(registration.PublicKey.User.ID)
	attestation := key.Register(registration.PublicKey.Challenge, handle, "example.com", passkeyOrigin)
	created := signedIn.do(http.MethodPost, "/api/v1/account/passkeys/register/finish", finishBody(registration.ChallengeID, attestation, "Work laptop"), originHeader)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"name":"Work laptop"`) {
		t.Fatalf("register finish = %d %s", created.Code, created.Body.String())
	}
	var passkey auth.Passkey
	_ = json.Unmarshal(created.Body.Bytes(), &passkey)

	// Signing in with it on an enrolled account: the code is still owed.
	anonymous := &client{t: t, h: routes}
	signIn := func(key *passkeytest.Authenticator, origin string) (beginAnswer, string) {
		t.Helper()
		response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/begin", "", originHeader)
		if response.Code != http.StatusOK {
			t.Fatalf("login begin = %d %s", response.Code, response.Body.String())
		}
		answer := decodeBegin(t, response.Body.Bytes())
		return answer, finishBody(answer.ChallengeID, key.Assert(answer.PublicKey.Challenge, "example.com", origin), "")
	}
	_, body := signIn(key, passkeyOrigin)
	finished := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/finish", body, originHeader)
	if finished.Code != http.StatusOK || !strings.Contains(finished.Body.String(), `"needsTotp":true`) || !strings.Contains(finished.Body.String(), `"authenticated":false`) {
		t.Fatalf("passkey sign-in on an enrolled account = %d %s", finished.Code, finished.Body.String())
	}
	cookie := ""
	for _, value := range finished.Result().Cookies() {
		if value.Name == httpx.SessionCookie {
			cookie = value.Name + "=" + value.Value
		}
	}
	partial := &client{t: t, h: routes, cookie: cookie}
	if response := partial.do(http.MethodGet, "/api/v1/account/passkeys", "", nil); response.Code == http.StatusOK {
		t.Fatalf("a session owing its code reached a feature route: %d %s", response.Code, response.Body.String())
	}
	if response := partial.do(http.MethodGet, "/api/v1/auth/session", "", nil); !strings.Contains(response.Body.String(), `"needsTotp":true`) {
		t.Fatalf("session = %s", response.Body.String())
	}
	// The same response again: its challenge is spent.
	if response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/finish", body, originHeader); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "passkey_expired") {
		t.Fatalf("a replayed challenge = %d %s", response.Code, response.Body.String())
	}
	if response := partial.do(http.MethodPost, "/api/v1/auth/2fa/verify", fmt.Sprintf(`{"code":%q}`, nextCode()), nil); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"authenticated":true`) {
		t.Fatalf("the code after a passkey = %d %s", response.Code, response.Body.String())
	}
	if response := partial.do(http.MethodGet, "/api/v1/account/passkeys", "", nil); response.Code != http.StatusOK {
		t.Fatalf("after the code = %d %s", response.Code, response.Body.String())
	}

	// Another origin, at the door and inside the signed response.
	_, body = signIn(key, passkeyOrigin)
	if response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/finish", body, map[string]string{"Origin": "https://evil.example"}); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "passkey_unavailable") {
		t.Fatalf("a finish from another origin = %d %s", response.Code, response.Body.String())
	}
	_, body = signIn(key, "https://evil.example")
	if response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/finish", body, originHeader); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "passkey_rejected") {
		t.Fatalf("a response signed for another origin = %d %s", response.Code, response.Body.String())
	}
	// The counter going backwards.
	key.SignCount = 0
	_, body = signIn(key, passkeyOrigin)
	if response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/finish", body, originHeader); response.Code != http.StatusUnauthorized ||
		!strings.Contains(response.Body.String(), "passkey_cloned") {
		t.Fatalf("a regressed counter = %d %s", response.Code, response.Body.String())
	}
	key.SignCount = 10

	// Renamed, then removed: it signs in nowhere after.
	item := fmt.Sprintf("/api/v1/account/passkeys/%d", passkey.ID)
	if response := signedIn.do(http.MethodPatch, item, `{"name":"Old laptop"}`, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Old laptop") {
		t.Fatalf("rename = %d %s", response.Code, response.Body.String())
	}
	if response := signedIn.do(http.MethodDelete, item, "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", response.Code, response.Body.String())
	}
	_, body = signIn(key, passkeyOrigin)
	if response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/finish", body, originHeader); response.Code != http.StatusUnauthorized ||
		!strings.Contains(response.Body.String(), "passkey_unknown") {
		t.Fatalf("a removed passkey = %d %s", response.Code, response.Body.String())
	}

	// An API key cannot register one: the account routes take a session.
	_, apiToken, err := s.Auth.CreateAPIToken(ctx, user, "passkey-token", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if response := anonymous.do(http.MethodPost, begin, `{"password":"Correct-Horse-Battery-9"}`, map[string]string{"Authorization": "Bearer " + apiToken, "Origin": passkeyOrigin}); response.Code != http.StatusForbidden {
		t.Fatalf("an API key registering a passkey = %d %s", response.Code, response.Body.String())
	}

	var audits string
	if err := s.Store.DB.QueryRow(`
		SELECT COALESCE(group_concat(action || ' ' || success || ' ' || detail, char(10)), '')
		  FROM audit_log WHERE action LIKE 'auth.passkey.%'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"auth.passkey.reauth 0", "auth.passkey.reauth 1", `auth.passkey.register 1 {"passkey":"Work laptop"`,
		`auth.passkey.login 1 {"needsEnrollment":false,"needsTotp":true,"passkey":"Work laptop"`,
		"auth.passkey.login 0", "may have been copied", "not registered with any account",
		`auth.passkey.rename 1`, `auth.passkey.remove 1 {"passkey":"Old laptop"`,
	} {
		if !strings.Contains(audits, want) {
			t.Errorf("audit log lacks %q:\n%s", want, audits)
		}
	}
	if strings.Contains(audits, "Correct-Horse-Battery-9") {
		t.Fatalf("the audit log holds the password:\n%s", audits)
	}
}

// Finishing a passkey sign-in spends the password sign-in's budget per
// address; beginning one does not, so a sign-in page's autofill cannot use it
// up.
func TestPasskeySignInSpendsThePasswordSignInBudget(t *testing.T) {
	s := testServer(t)
	anonymous := &client{t: t, h: s.Routes()}
	for range 10 {
		if response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/begin", "", originHeader); response.Code != http.StatusOK {
			t.Fatalf("begin = %d %s", response.Code, response.Body.String())
		}
	}
	codes := []int{}
	for range 6 {
		response := anonymous.do(http.MethodPost, "/api/v1/auth/passkey/finish", `{"challengeId":"x","credential":{}}`, originHeader)
		codes = append(codes, response.Code)
	}
	if codes[len(codes)-1] != http.StatusTooManyRequests {
		t.Fatalf("finishes answered %v; the sixth within the burst of five should be refused", codes)
	}
	if response := anonymous.do(http.MethodPost, "/api/v1/auth/login", `{"username":"a","password":"b"}`, nil); response.Code != http.StatusTooManyRequests {
		t.Fatalf("a password sign-in after the passkey attempts = %d; the budget is shared", response.Code)
	}
}

// decodeURLBase64 reads the base64url the WebAuthn options carry, padded or
// not.
func decodeURLBase64(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
}
