package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// The trigger list carries its delivery summary only for a caller who could
// read the delivery log itself, and both delivery logs honour ?limit=.
func TestAutomationListsCarryTheirDeliveriesToWhoeverMayReadThem(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "readings-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "readings-reader", auth.RoleReadOnly)}
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	created := admin.do(http.MethodPost, base+"/triggers", `{"name":"CI","kind":"generic_hook","enabled":true,"config":{}}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create trigger = %d %s", created.Code, created.Body.String())
	}
	var trigger deploy.TriggerCreated
	if err := json.Unmarshal(created.Body.Bytes(), &trigger); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		event := deploy.ProviderEvent{DeliveryID: fmt.Sprintf("d%d", i), Event: "deploy"}
		if err := s.modules.deployAutomation.RecordDelivery(t.Context(), &trigger.Trigger, event, []byte(event.DeliveryID), "rejected", "wrong_ref", 0); err != nil {
			t.Fatal(err)
		}
	}

	var triggers []deploy.Trigger
	decodeDeployResponse(t, admin.do(http.MethodGet, base+"/triggers", "", nil).Body.Bytes(), &triggers)
	if len(triggers) != 1 || len(triggers[0].Recent) != 3 || triggers[0].LastDelivery == nil || triggers[0].LastDelivery.DeliveryID != "d3" {
		t.Fatalf("admin trigger list = %+v, want three outcomes led to d3", triggers)
	}
	var readerTriggers []deploy.Trigger
	decodeDeployResponse(t, reader.do(http.MethodGet, base+"/triggers", "", nil).Body.Bytes(), &readerTriggers)
	if len(readerTriggers) != 1 || readerTriggers[0].Recent != nil || readerTriggers[0].LastDelivery != nil || readerTriggers[0].LastStatus != "rejected" {
		t.Fatalf("reader trigger list = %+v, want the list without the delivery log", readerTriggers)
	}
	var deliveries []deploy.TriggerDelivery
	decodeDeployResponse(t, admin.do(http.MethodGet, fmt.Sprintf("%s/triggers/%d/deliveries?limit=2", base, trigger.Trigger.ID), "", nil).Body.Bytes(), &deliveries)
	if len(deliveries) != 2 || deliveries[0].DeliveryID != "d3" {
		t.Fatalf("limited trigger deliveries = %+v, want the newest two", deliveries)
	}

	channel := admin.do(http.MethodPost, "/api/v1/deploy/notifications", `{"name":"ops","url":"https://hooks.example.test/ops","enabled":true}`, nil)
	if channel.Code != http.StatusCreated {
		t.Fatalf("create channel = %d %s", channel.Code, channel.Body.String())
	}
	var createdChannel struct {
		Channel deploy.NotificationChannel `json:"channel"`
	}
	decodeDeployResponse(t, channel.Body.Bytes(), &createdChannel)
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := s.Store.DB.Exec(`INSERT INTO deploy_notification_deliveries(channel_id,run_id,event,attempt,status,response_class,created_at) VALUES(?,0,'test',?,'delivered','2xx',?)`,
			createdChannel.Channel.ID, attempt, attempt); err != nil {
			t.Fatal(err)
		}
	}
	var channels []deploy.NotificationChannel
	decodeDeployResponse(t, reader.do(http.MethodGet, "/api/v1/deploy/notifications", "", nil).Body.Bytes(), &channels)
	if len(channels) != 1 || len(channels[0].Recent) != 3 || !channels[0].Recent[0].Test || channels[0].LastDelivery == nil {
		t.Fatalf("channel list = %+v, want its three test deliveries", channels)
	}
	var notified []deploy.NotificationDelivery
	decodeDeployResponse(t, reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/notifications/%d/deliveries?limit=1", createdChannel.Channel.ID), "", nil).Body.Bytes(), &notified)
	if len(notified) != 1 || notified[0].Attempt != 3 {
		t.Fatalf("limited notification deliveries = %+v, want the newest one", notified)
	}
}

// ?dryRun=1 answers per name and writes nothing, leaving its own audit line;
// a dryRun that is not a boolean is refused rather than read as an import.
func TestDotenvImportDryRunWritesNothing(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "dotenv-admin", auth.RoleAdmin)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/variables/import", projectID, environmentID)
	body := `{"revision":1,"dotenv":"API_TOKEN=abc\nBAD NAME=1\n","sensitivity":"secret","scopes":["runtime"]}`

	previewed := admin.do(http.MethodPost, path+"?dryRun=1", body, nil)
	if previewed.Code != http.StatusOK {
		t.Fatalf("dry run = %d %s", previewed.Code, previewed.Body.String())
	}
	var preview deploy.DotenvImportPreview
	decodeDeployResponse(t, previewed.Body.Bytes(), &preview)
	if len(preview.Variables) != 2 || preview.Variables[0].Change != "added" || preview.Variables[1].Change != "refused" || preview.Variables[1].Reason != "invalid_name" {
		t.Fatalf("preview = %+v", preview)
	}
	var written, audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE environment_id=?`, environmentID).Scan(&written); err != nil || written != 0 {
		t.Fatalf("dry run wrote %d variable revisions, %v", written, err)
	}
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='deploy.variable.import_preview' AND success=1`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("dry run audit count = %d, %v", audited, err)
	}
	if response := admin.do(http.MethodPost, path+"?dryRun=perhaps", body, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("unreadable dryRun = %d %s", response.Code, response.Body.String())
	}
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE environment_id=?`, environmentID).Scan(&written); err != nil || written != 0 {
		t.Fatalf("an unreadable dryRun wrote %d variable revisions, %v", written, err)
	}

	imported := admin.do(http.MethodPost, path+"?dryRun=0", `{"revision":1,"dotenv":"API_TOKEN=abc\n","sensitivity":"secret","scopes":["runtime"]}`, nil)
	if imported.Code != http.StatusOK {
		t.Fatalf("import = %d %s", imported.Code, imported.Body.String())
	}
	again := admin.do(http.MethodPost, path+"?dryRun=1", `{"revision":2,"dotenv":"API_TOKEN=abc\n","sensitivity":"secret","scopes":["runtime"]}`, nil)
	var unchanged deploy.DotenvImportPreview
	decodeDeployResponse(t, again.Body.Bytes(), &unchanged)
	if len(unchanged.Variables) != 1 || unchanged.Variables[0].Change != "unchanged" {
		t.Fatalf("preview after import = %+v, want unchanged", unchanged)
	}
}

// Testing an expression answers the next five firings in its zone, led by the
// same first firing it always reported.
func TestScheduleTestReportsTheNextRuns(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "schedule-test-admin", auth.RoleAdmin)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/schedules/test", projectID, environmentID)
	response := admin.do(http.MethodPost, path, `{"name":"nightly","expression":"30 2 * * 1-5","timezone":"America/New_York"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("schedule test = %d %s", response.Code, response.Body.String())
	}
	var result struct {
		NextRunAt string   `json:"nextRunAt"`
		NextRuns  []string `json:"nextRuns"`
	}
	decodeDeployResponse(t, response.Body.Bytes(), &result)
	if len(result.NextRuns) != deploy.ScheduleNextRuns || result.NextRuns[0] != result.NextRunAt {
		t.Fatalf("schedule test = %+v, want %d runs led by nextRunAt", result, deploy.ScheduleNextRuns)
	}
}
