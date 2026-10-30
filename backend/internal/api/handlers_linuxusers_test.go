package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/go-chi/chi/v5"
)

// usersHost is the server with its linuxusers module over a made-up /etc and a
// few lines standing in for the shadow suite. Nothing a route does here can reach
// this machine's accounts, and a test sees every tool the route asked for.
type usersHost struct {
	t     *testing.T
	s     *Server
	etc   string
	homes string
	admin *client
	calls []string
	// sshd is what `sshd -T` prints.
	sshd string
	// refuse makes one tool fail.
	refuse map[string]error
}

// fixtureUID is the account number the fixtures use. It has to be this process's,
// or the ownership checks on .ssh cannot pass without root.
func fixtureUID() int {
	if uid := os.Getuid(); uid != 0 {
		return uid
	}
	return 1000
}

func newUsersHost(t *testing.T) *usersHost {
	t.Helper()
	h := &usersHost{t: t, s: testServer(t), etc: t.TempDir(), homes: t.TempDir(),
		sshd:   "permitrootlogin yes\npasswordauthentication yes\npubkeyauthentication yes\nusepam yes\n",
		refuse: map[string]error{}}
	uid := strconv.Itoa(fixtureUID())
	files := map[string]string{
		"passwd": "root:x:0:0:root:" + h.homes + "/root:/bin/bash\n" +
			"daemon:x:1:1:daemon:" + h.homes + "/daemon:/usr/sbin/nologin\n" +
			"_apt:x:42:65534::" + h.homes + "/apt:/usr/sbin/nologin\n" +
			"ion:x:" + uid + ":" + uid + "::" + h.homes + "/ion:/bin/bash\n" +
			// A number of its own: two accounts sharing one is a state the code
			// refuses to act on. Nothing here writes a key file as mira.
			"mira:x:" + strconv.Itoa(fixtureUID()+1) + ":" + strconv.Itoa(fixtureUID()+1) + "::" + h.homes + "/mira:/bin/bash\n",
		"group":   "root:x:0:\ndaemon:x:1:\nsudo:x:27:\nnogroup:x:65534:\nion:x:" + uid + ":\n",
		"shadow":  "root:!:20000:0:99999:7:::\ndaemon:*:20000:0:99999:7:::\n_apt:*:20000:0:99999:7:::\nion:!:20000:0:99999:7:::\nmira:!:20000:0:99999:7:::\n",
		"shells":  "/bin/sh\n/bin/bash\n",
		"sudoers": "%sudo ALL=(ALL:ALL) ALL\n@includedir sudoers.d\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(h.etc, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(h.etc, "sudoers.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{"ion", "mira", "root"} {
		if err := os.Mkdir(filepath.Join(h.homes, home), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.s.modules.linuxUsers = linuxusers.NewForTest(h.etc, h.run)
	h.admin = &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	return h
}

func (h *usersHost) editFile(name string, edit func(string) string) {
	h.t.Helper()
	path := filepath.Join(h.etc, name)
	b, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(b))), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *usersHost) setHash(user, hash string) {
	h.editFile("shadow", func(content string) string {
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if fields := strings.Split(line, ":"); len(fields) > 1 && fields[0] == user {
				fields[1] = hash
				lines[i] = strings.Join(fields, ":")
			}
		}
		return strings.Join(lines, "\n")
	})
}

