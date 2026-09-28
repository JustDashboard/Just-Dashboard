package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// accessListServer is a server whose nginx directory is a Debian layout in
// a temporary directory, with a password file, behind an nginx that runs
// script.
func accessListServer(t *testing.T, script func(root string) string) (*client, *Server, string) {
	t.Helper()
	s := testServer(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"sites-available", "sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte("#!/bin/sh\n"+script(root)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.Cfg.NginxDir = root
	s.initModules()
	if _, err := s.modules.proxy.SetAuthUser("staging", "admin", "correcthorsebattery"); err != nil {
		t.Fatal(err)
	}
	return &client{t: t, h: s.Routes(), cookie: signIn(t, s)}, s, root
}

type accessListsBody struct {
	Dir           string `json:"dir"`
	ClientAddress string `json:"clientAddress"`
	Lists         []struct {
		Name    string   `json:"name"`
		Include string   `json:"include"`
		Allow   []string `json:"allow"`
		Satisfy string   `json:"satisfy"`
		UsedBy  []struct {
			Site    string `json:"site"`
			Enabled bool   `json:"enabled"`
		} `json:"usedBy"`
	} `json:"lists"`
}

type accessListSaveBody struct {
	List struct {
		Name     string   `json:"name"`
		Allow    []string `json:"allow"`
		AuthFile string   `json:"authFile"`
		Realm    string   `json:"realm"`
		Satisfy  string   `json:"satisfy"`
		UsedBy   []struct {
			Site string `json:"site"`
		} `json:"usedBy"`
	} `json:"list"`
	Reloaded    bool   `json:"reloaded"`
	ReloadError string `json:"reloadError"`
	Error       struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Raw     string `json:"raw"`
	} `json:"error"`
}

func decodeInto(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
}

