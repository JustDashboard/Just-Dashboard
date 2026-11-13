package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

// The unit's Environment property carries credentials as a container's
// environment does: an administrator reads them, anybody else reads them
// masked, in the property's own format.
func TestUnitShowMasksTheEnvironmentForAnyoneButAnAdministrator(t *testing.T) {
	s, _ := engineHost(t, map[string]string{"systemctl": `
printf '%s\n' 'Id=app.service' 'ActiveState=active' 'Environment=A=1 DB_PASSWORD=hunter2 "GREETING=hello world"' 'ExecStart={ path=/usr/bin/app ; argv[]=/usr/bin/app --token=t0k ; }'
`})
	read := func(c *client) map[string]string {
		t.Helper()
		w := c.do(http.MethodGet, "/api/v1/systemd/app.service", "", nil)
		var body struct{ Properties map[string]string }
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("show: %d %s", w.Code, w.Body.String())
		}
		return body.Properties
	}
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	if got := read(admin)["Environment"]; got != `A=1 DB_PASSWORD=hunter2 "GREETING=hello world"` {
		t.Fatalf("administrator's Environment = %s", got)
	}
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "show-"+string(role), role)}
		props := read(c)
		env := props["Environment"]
		if strings.Contains(env, "hunter2") || !slices.Contains(procs.SplitEnvironment(env), "DB_PASSWORD="+dockerx.RedactedEnvValue) ||
			!slices.Contains(procs.SplitEnvironment(env), "GREETING=hello world") {
			t.Fatalf("%s Environment = %s, want the password masked and the rest kept", role, env)
		}
		// Commands are left as they are.
		if !strings.Contains(props["ExecStart"], "--token=t0k") {
			t.Fatalf("%s ExecStart = %s", role, props["ExecStart"])
		}
	}
}

