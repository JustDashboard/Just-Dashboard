package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// alertWebhook is a notification channel of the webhook kind whose endpoint
// is a test server here: every delivery goes through the real channel
// delivery, signed, and nothing leaves the machine.
type alertWebhook struct {
	mu   sync.Mutex
	got  []deploy.NotificationEnvelope
	sigs []string
	id   int64
}

func newAlertWebhook(t *testing.T, s *Server) *alertWebhook {
	t.Helper()
	hook := &alertWebhook{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope deploy.NotificationEnvelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Errorf("webhook body: %v", err)
		}
		hook.mu.Lock()
		hook.got = append(hook.got, envelope)
		hook.sigs = append(hook.sigs, r.Header.Get("X-JD-Signature-256"))
		hook.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	channel, _, err := s.modules.deployAutomation.CreateNotificationChannel(t.Context(), deploy.NotificationWrite{
		Name: "on-call", Kind: deploy.NotificationKindWebhook, URL: server.URL, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	hook.id = channel.ID
	s.modules.dbAlerts.url = func() string { return "https://dash.example.test" }
	return hook
}

func (h *alertWebhook) received() []deploy.NotificationEnvelope {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]deploy.NotificationEnvelope(nil), h.got...)
}

// alertRedis speaks just enough RESP for the recorder: PING, and an INFO whose
// clients section the test decides. stop closes it and every connection, so
// the server is down from the next dial on.
func alertRedis(t *testing.T, clients func() string) (addr string, stop func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	open := map[net.Conn]bool{}
	stop = func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for conn := range open {
			conn.Close()
		}
	}
	t.Cleanup(stop)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			open[conn] = true
			mu.Unlock()
			go serveAlertRedis(conn, clients)
		}
	}()
	return listener.Addr().String(), stop
}

func serveAlertRedis(conn net.Conn, clients func() string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		count, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
		if count <= 0 {
			return
		}
		args := make([]string, count)
		for i := range args {
			line, _ := reader.ReadString('\n')
			length, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
			value := make([]byte, length+2)
			if _, err := io.ReadFull(reader, value); err != nil {
				return
			}
			args[i] = string(value[:length])
		}
		switch strings.ToLower(args[0]) {
		case "hello":
			fmt.Fprint(conn, "-ERR unknown command 'hello'\r\n")
		case "ping":
			fmt.Fprint(conn, "+PONG\r\n")
		case "info":
			info := "# Server\r\nredis_version:7.4.0\r\nuptime_in_seconds:600\r\n# Clients\r\n" + clients()
			fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(info), info)
		default:
			fmt.Fprint(conn, "+OK\r\n")
		}
	}
}

type dbAlertsReply struct {
	Rules    []dbAlertRule    `json:"rules"`
	Subjects []dbAlertSubject `json:"subjects"`
	Events   []dbAlertEvent   `json:"events"`
}

