package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestDeploymentRunSettingsDriftIsReadable(t *testing.T) {
	s := testServer(t)
	projectID, environmentID := insertRefDeployFixture(t, s, "drift-app", gitSourceFixture(), deploy.BuildDockerfile)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "drift-admin", auth.RoleAdmin)}
	body, _ := json.Marshal(map[string]any{"operation": "deploy", "sourceRevision": strings.Repeat("b", 40)})
	response := admin.do(http.MethodPost, runCreatePath(projectID, environmentID), string(body), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy = %d %s", response.Code, response.Body.String())
	}
	var run struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	response = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs/%d/settings-drift", projectID, run.ID), "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("drift = %d %s", response.Code, response.Body.String())
	}
	var drift deploy.RunSettingsDrift
	if err := json.Unmarshal(response.Body.Bytes(), &drift); err != nil {
		t.Fatal(err)
	}
	if drift.RunID != run.ID || drift.Changed || drift.Changes == nil {
		t.Fatalf("drift = %+v", drift)
	}
	response = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/runs/%d/settings-drift", projectID+100, run.ID), "", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("another project's run = %d %s", response.Code, response.Body.String())
	}
}