func dropLine(content, prefix string) string {
	kept := []string{}
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, prefix) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func (h *usersHost) run(_ context.Context, stdin, name string, args ...string) (string, error) {
	h.calls = append(h.calls, name+" "+strings.Join(args, " "))
	if err := h.refuse[name]; err != nil {
		return "", err
	}
	last := ""
	if len(args) > 0 {
		last = args[len(args)-1]
	}
	switch {
	case strings.HasSuffix(name, "sshd"):
		return h.sshd, nil
	case name == "useradd":
		h.editFile("passwd", func(c string) string {
			uid := strconv.Itoa(fixtureUID() + 100 + strings.Count(c, "\n"))
			return c + last + ":x:" + uid + ":" + uid + "::" + h.homes + "/" + last + ":/bin/bash\n"
		})
		h.editFile("shadow", func(c string) string { return c + last + ":!:20000:0:99999:7:::\n" })
		_ = os.Mkdir(filepath.Join(h.homes, last), 0o755)
	case name == "userdel":
		h.editFile("passwd", func(c string) string { return dropLine(c, last+":") })
		h.editFile("shadow", func(c string) string { return dropLine(c, last+":") })
	case name == "chpasswd":
		user, _, _ := strings.Cut(strings.TrimSpace(stdin), ":")
		h.setHash(user, "$y$fake$"+strconv.Itoa(len(h.calls)))
	case name == "usermod" && args[0] == "--password":
		h.setHash(last, args[1])
	}
	return "", nil
}

func (h *usersHost) ran(prefix string) bool {
	for _, call := range h.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

func usersErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not an error: %d %s", w.Code, w.Body.String())
	}
	return body.Error.Code
}

// The whole section sits behind system.admin, reads included: the routes that exist
// now, whatever they are, and any added later.
func TestEverySystemUsersRouteIsRefusedBelowSystemAdmin(t *testing.T) {
	h := newUsersHost(t)
	routes := h.s.Routes()
	walked := 0
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		caller := &client{t: t, h: routes, cookie: signInAs(t, h.s, "caller-"+string(role), role)}
		for _, rt := range apiRoutes(t, routes) {
			if !strings.HasPrefix(rt.pattern, "/api/v1/system-users") && !strings.HasPrefix(rt.pattern, "/api/v1/system-accounts") {
				continue
			}
			walked++
			w := caller.do(rt.method, rt.path, `{}`, nil)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d %s", rt.method, rt.pattern, role, w.Code, w.Body.String())
			}
		}
	}
	if walked < 2*30 {
		t.Fatalf("only %d route checks: the walk is not seeing the section", walked)
	}
	if len(h.calls) != 0 {
		t.Fatalf("a refused request reached a tool: %v", h.calls)
	}
}