// Reading the alerts is anybody's; a rule, its test and its removal are an
// administrator's, audited. A rule is held to what its database's engine can
// be watched for, and a transition is told once to the channels it names.
func TestDBAlertRoutesHoldRolesAndTellEachTransitionOnce(t *testing.T) {
	s := testServer(t)
	hook := newAlertWebhook(t, s)
	shop := saveOpsConnection(t, s, "shop", dbx.DriverPostgres, "postgres://app:s3cret-pass@127.0.0.1:1/shop")
	notes := saveOpsConnection(t, s, "notes", dbx.DriverSQLite, "file:/srv/notes/notes.db")
	a := s.modules.dbAlerts
	now := start
	a.now = func() time.Time { return now }
	h := s.Routes()
	admin := &client{t: t, h: h, cookie: signIn(t, s)}
	reader := &client{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	limited := &client{t: t, h: h, cookie: signInAs(t, s, "operator", auth.RoleLimited)}
	base := "/api/v1/databases/alerts"

	if w := reader.do(http.MethodGet, base+"?connection="+strconv.FormatInt(shop, 10), "", nil); w.Code != http.StatusOK {
		t.Fatalf("reader GET: %d %s", w.Code, w.Body.String())
	}
	if w := reader.do(http.MethodGet, base+"?connection=zero", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a malformed connection: %d", w.Code)
	}
	for _, who := range []*client{reader, limited} {
		for _, req := range []struct{ method, path, body string }{
			{http.MethodPost, base + "/rules", `{"kind":"down"}`},
			{http.MethodPost, base + "/rules/1/test", `{}`},
			{http.MethodPut, base + "/rules/1", `{"kind":"down"}`},
			{http.MethodDelete, base + "/rules/1", ""},
		} {
			if w := who.do(req.method, req.path, req.body, nil); w.Code != http.StatusForbidden {
				t.Fatalf("%s %s by a non-administrator: %d %s", req.method, req.path, w.Code, w.Body.String())
			}
		}
	}
	for _, body := range []string{
		`{"kind":"cpu"}`,
		`{"kind":"down","channels":[999]}`,
		`{"kind":"disk","threshold":100}`,
		// A file has no server to go down.
		fmt.Sprintf(`{"kind":"down","connectionId":%d}`, notes),
		`{"kind":"down","connectionId":4242}`,
	} {
		if w := admin.do(http.MethodPost, base+"/rules", body, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid rule %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	// A protected connection refuses changes to what is in it; a rule about
	// it is the dashboard's own state, and is not one.
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only=1 WHERE id=?`, shop); err != nil {
		t.Fatal(err)
	}
	w := admin.do(http.MethodPost, base+"/rules", fmt.Sprintf(`{"kind":"down","connectionId":%d,"minutes":1,"channels":[%d]}`, shop, hook.id), nil)
	var rule dbAlertRule
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil || w.Code != http.StatusCreated || rule.ID <= 0 ||
		rule.ConnectionID == nil || *rule.ConnectionID != shop || rule.Minutes != 1 || rule.CooldownMinutes != 30 {
		t.Fatalf("create: %d %s, %v", w.Code, w.Body.String(), err)
	}
	if _, detail, _ := auditEntry(t, s, "database.alert.create"); !strings.Contains(detail, `"minutes":1`) {
		t.Fatalf("create audit: %s", detail)
	}

	conns := []dbAlertConn{{ID: shop, Name: "shop", Driver: dbx.DriverPostgres}}
	pass := func(answered bool) {
		t.Helper()
		obs := dbAlertObservation{Conn: conns[0], Answered: answered}
		if !answered {
			obs.Error = "dial tcp 127.0.0.1:1: connect: connection refused"
		}
		a.Evaluate(context.Background(), conns, map[int64]dbAlertObservation{shop: obs})
		now = now.Add(30 * time.Second)
	}
	pass(true)
	pass(false)
	pass(false)
	if sent := hook.received(); len(sent) != 0 {
		t.Fatalf("told before the minute had passed: %+v", sent)
	}
	pass(false)
	pass(false)
	sent := hook.received()
	if len(sent) != 1 || sent[0].Event != deploy.NotificationEventDatabaseFiring || sent[0].Database == nil ||
		sent[0].Database.ConnectionID != shop || sent[0].Database.Label != "shop" ||
		!strings.HasPrefix(sent[0].Database.Detail, "shop does not answer:") || sent[0].Database.Since.IsZero() ||
		hook.sigs[0] == "" {
		t.Fatalf("firing: %+v", sent)
	}
	if strings.Contains(fmt.Sprint(sent), "s3cret-pass") {
		t.Fatal("an alert carried the connection's password")
	}
	pass(true)
	pass(true)
	if sent = hook.received(); len(sent) != 2 || sent[1].Event != deploy.NotificationEventDatabaseRecovered ||
		sent[1].Database.Detail != "shop answers." {
		t.Fatalf("recovery: %+v", sent)
	}

	var view dbAlertsReply
	w = reader.do(http.MethodGet, base+"?connection="+strconv.FormatInt(shop, 10), "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || len(view.Rules) != 1 || len(view.Subjects) != 1 ||
		view.Subjects[0].State != "quiet" || len(view.Events) != 2 || view.Events[0].Event != "recovered" ||
		view.Events[1].Delivered != 1 || view.Events[1].Kept != "" {
		t.Fatalf("view: %s", w.Body.String())
	}

	id := strconv.FormatInt(rule.ID, 10)
	w = admin.do(http.MethodPost, base+"/rules/"+id+"/test", `{}`, nil)
	var test map[string]int
	if err := json.Unmarshal(w.Body.Bytes(), &test); err != nil || test["delivered"] != 1 || test["channels"] != 1 {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	if sent = hook.received(); len(sent) != 3 || !sent[2].Database.Test || !strings.Contains(sent[2].Database.Detail, "This is a test") {
		t.Fatalf("test message: %+v", sent)
	}
	// Pausing a rule starts it over: nothing it followed is kept.
	paused := fmt.Sprintf(`{"kind":"down","connectionId":%d,"minutes":1,"channels":[%d],"enabled":false}`, shop, hook.id)
	if w := admin.do(http.MethodPut, base+"/rules/"+id, paused, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("pause: %d %s", w.Code, w.Body.String())
	}
	var states int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_alert_state WHERE rule_id=?`, rule.ID).Scan(&states)
	if states != 0 {
		t.Fatalf("a paused rule kept %d subjects", states)
	}
	if w := admin.do(http.MethodDelete, base+"/rules/"+id, "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodDelete, base+"/rules/"+id, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", w.Code)
	}
	for _, action := range []string{"database.alert.test", "database.alert.update", "database.alert.delete"} {
		auditEntry(t, s, action)
	}
	// Forgetting a database takes its rules and their history with it.
	w = admin.do(http.MethodPost, base+"/rules", fmt.Sprintf(`{"kind":"down","connectionId":%d}`, shop), nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("second rule: %d %s", w.Code, w.Body.String())
	}
	s.Store.DB.Exec(`DELETE FROM db_connections WHERE id=?`, shop)
	var rules, events int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_alert_rules`).Scan(&rules)
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_alert_events WHERE connection_id IS NOT NULL`).Scan(&events)
	if rules != 0 || events != 0 {
		t.Fatalf("a forgotten database left %d rules and %d events", rules, events)
	}
}

