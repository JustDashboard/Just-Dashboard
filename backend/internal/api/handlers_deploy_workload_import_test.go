package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func TestWorkloadDiscoveryGroupsStoppedComposeAndPM2InstancesWithoutSecrets(t *testing.T) {
	created := time.Unix(1000, 0).UTC()
	inventory := workloadInventory{
		selfStack: "dashboard", selfID: "dashboard-container",
		containers: []dockerx.Container{
			{ID: "one", Name: "bet-bot-api", ComposeStack: "bet-bot", Labels: map[string]string{"com.docker.compose.project": "bet-bot"}},
			{ID: "dashboard-container", Name: "backend", Labels: map[string]string{"com.docker.compose.project": "dashboard"}},
			{ID: "managed", Name: "managed", Labels: map[string]string{"io.just-dashboard.managed": "true"}},
			{ID: "single", Name: "n8n", State: "running", Command: "--password super-secret-value", Labels: map[string]string{"PASSWORD": "label-secret-value"}, CreatedAt: created},
		},
		stacks: []dockerx.ComposeStack{
			{Name: "bet-bot", Deployed: true, Running: 2, Total: 4, ConfigFiles: []string{"/opt/bet-bot/compose.yaml", "/opt/bet-bot/override.yaml"}, Services: []dockerx.ComposeService{
				{Name: "api", Container: "one", State: "running"}, {Name: "worker", Container: "two", State: "running"},
				{Name: "scheduler", Container: "three", State: "exited"}, {Name: "redis", Container: "four", State: "exited"},
			}},
			{Name: "dashboard", Deployed: true, Running: 2, Total: 2},
			{Name: "unused-compose", Deployed: false, Total: 1},
		},
		pm2: []procs.PM2Process{
			{ID: 0, DaemonID: "alice", Name: "web", Namespace: "default", PID: 9001, Status: "online", CreatedAtMS: 123},
			{ID: 1, DaemonID: "alice", Name: "web", Namespace: "default", PID: 9002, Status: "stopped", CreatedAtMS: 124},
			{ID: 0, DaemonID: "bob", Name: "web", Namespace: "default", PID: 9003, Status: "online", CreatedAtMS: 125},
		},
		units: []procs.Unit{
			{Name: "custom.service", LoadState: "loaded", ActiveState: "active"},
			{Name: "pm2-alice.service", LoadState: "loaded", ActiveState: "active"},
			{Name: "docker.service", LoadState: "loaded", ActiveState: "active"},
			{Name: "jd-terminal-owned.service", LoadState: "loaded", ActiveState: "active"},
		},
		listeners: []proxysvc.Listener{
			{PID: 9001, Process: "node", StartedAt: &created, Address: "127.0.0.1", Port: 3000, Protocol: "tcp"},
			{PID: 9010, Process: "python", Manager: "systemd", ManagerName: "custom.service", StartedAt: &created, Address: "127.0.0.1", Port: 5000, Protocol: "tcp"},
			{PID: 9020, Process: "node", Cmdline: "--token command-secret-value", StartedAt: &created, Address: "::", Port: 4000, Protocol: "tcp"},
			{PID: 9020, Process: "node", StartedAt: &created, Address: "0.0.0.0", Port: 4000, Protocol: "tcp"},
			{PID: 9021, Process: "docker-proxy", StartedAt: &created, Address: "0.0.0.0", Port: 5678, Protocol: "tcp"},
			{PID: 9022, Process: "node", Manager: "container", StartedAt: &created, Address: "0.0.0.0", Port: 6000, Protocol: "tcp"},
		},
	}
	items := workloadCandidates(inventory)
	if len(items) != 6 {
		t.Fatalf("candidates = %#v", items)
	}
	byKey := map[string]deploy.WorkloadCandidate{}
	for _, item := range items {
		byKey[item.Key] = item
	}
	stack := byKey["stack:bet-bot"]
	if stack.Running != 2 || stack.Total != 4 || len(stack.Services) != 4 {
		t.Fatalf("stopped services lost: %#v", stack)
	}
	cluster := byKey["pm2:alice/default/web"]
	if cluster.Total != 2 || cluster.Running != 1 || cluster.State != "partial" || len(cluster.Services[0].Ports) != 1 {
		t.Fatalf("PM2 group = %#v", cluster)
	}
	if _, ok := byKey["pm2:bob/default/web"]; !ok {
		t.Fatal("same app name in another account was merged")
	}
	process := byKey["process:9020/1000000"]
	if len(process.Services) != 1 || len(process.Services[0].Ports) != 2 {
		t.Fatalf("listener grouping = %#v", process)
	}
	encoded, _ := json.Marshal(items)
	for _, secret := range []string{"super-secret-value", "label-secret-value", "command-secret-value"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("discovery leaked %s", secret)
		}
	}
}

