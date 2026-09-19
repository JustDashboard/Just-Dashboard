package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// The alert routes: a reader can see the rules and cannot write them; an
// admin's rules come back with the vocabulary the form needs; a bad rule is
// a 400 with the reason; another project's rule is not reachable.
func TestTrafficAlertRoutes(t *testing.T) {
	s := testServer(t)
	projectID, _, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "alerts-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "alerts-reader", auth.RoleReadOnly)}
	base := fmt.Sprintf("/api/v1/deploy/%d/alerts", projectID)

	if response := reader.do(http.MethodPost, base, `{"kind":"error_rate","threshold":1,"windowMinutes":5}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader create=%d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPost, base, `{"kind":"error_rate","threshold":0,"windowMinutes":5}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid rule=%d %s", response.Code, response.Body.String())
	}
	created := admin.do(http.MethodPost, base, `{"kind":"error_rate","threshold":1,"windowMinutes":5}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var alert deploy.TrafficAlert
	if err := json.Unmarshal(created.Body.Bytes(), &alert); err != nil || alert.ID == 0 || alert.State != "ok" {
		t.Fatalf("created body: %v %s", err, created.Body.String())
	}

	listed := reader.do(http.MethodGet, base, "", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("reader list=%d", listed.Code)
	}
	var list struct {
		Alerts []deploy.TrafficAlert `json:"alerts"`
		Kinds  []string              `json:"kinds"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || len(list.Alerts) != 1 || len(list.Kinds) != 3 {
		t.Fatalf("list body: %v %s", err, listed.Body.String())
	}

	item := fmt.Sprintf("%s/%d", base, alert.ID)
	if response := admin.do(http.MethodPut, item, `{"kind":"latency","threshold":750,"windowMinutes":10}`, nil); response.Code != http.StatusOK {
		t.Fatalf("update=%d %s", response.Code, response.Body.String())
	}
	if response := reader.do(http.MethodDelete, item, "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader delete=%d", response.Code)
	}
	// Another project cannot reach the rule by id.
	if response := admin.do(http.MethodDelete, fmt.Sprintf("/api/v1/deploy/%d/alerts/%d", projectID+1000, alert.ID), "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("cross-project delete=%d %s", response.Code, response.Body.String())
	}
	// A test with no channels delivers to nobody and says so.
	tested := admin.do(http.MethodPost, item+"/test", "", nil)
	if tested.Code != http.StatusOK {
		t.Fatalf("test=%d %s", tested.Code, tested.Body.String())
	}
	var outcome map[string]int
	if err := json.Unmarshal(tested.Body.Bytes(), &outcome); err != nil || outcome["channels"] != 0 {
		t.Fatalf("test body: %v %s", err, tested.Body.String())
	}
	if response := admin.do(http.MethodDelete, item, "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", response.Code, response.Body.String())
	}
}