// The unit's files are read from the host as systemd names them, quoted
// drop-in paths included, and their credentials masked for anyone but an
// administrator.
func TestUnitFilesAreReadAndMaskedForAnyoneButAnAdministrator(t *testing.T) {
	dir := t.TempDir()
	fragment := filepath.Join(dir, "app.service")
	dropIn := filepath.Join(dir, "app.service.d", "my override.conf")
	if err := os.MkdirAll(filepath.Dir(dropIn), 0o755); err != nil {
		t.Fatal(err)
	}
	const unit = "[Service]\nEnvironment=DB_PASSWORD=hunter2 PORT=8080\nEnvironmentFile=/etc/app/secrets.env\nExecStart=/usr/bin/app\n"
	if err := os.WriteFile(fragment, []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropIn, []byte("[Service]\nSetCredential=dbpass:hunter2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, bin := engineHost(t, map[string]string{"systemctl": `
printf '%s\n' 'FragmentPath=` + fragment + `' 'DropInPaths="` + dropIn + `"' 'NeedDaemonReload=yes'
`})
	read := func(c *client) procs.UnitFiles {
		t.Helper()
		w := c.do(http.MethodGet, "/api/v1/systemd/app.service/files", "", nil)
		var body procs.UnitFiles
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("files: %d %s", w.Code, w.Body.String())
		}
		return body
	}
	admin := read(&client{t: t, h: s.Routes(), cookie: signIn(t, s)})
	if !admin.NeedDaemonReload || len(admin.Files) != 2 {
		t.Fatalf("files = %+v", admin)
	}
	if f := admin.Files[0]; f.Path != fragment || f.DropIn || f.Content != unit || f.Size != int64(len(unit)) || f.Error != "" {
		t.Fatalf("fragment = %+v", f)
	}
	if f := admin.Files[1]; f.Path != dropIn || !f.DropIn || !strings.Contains(f.Content, "dbpass:hunter2") {
		t.Fatalf("drop-in = %+v", f)
	}
	if log := shimLog(t, bin, "systemctl"); log != "show --no-pager -p FragmentPath,DropInPaths,NeedDaemonReload -- app.service\n" {
		t.Fatalf("systemctl ran %q", log)
	}

	limited := read(&client{t: t, h: s.Routes(), cookie: signInAs(t, s, "files-limited", auth.RoleLimited)})
	masked := limited.Files[0].Content
	if strings.Contains(masked, "hunter2") || !strings.Contains(masked, `Environment="DB_PASSWORD=`+dockerx.RedactedEnvValue+`" PORT=8080`) ||
		!strings.Contains(masked, "EnvironmentFile=/etc/app/secrets.env\n") {
		t.Fatalf("limited fragment = %q", masked)
	}
	if got := limited.Files[1].Content; strings.Contains(got, "hunter2") || !strings.Contains(got, "SetCredential=dbpass:"+dockerx.RedactedEnvValue) {
		t.Fatalf("limited drop-in = %q", got)
	}
}

// systemd's exposure assessment, and the answer for a unit systemd will not
// assess: available false with why, not an error.
func TestUnitSecurityAnswersEvenWhenSystemdWillNot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("systemd-analyze is the host's when running as root")
	}
	s, bin := engineHost(t, map[string]string{"systemd-analyze": `
case "$*" in
*apt-daily.timer*) echo 'Unit apt-daily.timer is not a service unit, refusing.' >&2; exit 1 ;;
*--json=short*) printf '%s\n' '[{"set":null,"name":"SupplementaryGroups=","json_field":"SupplementaryGroups","description":"Service runs as root, option does not matter","exposure":null},{"set":false,"name":"User=/DynamicUser=","json_field":"UserOrDynamicUser","description":"Service runs as root user","exposure":"0.4"}]' ;;
*) printf '%s\n' '' '→ Overall exposure level for cron.service: 9.6 UNSAFE :-{' ;;
esac
`})
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "security-reader", auth.RoleReadOnly)}
	w := reader.do(http.MethodGet, "/api/v1/systemd/cron.service/security", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("security: %d %s", w.Code, w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	checks, _ := raw["checks"].([]any)
	if raw["available"] != true || raw["score"] != 9.6 || raw["verdict"] != "UNSAFE" || len(checks) != 2 || raw["reason"] != nil {
		t.Fatalf("report = %s", w.Body.String())
	}
	first, _ := checks[0].(map[string]any)
	last, _ := checks[1].(map[string]any)
	if first["field"] != "UserOrDynamicUser" || first["exposure"] != 0.4 || first["set"] != false ||
		last["set"] != nil || last["exposure"] != nil || !reflect.DeepEqual(sortedKeysOf(last), []string{"description", "exposure", "field", "name", "set"}) {
		t.Fatalf("checks = %v", checks)
	}
	if log := shimLog(t, bin, "systemd-analyze"); log != "security --json=short --no-pager -- cron.service\nsecurity --no-pager -- cron.service\n" {
		t.Fatalf("systemd-analyze ran %q", log)
	}

	w = reader.do(http.MethodGet, "/api/v1/systemd/apt-daily.timer/security", "", nil)
	raw = nil
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &raw) != nil {
		t.Fatalf("timer: %d %s", w.Code, w.Body.String())
	}
	if raw["available"] != false || !strings.Contains(raw["reason"].(string), "services only") ||
		raw["score"] != nil || !reflect.DeepEqual(raw["checks"], []any{}) {
		t.Fatalf("timer report = %s", w.Body.String())
	}
	if w := reader.do(http.MethodGet, "/api/v1/systemd/-x.service/security", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("an invalid name answered %d", w.Code)
	}
}

// The units a unit names in its dependencies, read in one show over all of
// them, alias answered as the unit it points at.
func TestUnitRelatedListsTheDependenciesWithTheirState(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"systemctl": `
case "$*" in
*Requires*) printf '%s\n' 'Id=app.service' 'Requires=sysinit.target' 'Wants=dbus-org.freedesktop.resolve1.service' 'After=sysinit.target app.service dev-disk-by\x2duuid-1.device' 'WantedBy=multi-user.target' ;;
*) printf '%s\n' 'Id=sysinit.target' 'Description=System Initialization' 'LoadState=loaded' 'ActiveState=active' 'SubState=active' 'UnitFileState=static' '' \
	'Id=systemd-resolved.service' 'Description=Network Name Resolution' 'LoadState=loaded' 'ActiveState=active' 'SubState=running' 'UnitFileState=enabled' '' \
	'Id=multi-user.target' 'Description=Multi-User System' 'LoadState=loaded' 'ActiveState=active' 'SubState=active' 'UnitFileState=static' ;;