func TestWorkloadDigestIgnoresRuntimeStateButFencesContainerReplacementAndPIDReuse(t *testing.T) {
	candidate := deploy.WorkloadCandidate{Key: "stack:bot", Kind: "stack", Total: 1, Services: []deploy.WorkloadService{{ResourceID: "old", State: "exited"}}}
	old := deploy.WorkloadDigest(candidate)
	candidate.Running, candidate.State = 1, "running"
	candidate.Services[0].State = "running"
	if deploy.WorkloadDigest(candidate) != old {
		t.Fatal("runtime state change invalidated inspection")
	}
	candidate.Services[0].ResourceID = "replacement"
	if deploy.WorkloadDigest(candidate) == old {
		t.Fatal("container replacement reused inspection")
	}
	created := time.Unix(1000, 0).UTC()
	first := workloadCandidates(workloadInventory{listeners: []proxysvc.Listener{{PID: 9100, Process: "node", StartedAt: &created}}})
	later := created.Add(time.Second)
	second := workloadCandidates(workloadInventory{listeners: []proxysvc.Listener{{PID: 9100, Process: "node", StartedAt: &later}}})
	if len(first) != 1 || len(second) != 1 || first[0].Key == second[0].Key {
		t.Fatal("PID reuse kept old process identity")
	}
}

func TestWorkloadDiscoveryExcludesDashboardWhenCheckoutLocationIsUnavailable(t *testing.T) {
	items := workloadCandidates(workloadInventory{
		containers: []dockerx.Container{
			{ID: "backend", Image: "just-dashboard-backend:latest", Labels: map[string]string{"com.docker.compose.service": "backend", "com.docker.compose.project": "dashboard"}},
			{ID: "ingress", Labels: map[string]string{"com.just-dashboard.ingress": "true"}},
			{ID: "real-app", Name: "n8n", State: "running"},
		},
		stacks: []dockerx.ComposeStack{{Name: "dashboard", Deployed: true, Running: 3, Total: 3}},
	})
	if len(items) != 1 || items[0].ResourceID != "real-app" {
		t.Fatalf("dashboard resources were offered: %#v", items)
	}
}

func TestWorkloadDiscoveryCanonicalizesUnpublishedContainerPorts(t *testing.T) {
	first := workloadCandidates(workloadInventory{containers: []dockerx.Container{{ID: "one", Name: "app", Ports: []dockerx.Port{
		{PrivatePort: 443, Type: "tcp"}, {PrivatePort: 80, Type: "tcp"}, {PrivatePort: 2019, Type: "tcp"},
	}}}})
	second := workloadCandidates(workloadInventory{containers: []dockerx.Container{{ID: "one", Name: "app", Ports: []dockerx.Port{
		{PrivatePort: 2019, Type: "tcp"}, {PrivatePort: 443, Type: "tcp"}, {PrivatePort: 80, Type: "tcp"},
	}}}})
	if len(first) != 1 || len(second) != 1 || deploy.WorkloadDigest(first[0]) != deploy.WorkloadDigest(second[0]) {
		t.Fatal("Docker's arbitrary EXPOSE ordering invalidated inspection")
	}
}

func TestWorkloadDiscoveryNamesMissingComposeServicesAndCountsReplicas(t *testing.T) {
	items := workloadCandidates(workloadInventory{stacks: []dockerx.ComposeStack{{Name: "app", Deployed: true, Running: 2, Total: 2, Services: []dockerx.ComposeService{
		{Name: "web", Container: "one", State: "running"},
		{Name: "web", Container: "two", State: "running"},
		{Name: "worker", Missing: true},
	}}}})
	if len(items) != 1 || items[0].Total != 3 || items[0].Running != 2 || items[0].Services[2].State != "not created" {
		t.Fatalf("missing services or replica counts misrepresented: %#v", items)
	}
}