// A rule for every database watches the backup jobs that dump any of them,
// and a database's page shows what a job that dumps it told.
func TestDBAlertBackupRulesFollowTheJobs(t *testing.T) {
	s := testServer(t)
	hook := newAlertWebhook(t, s)
	shop := saveOpsConnection(t, s, "shop", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/shop")
	blog := saveOpsConnection(t, s, "blog", dbx.DriverMySQL, "app:pw@tcp(127.0.0.1:1)/blog")
	a := s.modules.dbAlerts
	now := start
	a.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := a.CreateRule(ctx, dbAlertWrite{Kind: dbx.AlertBackupFailed}, nil); err != nil {
		t.Fatal(err)
	}
	job := func(name string, dumps ...int64) int64 {
		list, _ := json.Marshal(dumps)
		res, err := s.Store.DB.Exec(`INSERT INTO backup_jobs(name, sources, target_kind, schedule, database_dumps, created_at)
			VALUES(?, '[]', 'local', '0 3 * * *', ?, ?)`, name, string(list), start.Add(-72*time.Hour).Unix())
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	run := func(jobID int64, status, log string) {
		if _, err := s.Store.DB.Exec(`INSERT INTO backup_runs(job_id, started_at, ended_at, status, log) VALUES(?,?,?,?,?)`,
			jobID, now.Unix(), now.Unix()+60, status, log); err != nil {
			t.Fatal(err)
		}
	}
	nightly := job("nightly", shop)
	job("files only")
	run(nightly, "failed", "dumping shop\nFAILED: pg_dump: error: connection refused\n")
	a.Evaluate(ctx, nil, nil)
	sent := hook.received()
	if len(sent) != 1 || sent[0].Database.Kind != dbx.AlertBackupFailed || sent[0].Database.Label != "nightly" ||
		sent[0].Database.ConnectionID != 0 || sent[0].URL != "https://dash.example.test/backups" ||
		!strings.HasSuffix(sent[0].Database.Detail, "failed: pg_dump: error: connection refused") {
		t.Fatalf("failed job: %+v", sent)
	}
	now = now.Add(time.Hour)
	run(nightly, "failed", "FAILED: again\n")
	a.Evaluate(ctx, nil, nil)
	if sent := hook.received(); len(sent) != 1 {
		t.Fatalf("a job that keeps failing was told twice: %+v", sent)
	}
	var view dbAlertsReply
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	for id, want := range map[int64]int{shop: 1, blog: 0} {
		w := reader.do(http.MethodGet, fmt.Sprintf("/api/v1/databases/alerts?connection=%d", id), "", nil)
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || len(view.Events) != want || len(view.Subjects) != want {
			t.Fatalf("database %d sees %s", id, w.Body.String())
		}
	}
	now = now.Add(24 * time.Hour)
	run(nightly, "success", "done\n")
	a.Evaluate(ctx, nil, nil)
	if sent := hook.received(); len(sent) != 2 || sent[1].Event != deploy.NotificationEventDatabaseRecovered {
		t.Fatalf("a job that succeeded again: %+v", sent)
	}
	// The job stops dumping the database: its subject leaves the rule
	// quietly, having nothing firing to end.
	s.Store.DB.Exec(`UPDATE backup_jobs SET database_dumps='[]' WHERE id=?`, nightly)
	a.Evaluate(ctx, nil, nil)
	var states int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_alert_state`).Scan(&states)
	if states != 0 || len(hook.received()) != 2 {
		t.Fatalf("a job that no longer dumps a database is still followed: %d states", states)
	}
}

// A rule edited while a pass is deciding it keeps its new terms: the pass's
// verdict on the old ones is neither written nor told.
func TestDBAlertPassDoesNotOverwriteAnEditedRule(t *testing.T) {
	s := testServer(t)
	hook := newAlertWebhook(t, s)
	shop := saveOpsConnection(t, s, "shop", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/shop")
	a := s.modules.dbAlerts
	ctx := context.Background()
	zero := 0
	rule, err := a.CreateRule(ctx, dbAlertWrite{ConnectionID: &shop, Kind: dbx.AlertDisk, Minutes: &zero},
		func(int64) ([]string, error) { return dbx.AlertKinds(dbx.DriverPostgres, ""), nil })
	if err != nil {
		t.Fatal(err)
	}
	stale := *rule
	if _, err := s.Store.DB.Exec(`UPDATE db_alert_rules SET revision=revision+1 WHERE id=?`, rule.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	written, err := upsertDBAlertState(ctx, tx, stale, dbAlertState{Subject: dbAlertConnSubject(shop), ConnectionID: &shop, FiredAt: 1})
	if err != nil || written {
		t.Fatalf("a stale verdict was written: %v %v", written, err)
	}
	tx.Rollback()
	if len(hook.received()) != 0 {
		t.Fatal("a stale verdict was told")
	}
}