esac
`})
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "related-reader", auth.RoleReadOnly)}
	w := c.do(http.MethodGet, "/api/v1/systemd/app.service/related", "", nil)
	var body struct{ Units []procs.RelatedUnit }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("related: %d %s", w.Code, w.Body.String())
	}
	var names []string
	for _, u := range body.Units {
		names = append(names, u.Name)
	}
	if !slices.Equal(names, []string{"sysinit.target", "systemd-resolved.service", "multi-user.target"}) {
		t.Fatalf("related = %+v", body.Units)
	}
	resolved := body.Units[1]
	if resolved.ActiveState != "active" || resolved.SubState != "running" || resolved.UnitFileState != "enabled" ||
		resolved.Description != "Network Name Resolution" || !slices.Equal(resolved.Relations, []string{"Wants"}) {
		t.Fatalf("resolved = %+v", resolved)
	}
	if !slices.Equal(body.Units[0].Relations, []string{"Requires", "After"}) {
		t.Fatalf("sysinit relations = %q", body.Units[0].Relations)
	}
	log := strings.Split(strings.TrimSpace(shimLog(t, bin, "systemctl")), "\n")
	if len(log) != 2 || log[1] != "show --no-pager -p Id,Description,LoadState,ActiveState,SubState,UnitFileState -- sysinit.target dbus-org.freedesktop.resolve1.service multi-user.target" {
		t.Fatalf("systemctl ran %q", log)
	}

	// A unit that names nothing is an empty list, and nothing more is asked.
	s, _ = engineHost(t, map[string]string{"systemctl": "printf '%s\\n' 'Id=lonely.service' 'Requires=' 'After='\n"})
	c = &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	w = c.do(http.MethodGet, "/api/v1/systemd/lonely.service/related", "", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"units":[]}` {
		t.Fatalf("lonely: %d %s", w.Code, w.Body.String())
	}
}

