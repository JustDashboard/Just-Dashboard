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
// $JD_TEST_OUT, whose dump prints the configuration it is given (nginx.conf,
// or the copy a disabled site is tested in after -c) and the directory that
// includes in the order nginx reads them, and whose reload exits
// $JD_TEST_RELOAD_EXIT.
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
	script := siteShimPrelude(dir) +
		"if [ \"$1\" = \"-t\" ]; then printf '%s\\n' \"$JD_TEST_OUT\"; exit 0; fi\n" +
		"if [ \"$1\" = \"-T\" ]; then for f in \"$conf\" $include; do\n" +
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

// siteShimPrelude sets $conf to the configuration a fake nginx is given — the
// file after -c, or nginx.conf — and $include to the pattern it includes.
func siteShimPrelude(dir string) string {
	return "#!/bin/sh\n" +
		"conf='" + dir + "/nginx.conf'\n" +
		"if [ \"$2\" = \"-c\" ]; then conf=\"$3\"; fi\n" +
		"include=$(sed -n 's/^ *include \\(.*\\);$/\\1/p' \"$conf\" | head -n 1)\n"
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

	// A link named app that enables another file holds the name as surely
	// as a file does, and a new site saved over it is refused with the
	// other site's link untouched.
	other := filepath.Join(dir, "sites-available", "shopfront.conf")
	if err := os.WriteFile(other, []byte("server { server_name shop.example.com; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(dir, "sites-enabled", "shop.example.com")); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodPost, "/api/v1/proxy/sites/preview", siteBody(t, "shop.example.com", "shop.example.com", `"enable"`, nil), nil)
	var held struct {
		Exists           bool   `json:"exists"`
		EnabledElsewhere string `json:"enabledElsewhere"`
	}
	decodeSite(t, w, &held)
	if held.Exists || held.EnabledElsewhere != "sites-enabled/shop.example.com already enables "+other {
		t.Fatalf("a name held by another site's link: %+v", held)
	}
	w = c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "shop.example.com", "shop.example.com", `"enable"`, map[string]any{"overwrite": false}), nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "already enables") {
		t.Fatalf("a new site over another's link: %d %s", w.Code, w.Body.String())
	}
	if target, err := os.Readlink(filepath.Join(dir, "sites-enabled", "shop.example.com")); err != nil || target != other {
		t.Fatalf("the other site's link now names %q: %v", target, err)
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

// The form reads whether a site is enabled from the site and from each
// preview, and offers a disabled one "Save (stays disabled)" and "Save and
// enable". A disabled site saved is tested in a copy of the configuration
// that links it: a failing test is its result, not a refusal, and nothing is
// reloaded after it.
func TestSiteSaveTestsADisabledSiteAsEnabled(t *testing.T) {
	c, dir := siteFormServer(t)
	link := filepath.Join(dir, "sites-enabled", "app")
	script := siteShimPrelude(dir) +
		"if [ \"$1\" = \"-t\" ]; then\n" +
		"  site=\"$(dirname \"$include\")/app\"\n" +
		"  if [ -e \"$site\" ]; then\n" +
		"    echo \"nginx: [emerg] unknown directive \\\"frobnicate\\\" in $site:3\"; exit 1\n" +
		"  fi\n" +
		"  echo 'nginx: configuration file test is successful'; exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-s\" ]; then touch '" + dir + "/reloaded'; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	enabled := func(path, body string) *bool {
		t.Helper()
		method := http.MethodGet
		if body != "" {
			method = http.MethodPost
		}
		w := c.do(method, path, body, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var out struct {
			Enabled *bool `json:"enabled"`
		}
		decodeSite(t, w, &out)
		return out.Enabled
	}

	w := c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"keep"`, nil), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("a disabled save failing its test enabled answered %d: %s", w.Code, w.Body.String())
	}
	var res proxysvc.SiteResult
	decodeSite(t, w, &res)
	if res.Enabled || !res.TestedAsEnabled || res.Validation == nil || res.Validation.Valid ||
		len(res.Validation.Diagnostics) != 1 || res.Validation.Diagnostics[0].Line != 3 || res.Reloaded {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("the disabled site was linked into the live configuration")
	}
	if _, err := os.Stat(filepath.Join(dir, "reloaded")); !os.IsNotExist(err) {
		t.Fatal("nginx was reloaded after a failing test")
	}
	if got := enabled("/api/v1/proxy/sites/app", ""); got == nil || *got {
		t.Fatalf("the saved disabled site reads back enabled=%v", got)
	}
	preview := siteBody(t, "app", "app.example.com", `"keep"`, nil)
	if got := enabled("/api/v1/proxy/sites/preview", preview); got == nil || *got {
		t.Fatalf("the preview of a disabled site says enabled=%v", got)
	}

	// Enabling it meets the same test, and is refused with it.
	w = c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"enable"`, nil), nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("enabling a site that fails its test answered %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("a refused enable left the link")
	}

	// Once it passes, "enable" links it and the site and preview say so.
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"),
		[]byte("#!/bin/sh\necho 'nginx: configuration file test is successful'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w = c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"enable"`, nil), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("enabling answered %d: %s", w.Code, w.Body.String())
	}
	if got := enabled("/api/v1/proxy/sites/app", ""); got == nil || !*got {
		t.Fatalf("the enabled site reads back enabled=%v", got)
	}
	if got := enabled("/api/v1/proxy/sites/preview", preview); got == nil || !*got {
		t.Fatalf("the preview of an enabled site says enabled=%v", got)
	}
}

// A conf.d site switched off by its name — app.conf.disabled — reads back
// off and in conf.d, and its preview names the file that was read: the form
// then offers only to keep it off, where it offered to keep it off and wrote
// app.conf.disabled.conf, which nginx reads.
func TestSiteSpecSaysAConfDSiteIsOffAndWhereItIs(t *testing.T) {
	c, dir := siteFormServer(t)
	if err := os.RemoveAll(filepath.Join(dir, "sites-available")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	off := filepath.Join(dir, "conf.d", "app.conf.disabled")
	content, err := proxysvc.RenderNginx(&proxysvc.SiteSpec{
		Name: "app.conf.disabled", Kind: "proxy", Domains: []string{"app.example.com"}, Upstream: "http://127.0.0.1:3000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(off, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		Spec    proxysvc.SiteSpec `json:"spec"`
		Path    string            `json:"path"`
		Enabled *bool             `json:"enabled"`
		Confd   bool              `json:"confd"`
	}
	w := c.do(http.MethodGet, "/api/v1/proxy/sites/app.conf.disabled", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var read answer
	decodeSite(t, w, &read)
	if read.Spec.Name != "app.conf.disabled" || read.Enabled == nil || *read.Enabled || !read.Confd {
		t.Fatalf("read back as %+v", read)
	}
	w = c.do(http.MethodPost, "/api/v1/proxy/sites/preview", siteBody(t, "app.conf.disabled", "app.example.com", `"keep"`, nil), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("preview answered %d: %s", w.Code, w.Body.String())
	}
	var preview answer
	decodeSite(t, w, &preview)
	if preview.Path != off || preview.Enabled == nil || *preview.Enabled || !preview.Confd {
		t.Fatalf("preview = %+v, want %s, off, in conf.d", preview, off)
	}
}

// A site whose sites-enabled entry is a file of its own is served from that
// file. The form was told it was disabled and offered "Save (stays
// disabled)"; the site, every preview and the save now say nginx serves the
// other file, and a hand-written file the save replaces is named where it
// was kept.
func TestSiteSaysNginxServesAFileOfItsOwn(t *testing.T) {
	c, dir := siteFormServer(t)
	full := filepath.Join(dir, "sites-available", "app")
	hand := "server { server_name app.example.com; } # written by hand\n"
	if err := os.WriteFile(full, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sites-enabled", "app"), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	type state struct {
		Enabled    *bool `json:"enabled"`
		ServedCopy *bool `json:"servedCopy"`
	}
	for _, ask := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/proxy/sites/app", ""},
		{http.MethodPost, "/api/v1/proxy/sites/preview", siteBody(t, "app", "app.example.com", `"keep"`, nil)},
	} {
		w := c.do(ask.method, ask.path, ask.body, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", ask.path, w.Code, w.Body.String())
		}
		var got state
		decodeSite(t, w, &got)
		if got.Enabled == nil || *got.Enabled || got.ServedCopy == nil || !*got.ServedCopy {
			t.Fatalf("%s: enabled=%v servedCopy=%v, want this file not read and the copy served", ask.path, got.Enabled, got.ServedCopy)
		}
	}

	w := c.do(http.MethodPost, "/api/v1/proxy/sites/", siteBody(t, "app", "app.example.com", `"keep"`, map[string]any{"reload": false}), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var res proxysvc.SiteResult
	decodeSite(t, w, &res)
	if !res.ServedCopy || res.Enabled || res.Backup != full+".bak" {
		t.Fatalf("result = %+v", res)
	}
	if b, err := os.ReadFile(full + ".bak"); err != nil || string(b) != hand {
		t.Fatalf("the .bak holds %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sites-enabled", "app")); string(b) != hand {
		t.Fatalf("the file nginx serves was changed:\n%s", b)
	}
}
