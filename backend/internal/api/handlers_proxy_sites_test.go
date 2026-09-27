package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// siteFormServer is a signed-in admin against a Debian nginx layout in a
// temporary directory, with an nginx first on PATH whose test prints
// $JD_TEST_OUT, whose dump prints nginx.conf and sites-enabled in the order
// nginx reads them, and whose reload exits $JD_TEST_RELOAD_EXIT.
func siteFormServer(t *testing.T) (*client, string) {
	t.Helper()
	s := testServer(t)
	dir := t.TempDir()
	for _, sub := range []string{"sites-available", "sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := "events {}\nhttp {\n    include " + filepath.Join(dir, "sites-enabled") + "/*;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-t\" ]; then printf '%s\\n' \"$JD_TEST_OUT\"; exit 0; fi\n" +
		"if [ \"$1\" = \"-T\" ]; then for f in '" + dir + "/nginx.conf' '" + dir + "'/sites-enabled/*; do\n" +
		"  [ -e \"$f\" ] || continue; printf '# configuration file %s:\\n' \"$f\"; cat \"$f\"; printf '\\n'\n" +
		"done; exit 0; fi\n" +
		"if [ \"$1\" = \"-s\" ]; then echo 'nginx: [error] invalid PID number \"\" in \"/run/nginx.pid\"'; exit ${JD_TEST_RELOAD_EXIT:-0}; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_OUT", "nginx: configuration file test is successful")
	s.Cfg.NginxDir = dir
	s.initModules()
	return &client{t: t, h: s.Routes(), cookie: signIn(t, s)}, dir
}

func siteBody(t *testing.T, name, domain, enable string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{
		"spec": proxysvc.SiteSpec{
			Name: name, Kind: "proxy", Domains: []string{domain}, Upstream: "http://127.0.0.1:3000",
			AllowFrom: []string{}, DenyFrom: []string{}, Locations: []proxysvc.SiteLocation{},
		},
		"enable": json.RawMessage(enable), "reload": true, "overwrite": true,
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func decodeSite(t *testing.T, w interface{ Result() *http.Response }, into any) {
	t.Helper()
	if err := json.NewDecoder(w.Result().Body).Decode(into); err != nil {
		t.Fatal(err)
	}
}

// The form posts "keep" for an existing site, and a disabled one stays
// disabled; it used to post true for every save and enable it.
func TestSiteSaveKeepsADisabledSiteDisabled(t *testing.T) {
	c, dir := siteFormServer(t)
	w := c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"keep"`, nil), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var res proxysvc.SiteResult
	decodeSite(t, w, &res)
	if res.Enabled {
		t.Fatal("a site saved with keep reported itself enabled")
	}
	if _, err := os.Lstat(filepath.Join(dir, "sites-enabled", "app")); err == nil {
		t.Fatal("keep made a link")
	}

	w = c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"enable"`, nil), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, "sites-enabled", "app")); err != nil {
		t.Fatal("enable made no link")
	}

	w = c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"disable"`, nil), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("an unknown enable word answered %d: %s", w.Code, w.Body.String())
	}
}

// A clean test followed by a failed reload is a saved site nginx has not
// picked up, not a site that was "not applied".
func TestSiteSaveAnswersAFailedReloadWithTheSavedSite(t *testing.T) {
	c, dir := siteFormServer(t)
	t.Setenv("JD_TEST_RELOAD_EXIT", "1")
	w := c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"enable"`, nil), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var res proxysvc.SiteResult
	decodeSite(t, w, &res)
	if res.Reloaded || !strings.Contains(res.ReloadError, "invalid PID number") || !res.Enabled {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "sites-available", "app")); err != nil {
		t.Fatal("the site is not on disk")
	}
}

// nginx's warning about a name already served becomes a 409 that says what
// saving would do — app sorts before legacy, so it would take the name — and
// allowConflict saves anyway.
func TestSiteSaveRefusesANameAnotherSiteServes(t *testing.T) {
	c, dir := siteFormServer(t)
	if w := c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "legacy", "app.example.com", `"enable"`, nil), nil); w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	t.Setenv("JD_TEST_OUT", `nginx: [warn] conflicting server name "app.example.com" on 0.0.0.0:80, ignored`)

	w := c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"enable"`, nil), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var refusal struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	decodeSite(t, w, &refusal)
	if refusal.Error.Code != "name_conflict" ||
		refusal.Error.Message != "Saving anyway takes app.example.com on 0.0.0.0:80 from legacy, since nginx reads this site first." {
		t.Fatalf("refusal = %+v", refusal.Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "sites-available", "app")); !os.IsNotExist(err) {
		t.Fatal("the refused site was left on disk")
	}

	w = c.do(http.MethodPost, "/api/v1/proxy/sites/",
		siteBody(t, "app", "app.example.com", `"enable"`, map[string]any{"allowConflict": true}), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("saving anyway answered %d: %s", w.Code, w.Body.String())
	}
	var res proxysvc.SiteResult
	decodeSite(t, w, &res)
	if len(res.Conflicts) != 1 || res.Conflicts[0].Site != "legacy" || res.Conflicts[0].Effect != proxysvc.ConflictTakes {
		t.Fatalf("saved anyway without saying so: %+v", res)
	}
}

// On a conf.d host the form reads app.conf back as app, the name its save
// writes to and its logs are named after.
func TestSiteSpecReadsAConfDSiteUnderItsOwnName(t *testing.T) {
	c, dir := siteFormServer(t)
	if err := os.RemoveAll(filepath.Join(dir, "sites-available")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := &proxysvc.SiteSpec{
		Name: "app", Kind: "proxy", Domains: []string{"app.example.com"},
		Upstream: "http://127.0.0.1:3000", AccessLog: true,
	}
	content, err := proxysvc.RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conf.d", "app.conf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodGet, "/api/v1/proxy/sites/app.conf", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Spec proxysvc.SiteSpec `json:"spec"`
	}
	decodeSite(t, w, &res)
	if res.Spec.Name != "app" {
		t.Fatalf("spec name = %q, want app", res.Spec.Name)
	}
}

// The preview says which file a save writes, so the form can show a new
// site's file name as a path, and that a file of that name is already there
// before the save refuses it.
func TestSitePreviewSaysWhichFileASaveWrites(t *testing.T) {
	c, dir := siteFormServer(t)
	type preview struct {
		Content string  `json:"content"`
		Path    *string `json:"path"`
		Exists  *bool   `json:"exists"`
	}
	ask := func() preview {
		t.Helper()
		w := c.do(http.MethodPost, "/api/v1/proxy/sites/preview", siteBody(t, "app", "app.example.com", `"enable"`, nil), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
		var p preview
		decodeSite(t, w, &p)
		return p
	}
	want := filepath.Join(dir, "sites-available", "app")
	if p := ask(); p.Path == nil || *p.Path != want || p.Exists == nil || *p.Exists {
		t.Fatalf("before the save: %+v, want %s and not there yet", p, want)
	}
	if w := c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"enable"`, nil), nil); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if p := ask(); p.Exists == nil || !*p.Exists {
		t.Fatalf("after the save the file is not reported: %+v", p)
	}

	// A conf.d host writes app.conf there.
	if err := os.RemoveAll(filepath.Join(dir, "sites-available")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p := ask(); p.Path == nil || *p.Path != filepath.Join(dir, "conf.d", "app.conf") || *p.Exists {
		t.Fatalf("on a conf.d host: %+v", p)
	}

	// With neither directory the preview still renders; it just cannot say
	// where the file would go.
	if err := os.RemoveAll(filepath.Join(dir, "conf.d")); err != nil {
		t.Fatal(err)
	}
	if p := ask(); p.Content == "" || p.Path != nil || p.Exists != nil {
		t.Fatalf("with no site directory: %+v", p)
	}
}
