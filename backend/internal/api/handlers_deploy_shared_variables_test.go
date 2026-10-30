package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Shared variables are driven through real HTTP at both addresses: a reader
// lists them masked and nothing else, an API token writes nothing, an
// administrator writes and reveals with an audit entry that never holds the
// value, an environment's variable reads one by reference, and removing one
// is refused while it is read.
func TestSharedVariableRoutesKeepTheVariableRules(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "shared-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "shared-reader", auth.RoleReadOnly)}
	const secret = "sk_live_shared_value"
	server := "/api/v1/deploy/shared-variables"
	project := fmt.Sprintf("/api/v1/deploy/%d/shared-variables", projectID)
	body := fmt.Sprintf(`{"value":%q,"sensitivity":"secret"}`, secret)

	if response := reader.do(http.MethodGet, server, "", nil); response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("empty list = %d %s", response.Code, response.Body.String())
	}
	if response := reader.do(http.MethodPut, server+"/STRIPE_KEY", body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader write = %d %s", response.Code, response.Body.String())
	}
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'shared-admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminUser, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), adminUser, "shared-token", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenClient := &client{t: t, h: routes}
	bearer := map[string]string{"Authorization": "Bearer " + token}
	if response := tokenClient.do(http.MethodPut, server+"/STRIPE_KEY", body, bearer); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"session_required"`) {
		t.Fatalf("token write = %d %s", response.Code, response.Body.String())
	}

	created := admin.do(http.MethodPut, server+"/STRIPE_KEY", body, nil)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), secret) {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var variable deploy.SharedVariable
	decodeDeployResponse(t, created.Body.Bytes(), &variable)
	if variable.Scope != "server" || variable.Reference != "${{server.STRIPE_KEY}}" || variable.UpdatedBy != "shared-admin" {
		t.Fatalf("created = %#v", variable)
	}
	if response := admin.do(http.MethodPut, project+"/REGION", `{"value":"eu-west","sensitivity":"plain"}`, nil); response.Code != http.StatusCreated ||
		!strings.Contains(response.Body.String(), `"reference":"${{project.REGION}}"`) {
		t.Fatalf("project create = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{server, project} {
		listed := reader.do(http.MethodGet, path, "", nil)
		if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), secret) || strings.Contains(listed.Body.String(), "eu-west") {
			t.Fatalf("list %s = %d %s", path, listed.Code, listed.Body.String())
		}
	}
	if response := reader.do(http.MethodGet, server+"/STRIPE_KEY/reveal", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader reveal = %d %s", response.Code, response.Body.String())
	}
	revealed := admin.do(http.MethodGet, server+"/STRIPE_KEY/reveal", "", nil)
	if revealed.Code != http.StatusOK || !strings.Contains(revealed.Body.String(), secret) {
		t.Fatalf("reveal = %d %s", revealed.Code, revealed.Body.String())
	}
	if response := admin.do(http.MethodGet, server+"/NOT_SAVED/reveal", "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("reveal of nothing = %d %s", response.Code, response.Body.String())
	}

	// An environment reads it through a variable of its own.
	variables := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables", projectID, environmentID)
	if response := admin.do(http.MethodPut, variables+"/STRIPE_SECRET_KEY",
		`{"revision":1,"reference":"${{server.STRIPE_KEY}}","sensitivity":"secret","scopes":["runtime"]}`, nil); response.Code != http.StatusOK {
		t.Fatalf("reference = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPut, variables+"/MISSING",
		`{"revision":2,"reference":"${{project.NOT_SAVED}}","sensitivity":"plain","scopes":["runtime"]}`, nil); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "this project has no shared variable NOT_SAVED") {
		t.Fatalf("missing reference = %d %s", response.Code, response.Body.String())
	}
	listed := reader.do(http.MethodGet, server, "", nil)
	if !strings.Contains(listed.Body.String(), `"variable":"STRIPE_SECRET_KEY"`) || !strings.Contains(listed.Body.String(), `"projectName":"api-config-app"`) {
		t.Fatalf("used by = %s", listed.Body.String())
	}

	removed := admin.do(http.MethodDelete, server+"/STRIPE_KEY", "", nil)
	if removed.Code != http.StatusConflict || !strings.Contains(removed.Body.String(), "shared_variable_in_use") ||
		!strings.Contains(removed.Body.String(), "api-config-app (production) as STRIPE_SECRET_KEY") {
		t.Fatalf("remove while read = %d %s", removed.Code, removed.Body.String())
	}
	if response := reader.do(http.MethodDelete, project+"/REGION", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader remove = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodDelete, project+"/REGION", "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodDelete, project+"/REGION", "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("second remove = %d %s", response.Code, response.Body.String())
	}

	var audits string
	if err := s.Store.DB.QueryRow(`
		SELECT COALESCE(group_concat(action || ' ' || target || ' ' || detail, char(10)), '')
		  FROM audit_log WHERE action LIKE 'deploy.shared_variable.%'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"deploy.shared_variable.create STRIPE_KEY", "deploy.shared_variable.create REGION",
		"deploy.shared_variable.reveal STRIPE_KEY", "deploy.shared_variable.delete REGION", `"scope":"project"`,
	} {
		if !strings.Contains(audits, want) {
			t.Errorf("audit log lacks %q:\n%s", want, audits)
		}
	}
	if strings.Contains(audits, secret) || strings.Contains(audits, "eu-west") {
		t.Fatalf("audit log holds a value:\n%s", audits)
	}
}
