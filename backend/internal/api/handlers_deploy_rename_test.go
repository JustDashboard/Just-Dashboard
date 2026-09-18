package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func decodedAPIError(t *testing.T, body []byte) struct {
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	t.Helper()
	var wrapper struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, body)
	}
	return wrapper.Error
}

// A `{name}`-only body is the one partial shape PUT /deploy/{id} accepts: it
// renames the project and must not blank the legacy fields a full body
// replaces. This is the primary contract Feature 2 adds.
func TestDeployUpdateRenameOnlyBodyLeavesOtherColumnsUntouched(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "rename-source")

	response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d", project.ID),
		`{"name":"rename-target"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", response.Code, response.Body.String())
	}
	var renamed deploy.Project
	if err := json.Unmarshal(response.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "rename-target" {
		t.Fatalf("renamed project name = %q", renamed.Name)
	}
	if renamed.RepoPath != project.RepoPath || renamed.Branch != project.Branch ||
		renamed.ComposeFile != project.ComposeFile || renamed.Enabled != project.Enabled {
		t.Fatalf("rename changed other columns: got %#v, want repoPath/branch/composeFile/enabled from %#v", renamed, project)
	}

	get := c.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d", project.ID), "", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get after rename = %d %s", get.Code, get.Body.String())
	}
	var getBody struct {
		Project deploy.Project `json:"project"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &getBody); err != nil {
		t.Fatal(err)
	}
	if getBody.Project.Name != "rename-target" {
		t.Fatalf("GET /deploy/{id} after rename = %q, want rename-target", getBody.Project.Name)
	}

	fleet := c.do(http.MethodGet, "/api/v1/deploy?view=fleet", "", nil)
	if fleet.Code != http.StatusOK {
		t.Fatalf("fleet = %d %s", fleet.Code, fleet.Body.String())
	}
	var fleetBody struct {
		Deployments []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"deployments"`
	}
	if err := json.Unmarshal(fleet.Body.Bytes(), &fleetBody); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range fleetBody.Deployments {
		if d.ID == project.ID {
			found = true
			if d.Name != "rename-target" {
				t.Fatalf("fleet name = %q, want rename-target", d.Name)
			}
		}
	}
	if !found {
		t.Fatalf("renamed project %d missing from fleet: %s", project.ID, fleet.Body.String())
	}
}

// The rename path also has to work for a project shaped like a real
// normalized deployment (its own environment, source, build and runtime
// plans), not only the legacy-migrated shape createLegacyDeploymentFixture
// produces.
func TestDeployUpdateRenameWorksForNormalizedProject(t *testing.T) {
	c, s := newClient(t)
	projectID, _, _ := insertDeploymentConfigurationAPI(t, s)

	response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d", projectID),
		`{"name":"api-config-app-renamed"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("rename normalized project = %d %s", response.Code, response.Body.String())
	}
	var renamed deploy.Project
	if err := json.Unmarshal(response.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "api-config-app-renamed" {
		t.Fatalf("renamed project name = %q", renamed.Name)
	}
	if renamed.Profile != deploy.ProfileWorker {
		t.Fatalf("rename changed profile: got %q, want %q", renamed.Profile, deploy.ProfileWorker)
	}
}

func TestDeployUpdateRenameRejectsInvalidName(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "invalid-name-source")

	response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d", project.ID),
		`{"name":"not a valid name"}`, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid rename = %d %s", response.Code, response.Body.String())
	}

	current, err := s.modules.deployStore.Get(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != project.Name {
		t.Fatalf("rejected rename changed the name: got %q, want %q", current.Name, project.Name)
	}
}

func TestDeployUpdateRenameConflictsOnDuplicateName(t *testing.T) {
	c, s := newClient(t)
	first := createLegacyDeploymentFixture(t, s, "duplicate-first")
	second := createLegacyDeploymentFixture(t, s, "duplicate-second")

	response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d", first.ID),
		fmt.Sprintf(`{"name":%q}`, second.Name), nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate rename = %d %s", response.Code, response.Body.String())
	}
	apiErr := decodedAPIError(t, response.Body.Bytes())
	if apiErr.Code != "name_taken" {
		t.Fatalf("duplicate rename error code = %q, want name_taken", apiErr.Code)
	}

	current, err := s.modules.deployStore.Get(t.Context(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != first.Name {
		t.Fatalf("conflicting rename changed the name: got %q, want %q", current.Name, first.Name)
	}
}

// Renaming an archived project used to overwrite deploy_projects.name — the
// internal tombstone Archive wrote there, never the project's display name
// (archived_name holds that) — so a later POST /deploy with the attempted
// name failed against a project that appears nowhere in any listing, and the
// rename response still showed the old archived display name regardless.
func TestDeployUpdateRenameRefusesAnArchivedProject(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "archived-rename-source")

	archiveResponse := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/archive", project.ID), `{}`, nil)
	if archiveResponse.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", archiveResponse.Code, archiveResponse.Body.String())
	}
	var tombstone, archivedName string
	if err := s.Store.DB.QueryRow(`SELECT name, archived_name FROM deploy_projects WHERE id = ?`, project.ID).
		Scan(&tombstone, &archivedName); err != nil {
		t.Fatal(err)
	}
	if archivedName != project.Name {
		t.Fatalf("archived_name = %q, want the original display name %q", archivedName, project.Name)
	}

	response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d", project.ID),
		`{"name":"archived-rename-target"}`, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("rename of an archived project = %d %s, want 409", response.Code, response.Body.String())
	}
	apiErr := decodedAPIError(t, response.Body.Bytes())
	if apiErr.Code != "project_archived" {
		t.Fatalf("rename of an archived project error code = %q, want project_archived", apiErr.Code)
	}

	var currentTombstone, currentArchivedName string
	if err := s.Store.DB.QueryRow(`SELECT name, archived_name FROM deploy_projects WHERE id = ?`, project.ID).
		Scan(&currentTombstone, &currentArchivedName); err != nil {
		t.Fatal(err)
	}
	if currentTombstone != tombstone || currentArchivedName != archivedName {
		t.Fatalf("refused rename changed the tombstone: got name=%q archived_name=%q, want name=%q archived_name=%q",
			currentTombstone, currentArchivedName, tombstone, archivedName)
	}

	if _, _, err := s.modules.deployStore.Create(t.Context(), &deploy.Project{
		Name: "archived-rename-target", RepoPath: t.TempDir(), Branch: "main", ComposeFile: "compose.yml", Enabled: true,
	}); err != nil {
		t.Fatalf("creating a project with the attempted name failed: %v", err)
	}
}

// A full legacy body — every field present, as the legacy edit form always
// sends — must keep replacing every column exactly as it did before this
// route learned to accept a partial one.
func TestDeployUpdateFullLegacyBodyStillReplacesEveryField(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "legacy-full-body")
	newRepo := project.RepoPath

	body := fmt.Sprintf(`{"name":%q,"repoPath":%q,"branch":"develop","composeFile":"other.yml","preCommand":"echo pre","postCommand":"echo post","enabled":false}`,
		project.Name, newRepo)
	response := c.do(http.MethodPut, fmt.Sprintf("/api/v1/deploy/%d", project.ID), body, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("full legacy update = %d %s", response.Code, response.Body.String())
	}
	var updated deploy.Project
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Branch != "develop" || updated.ComposeFile != "other.yml" ||
		updated.PreCommand != "echo pre" || updated.PostCommand != "echo post" || updated.Enabled {
		t.Fatalf("full legacy update = %#v", updated)
	}
}
