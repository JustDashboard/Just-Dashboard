package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestPoolerRoutesAreMountedWithTheirCapabilities(t *testing.T) {
	h := newConnHarness(t)
	handler := h.s.Routes()
	have := map[string]bool{}
	for _, rt := range apiRoutes(t, handler) {
		have[rt.method+" "+rt.pattern] = true
	}
	for _, want := range []string{
		"GET /api/v1/databases/{id}/pooler", "GET /api/v1/databases/{id}/pooler/stats",
		"PUT /api/v1/databases/{id}/pooler", "DELETE /api/v1/databases/{id}/pooler",
	} {
		if !have[want] {
			t.Errorf("%s is not a route", want)
		}
	}

	// Anyone who may read reads it; changing it takes system.admin, and
	// removing it the destructive capability too.
	id := h.add("db", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/app?sslmode=disable")
	path := pathf("/databases/%d/pooler", id)
	settings := `{"poolMode":"transaction","poolSize":5,"maxClientConn":50,"authMode":"userlist"}`
	for _, tc := range []struct {
		role         auth.Role
		method, body string
		allowed      bool
	}{
		{auth.RoleReadOnly, http.MethodGet, "", true},
		{auth.RoleReadOnly, http.MethodPut, settings, false},
		{auth.RoleLimited, http.MethodPut, settings, false},
		{auth.RoleReadOnly, http.MethodDelete, "", false},
		{auth.RoleLimited, http.MethodDelete, "", false},
		{auth.RoleAdmin, http.MethodDelete, "", true},
	} {
		rec := do(t, h.as(tc.role), tc.method, path, tc.body)
		if forbidden := rec.Code == http.StatusForbidden; forbidden == tc.allowed {
			t.Errorf("%s %s as %s = %d %s", tc.method, path, tc.role, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestOnlyAPostgresConnectionIsOfferedAPooler(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	id := h.add("cache", dbx.DriverRedis, "redis://127.0.0.1:1/0")
	rec := do(t, router, http.MethodGet, pathf("/databases/%d/pooler", id), "")
	view := readJSON[poolerView](t, rec)
	if rec.Code != http.StatusOK || view.Available || view.Enabled || view.Reason != poolerUnsupported {
		t.Errorf("a Redis connection reads as %d %+v", rec.Code, view)
	}
	rec = do(t, router, http.MethodPut, pathf("/databases/%d/pooler", id), `{"poolMode":"transaction","poolSize":5,"maxClientConn":50,"authMode":"userlist"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), poolerUnsupported) {
		t.Errorf("turning one on for Redis = %d %s", rec.Code, rec.Body)
	}
	if calls := h.engine.did(); len(calls) != 0 {
		t.Errorf("Docker was asked %v for an engine with no pooler", calls)
	}
}

func TestSettingsAreValidatedBeforeAnythingIsRun(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	id := h.add("db", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/app?sslmode=disable")
	for _, body := range []string{
		`{}`,
		`{"poolMode":"statement","poolSize":5,"maxClientConn":50,"authMode":"userlist"}`,
		`{"poolMode":"session","poolSize":0,"maxClientConn":50,"authMode":"userlist"}`,
		`{"poolMode":"session","poolSize":5,"maxClientConn":99999,"authMode":"userlist"}`,
		`{"poolMode":"session","poolSize":5,"maxClientConn":50,"authMode":"trust"}`,
		`{"poolMode":"session","poolSize":5,"maxClientConn":50,"authMode":"userlist","image":"evil"}`,
	} {
		if rec := do(t, router, http.MethodPut, pathf("/databases/%d/pooler", id), body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", body, rec.Code)
		}
	}
	if calls := h.engine.did(); len(calls) != 0 {
		t.Errorf("Docker was asked %v for invalid settings", calls)
	}
}

func TestAPostgresServerThatIsNotRunningIsNotOfferedAPooler(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	port, _ := deafPort(t)
	h.engine.containers = []*fakeDBContainer{{id: "aa11", name: "pg", image: "postgres:17-alpine", state: "exited",
		hostIP: "127.0.0.1", hostPort: port, port: 5432}}
	id := h.add("pg", dbx.DriverPostgres, fmt.Sprintf("postgres://app:pw@127.0.0.1:%d/app?sslmode=disable", port))
	view := readJSON[poolerView](t, do(t, router, http.MethodGet, pathf("/databases/%d/pooler", id), ""))
	if view.Available || !strings.Contains(view.Reason, "no running container") {
		t.Errorf("a stopped database reads as %+v", view)
	}
	if view.Reference != fmt.Sprintf("${{database.%d.pooled}}", id) || view.LinkedHost != poolerDNSName(id) || view.Image != dbx.PoolerImage {
		t.Errorf("the page is not told how it will be reached: %+v", view)
	}
}

func TestNoPoolerIsAConflictForItsStatisticsAndANotFoundToRemove(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	id := h.add("db", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/app?sslmode=disable")
	if rec := do(t, router, http.MethodGet, pathf("/databases/%d/pooler/stats", id), ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no_pooler") {
		t.Errorf("statistics of nothing = %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodDelete, pathf("/databases/%d/pooler", id), ""); rec.Code != http.StatusNotFound {
		t.Errorf("removing nothing = %d", rec.Code)
	}
	if rec := do(t, router, http.MethodGet, pathf("/databases/%d/url", id)+"?pooled=1", ""); rec.Code != http.StatusConflict {
		t.Errorf("a pooled string with no pooler = %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, router, http.MethodGet, pathf("/databases/%d/url", id)+"?pooled=yes", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown pooled value = %d", rec.Code)
	}
}

// pooledHarness is a Postgres container with a pooler beside it, in the
// engine the test owns.
func pooledHarness(t *testing.T) (h *connHarness, id int64, pooler *fakeDBContainer, database *fakeDBContainer) {
	t.Helper()
	h = newConnHarness(t)
	port, _ := deafPort(t)
	database = &fakeDBContainer{id: "aa11", name: "pg", image: "postgres:17-alpine", state: "running",
		hostIP: "127.0.0.1", hostPort: port, port: 5432}
	pooler = &fakeDBContainer{id: "bb22", name: "pg-pgbouncer", image: dbx.PoolerImage, state: "running",
		hostIP: "127.0.0.1", hostPort: 6433, port: dbx.PoolerListenPort}
	h.engine.containers = []*fakeDBContainer{database, pooler}
	id = h.add("pg", dbx.DriverPostgres, fmt.Sprintf("postgres://app:pw@127.0.0.1:%d/app?sslmode=disable", port))
	pooler.labels = map[string]string{poolerLabel: strconv.FormatInt(id, 10)}
	rec := &poolerRecord{
		ConnectionID: id, Container: "pg-pgbouncer", ContainerID: "bb22", Network: poolerNetworkName(id), HostPort: 6433,
		ServerName: "pg", ServerPort: 5432, Settings: dbx.DefaultPoolerSettings(),
		Secrets: poolerSecrets{Auth: "auth-secret", Stats: "stats-secret"},
	}
	_, dsn, err := h.s.dbConnRow(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rec.ConfigSHA, err = poolerFiles(rec, dsn); err != nil {
		t.Fatal(err)
	}
	if err := h.s.savePoolerRecord(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	return h, id, pooler, database
}

func TestThePoolerIsReadWithItsStateItsBindingAndWhetherItIsInSync(t *testing.T) {
	h, id, pooler, _ := pooledHarness(t)
	router := h.as(auth.RoleReadOnly)
	read := func() poolerView {
		t.Helper()
		rec := do(t, router, http.MethodGet, pathf("/databases/%d/pooler", id), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("read = %d %s", rec.Code, rec.Body)
		}
		return readJSON[poolerView](t, rec)
	}
	view := read()
	if !view.Enabled || !view.Available || view.State != "running" || view.Port != 6433 || view.Exposure != exposureLocal ||
		view.Container != "pg-pgbouncer" || !view.InSync || view.Settings != dbx.DefaultPoolerSettings() {
		t.Errorf("a running pooler reads as %+v", view)
	}
	if strings.Contains(fmt.Sprint(view), "stats-secret") || strings.Contains(fmt.Sprint(view), "auth-secret") {
		t.Error("the reading carries a secret")
	}
	pooler.hostIP = "0.0.0.0"
	if got := read().Exposure; got != exposurePublic {
		t.Errorf("a pooler bound to every interface reads as %s", got)
	}
	pooler.state = "exited"
	if got := read().State; got != "exited" {
		t.Errorf("a stopped pooler reads as %s", got)
	}
	// The connection's password moves on; the files the pooler holds do not.
	sealed, _ := h.s.Sealer.Seal(strings.Replace(savedDSN(t, h, id), ":pw@", ":changed@", 1))
	if _, err := h.s.Store.DB.Exec(`UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, id); err != nil {
		t.Fatal(err)
	}
	if read().InSync {
		t.Error("a changed password is reported as in sync")
	}
	h.engine.containers = []*fakeDBContainer{}
	if got := read().State; got != "missing" {
		t.Errorf("a pooler whose container is gone reads as %s", got)
	}
}

func savedDSN(t *testing.T, h *connHarness, id int64) string {
	t.Helper()
	_, dsn, err := h.s.dbConnRow(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestThePoolerStopsAndStartsWithItsDatabase(t *testing.T) {
	h, id, pooler, database := pooledHarness(t)
	c := &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	path := "/api/v1" + pathf("/databases/%d/power", id)

	rec := c.do(http.MethodPost, path, `{"action":"stop"}`, nil)
	if rec.Code != http.StatusOK || database.state != "exited" || pooler.state != "exited" {
		t.Fatalf("stop = %d; database %s, pooler %s", rec.Code, database.state, pooler.state)
	}
	if got := readJSON[map[string]string](t, rec)["pooler"]; got != "stopped" {
		t.Errorf("the reply says the pooler %q", got)
	}
	rec = c.do(http.MethodPost, path, `{"action":"start"}`, nil)
	if rec.Code != http.StatusOK || database.state != "running" || pooler.state != "running" {
		t.Fatalf("start = %d; database %s, pooler %s", rec.Code, database.state, pooler.state)
	}
	// A restart is the database's alone: PgBouncer reconnects by itself.
	calls := len(h.engine.did())
	if rec := c.do(http.MethodPost, path, `{"action":"restart"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("restart = %d %s", rec.Code, rec.Body)
	}
	if after := h.engine.did()[calls:]; slices.Contains(after, "restart pg-pgbouncer") || slices.Contains(after, "stop pg-pgbouncer") {
		t.Errorf("a restart of the database touched the pooler: %v", after)
	}

	var detail string
	if err := h.s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'database.power.stop'`).Scan(&detail); err != nil || !strings.Contains(detail, `"pooler":"stopped"`) {
		t.Errorf("the power entry does not say what became of the pooler: %q %v", detail, err)
	}
}

func TestForgettingAConnectionRemovesItsPoolerAndSaysSo(t *testing.T) {
	h, id, _, database := pooledHarness(t)
	c := &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	if rec := c.do(http.MethodDelete, "/api/v1"+pathf("/databases/%d", id), "", nil); rec.Code >= 300 {
		t.Fatalf("forget = %d %s", rec.Code, rec.Body)
	}
	if calls := h.engine.did(); !slices.Contains(calls, "remove pg-pgbouncer") || slices.Contains(calls, "remove pg") {
		t.Errorf("Docker was asked %v, want the pooler removed and the database kept", calls)
	}
	if database.state != "running" {
		t.Error("forgetting the connection stopped its database")
	}
	var detail string
	if err := h.s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'database.connection.delete'`).Scan(&detail); err != nil || !strings.Contains(detail, "removed pg-pgbouncer") {
		t.Errorf("the delete entry does not say the pooler went: %q %v", detail, err)
	}
	if rec, _ := h.s.poolerRecord(t.Context(), id); rec != nil {
		t.Error("the pooler's record outlived its connection")
	}
}

func TestAPoolerIsRemovedFromTheMachineAndTheRecordTogether(t *testing.T) {
	h, id, _, _ := pooledHarness(t)
	c := &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	rec := c.do(http.MethodDelete, "/api/v1"+pathf("/databases/%d/pooler", id), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	if !slices.Contains(h.engine.did(), "remove pg-pgbouncer") {
		t.Errorf("Docker was asked %v", h.engine.did())
	}
	if left, _ := h.s.poolerRecord(t.Context(), id); left != nil {
		t.Error("the record is still there")
	}
	var status int
	var target, detail string
	if err := h.s.Store.DB.QueryRow(`SELECT status, target, detail FROM audit_log WHERE action = 'database.pooler.disable'`).Scan(&status, &target, &detail); err != nil ||
		status != http.StatusOK || target != "pg" || !strings.Contains(detail, "pg-pgbouncer") {
		t.Errorf("audit entry = %d %q %s (%v)", status, target, detail, err)
	}
	if strings.Contains(detail, "stats-secret") || strings.Contains(detail, "auth-secret") {
		t.Error("the entry carries a secret")
	}
}

func TestThePooledReferenceAndItsHostnameAreRecognised(t *testing.T) {
	for target, want := range map[string]string{
		"5.pooled": "5", "5.pooled.jdbc": "5.jdbc", "5.pooled.url.app_cache": "5.url.app_cache",
	} {
		match := pooledReferenceRE.FindStringSubmatch(target)
		if match == nil || match[1]+match[2] != want {
			t.Errorf("%s read as %v, want %s", target, match, want)
		}
	}
	for _, target := range []string{"5", "5.jdbc", "pooled", "5.pooledx", "5.pooled..x", "orders.pooled", "5.pooled/../x"} {
		if match := pooledReferenceRE.FindStringSubmatch(target); match != nil {
			t.Errorf("%s was read as a pooled reference: %v", target, match)
		}
	}
	ids, err := databaseURLConnectionIDs(map[string]string{
		"A": "postgres://u:p@db-7-pool.jd.internal:6432/app",
		"B": "postgres://u:p@db-3.jd.internal:5432/app",
		"C": "postgres://u:p@db-7.jd.internal:5432/app",
	})
	if err != nil || !slices.Equal(ids, []int64{3, 7}) {
		t.Errorf("ids = %v (%v), want 3 and 7 once each", ids, err)
	}
	for _, bad := range []string{"db-x-pool.jd.internal", "db-0-pool.jd.internal", "db-7-pool-pool.jd.internal", "evil-7-pool.jd.internal", "db-7-poolx.jd.internal"} {
		if _, err := databaseURLConnectionIDs(map[string]string{"A": "postgres://u:p@" + bad + ":6432/app"}); err == nil {
			t.Errorf("%s was accepted as a managed hostname", bad)
		}
	}
}

func TestThePoolersFilesFollowTheConnectionsPassword(t *testing.T) {
	rec := &poolerRecord{ConnectionID: 1, ServerName: "pg", ServerPort: 5432, Settings: dbx.DefaultPoolerSettings(),
		Secrets: poolerSecrets{Auth: "a", Stats: "s"}}
	ini, list, first, err := poolerFiles(rec, "postgres://app:one@127.0.0.1:5432/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ini, "host=pg port=5432") || !strings.Contains(list, `"app" "one"`) {
		t.Errorf("files:\n%s\n%s", ini, list)
	}
	_, list, second, err := poolerFiles(rec, "postgres://app:two@127.0.0.1:5432/app")
	if err != nil || first == second || !strings.Contains(list, `"app" "two"`) {
		t.Errorf("a changed password left the digest %q the same as %q (%v)", second, first, err)
	}
	if _, _, _, err := poolerFiles(rec, "postgres://app@127.0.0.1:5432/app"); err == nil {
		t.Error("a connection with no password was written to the userlist")
	}
}

func TestAPoolerIsBoundWhereItsDatabaseIsAndFoundByItsConfiguration(t *testing.T) {
	spec := dockerx.ContainerSpec{Ports: []dockerx.PortMapping{
		{HostIP: "", HostPort: 9, ContainerPort: 80},
		{HostIP: "", HostPort: 6433, ContainerPort: dbx.PoolerListenPort},
	}}
	if ip, port := poolerBinding(&spec); ip != "0.0.0.0" || port != 6433 {
		t.Errorf("binding = %s:%d, want every interface and 6433", ip, port)
	}
	spec.Ports = spec.Ports[:1]
	if ip, port := poolerBinding(&spec); ip != "" || port != 0 {
		t.Errorf("a container with no pooler port reads as %s:%d", ip, port)
	}
	var out poolerSpecFields
	raw, _ := json.Marshal(poolerSpec(&poolerRecord{ConnectionID: 4, Container: "pg-pgbouncer", Network: "jd-pool-4"}, 6433, "127.0.0.1"))
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Image != dbx.PoolerImage || out.Entrypoint[0] != "/usr/bin/pgbouncer" || out.Ports[0].HostIP != "127.0.0.1" ||
		out.Ports[0].ContainerPort != dbx.PoolerListenPort || out.Networks[0] != "jd-pool-4" || out.RestartPolicy != "unless-stopped" {
		t.Errorf("spec = %+v", out)
	}
	if !slices.Equal(out.CapDrop, []string{"ALL"}) || out.Limits.MemoryMB != poolerMemoryMB {
		t.Errorf("the pooler keeps capabilities or has no memory limit: %+v", out)
	}
	if strings.Contains(string(raw), "PASSWORD") || len(out.Env) != 0 {
		t.Error("the pooler is given its credentials in its environment, where `docker inspect` shows them")
	}
}

// poolerSpecFields is the part of the container spec the test reads.
type poolerSpecFields struct {
	Image         string                 `json:"image"`
	Entrypoint    []string               `json:"entrypoint"`
	Env           []dockerx.EnvVar       `json:"env"`
	Ports         []dockerx.PortMapping  `json:"ports"`
	Networks      []string               `json:"networks"`
	RestartPolicy string                 `json:"restartPolicy"`
	CapDrop       []string               `json:"capDrop"`
	Limits        dockerx.ResourceLimits `json:"limits"`
}

// A pooler is made on loopback, kept where it is by everything that merely runs
// it, and moved only when the access switch names an address.
func TestAPoolerIsPublishedOnLoopbackUnlessTheSwitchSaysOtherwise(t *testing.T) {
	for _, tc := range []struct{ current, requested, want string }{
		{"", "", "127.0.0.1"},
		{"127.0.0.1", "", "127.0.0.1"},
		{"0.0.0.0", "", "0.0.0.0"},
		{"", "0.0.0.0", "0.0.0.0"},
		{"0.0.0.0", "127.0.0.1", "127.0.0.1"},
		{"127.0.0.1", "0.0.0.0", "0.0.0.0"},
	} {
		if got := poolerPublishAddress(tc.current, tc.requested); got != tc.want {
			t.Errorf("poolerPublishAddress(%q, %q) = %q, want %q", tc.current, tc.requested, got, tc.want)
		}
	}
}

// Turning one on for a second connection to a database container that has a
// pooler is refused before the database or Docker is written to: the dial of
// the saved string here would fail, and so would any Docker write.
func TestASecondPoolerOnTheSameContainerIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	h, _, _, _ := pooledHarness(t)
	router := h.as(auth.RoleAdmin)
	second := h.add("pg-second", dbx.DriverPostgres, savedDSN(t, h, 1))
	calls := len(h.engine.did())
	for _, mode := range []string{"userlist", "auth_query"} {
		rec := do(t, router, http.MethodPut, pathf("/databases/%d/pooler", second),
			`{"poolMode":"transaction","poolSize":5,"maxClientConn":50,"authMode":"`+mode+`"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "pooler_exists") || !strings.Contains(rec.Body.String(), "pg") {
			t.Errorf("%s: a second pooler on the container = %d %s", mode, rec.Code, rec.Body)
		}
	}
	if after := h.engine.did()[calls:]; len(after) != 0 {
		t.Errorf("Docker was written to: %v", after)
	}
	if rec, _ := h.s.poolerRecord(t.Context(), second); rec != nil {
		t.Error("a record was kept for the refused pooler")
	}
}

// The access switch fails closed. A move to loopback that cannot be made
// removes the pooler instead of leaving it published, and says so; a move to
// anywhere that cannot be made leaves it where it was.
func TestAPoolerThatCannotBeMovedToLoopbackIsRemovedNotLeftPublished(t *testing.T) {
	h, id, pooler, _ := pooledHarness(t)
	pooler.hostIP = "0.0.0.0"
	conn, dsn, err := h.s.dbConnRow(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	// The test engine serves no networks, so no move can be made.
	out := h.s.followPoolerExposure(t.Context(), nil, conn, dsn, "127.0.0.1")
	if out == nil || out["error"] == nil || out["removed"] != true {
		t.Fatalf("outcome = %v, want an error and the container removed", out)
	}
	if !slices.Contains(h.engine.did(), "remove pg-pgbouncer") {
		t.Errorf("Docker was asked %v, want the published pooler removed", h.engine.did())
	}
	if out["firewall"] == nil {
		t.Errorf("the rule its port was opened with was not looked for: %v", out)
	}
}

func TestAPoolerThatCannotBeMovedToAnywhereStaysWhereItWas(t *testing.T) {
	h, id, _, _ := pooledHarness(t)
	conn, dsn, _ := h.s.dbConnRow(t.Context(), id)
	out := h.s.followPoolerExposure(t.Context(), nil, conn, dsn, "0.0.0.0")
	if out == nil || out["error"] == nil || out["removed"] != nil {
		t.Fatalf("outcome = %v, want an error and nothing removed", out)
	}
	if slices.Contains(h.engine.did(), "remove pg-pgbouncer") {
		t.Error("a pooler that was on loopback was removed because it could not be widened")
	}
}

func TestAPoolerAlreadyWhereTheSwitchAsksIsLeftAlone(t *testing.T) {
	h, id, _, _ := pooledHarness(t)
	conn, dsn, _ := h.s.dbConnRow(t.Context(), id)
	if out := h.s.followPoolerExposure(t.Context(), nil, conn, dsn, "127.0.0.1"); out != nil {
		t.Errorf("a pooler already on loopback was touched: %v", out)
	}
	if calls := h.engine.did(); len(calls) != 0 {
		t.Errorf("Docker was asked %v", calls)
	}
}

// The keeper updates a record it saw; it does not bring back one that went.
func TestTheKeeperDoesNotResurrectARemovedPooler(t *testing.T) {
	h, id, _, _ := pooledHarness(t)
	sealed, _ := h.s.Sealer.Seal(strings.Replace(savedDSN(t, h, id), ":pw@", ":changed@", 1))
	if _, err := h.s.Store.DB.Exec(`UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.Store.DB.Exec(`DELETE FROM db_poolers WHERE connection_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if err := h.s.syncPooler(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if rec, _ := h.s.poolerRecord(t.Context(), id); rec != nil {
		t.Error("the keeper wrote a removed pooler's record back")
	}
}

func TestOnePoolerIsChangedAtATime(t *testing.T) {
	h := newConnHarness(t)
	unlock := h.s.lockPooler(7)
	if _, free := h.s.tryLockPooler(7); free {
		t.Fatal("the keeper took a pooler that a request holds")
	}
	if release, free := h.s.tryLockPooler(8); !free {
		t.Fatal("another connection's pooler was held")
	} else {
		release()
	}
	unlock()
	release, free := h.s.tryLockPooler(7)
	if !free {
		t.Fatal("the pooler was not released")
	}
	release()
}

// Forgetting a connection says what became of everything the pooler made, and
// does not write to a protected connection's database.
func TestForgettingAPoolerReportsWhatItLeavesInTheDatabase(t *testing.T) {
	h, id, _, _ := pooledHarness(t)
	rec, _ := h.s.poolerRecord(t.Context(), id)
	rec.Settings.AuthMode = dbx.PoolerAuthQuery
	rec.HostPort = 6433
	conn, dsn, _ := h.s.dbConnRow(t.Context(), id)

	conn.ReadOnly = true
	if said := h.s.forgetPooler(t.Context(), rec, conn, dsn); !strings.Contains(said, "removed pg-pgbouncer") ||
		!strings.Contains(said, "left in the database (the connection is protected)") {
		t.Errorf("protected: %q", said)
	}
	conn.ReadOnly = false
	if said := h.s.forgetPooler(t.Context(), rec, conn, dsn); !strings.Contains(said, "left in the database:") {
		t.Errorf("a database that does not answer: %q", said)
	}
	if said := h.s.forgetPooler(t.Context(), rec, nil, ""); !strings.Contains(said, "could not be read") {
		t.Errorf("a connection that could not be read: %q", said)
	}
	if strings.Contains(h.s.forgetPooler(t.Context(), rec, conn, dsn), "pw@") {
		t.Error("the report quotes the connection string")
	}
}

// A network that is not this pooler's is never disconnected from or removed.
func TestAForeignNetworkOfThePoolersNameIsNotRemoved(t *testing.T) {
	h, id, _, _ := pooledHarness(t)
	var removed, disconnected atomic.Bool
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := dockerAPIVersion.ReplaceAllString(r.URL.Path, "")
		switch {
		case strings.HasPrefix(path, "/networks/") && strings.HasSuffix(path, "/disconnect"):
			disconnected.Store(true)
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(path, "/networks/") && r.Method == http.MethodDelete:
			removed.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(path, "/networks/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Name": poolerNetworkName(id), "Id": "net1", "Driver": "bridge",
				"Labels": map[string]string{"owner": "somebody else"}})
		default:
			h.engine.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(engine.Close)
	original := h.s.modules.docker
	h.s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = h.s.modules.docker.Close(); h.s.modules.docker = original })

	rec, _ := h.s.poolerRecord(t.Context(), id)
	if err := h.s.removePoolerResources(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if removed.Load() || disconnected.Load() {
		t.Errorf("a network that is not the pooler's was touched (disconnected %v, removed %v)", disconnected.Load(), removed.Load())
	}
	if !slices.Contains(h.engine.did(), "remove pg-pgbouncer") {
		t.Error("the pooler's own container was not removed")
	}
}

func TestStatisticsNeedDockerAndSayWhenThereIsNone(t *testing.T) {
	h, id, _, _ := pooledHarness(t)
	original := h.s.modules.docker
	h.s.modules.docker = nil
	defer func() { h.s.modules.docker = original }()
	rec := do(t, h.as(auth.RoleReadOnly), http.MethodGet, pathf("/databases/%d/pooler/stats", id), "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "docker_unavailable") {
		t.Errorf("statistics with no Docker = %d %s", rec.Code, rec.Body)
	}
}