func destructiveRoutesForTest(t *testing.T, h http.Handler) map[string]bool {
	t.Helper()
	routes, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("Routes() no longer returns something chi can walk")
	}
	out := map[string]bool{}
	err := chi.Walk(routes, func(method, pattern string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if isDestructive(middlewares) {
			out[method+" "+pattern] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Everything that destroys something takes the destructive budget, not just the
// capability (invariant 3).
func TestWhatDestroysIsBehindTheDestructiveGroup(t *testing.T) {
	h := newUsersHost(t)
	want := map[string]bool{
		"DELETE /api/v1/system-users/{name}/":           true,
		"DELETE /api/v1/system-users/{name}/keys":       true,
		"DELETE /api/v1/system-users/{name}/password":   true,
		"DELETE /api/v1/system-users/{name}/sessions":   true,
		"DELETE /api/v1/system-users/{name}/sudo/rules": true,
		"DELETE /api/v1/system-accounts/groups/{group}": true,
		"POST /api/v1/system-accounts/bulk/delete":      true,
	}
	destructive := destructiveRoutesForTest(t, h.s.Routes())
	for route := range want {
		if !destructive[route] {
			t.Errorf("%s is not behind s.destructive", route)
		}
	}
	for route := range destructive {
		if strings.Contains(route, "/system-users") || strings.Contains(route, "/system-accounts") {
			if !want[route] {
				t.Errorf("%s is destructive and this test does not know why", route)
			}
		}
	}
}

func TestADaemonsAccountIsReadOnlyUntilTheRequestSaysItMeansIt(t *testing.T) {
	h := newUsersHost(t)
	if w := h.admin.do(http.MethodGet, "/api/v1/system-users/_apt", "", nil); w.Code != http.StatusOK {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	body := `{"comment":"changed"}`
	w := h.admin.do(http.MethodPatch, "/api/v1/system-users/_apt", body, nil)
	if w.Code != http.StatusForbidden || usersErrorCode(t, w) != "system_account" {
		t.Fatalf("write: %d %s", w.Code, w.Body.String())
	}
	if h.ran("usermod") {
		t.Fatal("usermod was asked")
	}
	w = h.admin.do(http.MethodPatch, "/api/v1/system-users/_apt?allowSystem=true", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("with allowSystem: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("usermod --comment changed _apt") {
		t.Fatalf("calls = %v", h.calls)
	}

	// The same goes for every kind of write below the account.
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/system-users/_apt/keys", `{"key":"x"}`},
		{http.MethodPut, "/api/v1/system-users/_apt/sudo", `{"level":"nopassword"}`},
		{http.MethodPut, "/api/v1/system-users/_apt/limits", `{"tasks":64}`},
		{http.MethodPost, "/api/v1/system-users/_apt/password", `{"generate":true}`},
		{http.MethodDelete, "/api/v1/system-users/_apt", ""},
	} {
		if w := h.admin.do(req.method, req.path, req.body, nil); w.Code != http.StatusForbidden || usersErrorCode(t, w) != "system_account" {
			t.Errorf("%s %s: %d %s", req.method, req.path, w.Code, w.Body.String())
		}
	}

	// Root is a system account by its number and is not a daemon's.
	if w := h.admin.do(http.MethodPatch, "/api/v1/system-users/root", `{"comment":"Superuser"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("root: %d %s", w.Code, w.Body.String())
	}
	// What must not happen to it is refused however the request is worded.
	w = h.admin.do(http.MethodPatch, "/api/v1/system-users/root?allowSystem=true", `{"locked":true}`, nil)
	if w.Code != http.StatusForbidden || usersErrorCode(t, w) != "protected_account" {
		t.Fatalf("lock root: %d %s", w.Code, w.Body.String())
	}
	if w := h.admin.do(http.MethodPatch, "/api/v1/system-users/ion", `{"comment":"Ion"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("a person: %d %s", w.Code, w.Body.String())
	}
}

func TestRootIsListedWhateverTheFilterAndDaemonsOnlyWhenAsked(t *testing.T) {
	h := newUsersHost(t)
	names := func(path string) []string {
		w := h.admin.do(http.MethodGet, path, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var users []linuxusers.User
		if err := json.Unmarshal(w.Body.Bytes(), &users); err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, u := range users {
			out = append(out, u.Username)
		}
		return out
	}
	if got := strings.Join(names("/api/v1/system-users/"), ","); got != "ion,mira,root" {
		t.Fatalf("default = %s", got)
	}
	if got := strings.Join(names("/api/v1/system-users/?system=true"), ","); !strings.Contains(got, "_apt") || !strings.Contains(got, "daemon") {
		t.Fatalf("with system = %s", got)
	}
}

func TestDeletingAHomeTakesTheTypedNameAndKeepingItDoesNot(t *testing.T) {
	h := newUsersHost(t)
	for _, query := range []string{"home=delete", "removeHome=true"} {
		w := h.admin.do(http.MethodDelete, "/api/v1/system-users/ion?"+query, "", nil)
		if w.Code == http.StatusOK || usersErrorCode(t, w) != "confirmation_required" {
			t.Fatalf("%s with no phrase: %d %s", query, w.Code, w.Body.String())
		}
		w = h.admin.do(http.MethodDelete, "/api/v1/system-users/ion?"+query, "", map[string]string{"X-Confirm": "mira"})
		if w.Code == http.StatusOK {
			t.Fatalf("%s with the wrong phrase: %d %s", query, w.Code, w.Body.String())
		}
	}
	if h.ran("userdel") {
		t.Fatal("userdel was asked")
	}
	w := h.admin.do(http.MethodDelete, "/api/v1/system-users/ion?home=delete", "", map[string]string{"X-Confirm": "ion"})
	if w.Code != http.StatusOK {
		t.Fatalf("with the phrase: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("userdel --remove ion") {
		t.Fatalf("calls = %v", h.calls)
	}
	// Keeping the home loses nothing that cannot be put back.
	w = h.admin.do(http.MethodDelete, "/api/v1/system-users/mira?home=keep", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("keep: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("userdel mira") {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestAnAPIKeyCannotMintACredential(t *testing.T) {
	h := newUsersHost(t)
	user, err := h.s.Auth.CreateUser(t.Context(), "keyholder", "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := h.s.Auth.CreateAPIToken(t.Context(), user, "scripts", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + secret}
	key := &client{t: t, h: h.s.Routes()}
	for _, req := range []struct{ path, body string }{
		{"/api/v1/system-users/ion/password", `{"generate":true}`},
		{"/api/v1/system-users/ion/keys/generate", `{"comment":"x"}`},
		{"/api/v1/system-users/", `{"username":"new","generatePassword":true}`},
	} {
		w := key.do(http.MethodPost, req.path, req.body, bearer)
		if w.Code != http.StatusForbidden || usersErrorCode(t, w) != "session_required" {
			t.Errorf("%s: %d %s", req.path, w.Code, w.Body.String())
		}
	}
	if h.ran("chpasswd") || h.ran("useradd") {
		t.Fatalf("calls = %v", h.calls)
	}
	// A key can still read, and everything else a key is for.
	if w := key.do(http.MethodGet, "/api/v1/system-users/", "", bearer); w.Code != http.StatusOK {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
}

func auditEverything(t *testing.T, s *Server) string {
	t.Helper()
	rows, err := s.Store.DB.Query(`SELECT action, target, path, COALESCE(detail, '') FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var action, target, path, detail string
		if err := rows.Scan(&action, &target, &path, &detail); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%s %s %s %s\n", action, target, path, detail)
	}
	return out.String()
}

func TestNoPasswordOrPrivateKeyReachesTheAuditLog(t *testing.T) {
	h := newUsersHost(t)
	const typed = "a-password-somebody-typed"
	w := h.admin.do(http.MethodPost, "/api/v1/system-users/ion/password", `{"password":"`+typed+`"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), typed) {
		t.Fatal("the response repeats the password")
	}

	w = h.admin.do(http.MethodPost, "/api/v1/system-users/mira/password", `{"generate":true,"mustChange":true}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	var generated struct {
		GeneratedPassword string `json:"generatedPassword"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &generated); err != nil || len(generated.GeneratedPassword) < 16 {
		t.Fatalf("generated = %+v, %v", generated, err)
	}

	w = h.admin.do(http.MethodPost, "/api/v1/system-users/ion/keys/generate", `{"comment":"laptop","passphrase":"a-passphrase-typed"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	var key linuxusers.GeneratedKey
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil || !strings.Contains(key.PrivateKey, "PRIVATE KEY") {
		t.Fatalf("key = %+v, %v", key, err)
	}

	w = h.admin.do(http.MethodPost, "/api/v1/system-users/", `{"username":"newbie","password":"another-typed-password"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	log := auditEverything(t, h.s)
	for _, secret := range []string{typed, generated.GeneratedPassword, "a-passphrase-typed", "another-typed-password", "PRIVATE KEY", strings.TrimSpace(strings.Split(key.PrivateKey, "\n")[1])} {
		if strings.Contains(log, secret) {
			t.Errorf("the audit log holds %q:\n%s", secret, log)
		}
	}
	for _, action := range []string{"system.user.password.set", "system.user.sshkey.generate", "system.user.create"} {
		if !strings.Contains(log, action) {
			t.Errorf("no %s entry:\n%s", action, log)
		}
	}
}

func TestAnAccountNobodyCanSignInToHasToBeAskedFor(t *testing.T) {
	h := newUsersHost(t)
	w := h.admin.do(http.MethodPost, "/api/v1/system-users/", `{"username":"zoro","shell":"/bin/bash","groups":["sudo"]}`, nil)
	if w.Code != http.StatusBadRequest || usersErrorCode(t, w) != "no_way_in" {
		t.Fatalf("no way in: %d %s", w.Code, w.Body.String())
	}
	if h.ran("useradd") {
		t.Fatal("useradd was asked")
	}
	// Saying so is enough, and a service has no sign-in to want.
	for _, body := range []string{
		`{"username":"zoro","shell":"/bin/bash","noSignIn":true}`,
		`{"username":"svc1","shell":"/usr/sbin/nologin"}`,
		`{"username":"svc2","system":true}`,
	} {
		if w := h.admin.do(http.MethodPost, "/api/v1/system-users/", body, nil); w.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
}

func TestCreateChecksEverythingBeforeTheAccountExists(t *testing.T) {
	h := newUsersHost(t)
	for name, body := range map[string]string{
		"bad key":     `{"username":"a1","sshKey":"not a key"}`,
		"short":       `{"username":"a1","password":"short"}`,
		"bad grant":   `{"username":"a1","noSignIn":true,"sudo":{"level":"always"}}`,
		"bad command": `{"username":"a1","noSignIn":true,"sudo":{"level":"password","commands":["ls"]}}`,
		"must change": `{"username":"a1","noSignIn":true,"mustChangePassword":true}`,
	} {
		w := h.admin.do(http.MethodPost, "/api/v1/system-users/", body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if h.ran("useradd") {
		t.Fatalf("an account was made: %v", h.calls)
	}
}

func TestAnAccountThatCouldNotBeFinishedIsTakenBackOut(t *testing.T) {
	h := newUsersHost(t)
	h.refuse["visudo"] = errors.New("visudo: parse error")
	w := h.admin.do(http.MethodPost, "/api/v1/system-users/",
		`{"username":"half","password":"long enough password","sudo":{"level":"password"}}`, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not created") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if !h.ran("userdel --remove half") {
		t.Fatalf("calls = %v", h.calls)
	}
	if _, err := h.s.modules.linuxUsers.Lookup("half"); !errors.Is(err, linuxusers.ErrNotFound) {
		t.Fatalf("the account stayed: %v", err)
	}
}

func TestCreateFinishesWhatItWasAsked(t *testing.T) {
	h := newUsersHost(t)
	w := h.admin.do(http.MethodPost, "/api/v1/system-users/",
		`{"username":"ada","comment":"Ada L","shell":"/bin/bash","generatePassword":true,"mustChangePassword":true,"sudo":{"level":"nopassword"}}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	var created struct {
		linuxusers.User
		GeneratedPassword string `json:"generatedPassword"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Username != "ada" || created.GeneratedPassword == "" || created.Password != linuxusers.PasswordSet {
		t.Fatalf("created = %+v", created)
	}
	if !h.ran("chage --lastday 0 ada") {
		t.Fatalf("calls = %v", h.calls)
	}
	if b, err := os.ReadFile(filepath.Join(h.etc, "sudoers.d", "jd-ada")); err != nil || !strings.Contains(string(b), "NOPASSWD: ALL") {
		t.Fatalf("rule = %q, %v", b, err)
	}
}

func TestTheUnlockThatUsedToLieNowSaysWhy(t *testing.T) {
	h := newUsersHost(t)
	w := h.admin.do(http.MethodPatch, "/api/v1/system-users/ion", `{"locked":false}`, nil)
	if w.Code != http.StatusConflict || usersErrorCode(t, w) != "nothing_to_unlock" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if h.ran("usermod") {
		t.Fatal("usermod was asked")
	}
}

func TestTheLastWayInIsAConflictOnEveryRoute(t *testing.T) {
	h := newUsersHost(t)
	h.setHash("root", "$y$real$hash")
	last := func(w *httptest.ResponseRecorder, what string) {
		t.Helper()
		if w.Code != http.StatusConflict || usersErrorCode(t, w) != "last_way_in" {
			t.Errorf("%s: %d %s", what, w.Code, w.Body.String())
		}
	}
	// ion is the only way in, by a key and a rule of its own.
	if w := h.admin.do(http.MethodPost, "/api/v1/system-users/ion/keys/generate", `{"comment":"only"}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	keys := h.admin.do(http.MethodGet, "/api/v1/system-users/ion/keys", "", nil)
	var listed struct {
		Keys []linuxusers.SSHKey `json:"keys"`
	}
	if err := json.Unmarshal(keys.Body.Bytes(), &listed); err != nil || len(listed.Keys) != 1 {
		t.Fatalf("keys = %s, %v", keys.Body.String(), err)
	}
	if w := h.admin.do(http.MethodPut, "/api/v1/system-users/ion/sudo", `{"level":"nopassword"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("sudo: %d %s", w.Code, w.Body.String())
	}
	h.setHash("root", "!")

	last(h.admin.do(http.MethodPatch, "/api/v1/system-users/ion", `{"disabled":true}`, nil), "disable")
	last(h.admin.do(http.MethodPatch, "/api/v1/system-users/ion", `{"shell":"/usr/sbin/nologin"}`, nil), "shell")
	last(h.admin.do(http.MethodDelete, "/api/v1/system-users/ion?home=keep", "", nil), "delete")
	last(h.admin.do(http.MethodDelete, "/api/v1/system-users/ion/keys?fingerprint="+url.QueryEscape(listed.Keys[0].Fingerprint), "", nil), "last key")
	last(h.admin.do(http.MethodPut, "/api/v1/system-users/ion/sudo", `{"level":"none"}`, nil), "sudo")
	if h.ran("userdel") {
		t.Fatal("userdel was asked")
	}
}

func TestBulkReportsEachAccountAndNeverSweepsUpASystemOne(t *testing.T) {
	h := newUsersHost(t)
	w := h.admin.do(http.MethodPost, "/api/v1/system-accounts/bulk",
		`{"action":"disable","usernames":["ion","_apt","ghost"]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Results []struct{ Username, Error string } `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	outcome := map[string]string{}
	for _, r := range body.Results {
		outcome[r.Username] = r.Error
	}
	if outcome["ion"] != "" || outcome["_apt"] == "" || outcome["ghost"] == "" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !h.ran("chage --expiredate 1 ion") || h.ran("chage --expiredate 1 _apt") {
		t.Fatalf("calls = %v", h.calls)
	}
	if w := h.admin.do(http.MethodPost, "/api/v1/system-accounts/bulk", `{"action":"explode","usernames":["ion"]}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: %d", w.Code)
	}
	// Adding to a group keeps the others, and leaves the main group out of the list.
	if w := h.admin.do(http.MethodPost, "/api/v1/system-accounts/bulk", `{"action":"add-group","usernames":["ion"],"group":"sudo"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("add-group: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("usermod --groups sudo ion") {
		t.Fatalf("calls = %v", h.calls)
	}
	// Many at once is not where the one unrecoverable choice gets made.
	if w := h.admin.do(http.MethodPost, "/api/v1/system-accounts/bulk/delete", `{"usernames":["mira"]}`, nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("userdel mira") || h.ran("userdel --remove") {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestAKeyCannotBeCopiedOntoADaemonsAccountWithoutSayingSo(t *testing.T) {
	h := newUsersHost(t)
	w := h.admin.do(http.MethodPost, "/api/v1/system-users/ion/keys/generate", `{"comment":"laptop"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	var generated linuxusers.GeneratedKey
	if err := json.Unmarshal(w.Body.Bytes(), &generated); err != nil {
		t.Fatal(err)
	}
	body := `{"fingerprint":"` + generated.Key.Fingerprint + `","to":["mira","_apt","root","ghost"]}`
	w = h.admin.do(http.MethodPost, "/api/v1/system-users/ion/keys/copy", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("copy: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Results map[string]string `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// Being refused by the rule is the failure; this process merely cannot chown to it.
	if strings.Contains(out.Results["mira"], "belongs to the system") {
		t.Fatalf("results = %+v", out.Results)
	}
	// Root is not held back by the rule; this process merely cannot chown to it.
	if strings.Contains(out.Results["root"], "belongs to the system") {
		t.Fatalf("results = %+v", out.Results)
	}
	if out.Results["_apt"] == "" || out.Results["ghost"] == "" {
		t.Fatalf("results = %+v", out.Results)
	}
	if keys, _, _ := h.s.modules.linuxUsers.ListKeys("_apt"); len(keys) != 0 {
		t.Fatalf("_apt holds %+v", keys)
	}
}

func TestTemplatesKeepTheBuiltInOnesOutOfTheOperatorsHands(t *testing.T) {
	h := newUsersHost(t)
	path := "/api/v1/system-accounts/templates"
	list := func() []linuxusers.Template {
		w := h.admin.do(http.MethodGet, path, "", nil)
		var out []linuxusers.Template
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v", w.Body.String(), err)
		}
		return out
	}
	builtin := len(list())
	if builtin == 0 {
		t.Fatal("no built-in templates")
	}
	if w := h.admin.do(http.MethodPut, path, `[{"id":"person","name":"Mine"}]`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("shadowing a built-in: %d", w.Code)
	}
	if w := h.admin.do(http.MethodPut, path, `[{"id":"ops","name":"Ops","shell":"/bin/bash","groups":["sudo"],"sudo":"password"}]`, nil); w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	got := list()
	if len(got) != builtin+1 || got[len(got)-1].ID != "ops" || got[len(got)-1].Builtin {
		t.Fatalf("got %+v", got)
	}
	if w := h.admin.do(http.MethodPut, path, `[]`, nil); w.Code != http.StatusOK || len(list()) != builtin {
		t.Fatalf("clearing: %d", w.Code)
	}
}

func TestGroupRoutes(t *testing.T) {
	h := newUsersHost(t)
	if w := h.admin.do(http.MethodGet, "/api/v1/system-accounts/groups", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"sudo"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if w := h.admin.do(http.MethodPost, "/api/v1/system-accounts/groups", `{"name":"ops"}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("groupadd ops") {
		t.Fatalf("calls = %v", h.calls)
	}
	// A package's group keeps its name, number and existence until the request says
	// it means it, and a group the system rests on keeps them regardless.
	w := h.admin.do(http.MethodPatch, "/api/v1/system-accounts/groups/sudo", `{"newName":"other"}`, nil)
	if w.Code != http.StatusForbidden || usersErrorCode(t, w) != "system_group" {
		t.Fatalf("rename sudo: %d %s", w.Code, w.Body.String())
	}
	w = h.admin.do(http.MethodPatch, "/api/v1/system-accounts/groups/sudo?allowSystem=true", `{"newName":"other"}`, nil)
	if w.Code != http.StatusForbidden || usersErrorCode(t, w) != "protected_account" {
		t.Fatalf("rename sudo, meaning it: %d %s", w.Code, w.Body.String())
	}
	w = h.admin.do(http.MethodDelete, "/api/v1/system-accounts/groups/sudo", "", nil)
	if w.Code != http.StatusForbidden || usersErrorCode(t, w) != "system_group" {
		t.Fatalf("delete sudo: %d %s", w.Code, w.Body.String())
	}
	if h.ran("groupmod") || h.ran("groupdel") {
		t.Fatalf("calls = %v", h.calls)
	}
	// Who is in one is what this route is for, and needs no such word.
	if w := h.admin.do(http.MethodPatch, "/api/v1/system-accounts/groups/sudo", `{"members":["ion"]}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("members: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("gpasswd --members ion sudo") {
		t.Fatalf("calls = %v", h.calls)
	}
	if w := h.admin.do(http.MethodPatch, "/api/v1/system-accounts/groups/ghost", `{"members":[]}`, nil); w.Code != http.StatusNotFound {
		t.Fatalf("ghost: %d %s", w.Code, w.Body.String())
	}
}

func TestDefaultsRouteReadsAndWritesTheHostsFiles(t *testing.T) {
	h := newUsersHost(t)
	if err := os.WriteFile(filepath.Join(h.etc, "login.defs"), []byte("# comment\nPASS_MAX_DAYS\t99999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := h.admin.do(http.MethodPut, "/api/v1/system-accounts/defaults", `{"passwordMaxDays":90}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"passwordMaxDays":90`) {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	b, _ := os.ReadFile(filepath.Join(h.etc, "login.defs"))
	if string(b) != "# comment\nPASS_MAX_DAYS\t90\n" {
		t.Fatalf("file = %q", b)
	}
	if w := h.admin.do(http.MethodPut, "/api/v1/system-accounts/defaults", `{"shell":"/bin/nope"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad shell: %d %s", w.Code, w.Body.String())
	}
}

// One sign-in by its process: the account in the URL and the process in the query
// have to agree, so this cannot be aimed at somebody else's login.
func TestEndingASessionHasToBeThatAccountsSession(t *testing.T) {
	h := newUsersHost(t)
	w := h.admin.do(http.MethodDelete, "/api/v1/system-users/ion/sessions?pid=1", "", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if w := h.admin.do(http.MethodDelete, "/api/v1/system-users/ion/sessions?pid=abc", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("not a number: %d", w.Code)
	}
	if w := h.admin.do(http.MethodDelete, "/api/v1/system-users/ion/sessions", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("all: %d %s", w.Code, w.Body.String())
	}
	if !h.ran("pkill --signal KILL --uid " + strconv.Itoa(fixtureUID())) {
		t.Fatalf("calls = %v", h.calls)
	}
	w = h.admin.do(http.MethodDelete, "/api/v1/system-users/root/sessions", "", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("root: %d %s", w.Code, w.Body.String())
	}
}

func TestAccountReadsAnswerOnABareHost(t *testing.T) {
	h := newUsersHost(t)
	for _, path := range []string{"", "/activity", "/history", "/removal", "/limits", "/sudo", "/keys", "/disk"} {
		w := h.admin.do(http.MethodGet, "/api/v1/system-users/ion"+path, "", nil)
		// du on a made-up tool prints nothing, which is its one honest failure.
		if path == "/disk" {
			continue
		}
		if w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"", "/activity", "/history", "/removal", "/limits", "/sudo", "/keys"} {
		if w := h.admin.do(http.MethodGet, "/api/v1/system-users/ghost"+path, "", nil); w.Code != http.StatusNotFound {
			t.Errorf("ghost%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/system-accounts/defaults", "/api/v1/system-accounts/templates", "/api/v1/system-accounts/groups"} {
		if w := h.admin.do(http.MethodGet, path, "", nil); w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// Opening a shell as a chosen account is more than having a shell: root is one of
// the accounts it can name.
func TestATerminalAsAnotherAccountTakesSystemAdmin(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "limited-user", auth.RoleLimited)}
	w := limited.do(http.MethodPost, "/api/v1/terminal/", `{"asUser":"root"}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("limited: %d %s", w.Code, w.Body.String())
	}
	// No role has a terminal without system.admin today, so the route above cannot
	// tell whose refusal that was. The handler's own has to hold when one does.
	req := httptest.NewRequest(http.MethodPost, "/terminal/", strings.NewReader(`{"asUser":"root"}`))
	req.Header.Set("Content-Type", "application/json")
	narrowed := &httpx.Principal{User: &auth.User{ID: 9, Username: "narrow"}, Role: auth.RoleLimited, Kind: "session", IP: "127.0.0.1"}
	err := s.handleTerminalCreate(httptest.NewRecorder(), req.WithContext(httpx.WithPrincipal(req.Context(), narrowed)))
	if !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("the handler answered %v", err)
	}
	admin := &client{t: t, h: routes, cookie: signIn(t, s)}
	for _, name := range []string{"../etc", "--login", "Root Shell"} {
		w := admin.do(http.MethodPost, "/api/v1/terminal/", `{"asUser":"`+name+`"}`, nil)
		if got := usersErrorCode(t, w); got != "invalid_username" {
			t.Errorf("%s: %s (%d %s)", name, got, w.Code, w.Body.String())
		}
	}
	// The manager's refusals are its own tests'; here they keep their meanings.
	for err, status := range map[error]int{
		term.ErrNoSuchAccount:      http.StatusNotFound,
		term.ErrAccountCannotLogin: http.StatusBadRequest,
	} {
		var mapped *httpx.APIError
		if !errors.As(mapTermError(fmt.Errorf("%w: zoro", err)), &mapped) || mapped.Status != status {
			t.Errorf("%v mapped to %+v", err, mapped)
		}
	}
}
