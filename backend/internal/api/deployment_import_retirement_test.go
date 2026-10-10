package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRetiredImportedProjectKeepsReadsAndRefusesMutations(t *testing.T) {
	c, s := newClient(t)
	project := createLegacyDeploymentFixture(t, s, "retired-import")
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, started_at, status, operation, trigger, actor) VALUES(?, 1, 'success', 'import_adopt', 'migration', 'tester')`, project.ID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deploy/%d", project.ID)
	read := c.do(http.MethodGet, path, "", nil)
	if read.Code != http.StatusOK {
		t.Fatalf("project read: %d %s", read.Code, read.Body.String())
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPost} {
		target := path
		if method == http.MethodPost {
			target += "/run"
		}
		response := c.do(method, target, `{"name":"changed"}`, nil)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "read-only") {
			t.Fatalf("%s %s: %d %s", method, target, response.Code, response.Body.String())
		}
	}
	stored, err := s.modules.deployStore.Get(t.Context(), project.ID)
	if err != nil || stored.Name != project.Name {
		t.Fatalf("guard changed project: %#v %v", stored, err)
	}
}

func TestRetiredImportReviewsCannotBeChangedIntoOrdinaryDrafts(t *testing.T) {
	c, s := newClient(t)
	draft, err := s.modules.deployPlanning.Create(t.Context(), 1, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_drafts SET data_json = json_set(data_json, '$.adoption', json('{}')) WHERE id = ?`, draft.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/deploy/drafts/" + draft.ID
	for _, target := range []string{path, path + "/commit", path + "/preflight"} {
		method := http.MethodPost
		if target == path {
			method = http.MethodPut
		}
		response := c.do(method, target, `{"revision":1}`, nil)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "import_removed") {
			t.Fatalf("retired review %s: %d %s", target, response.Code, response.Body.String())
		}
	}
	var data string
	if err := s.Store.DB.QueryRow(`SELECT data_json FROM deploy_drafts WHERE id = ?`, draft.ID).Scan(&data); err != nil || !strings.Contains(data, "adoption") {
		t.Fatalf("retired review changed: %q %v", data, err)
	}
}

func TestRetiredWorkloadEndpointsCannotImportContainers(t *testing.T) {
	c, _ := newClient(t)
	response := c.do(http.MethodGet, "/api/v1/deploy/import/discovery", "", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("retired discovery: %d %s", response.Code, response.Body.String())
	}
	response = c.do(http.MethodPost, "/api/v1/deploy/import/preview", `{"kind":"import","mode":"existing_container","resourceId":"container"}`, nil)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "import_removed") {
		t.Fatalf("retired preview: %d %s", response.Code, response.Body.String())
	}
}