func TestWorkloadImportRoutesRequireAdministratorSession(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "workload-reader", auth.RoleReadOnly)}
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "workload-admin", auth.RoleAdmin)}
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='workload-admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminUser, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), adminUser, "workload-import-token", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated := &client{t: t, h: routes}
	for _, item := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/deploy/import/discovery", ""},
		{http.MethodPost, "/api/v1/deploy/import/inspect", `{"key":"container:any"}`},
		{http.MethodPost, "/api/v1/deploy/import/recover", `{"key":"container:any","name":"app","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`},
		{http.MethodPost, "/api/v1/deploy/import/register", `{"key":"container:any","name":"app","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`},
	} {
		response := reader.do(item.method, item.path, item.body, nil)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s = %d %s", item.path, response.Code, response.Body.String())
		}
		response = unauthenticated.do(item.method, item.path, item.body, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s = %d %s", item.path, response.Code, response.Body.String())
		}
		response = unauthenticated.do(item.method, item.path, item.body, map[string]string{"Authorization": "Bearer " + token})
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "session_required") {
			t.Fatalf("API token %s = %d %s", item.path, response.Code, response.Body.String())
		}
		if item.method == http.MethodPost {
			response = admin.do(item.method, item.path, item.body, map[string]string{httpx.CSRFHeader: ""})
			if response.Code != http.StatusForbidden {
				t.Fatalf("missing CSRF %s = %d %s", item.path, response.Code, response.Body.String())
			}
		}
	}
}

func TestWorkloadImportRegistrationFreshlyInspectsAndDoesNotMutateDocker(t *testing.T) {
	s := testServer(t)
	var containerVersion atomic.Int32
	containerVersion.Store(1)
	var mutations atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mutations.Add(1)
			w.WriteHeader(500)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_ping") || r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			fmt.Fprint(w, "OK")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			fmt.Fprintf(w, `[{"Id":"fixture-%d","Names":["/existing-n8n"],"Image":"n8nio/n8n:latest","State":"exited","Created":1000,"Labels":{"PASSWORD":"must-stay-private"}}]`, containerVersion.Load())
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"not found"}`)
	}))
	defer daemon.Close()
	s.modules.docker = dockerx.New(daemon.URL)
	defer s.modules.docker.Close()
	s.Cfg.ComposeRoots = []string{t.TempDir()}
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	inspected := doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/import/inspect", map[string]any{"key": "container:fixture-1"})
	if inspected.Code != http.StatusOK {
		t.Fatalf("inspect = %d %s", inspected.Code, inspected.Body.String())
	}
	var candidate deploy.WorkloadCandidate
	decodePlanningResponse(t, inspected.Body.Bytes(), &candidate)
	if strings.Contains(inspected.Body.String(), "must-stay-private") {
		t.Fatal("label secret leaked")
	}
	changed := candidate
	changed.Services[0].Ports = []dockerx.PortMapping{{HostPort: 1234}}
	stale := doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/import/register", map[string]any{"key": candidate.Key, "name": "existing-n8n", "digest": deploy.WorkloadDigest(changed)})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "workload_changed") {
		t.Fatalf("stale = %d %s", stale.Code, stale.Body.String())
	}
	createdResponse := doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/import/register", map[string]any{"key": candidate.Key, "name": "existing-n8n", "digest": candidate.Digest})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	var result deploy.DraftCommitResult
	decodePlanningResponse(t, createdResponse.Body.Bytes(), &result)
	if result.ProjectID == 0 {
		t.Fatal("registration created no deployment")
	}
	duplicate := doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/import/register", map[string]any{"key": candidate.Key, "name": "another-name", "digest": candidate.Digest})
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), "workload_already_imported") {
		t.Fatalf("duplicate = %d %s", duplicate.Code, duplicate.Body.String())
	}
	for _, table := range []string{"deploy_runs", "deploy_releases", "deploy_release_runtimes", "deploy_git_policies"} {
		var count int
		if err := s.Store.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows=%d err=%v", table, count, err)
		}
	}
	var auditCount int
	if err := s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='deploy.import.register'").Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit rows=%d err=%v", auditCount, err)
	}
	containerVersion.Store(2)
	replacement := doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/import/inspect", map[string]any{"key": "container:fixture-2"})
	if replacement.Code != http.StatusOK {
		t.Fatalf("inspect replacement = %d %s", replacement.Code, replacement.Body.String())
	}
	var replacementCandidate deploy.WorkloadCandidate
	decodePlanningResponse(t, replacement.Body.Bytes(), &replacementCandidate)
	nameTaken := doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/import/register", map[string]any{"key": replacementCandidate.Key, "name": "existing-n8n", "digest": replacementCandidate.Digest})
	if nameTaken.Code != http.StatusConflict || !strings.Contains(nameTaken.Body.String(), "name_taken") {
		t.Fatalf("name collision = %d %s", nameTaken.Code, nameTaken.Body.String())
	}
	missing := doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/import/register", map[string]any{"key": candidate.Key, "name": "existing-n8n", "digest": candidate.Digest})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("replaced container = %d %s", missing.Code, missing.Body.String())
	}
	if mutations.Load() != 0 {
		t.Fatal("import mutated Docker")
	}
}