// The whole round: an empty listing, a new list, a second of the same name
// refused, a site taking it in, an edit reaching that site through one
// reload, the delete refused while the site includes it and allowed after.
func TestAccessListsThroughTheAPI(t *testing.T) {
	c, s, root := accessListServer(t, func(string) string { return "exit 0" })

	w := c.do(http.MethodGet, "/api/v1/proxy/access-lists/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listing accessListsBody
	decodeInto(t, w.Body.Bytes(), &listing)
	if listing.Dir != filepath.Join(root, "jd-access") || listing.ClientAddress != "127.0.0.1" || listing.Lists == nil || len(listing.Lists) != 0 {
		t.Fatalf("an empty listing: %s", w.Body.String())
	}

	w = c.do(http.MethodPut, "/api/v1/proxy/access-lists/office",
		`{"allow":["10.0.0.0/8"],"deny":[],"authFile":"staging","satisfy":"any","overwrite":false}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var saved accessListSaveBody
	decodeInto(t, w.Body.Bytes(), &saved)
	if saved.List.Name != "office" || saved.List.Satisfy != "any" || saved.List.Realm != "Restricted" || !saved.Reloaded || len(saved.List.UsedBy) != 0 {
		t.Errorf("created: %s", w.Body.String())
	}
	if entry := lastAudit(t, s, "proxy.accesslist.save"); !entry.Success || !strings.Contains(entry.Detail, `"created":true`) {
		t.Errorf("audit = %+v", entry)
	}

	w = c.do(http.MethodPut, "/api/v1/proxy/access-lists/office", `{"allow":["10.0.0.1"],"deny":[],"overwrite":false}`, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"exists"`) {
		t.Errorf("a second office: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodPut, "/api/v1/proxy/access-lists/office", `{"allow":["10.0.0.5/8"],"deny":[],"overwrite":true}`, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "the range is 10.0.0.0/8") {
		t.Errorf("a range with bits past it: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodPut, "/api/v1/proxy/access-lists/office", `{"allow":[],"deny":[],"users":["x"],"overwrite":true}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("an unknown field: %d %s", w.Code, w.Body.String())
	}

	site := filepath.Join(root, "sites-available", "app")
	if err := os.WriteFile(site, []byte("server {\n    server_name app.test;\n    include jd-access/office.conf;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(site, filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	w = c.do(http.MethodPut, "/api/v1/proxy/access-lists/office", `{"allow":["10.0.0.0/8","192.168.1.0/24"],"deny":[],"authFile":"staging","satisfy":"any","overwrite":true}`, nil)
	saved = accessListSaveBody{}
	decodeInto(t, w.Body.Bytes(), &saved)
	if w.Code != http.StatusOK || len(saved.List.UsedBy) != 1 || saved.List.UsedBy[0].Site != "app" || len(saved.List.Allow) != 2 || !saved.Reloaded {
		t.Errorf("edit: %d %s", w.Code, w.Body.String())
	}
	if entry := lastAudit(t, s, "proxy.accesslist.save"); !strings.Contains(entry.Detail, `"usedBy":["app"]`) || !strings.Contains(entry.Detail, `"reloaded":true`) {
		t.Errorf("audit = %+v", entry)
	}

	w = c.do(http.MethodDelete, "/api/v1/proxy/access-lists/office", "", nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"in_use"`) || !strings.Contains(w.Body.String(), "app includes office") {
		t.Errorf("deleting a list app includes: %d %s", w.Code, w.Body.String())
	}
	if entry := lastAudit(t, s, "proxy.accesslist.delete"); entry.Success {
		t.Errorf("a refused delete was audited as done: %+v", entry)
	}
	if err := os.WriteFile(site, []byte("server {\n    server_name app.test;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An ordinary confirmation, no typed phrase: the list is refused while
	// in use and a copy is kept.
	w = c.do(http.MethodDelete, "/api/v1/proxy/access-lists/office", "", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if entry := lastAudit(t, s, "proxy.accesslist.delete"); !entry.Success || entry.Target != "office" {
		t.Errorf("audit = %+v", entry)
	}
	w = c.do(http.MethodDelete, "/api/v1/proxy/access-lists/office", "", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("deleting it again: %d %s", w.Code, w.Body.String())
	}
}

// nginx refusing a list is a 422 carrying nginx's reason, the file is put
// back, and the refusal is audited as one.
func TestAccessListSaveSaysWhyNginxRefusedIt(t *testing.T) {
	c, s, root := accessListServer(t, func(root string) string {
		list := filepath.Join(root, "jd-access", "office.conf")
		return fmt.Sprintf(`if grep -qs auth_basic '%s'; then echo 'nginx: [emerg] "auth_basic" directive is duplicate in %s:7' >&2; echo 'nginx: configuration file test failed' >&2; exit 1; fi; exit 0`, list, list)
	})
	w := c.do(http.MethodPut, "/api/v1/proxy/access-lists/office", `{"allow":["10.0.0.0/8"],"deny":[],"overwrite":false}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	list := filepath.Join(root, "jd-access", "office.conf")
	before, _ := os.ReadFile(list)
	w = c.do(http.MethodPut, "/api/v1/proxy/access-lists/office", `{"allow":["10.0.0.0/8"],"deny":[],"authFile":"staging","overwrite":true}`, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var answer accessListSaveBody
	decodeInto(t, w.Body.Bytes(), &answer)
	if answer.Error.Code != "invalid_config" || answer.Error.Message != `nginx refuses office where a site includes it: "auth_basic" directive is duplicate in `+list+":7" || !strings.Contains(answer.Error.Raw, "test failed") {
		t.Errorf("error = %+v", answer.Error)
	}
	if after, _ := os.ReadFile(list); string(after) != string(before) {
		t.Errorf("the refused list stayed:\n%s", after)
	}
	if entry := lastAudit(t, s, "proxy.accesslist.save"); entry.Success || !strings.Contains(entry.Detail, "refused") {
		t.Errorf("audit = %+v", entry)
	}
}

// Every route is system.admin, and the delete is destructive: a read-only
// or limited account gets a 403 before anything is read or written.
func TestAccessListRoutesAreForAdministrators(t *testing.T) {
	_, s, root := accessListServer(t, func(string) string { return "exit 0" })
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		other := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "user-"+string(role), role)}
		for _, call := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/v1/proxy/access-lists/", ""},
			{http.MethodPut, "/api/v1/proxy/access-lists/office", `{"allow":["10.0.0.1"],"deny":[],"overwrite":false}`},
			{http.MethodDelete, "/api/v1/proxy/access-lists/office", ""},
		} {
			if w := other.do(call.method, call.path, call.body, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d %s", role, call.method, call.path, w.Code, w.Body.String())
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "jd-access", "office.conf")); !os.IsNotExist(err) {
		t.Errorf("a refused account wrote a list: %v", err)
	}
}
