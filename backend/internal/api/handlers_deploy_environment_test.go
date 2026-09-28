package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestDeploymentDraftInputsArePrivateAndCommittedBeforeTheFirstRun(t *testing.T) {
	s, checkout := planningServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	const secret = "external-database-password-private-to-this-draft"
	result, draftID := commitWorkerDraft(t, admin, checkout, "staged-database-input",
		func(draft deploy.Draft) deploy.DraftCommitRequest {
			configuration := *draft.Data.Configuration
			configuration.Variables = []deploy.PlannedVariable{{
				Name: "DATABASE_URL", Sensitivity: "secret", Scopes: []string{"runtime"}, Required: true,
			}}
			dotenv := "DATABASE_URL=postgres://operator:" + secret + "@external.example.test:5432/app?sslmode=require"
			response := doPlanningJSON(t, admin, http.MethodPut, "/api/v1/deploy/drafts/"+draft.ID, deploy.DraftSaveRequest{
				Revision: draft.Revision, Step: deploy.DraftConfiguration, Configuration: &configuration, Dotenv: &dotenv,
			})
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), secret) ||
				!strings.Contains(response.Body.String(), `"environmentKeys":["DATABASE_URL"]`) {
				t.Fatalf("private draft save = %d %s", response.Code, response.Body.String())
			}
			decodePlanningResponse(t, response.Body.Bytes(), &draft)
			response = doPlanningJSON(t, admin, http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/preflight", map[string]int{"revision": draft.Revision})
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), secret) ||
				strings.Contains(response.Body.String(), "variable_required_database_url") {
				t.Fatalf("staged required variable preflight = %d %s", response.Code, response.Body.String())
			}
			var checked struct {
				Draft deploy.Draft `json:"draft"`
			}
			decodePlanningResponse(t, response.Body.Bytes(), &checked)
			return deploy.DraftCommitRequest{Revision: checked.Draft.Revision}
		})
	variables, err := s.modules.deployPlanning.OpenScopedVariables(t.Context(), result.EnvironmentID, "runtime")
	if err != nil || len(variables) != 1 || !strings.Contains(variables[0].Value, secret) {
		t.Fatalf("first run inputs missing after commit: count=%d error=%v", len(variables), err)
	}
	if result.PlanRevision != 1 {
		t.Fatal("staged inputs required a post-creation mutation")
	}
	response := admin.do(http.MethodGet, "/api/v1/deploy/drafts/"+draftID, "", nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), secret) {
		t.Fatal("draft read revealed a staged input")
	}
	var audited string
	if err := s.Store.DB.QueryRow(`SELECT COALESCE(group_concat(detail || target), '') FROM audit_log`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audited, secret) {
		t.Fatal("audit contains a staged secret")
	}
}
