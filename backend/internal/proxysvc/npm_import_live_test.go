package proxysvc

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A representative NPM SQLite export must become working nginx files. The
// importer used to describe controls the site form can now carry as lost.
func TestLiveNPMImportPreservesRedirectAndAccessOptions(t *testing.T) {
	root := liveNginx(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer app.Close()
	path := filepath.Join(t.TempDir(), "database.sqlite")
	if source := os.Getenv("JD_NPM_AUDIT_DB"); source != "" {
		// A database initialised by NPM itself can replace the small fixture.
		// Work on a copy: its proxy row points at this test's new app port.
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("JD_NPM_AUDIT_DB") == "" {
		for _, statement := range []string{
			`CREATE TABLE proxy_host (id INTEGER, domain_names TEXT, forward_scheme TEXT, forward_host TEXT,
		  forward_port INTEGER, enabled INTEGER, access_list_id INTEGER)`,
			`CREATE TABLE redirection_host (id INTEGER, domain_names TEXT, forward_scheme TEXT,
		  forward_domain_name TEXT, forward_http_code INTEGER, preserve_path INTEGER, enabled INTEGER)`,
			`CREATE TABLE access_list (id INTEGER, name TEXT, satisfy_any INTEGER, pass_auth INTEGER)`,
			`CREATE TABLE access_list_client (access_list_id INTEGER, address TEXT, directive TEXT)`,
			`CREATE TABLE access_list_auth (access_list_id INTEGER, username TEXT, password TEXT)`,
			`INSERT INTO access_list VALUES (7, 'team', 1, 0)`,
			`INSERT INTO access_list_client VALUES (7, '127.0.0.1', 'allow')`,
			`INSERT INTO access_list_auth VALUES (7, 'alice', 'secret')`,
			`INSERT INTO redirection_host VALUES (8, '["old.test"]', 'https', 'new.test', 307, 0, 1)`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatalf("NPM fixture: %v", err)
			}
		}
	}
	upstream := strings.TrimPrefix(app.URL, "http://")
	host, portText, _ := strings.Cut(upstream, ":")
	portNumber, _ := strconv.Atoi(portText)
	statement := `UPDATE proxy_host SET forward_host = ?, forward_port = ? WHERE id = 9`
	if os.Getenv("JD_NPM_AUDIT_DB") == "" {
		statement = `INSERT INTO proxy_host VALUES (9, '["protected.test"]', 'http', ?, ?, 1, 7)`
	}
	if _, err := db.Exec(statement, host, portNumber); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	preview, err := service.PreviewNPMImport(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 3 {
		t.Fatalf("preview has %d items: %+v", len(preview.Items), preview.Items)
	}
	byID := map[string]NPMItem{}
	for _, item := range preview.Items {
		byID[item.ID] = item
		if item.Skipped != "" {
			t.Fatalf("%s was skipped: %s", item.ID, item.Skipped)
		}
	}
	if !strings.Contains(byID["proxy-9"].Content, "satisfy any;") ||
		!strings.Contains(byID["proxy-9"].Content, `proxy_set_header Authorization "";`) {
		t.Fatalf("the imported access rules changed:\n%s", byID["proxy-9"].Content)
	}
	if !strings.Contains(byID["redirect-8"].Content, "return 307 https://new.test;") {
		t.Fatalf("the imported redirect changed:\n%s", byID["redirect-8"].Content)
	}
	// The export's sites normally listen on 80. Use one private loopback port
	// so the real nginx fixture can load and answer them as an ordinary user.
	listen := fmt.Sprintf("    listen 127.0.0.1:%d;\n", freePort(t))
	npmPlans.Lock()
	plan := npmPlans.m[preview.Token]
	for _, id := range []string{"proxy-9", "redirect-8"} {
		item := plan.items[id]
		item.item.Content = strings.ReplaceAll(item.item.Content, "    listen 80;\n", listen)
		item.item.Content = strings.ReplaceAll(item.item.Content, "    listen [::]:80;\n", "")
		item.item.Content = strings.ReplaceAll(item.item.Content, "/var/log/nginx/", filepath.Join(root, "logs")+"/")
	}
	npmPlans.Unlock()
	port, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(listen, "    listen 127.0.0.1:"), ";\n"))
	applied, _, err := service.ApplyNPMImport(context.Background(), preview.Token, []string{"proxy-9", "redirect-8"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Sites) != 2 || len(applied.AuthFiles) != 1 {
		t.Fatalf("apply = %+v", applied)
	}
	startNginx(t, root)
	response, body := siteGet(t, port, "protected.test", "/", "Authorization", "Basic invalid")
	if response.StatusCode != http.StatusOK || body != "" {
		t.Fatalf("imported access: status %d, forwarded Authorization %q", response.StatusCode, body)
	}
	request, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/old/path?x=1", port), nil)
	request.Host = "old.test"
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != "https://new.test" {
		t.Fatalf("imported redirect: status %d, location %q", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestNPMImportKeepsHSTSOnOnlyTheNamedHost(t *testing.T) {
	root := t.TempDir()
	cert, key := presetCertificate(t, root)
	b := &npmBuilder{
		s:     New(root, ""),
		plan:  &npmPlan{layout: "sites-available", items: map[string]*npmPlanned{}},
		names: map[string]bool{}, lists: map[int64]*npmAccess{},
		certs: []Certificate{{Path: cert, Domains: []string{"*.preset.test"}}},
		keys:  map[string]string{cert: key},
	}
	b.host(npmRow{
		"id": int64(1), "domain_names": `["app.preset.test"]`, "forward_scheme": "http",
		"forward_host": "127.0.0.1", "forward_port": int64(3000),
		"certificate_id": int64(1), "ssl_forced": int64(1), "hsts_enabled": int64(1),
		"hsts_subdomains": int64(0),
	}, "proxy")
	item := b.plan.items["proxy-1"]
	if item == nil || item.item.Skipped != "" || item.site == nil ||
		!item.site.HSTSOwnNameOnly || strings.Contains(item.item.Content, "includeSubDomains") {
		t.Fatalf("the imported HSTS scope changed: %+v", item)
	}
}
