package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The maintenance switch takes the application away from its visitors, so it
// is a destructive action as stopping it is; a page is written as the
// configuration is. Both are audited, and neither writes the route.
func TestDeploymentMaintenanceRoutesEnforceCapabilityAndAudit(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.modules.proxy = proxysvc.New(root, filepath.Join(root, "Caddyfile"))
	name := deploy.RouteNameFor(environmentID)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "maintenance-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "maintenance-limited", auth.RoleLimited)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "maintenance-reader", auth.RoleReadOnly)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	state := func(response interface{ Bytes() []byte }) proxysvc.MaintenanceSwitch {
		t.Helper()
		var out proxysvc.MaintenanceSwitch
		if err := json.Unmarshal(response.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// No route on this host yet: nothing to switch.
	if response := admin.do(http.MethodPut, base+"/maintenance", `{"on":true}`, nil); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"no_public_route"`) {
		t.Fatalf("switch without a route = %d %s", response.Code, response.Body.String())
	}
	// The route a release from before the switch wrote.
	route := filepath.Join(root, "sites-available", name)
	if err := os.WriteFile(route, []byte("server {\n    listen 80;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if response := admin.do(http.MethodPut, base+"/maintenance", `{"on":true}`, nil); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"maintenance_unready"`) {
		t.Fatalf("switch on an old route = %d %s", response.Code, response.Body.String())
	}
	// The route a release writes now carries the check.
	flag := filepath.Join(root, "jd-pages", name, "maintenance.on")
	content := "server {\n    if (-f " + flag + ") {\n        set $jd_x_maint $jd_x_maint_ip;\n    }\n}\n"
	if err := os.WriteFile(route, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	response := reader.do(http.MethodGet, base+"/maintenance", "", nil)
	if read := state(response.Body); response.Code != http.StatusOK || !read.Ready || read.On || read.Driver != "nginx" {
		t.Fatalf("reader's view = %d %s", response.Code, response.Body.String())
	}
	for _, refused := range []*client{reader, limited} {
		if response := refused.do(http.MethodPut, base+"/maintenance", `{"on":true}`, nil); response.Code != http.StatusForbidden {
			t.Fatalf("switch without the destructive capability = %d %s", response.Code, response.Body.String())
		}
	}
	if _, err := os.Stat(flag); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused switch was written")
	}
	response = admin.do(http.MethodPut, base+"/maintenance", `{"on":true}`, nil)
	if on := state(response.Body); response.Code != http.StatusOK || !on.On || on.Since == nil {
		t.Fatalf("switch on = %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(flag); err != nil {
		t.Fatalf("the switch file = %v", err)
	}
	if after, _ := os.ReadFile(route); string(after) != content {
		t.Fatal("switching maintenance rewrote the route")
	}
	response = admin.do(http.MethodPut, base+"/maintenance", `{"on":false}`, nil)
	if off := state(response.Body); response.Code != http.StatusOK || off.On {
		t.Fatalf("switch off = %d %s", response.Code, response.Body.String())
	}
	other := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/maintenance", projectID+1, environmentID)
	if response := admin.do(http.MethodPut, other, `{"on":true}`, nil); response.Code != http.StatusNotFound {
		t.Fatalf("another project's environment = %d %s", response.Code, response.Body.String())
	}

	// Pages: read as the deployment is, written as its configuration is.
	response = reader.do(http.MethodGet, base+"/pages/502", "", nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"custom":true`) {
		t.Fatalf("reader's 502 = %d %s", response.Code, response.Body.String())
	}
	if response := limited.do(http.MethodPut, base+"/pages/502", `{"content":"<h1>x</h1>"}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("limited page write = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPut, base+"/pages/security", `{"content":"x"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("a page the route has not = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPut, base+"/pages/502", `{"content":"<h1>Back soon</h1>"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("admin page write = %d %s", response.Code, response.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(root, "jd-pages", name, "502.html")); err != nil || string(b) != "<h1>Back soon</h1>" {
		t.Fatalf("502 page on disk = %q, %v", b, err)
	}

	var audits string
	if err := s.Store.DB.QueryRow(`SELECT COALESCE(group_concat(action || ' ' || target || ' ' || detail, char(10)), '') FROM audit_log
		WHERE action IN ('deploy.maintenance', 'deploy.page')`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	target := fmt.Sprint(environmentID)
	for _, want := range []string{
		"deploy.maintenance " + target + ` {"deploymentId":`,
		`"on":true,"result":"unready"`,
		`"on":true}`,
		`"on":false}`,
		"deploy.page " + target,
		`"bytes":18`,
		`"page":"security","result":"failed"`,
	} {
		if !strings.Contains(audits, want) {
			t.Fatalf("audit is missing %q:\n%s", want, audits)
		}
	}
}
