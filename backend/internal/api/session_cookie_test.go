package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sessioncookie"
)

// The cookie a response set under name, or nil.
func setCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			found = c
		}
	}
	return found
}

func signInRequest(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	if _, err := s.Auth.CreateUser(t.Context(), "cookie", "Correct-Horse-Battery-9", "admin", false); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: s.Routes()}
	w := c.do(http.MethodPost, "/api/v1/auth/login", `{"username":"cookie","password":"Correct-Horse-Battery-9"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d %s", w.Code, w.Body.String())
	}
	return w
}

// Over HTTPS the session is a __Host- cookie: Secure, Path=/ and no Domain,
// which is what the prefix demands and what keeps a browser from sending it
// to any other name; HttpOnly and SameSite=Strict as before.
func TestSignInIssuesAHostPrefixedSessionCookie(t *testing.T) {
	w := signInRequest(t, testServer(t))
	cookie := setCookie(w, "__Host-jd_session")
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("no __Host-jd_session in %v", w.Result().Header.Values("Set-Cookie"))
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge <= 0 {
		t.Fatalf("cookie attributes = %+v", cookie)
	}
	if old := setCookie(w, sessioncookie.LegacyName); old != nil {
		t.Fatalf("sign-in set the legacy name too: %+v", old)
	}
}

// On loopback HTTP a browser refuses a __Host- cookie, which needs Secure, so
// the cookie there has the plain name and no Secure.
func TestSignInOverPlainHTTPIssuesThePlainName(t *testing.T) {
	s := testServer(t)
	s.Authn.Secure = false
	w := signInRequest(t, s)
	cookie := setCookie(w, "jd_session")
	if cookie == nil || cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("plain cookie = %+v in %v", cookie, w.Result().Header.Values("Set-Cookie"))
	}
	if setCookie(w, sessioncookie.Name) != nil {
		t.Fatal("a __Host- cookie was set over plain HTTP")
	}
}

// A browser still holding the cookie an earlier release set is not signed out
// by the upgrade: its first request is answered, the session is given the
// current name for what is left of its lifetime and the old cookie cleared,
// and from then on the current name alone is sent and nothing is re-issued.
func TestALegacySessionCookieIsRenamedOnItsFirstRequest(t *testing.T) {
	s := testServer(t)
	current := signIn(t, s)
	token := strings.TrimPrefix(current, httpx.SessionCookie+"=")
	routes := s.Routes()
	legacy := &client{t: t, h: routes, cookie: sessioncookie.LegacyName + "=" + token}

	w := legacy.do(http.MethodGet, "/api/v1/account/sessions", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("a legacy session on a feature route = %d %s", w.Code, w.Body.String())
	}
	renamed, cleared := setCookie(w, sessioncookie.Name), setCookie(w, sessioncookie.LegacyName)
	if renamed == nil || renamed.Value != token || renamed.MaxAge <= 0 || !renamed.Secure || !renamed.HttpOnly ||
		renamed.SameSite != http.SameSiteStrictMode || renamed.Path != "/" || renamed.Domain != "" {
		t.Fatalf("renamed = %+v", renamed)
	}
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("the legacy cookie was not cleared: %+v", cleared)
	}

	again := (&client{t: t, h: routes, cookie: sessioncookie.Name + "=" + token}).do(http.MethodGet, "/api/v1/account/sessions", "", nil)
	if again.Code != http.StatusOK || len(again.Result().Cookies()) != 0 {
		t.Fatalf("the renamed session = %d, set %v", again.Code, again.Result().Header.Values("Set-Cookie"))
	}

	// Both names sent, as from a browser whose clearing was lost: the
	// current one is the session, and the old one is cleared again.
	both := (&client{t: t, h: routes, cookie: sessioncookie.Name + "=" + token + "; " + sessioncookie.LegacyName + "=stale"}).
		do(http.MethodGet, "/api/v1/account/sessions", "", nil)
	if both.Code != http.StatusOK || setCookie(both, sessioncookie.Name) != nil {
		t.Fatalf("both names = %d, set %v", both.Code, both.Result().Header.Values("Set-Cookie"))
	}
	if cleared := setCookie(both, sessioncookie.LegacyName); cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("the stale legacy cookie stayed: %v", both.Result().Header.Values("Set-Cookie"))
	}

	// A legacy cookie that names no session is refused like any other, and
	// nothing is issued for it.
	unknown := (&client{t: t, h: routes, cookie: sessioncookie.LegacyName + "=not-a-session"}).do(http.MethodGet, "/api/v1/account/sessions", "", nil)
	if unknown.Code != http.StatusUnauthorized || setCookie(unknown, sessioncookie.Name) != nil {
		t.Fatalf("an unknown legacy cookie = %d, set %v", unknown.Code, unknown.Result().Header.Values("Set-Cookie"))
	}
}

// A session still owing its second factor is renamed too, on the routes it
// may reach, so the upgrade does not cost a sign-in half done.
func TestAPartialLegacySessionIsRenamedOnTheSecondFactorRoutes(t *testing.T) {
	s := testServer(t)
	signIn(t, s)
	login, err := s.Auth.Login(t.Context(), "tester", "Correct-Horse-Battery-9", "127.0.0.1", "pending")
	if err != nil || !login.NeedsTOTP {
		t.Fatalf("expected a partial login: %v", err)
	}
	legacy := &client{t: t, h: s.Routes(), cookie: sessioncookie.LegacyName + "=" + login.Token}
	w := legacy.do(http.MethodGet, "/api/v1/auth/session", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"needsTotp":true`) {
		t.Fatalf("session = %d %s", w.Code, w.Body.String())
	}
	if renamed := setCookie(w, sessioncookie.Name); renamed == nil || renamed.Value != login.Token {
		t.Fatalf("a partial legacy session was not renamed: %v", w.Result().Header.Values("Set-Cookie"))
	}
	// It is still a session owing its code under the new name.
	if w := legacy.do(http.MethodGet, "/api/v1/account/sessions", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("a partial session reached a feature route: %d %s", w.Code, w.Body.String())
	}
}

// Signing out clears the cookie under both names, so a browser that kept the
// old one is signed out of it as well.
func TestSignOutClearsBothNames(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s) + "; " + sessioncookie.LegacyName + "=old"}
	w := c.do(http.MethodPost, "/api/v1/auth/logout", "", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{sessioncookie.Name, sessioncookie.LegacyName} {
		cookie := setCookie(w, name)
		if cookie == nil || cookie.Value != "" || cookie.MaxAge >= 0 || cookie.Path != "/" || !cookie.HttpOnly {
			t.Fatalf("%s after logout = %+v in %v", name, cookie, w.Result().Header.Values("Set-Cookie"))
		}
	}
	if w := c.do(http.MethodGet, "/api/v1/account/sessions", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("the session survived logout: %d", w.Code)
	}
}
