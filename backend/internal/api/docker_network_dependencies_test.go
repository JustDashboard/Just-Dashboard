package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// networkEngine is a Docker Engine fixture: the dashboard's own stack (its
// backend mounts the data directory, its frontend sits on jd_internal), a
// shared ingress and an application on lab, a network only a stopped
// container names, and a deployment's network.
type networkEngine struct {
	mu        sync.Mutex
	mutations []string
	listFails bool
}

func (e *networkEngine) serve(t *testing.T, dataDir string, environment int64) http.HandlerFunc {
	networks := map[string]string{
		"jd_internal": `{"Id":"n-internal","Name":"jd_internal","Driver":"bridge","Scope":"local","Labels":{"com.docker.compose.project":"just-dashboard"},"IPAM":{"Config":[{"Subnet":"10.10.0.0/24"}]},"Containers":{"frontend":{"Name":"jd-frontend"}}}`,
		"lab":         `{"Id":"n-lab","Name":"lab","Driver":"bridge","Scope":"local","Labels":{},"IPAM":{"Config":[{"Subnet":"10.4.0.0/24"}]},"Containers":{"api":{"Name":"api"},"ingress":{"Name":"just-dashboard-ingress"}}}`,
		"dormant":     `{"Id":"n-dormant","Name":"dormant","Driver":"bridge","Scope":"local","Labels":{},"IPAM":{"Config":[]},"Containers":{}}`,
		"managed":     fmt.Sprintf(`{"Id":"n-managed","Name":"managed","Driver":"bridge","Scope":"local","Labels":{"io.just-dashboard.managed":"true","io.just-dashboard.environment-id":"%d"},"IPAM":{"Config":[]},"Containers":{}}`, environment),
		"spare":       `{"Id":"n-spare","Name":"spare","Driver":"bridge","Scope":"local","Labels":{},"IPAM":{"Config":[]},"Containers":{}}`,
	}
	containers := fmt.Sprintf(`[
		{"Id":"backend","Names":["/jd-backend"],"Image":"just-dashboard-backend","State":"running","Labels":{"com.docker.compose.project":"just-dashboard","com.docker.compose.service":"backend"},"Mounts":[{"Type":"bind","Destination":%q}],"NetworkSettings":{"Networks":{"host":{}}}},
		{"Id":"frontend","Names":["/jd-frontend"],"Image":"just-dashboard-frontend","State":"running","Labels":{"com.docker.compose.project":"just-dashboard","com.docker.compose.service":"frontend"},"NetworkSettings":{"Networks":{"jd_internal":{}}}},
		{"Id":"api","Names":["/api"],"Image":"api","State":"running","Labels":{},"NetworkSettings":{"Networks":{"lab":{},"bridge":{}}}},
		{"Id":"ingress","Names":["/just-dashboard-ingress"],"Image":"caddy:2-alpine","State":"running","Labels":{"com.just-dashboard.ingress":"true"},"NetworkSettings":{"Networks":{"lab":{},"bridge":{}}}},
		{"Id":"batch","Names":["/batch"],"Image":"batch","State":"exited","Labels":{},"NetworkSettings":{"Networks":{"dormant":{}}}}
	]`, dataDir)
	inspect := func(id, name, project, networks string) string {
		return fmt.Sprintf(`{"Id":%q,"Name":"/%s","State":{"Status":"running"},"Config":{"Labels":{"com.docker.compose.project":%q}},"HostConfig":{"NetworkMode":"bridge"},"NetworkSettings":{"Networks":%s}}`, id, name, project, networks)
	}
	containerInspects := map[string]string{
		"frontend": inspect("frontend", "jd-frontend", "just-dashboard", `{"jd_internal":{"Aliases":["frontend"]}}`),
		"api":      inspect("api", "api", "", `{"lab":{"Aliases":["app"]},"bridge":{}}`),
		"ingress":  inspect("ingress", "just-dashboard-ingress", "", `{"lab":{},"bridge":{}}`),
	}
	for _, alias := range []string{"jd-frontend", "just-dashboard-ingress"} {
		for id, body := range containerInspects {
			if strings.Contains(body, `"/`+alias+`"`) {
				containerInspects[alias] = containerInspects[id]
			}
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path[strings.Index(r.URL.Path[1:], "/")+1:]
		e.mu.Lock()
		defer e.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && path == "/containers/json":
			if e.listFails {
				http.Error(w, `{"message":"daemon busy"}`, http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(containers))
		case r.Method == http.MethodGet && path == "/networks":
			list := []json.RawMessage{}
			for _, body := range networks {
				list = append(list, json.RawMessage(body))
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/networks/"):
			name := strings.TrimPrefix(path, "/networks/")
			for key, body := range networks {
				if key == name || strings.Contains(body, `"Id":"`+name+`"`) {
					_, _ = w.Write([]byte(body))
					return
				}
			}
			http.Error(w, `{"message":"No such network"}`, http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
			if body, ok := containerInspects[id]; ok {
				_, _ = w.Write([]byte(body))
				return
			}
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
		case r.Method == http.MethodPost || r.Method == http.MethodDelete:
			e.mutations = append(e.mutations, r.Method+" "+path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected Engine request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func (e *networkEngine) changed() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.mutations...)
}

func networkDependencyRouter(t *testing.T) (*Server, *networkEngine, http.Handler, int64) {
	t.Helper()
	s, router := gatewayRouter(t, auth.RoleAdmin, false)
	project, err := s.Store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('shop', '/srv/shop', 'sealed', 'shop-hook', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := project.LastInsertId()
	environment, err := s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id,name,slug,kind,created_at,updated_at) VALUES(?,'production','production','production',1,1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := environment.LastInsertId()
	fake := &networkEngine{}
	engine := httptest.NewServer(fake.serve(t, s.Cfg.DataDir, environmentID))
	t.Cleanup(engine.Close)
	original := s.modules.docker
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close(); s.modules.docker = original })
	s.mountDockerRoutes(router)
	return s, fake, router, environmentID
}

func TestNetworkMutationsRefuseWhatTheirPreviewBlocks(t *testing.T) {
	_, fake, router, _ := networkDependencyRouter(t)
	var preview networkChangePreview
	rec := gwDo(router, http.MethodGet, "/docker/networks/jd_internal/disconnect?container=jd-frontend", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &preview) != nil || !preview.Blocked || preview.Owner.Kind != dockerx.NetworkOwnerDashboard {
		t.Fatalf("detaching the dashboard's frontend must preview as blocked: %d %s", rec.Code, rec.Body.String())
	}
	for _, test := range []struct{ name, path, body, code string }{
		{"dashboard container", "/docker/networks/jd_internal/disconnect", `{"container":"jd-frontend"}`, "network_conflict"},
		{"shared ingress", "/docker/networks/lab/disconnect", `{"container":"just-dashboard-ingress","force":true}`, "network_conflict"},
		{"dashboard network", "/docker/networks/jd_internal/connect", `{"container":"api"}`, "network_conflict"},
		{"invalid alias", "/docker/networks/lab/connect", `{"container":"frontend","aliases":["no spaces"]}`, "network_conflict"},
	} {
		rec := gwDo(router, http.MethodPost, test.path, test.body)
		if code, _ := gwErr(rec); rec.Code != http.StatusConflict || code != test.code {
			t.Fatalf("%s: %d %s", test.name, rec.Code, rec.Body.String())
		}
	}
	if changed := fake.changed(); len(changed) != 0 {
		t.Fatalf("a refused change reached the Engine: %v", changed)
	}
	rec = gwDo(router, http.MethodGet, "/docker/networks/lab/disconnect?container=api", "")
	if json.Unmarshal(rec.Body.Bytes(), &preview) != nil || preview.Blocked || !strings.Contains(rec.Body.String(), `"peers"`) {
		t.Fatalf("an ordinary member previews its peers without blocking: %s", rec.Body.String())
	}
	if rec := gwDo(router, http.MethodPost, "/docker/networks/lab/disconnect", `{"container":"api"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("an ordinary detach: %d %s", rec.Code, rec.Body.String())
	}
	if changed := fake.changed(); len(changed) != 1 || changed[0] != "POST /networks/n-lab/disconnect" {
		t.Fatalf("the detach went to the inspected network by ID: %v", changed)
	}
}

func TestNetworkRemovalAndPruneKeepWhatIsStillNamed(t *testing.T) {
	_, fake, router, _ := networkDependencyRouter(t)
	var preview networkChangePreview
	rec := gwDo(router, http.MethodGet, "/docker/networks/managed/removal", "")
	if json.Unmarshal(rec.Body.Bytes(), &preview) != nil || !preview.Blocked || preview.Owner.Deployment != "shop · production" {
		t.Fatalf("a live deployment's network: %d %s", rec.Code, rec.Body.String())
	}
	if rec := gwDo(router, http.MethodDelete, "/docker/networks/managed", ""); rec.Code != http.StatusConflict {
		t.Fatalf("removing a live deployment's network must be refused: %d %s", rec.Code, rec.Body.String())
	}
	rec = gwDo(router, http.MethodGet, "/docker/networks/dormant/removal", "")
	if json.Unmarshal(rec.Body.Bytes(), &preview) != nil || preview.Blocked || !strings.Contains(rec.Body.String(), "batch still names this network") {
		t.Fatalf("a network a stopped container names is removable after its warning: %s", rec.Body.String())
	}
	var prune networkPrunePreview
	rec = gwDo(router, http.MethodGet, "/docker/networks/prune", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &prune) != nil {
		t.Fatalf("prune preview: %d %s", rec.Code, rec.Body.String())
	}
	removable := map[string]bool{}
	for _, c := range prune.Candidates {
		removable[c.Name] = c.Removable
	}
	if len(removable) != 3 || !removable["spare"] || removable["dormant"] || removable["managed"] {
		t.Fatalf("prune candidates: %+v", prune.Candidates)
	}
	rec = gwDo(router, http.MethodPost, "/docker/networks/prune", `{"ids":["n-spare","n-dormant","n-lab"]}`)
	var result struct {
		Items   []string           `json:"items"`
		Skipped []networkPruneSkip `json:"skipped"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result.Items) != 1 || result.Items[0] != "spare" || len(result.Skipped) != 2 {
		t.Fatalf("the reviewed prune removes only what is still removable: %d %s", rec.Code, rec.Body.String())
	}
	if changed := fake.changed(); len(changed) != 1 || changed[0] != "DELETE /networks/n-spare" {
		t.Fatalf("the Engine's own prune must never run: %v", changed)
	}
	if rec := gwDo(router, http.MethodDelete, "/docker/networks/dormant", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("a confirmed removal of a network only stopped containers name: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNetworkInventoryNamesOwnersAndUnreadMembership(t *testing.T) {
	_, fake, router, _ := networkDependencyRouter(t)
	var list []dockerx.Network
	rec := gwDo(router, http.MethodGet, "/docker/networks/", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	owners := map[string]string{}
	for _, n := range list {
		if !n.MembersKnown || n.Owner == nil {
			t.Fatalf("%s: membership and owner must be read: %+v", n.Name, n)
		}
		owners[n.Name] = n.Owner.Kind + "/" + n.Owner.Deployment
	}
	if owners["jd_internal"] != "dashboard/" || owners["managed"] != "deployment/shop · production" || owners["spare"] != "manual/" {
		t.Fatalf("owners: %v", owners)
	}
	var detail dockerx.NetworkDetail
	rec = gwDo(router, http.MethodGet, "/docker/networks/lab", "")
	if json.Unmarshal(rec.Body.Bytes(), &detail) != nil {
		t.Fatalf("detail: %s", rec.Body.String())
	}
	for _, m := range detail.Members {
		if m.Name == "just-dashboard-ingress" && !m.Ingress {
			t.Fatalf("the ingress member must be marked: %+v", m)
		}
		if m.Name == "api" && (len(m.Networks) != 1 || m.Networks[0] != "bridge") {
			t.Fatalf("a member's other networks are its topology: %+v", m)
		}
	}
	fake.mu.Lock()
	fake.listFails = true
	fake.mu.Unlock()
	rec = gwDo(router, http.MethodGet, "/docker/networks/", "")
	if json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) == 0 || list[0].MembersKnown || list[0].MembersError == "" {
		t.Fatalf("a failed container listing is not an empty network: %s", rec.Body.String())
	}
	if rec := gwDo(router, http.MethodGet, "/docker/networks/prune", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a prune preview without the containers must not guess: %d %s", rec.Code, rec.Body.String())
	}
	if rec := gwDo(router, http.MethodDelete, "/docker/networks/spare", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a removal without its dependents read must not proceed: %d %s", rec.Code, rec.Body.String())
	}
}