// Every new control reaches systemctl as an argv of validated parts, is
// audited, and is gated as the page's other controls are.
func TestUnitControlsAreGatedAuditedAndExact(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"systemctl": ""})
	// More destructive requests than one burst of the default budget.
	s.destrLim = httpx.NewLimiter(1<<20, 1<<20)
	h := s.Routes()
	admin := &client{t: t, h: h, cookie: signIn(t, s)}
	for _, c := range []struct {
		method, path, body string
		argv               string
		action             string
	}{
		{"POST", "kill", `{"signal":"sighup","whom":"main"}`, "kill --signal=SIGHUP --kill-who=main -- app.service", "systemd.kill"},
		{"POST", "kill", `{"signal":"SIGUSR1"}`, "kill --signal=SIGUSR1 --kill-who=all -- app.service", "systemd.kill"},
		{"POST", "mask", "", "mask app.service", "systemd.mask"},
		{"POST", "unmask", "", "unmask app.service", "systemd.unmask"},
		{"POST", "freeze", "", "freeze app.service", "systemd.freeze"},
		{"POST", "thaw", "", "thaw app.service", "systemd.thaw"},
		{"PUT", "limits", `{"memoryMax":"536870912","cpuQuota":"","persistent":false}`,
			"set-property --runtime -- app.service MemoryMax=536870912 CPUQuota=", "systemd.set-property"},
		{"PUT", "limits", `{"tasksMax":"infinity","persistent":true}`,
			"set-property -- app.service TasksMax=infinity", "systemd.set-property"},
	} {
		before := shimLog(t, bin, "systemctl")
		w := admin.do(c.method, "/api/v1/systemd/app.service/"+c.path, c.body, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
		if ran := strings.TrimPrefix(shimLog(t, bin, "systemctl"), before); ran != c.argv+"\n" {
			t.Errorf("%s %s ran %q, want %q", c.method, c.path, ran, c.argv)
		}
		var action, detail string
		if err := s.Store.DB.QueryRow(`SELECT action, detail FROM audit_log WHERE target = 'app.service' AND success = 1
			ORDER BY id DESC LIMIT 1`).Scan(&action, &detail); err != nil || action != c.action {
			t.Errorf("%s %s audited %q (%v)", c.method, c.path, action, err)
		}
		if c.path == "kill" && !(strings.Contains(detail, `"signal":"SIG`) && strings.Contains(detail, `"whom":`) && strings.Contains(detail, `"exitCode":0`)) {
			t.Errorf("kill audit detail = %s", detail)
		}
		if c.path == "limits" && !strings.Contains(detail, `"persistent":`) {
			t.Errorf("limits audit detail = %s", detail)
		}
	}
	limitsDetail := ""
	_ = s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'systemd.set-property' ORDER BY id LIMIT 1`).Scan(&limitsDetail)
	if !strings.Contains(limitsDetail, `"memoryMax":"536870912"`) || !strings.Contains(limitsDetail, `"cpuQuota":""`) || strings.Contains(limitsDetail, "tasksMax") {
		t.Errorf("limits audit detail = %s", limitsDetail)
	}

	// Bad values never reach systemctl.
	before := shimLog(t, bin, "systemctl")
	for _, c := range []struct{ method, path, body string }{
		{"POST", "kill", `{"signal":"SIGSEGV"}`},
		{"POST", "kill", `{"signal":"HUP"}`},
		{"POST", "kill", `{}`},
		{"POST", "kill", `{"signal":"SIGHUP","whom":"control"}`},
		{"PUT", "limits", `{"persistent":true}`},
		{"PUT", "limits", `{"memoryMax":"1G"}`},
		{"PUT", "limits", `{"cpuQuota":"150"}`},
		{"PUT", "limits", `{"tasksMax":"8 MemoryMax=1"}`},
	} {
		if w := admin.do(c.method, "/api/v1/systemd/app.service/"+c.path, c.body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s answered %d %s", c.method, c.path, c.body, w.Code, w.Body.String())
		}
	}
	if w := admin.do(http.MethodPost, "/api/v1/systemd/-x.service/mask", "", nil); w.Code != http.StatusBadRequest {
		t.Errorf("an invalid name answered %d", w.Code)
	}
	if after := shimLog(t, bin, "systemctl"); after != before {
		t.Fatalf("a refused request ran systemctl: %q", strings.TrimPrefix(after, before))
	}

	// The gates: thaw is service control, unmask and limits are system.admin,
	// kill and freeze destructive, and mask both.
	gates := routeGates(t, h)
	destructive := map[string]bool{}
	_ = chi.Walk(h.(chi.Routes), func(method, pattern string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		destructive[method+" "+pattern] = isDestructive(mws)
		return nil
	})
	for route, want := range map[string]struct {
		capabilities int
		destructive  bool
	}{
		"POST /api/v1/systemd/{name}/thaw":   {1, false},
		"POST /api/v1/systemd/{name}/unmask": {1, false},
		"PUT /api/v1/systemd/{name}/limits":  {1, false},
		"POST /api/v1/systemd/{name}/kill":   {1, true},
		"POST /api/v1/systemd/{name}/freeze": {1, true},
		"POST /api/v1/systemd/{name}/mask":   {2, true},
		"GET /api/v1/systemd/{name}/files":   {0, false},
	} {
		if got := gates[route][0]; got != want.capabilities || destructive[route] != want.destructive {
			t.Errorf("%s: %d capability checks, destructive %v; want %d, %v", route, got, destructive[route], want.capabilities, want.destructive)
		}
	}
	limited := &client{t: t, h: h, cookie: signInAs(t, s, "controls-limited", auth.RoleLimited)}
	reader := &client{t: t, h: h, cookie: signInAs(t, s, "controls-reader", auth.RoleReadOnly)}
	if w := limited.do(http.MethodPost, "/api/v1/systemd/app.service/thaw", "", nil); w.Code != http.StatusOK {
		t.Errorf("a limited account's thaw answered %d", w.Code)
	}
	if w := reader.do(http.MethodPost, "/api/v1/systemd/app.service/thaw", "", nil); w.Code != http.StatusForbidden {
		t.Errorf("a read-only account's thaw answered %d", w.Code)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "kill", `{"signal":"SIGHUP"}`}, {"POST", "freeze", ""}, {"POST", "mask", ""},
		{"POST", "unmask", ""}, {"PUT", "limits", `{"tasksMax":"10"}`},
	} {
		if w := limited.do(c.method, "/api/v1/systemd/app.service/"+c.path, c.body, nil); w.Code != http.StatusForbidden {
			t.Errorf("a limited account's %s answered %d", c.path, w.Code)
		}
	}
}

// A change the assistant makes to a unit is shown on that unit's page; one
// to systemd itself, on the Services page.
func TestAUnitChangeIsShownOnTheServicePage(t *testing.T) {
	for path, want := range map[string]agentChange{
		"/systemd/nginx.service/restart": {Label: "Service", Href: "/processes/services/nginx.service"},
		"/systemd/app@1.service/limits":  {Label: "Service", Href: "/processes/services/app@1.service"},
		"/systemd/daemon-reload":         {Label: "Services", Href: "/processes/services"},
		"/processes/812/signal":          {Label: "Process", Href: "/processes/812"},
	} {
		got := agentAPIChange(&apiRoute{Pattern: path}, path)
		if got == nil || got.Label != want.Label || got.Href != want.Href {
			t.Errorf("%s is shown on %+v, want %+v", path, got, want)
		}
	}
}
