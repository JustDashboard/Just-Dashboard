package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/media"
)

// seedStackDeployment is a stack deployment as a commit of its preset left
// it: the preset's template rendered and saved as the environment's source,
// build, runtime, checks and domains, registered as the stack. keep chooses
// which of the render's domains it has, standing in for a stack made before
// the apps had addresses (only the primary) or one made without a domain.
func seedStackDeployment(t *testing.T, s *Server, name, template, preset string, keep func(deploy.PlannedDomain) bool) (int64, int64) {
	t.Helper()
	source := deploy.DraftSourceConfig{Kind: deploy.SourceCompose, Mode: deploy.SourceModeBlueprint,
		BlueprintID: template, BlueprintVersion: "1.1.0", BlueprintInputs: map[string]string{"domain": "media.example.test"}}
	plan, err := deploy.RenderBlueprintPlan(source, name)
	if err != nil {
		t.Fatal(err)
	}
	source.ComposeFiles = plan.ComposeFiles
	configuration := plan.Configuration
	db := s.Store.DB
	now := time.Now().Unix()
	result, err := db.Exec(`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, 'compose', '', 'main', '', '', ?, 1, ?, ?)`, name, name+"-hook", now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = db.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, ?, ?)`, projectID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(plan.Detection.Source)
	buildJSON, _ := json.Marshal(configuration.Build)
	runtimeJSON, _ := json.Marshal(configuration.Runtime)
	// The Compose analysis travels in the build plan's evidence, as a
	// commit leaves it: a service domain is held to it on every save.
	evidenceJSON, _ := json.Marshal(deploy.StoredBuildEvidence{Compose: plan.Detection.Compose})
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at) VALUES(?, 1, 'compose', ?, 0, ?, 'sha256:seed', ?)`,
			[]any{environmentID, string(sourceJSON), string(identityJSON), now}},
		{`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 1, 'compose', ?, ?, '{}', 'sha256:build-seed', ?)`,
			[]any{environmentID, string(buildJSON), string(evidenceJSON), now}},
		{`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, ?, '{}', 'sha256:runtime-seed', ?)`,
			[]any{environmentID, string(runtimeJSON), now}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, domain := range configuration.Domains {
		if !keep(domain) {
			continue
		}
		row := map[string]any{"hostname": domain.Hostname, "https": domain.HTTPS}
		if domain.Service != "" {
			row["service"] = domain.Service
		}
		config, _ := json.Marshal(row)
		if _, err := db.Exec(`INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
			VALUES(?, 0, 'domain', 'managed', 'proxy_site', ?, ?, ?)`, environmentID, domain.Hostname, string(config), now); err != nil {
			t.Fatal(err)
		}
	}
	for ordinal, check := range configuration.Checks {
		if _, err := db.Exec(`INSERT INTO deploy_checks(environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at)
			VALUES(?, 0, ?, ?, ?, ?, ?, ?, ?)`, environmentID, check.Name, check.Kind, check.Phase, string(check.Config), check.Required, ordinal+1, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.modules.stackStore.Register(context.Background(), projectID, environmentID, media.ID, preset, "tester"); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}

// A Complete stack made before its apps had addresses has only Jellyfin's
// domain. "Give every app its address" names the six it would get, saves
// them as the Domains settings save domains (audited as a configuration
// update), starts the release that applies them, and then has nothing left
// to add.
func TestStackAddressesAreSavedAsDomainsAndDeployed(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID := seedStackDeployment(t, s, "media", media.TemplateComplete, "complete",
		func(domain deploy.PlannedDomain) bool { return domain.Service == "" })
	path := fmt.Sprintf("/api/v1/deploy/%d/stack", projectID)

	read := func() stackState {
		t.Helper()
		response := c.do(http.MethodGet, path, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("state = %d %s", response.Code, response.Body.String())
		}
		var state stackState
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	state := read()
	wanted := map[string]string{
		"seerr": "media-requests.example.test", "radarr": "media-radarr.example.test", "sonarr": "media-sonarr.example.test",
		"prowlarr": "media-prowlarr.example.test", "qbittorrent": "media-downloads.example.test", "bazarr": "media-subtitles.example.test",
	}
	if state.Addresses.Primary != "media.example.test" || len(state.Addresses.Missing) != len(wanted) {
		t.Fatalf("addresses = %+v", state.Addresses)
	}
	for _, address := range state.Addresses.Missing {
		if wanted[address.Service] != address.Hostname || address.Title == "" {
			t.Fatalf("missing address = %+v", address)
		}
	}

	response := c.do(http.MethodPost, path+"/addresses", "", nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("addresses = %d %s", response.Code, response.Body.String())
	}
	var answer stackAddressesAnswer
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.RunID == 0 || len(answer.Domains) != len(wanted) {
		t.Fatalf("answer = %+v %v", answer, err)
	}

	configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Revision != 2 || len(configuration.Domains) != len(wanted)+1 {
		t.Fatalf("configuration revision %d domains %+v", configuration.Revision, configuration.Domains)
	}
	for _, domain := range configuration.Domains {
		if !domain.HTTPS || domain.Ownership != deploy.OwnershipManaged {
			t.Fatalf("domain %+v is not served as the primary is", domain)
		}
		if domain.Service == "" && domain.Hostname != "media.example.test" || domain.Service != "" && wanted[domain.Service] != domain.Hostname {
			t.Fatalf("domain %+v", domain)
		}
	}
	run, err := s.modules.deployRuns.Run(t.Context(), answer.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Operation != deploy.OperationDeploy || run.EnvironmentID != environmentID || run.PlanRevision != configuration.Revision {
		t.Fatalf("run = %+v", run)
	}
	for _, action := range []string{"deploy.configuration.update", "deploy.run.request", "deploy.stack.addresses"} {
		var audited int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&audited); err != nil || audited != 1 {
			t.Fatalf("%s audited %d times: %v", action, audited, err)
		}
	}

	if state := read(); len(state.Addresses.Missing) != 0 || state.Addresses.Primary != "media.example.test" {
		t.Fatalf("addresses after = %+v", state.Addresses)
	}
	refused := c.do(http.MethodPost, path+"/addresses", "{}", nil)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "stack_addresses_present") {
		t.Fatalf("second press = %d %s", refused.Code, refused.Body.String())
	}
}

// Addresses that are saved but that the live release does not serve are
// published by deploying again, through the same press, once no release of
// the deployment is under way: one that is may be the release publishing
// them, and the page must not call it unfinished or start a second.
func TestStackAddressesDeployAgainOnlyOnceNoReleaseIsUnderWay(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID := seedStackDeployment(t, s, "media", media.TemplateComplete, "complete",
		func(deploy.PlannedDomain) bool { return true })
	path := fmt.Sprintf("/api/v1/deploy/%d/stack", projectID)
	// The live release is one from before the addresses were saved: it
	// serves none of them.
	db := s.Store.DB
	var sourceID, buildID, runtimeID int64
	if err := db.QueryRow(`SELECT (SELECT id FROM deploy_sources WHERE environment_id = ?), (SELECT id FROM deploy_build_plans WHERE environment_id = ?),
		(SELECT id FROM deploy_runtime_plans WHERE environment_id = ?)`, environmentID, environmentID, environmentID).Scan(&sourceID, &buildID, &runtimeID); err != nil {
		t.Fatal(err)
	}
	deployed, err := db.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, ended_at, status, state) VALUES(?, ?, 1, 2, 'success', 'succeeded')`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	deployedID, _ := deployed.LastInsertId()
	if _, err := db.Exec(`INSERT INTO deploy_run_plan_snapshots(run_id, environment_id, digest, created_at) VALUES(?, ?, 'sha256:plan-seed', 1)`, deployedID, environmentID); err != nil {
		t.Fatal(err)
	}
	release, err := db.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, plan_revision, run_id, source_id, build_plan_id, runtime_plan_id, source_identity_json, created_at)
		VALUES(?, ?, 1, 1, ?, ?, ?, ?, '{"template":"media-complete@1.1.0"}', 1)`, projectID, environmentID, deployedID, sourceID, buildID, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := release.LastInsertId()
	for _, statement := range []string{
		`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, created_at, updated_at) VALUES(?, ?, 'compose', 'jd-media', 1, 1)`,
		`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`,
		`UPDATE deploy_stacks SET live_release_id = ? WHERE environment_id = ?`,
	} {
		if _, err := db.Exec(statement, releaseID, environmentID); err != nil {
			t.Fatal(err)
		}
	}
	read := func() stackAddresses {
		t.Helper()
		response := c.do(http.MethodGet, path, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("state = %d %s", response.Code, response.Body.String())
		}
		var state stackState
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state.Addresses
	}
	// The runs since the one that made the live release.
	runs := func() int {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM deploy_runs WHERE project_id = ? AND id != ?`, projectID, deployedID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	// A release is under way, perhaps the one publishing them.
	run, err := s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state) VALUES(?, ?, 1, 'running', 'running')`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := run.LastInsertId()
	if addresses := read(); len(addresses.Missing) != 0 || len(addresses.Unpublished) != 0 {
		t.Fatalf("while a release is under way, addresses = %+v", addresses)
	}
	if refused := c.do(http.MethodPost, path+"/addresses", "", nil); refused.Code != http.StatusConflict ||
		!strings.Contains(refused.Body.String(), "stack_addresses_present") || runs() != 1 {
		t.Fatalf("a press while a release is under way = %d %s, %d runs", refused.Code, refused.Body.String(), runs())
	}

	// It failed: the six saved addresses are unpublished, and the press
	// deploys again without adding any.
	if _, err := s.Store.DB.Exec(`UPDATE deploy_runs SET state = 'failed', status = 'failed', ended_at = 2 WHERE id = ?`, runID); err != nil {
		t.Fatal(err)
	}
	if addresses := read(); len(addresses.Missing) != 0 || len(addresses.Unpublished) != 6 {
		t.Fatalf("after the release failed, addresses = %+v", addresses)
	}
	response := c.do(http.MethodPost, path+"/addresses", "", nil)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"domains":[]`) {
		t.Fatalf("deploy again = %d %s", response.Code, response.Body.String())
	}
	var answer stackAddressesAnswer
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.RunID == 0 || runs() != 2 {
		t.Fatalf("answer = %+v %v, %d runs", answer, err, runs())
	}
	started, err := s.modules.deployRuns.Run(t.Context(), answer.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Operation != deploy.OperationDeploy || started.EnvironmentID != environmentID || started.PlanRevision != 1 {
		t.Fatalf("run = %+v", started)
	}
}

// A stack made with no domain has nothing to put the addresses beside: the
// state names the services with no hostname, the request without a domain is
// refused in words, and with one that domain becomes the primary.
func TestStackAddressesTakeADomainWhenThereIsNone(t *testing.T) {
	s := testServer(t)
	wireDeployPlanningAndSources(t, s, []string{t.TempDir()})
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID := seedStackDeployment(t, s, "media", media.TemplateEssential, "essential",
		func(deploy.PlannedDomain) bool { return false })
	path := fmt.Sprintf("/api/v1/deploy/%d/stack", projectID)

	response := c.do(http.MethodGet, path, "", nil)
	var state stackState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Addresses.Primary != "" || len(state.Addresses.Missing) != 1 || state.Addresses.Missing[0].Service != "seerr" ||
		state.Addresses.Missing[0].Hostname != "" {
		t.Fatalf("addresses = %+v", state.Addresses)
	}
	refused := c.do(http.MethodPost, path+"/addresses", "{}", nil)
	if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), "stack_no_domain") {
		t.Fatalf("no domain = %d %s", refused.Code, refused.Body.String())
	}
	accepted := c.do(http.MethodPost, path+"/addresses", `{"domain":"Watch.Example.Test"}`, nil)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("with a domain = %d %s", accepted.Code, accepted.Body.String())
	}
	var answer stackAddressesAnswer
	_ = json.Unmarshal(accepted.Body.Bytes(), &answer)
	if !slices.Equal(answer.Domains, []string{"watch.example.test", "watch-requests.example.test"}) {
		t.Fatalf("domains = %v", answer.Domains)
	}
	configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	services := map[string]string{}
	for _, domain := range configuration.Domains {
		services[domain.Hostname] = domain.Service
	}
	if len(services) != 2 || services["watch.example.test"] != "" || services["watch-requests.example.test"] != "seerr" {
		t.Fatalf("domains = %+v", configuration.Domains)
	}
}

// The one login is changed by a session: a weak password is refused in the
// words of the dashboard's own rule, a change keeps the one before for the
// pass that brings every application to it, nothing records the value, and
// the reveal gives the new one.
func TestStackLoginChangeKeepsThePreviousAndAsksForAPass(t *testing.T) {
	s := testServer(t)
	s.modules.stackStore = stacks.NewStore(s.Store.DB, s.Sealer)
	s.modules.stacks = s.newStackService()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	projectID, environmentID := seedStackProject(t, s)
	path := fmt.Sprintf("/api/v1/deploy/%d/stack", projectID)
	credentials := s.modules.stackStore.Credentials(projectID)

	weak := c.do(http.MethodPut, path+"/login", `{"password":"short"}`, nil)
	if weak.Code != http.StatusUnprocessableEntity || !strings.Contains(weak.Body.String(), "password_weak") ||
		!strings.Contains(weak.Body.String(), "at least 12 characters") {
		t.Fatalf("weak = %d %s", weak.Code, weak.Body.String())
	}
	if _, found, _ := credentials.Get(t.Context(), stacks.CredentialLogin); found {
		t.Fatal("a weak password was stored")
	}

	const first, second = "First-Password-2026", "Second-Password-2026"
	// No live release yet: stored for the first pass.
	waiting := c.do(http.MethodPut, path+"/login", `{"password":"`+first+`"}`, nil)
	if waiting.Code != http.StatusAccepted || !strings.Contains(waiting.Body.String(), `"status":"waiting"`) {
		t.Fatalf("first = %d %s", waiting.Code, waiting.Body.String())
	}
	if value, _, _ := credentials.Get(t.Context(), stacks.CredentialLogin); value != first {
		t.Fatal("the first password was not stored")
	}
	if _, found, _ := credentials.Get(t.Context(), stacks.CredentialLoginPrevious); found {
		t.Fatal("a first login has no previous one")
	}

	// A live release of a preset: the pass is asked for.
	release, err := s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, source_identity_json, created_at) VALUES(?, ?, 1, '{"template":"media-essential@1.1.0"}', 1)`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := release.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, created_at, updated_at) VALUES(?, ?, 'compose', 'jd-media', 1, 1)`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	changed := c.do(http.MethodPut, path+"/login", `{"password":"`+second+`"}`, nil)
	if changed.Code != http.StatusAccepted || !strings.Contains(changed.Body.String(), `"status":"configuring"`) {
		t.Fatalf("second = %d %s", changed.Code, changed.Body.String())
	}
	if value, _, _ := credentials.Get(t.Context(), stacks.CredentialLogin); value != second {
		t.Fatal("the second password was not stored")
	}
	if value, _, _ := credentials.Get(t.Context(), stacks.CredentialLoginPrevious); value != first {
		t.Fatal("the password before was not kept for the pass")
	}
	var requested int64
	if err := s.Store.DB.QueryRow(`SELECT requested_at FROM deploy_stacks WHERE project_id = ?`, projectID).Scan(&requested); err != nil || requested == 0 {
		t.Fatalf("no pass was asked for: %d %v", requested, err)
	}

	revealed := c.do(http.MethodGet, path+"/credentials/"+stacks.CredentialLogin+"/reveal", "", nil)
	if revealed.Code != http.StatusOK || !strings.Contains(revealed.Body.String(), second) {
		t.Fatalf("reveal = %d %s", revealed.Code, revealed.Body.String())
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.stack.login.update'`).Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("changes audited = %d %v", audited, err)
	}
	var leaked int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE ? OR detail LIKE ?`, "%"+first+"%", "%"+second+"%").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the audit log carries a password: %d %v", leaked, err)
	}
	state := c.do(http.MethodGet, path, "", nil).Body.String()
	if strings.Contains(state, first) || strings.Contains(state, second) || !strings.Contains(state, `"login":{"username":"admin","stored":true}`) {
		t.Fatalf("state = %s", state)
	}
}

// Each service is reached at an HTTPS address when it has one, Jellyfin at
// the domain that names no service, and a redirect is nobody's address.
func TestStackURLsComeFromTheReleasesDomains(t *testing.T) {
	urls := stackURLsOf("jellyfin", []deploy.PlannedDomain{
		{Hostname: "media-requests.example.test", Service: "seerr"},
		{Hostname: "media-requests-secure.example.test", HTTPS: true, Service: "seerr"},
		{Hostname: "media.example.test", HTTPS: true},
		{Hostname: "www.media.example.test", HTTPS: true, RedirectTo: "media.example.test"},
		{Hostname: "media-downloads.example.test", Tunnel: true, Service: "qbittorrent"},
		{Hostname: "media-radarr.example.test", Service: "radarr"},
	})
	want := map[string]string{
		"jellyfin": "https://media.example.test", "seerr": "https://media-requests-secure.example.test",
		"qbittorrent": "https://media-downloads.example.test", "radarr": "http://media-radarr.example.test",
	}
	if len(urls) != len(want) {
		t.Fatalf("urls = %v", urls)
	}
	for service, url := range want {
		if urls[service] != url {
			t.Fatalf("%s = %q, want %q", service, urls[service], url)
		}
	}
}

// An address that is saved but that the live release gives no URL, because
// the release that was to publish it failed, is unpublished: the button
// deploys again rather than saying every app has one.
func TestStackUnpublishedAddressesAreTheSavedOnesTheLiveReleaseDoesNotServe(t *testing.T) {
	manifest := media.Manifest()
	domains := []deploy.PlannedDomain{
		{Hostname: "media.example.test"},
		{Hostname: "media-requests.example.test", Service: "seerr"},
		{Hostname: "media-radarr.example.test", Service: "radarr"},
		{Hostname: "old-radarr.example.test", Service: "radarr"},
		{Hostname: "www-requests.example.test", Service: "seerr", RedirectTo: "media-requests.example.test"},
		{Hostname: "elsewhere.example.test", Service: "not-in-the-stack"},
	}
	services := []stackServiceState{
		{Name: "jellyfin", URL: "https://media.example.test"},
		{Name: "seerr", URL: "https://media-requests.example.test"},
		{Name: "radarr"},
		{Name: "sonarr"},
	}
	got := stackUnpublishedAddresses(manifest, domains, services)
	if len(got) != 1 || got[0].Service != "radarr" || got[0].Hostname != "media-radarr.example.test" || got[0].Title != "Radarr" {
		t.Fatalf("unpublished = %+v, want Radarr's saved address alone", got)
	}
	if got := stackUnpublishedAddresses(manifest, domains[:2], services[:2]); len(got) != 0 {
		t.Fatalf("every saved address is served, yet unpublished = %+v", got)
	}
}
