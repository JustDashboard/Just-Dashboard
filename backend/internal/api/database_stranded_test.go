package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// strandRow stores a connection the way an earlier install with another
// master key left it: made from a found server, sealed under a key this one
// does not have.
func strandRow(t *testing.T, s *Server, name, driver, origin string) int64 {
	t.Helper()
	other, err := auth.NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := other.Seal("postgres://shop:old@127.0.0.1:15432/shop")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at, origin) VALUES(?,?,?,0,?)`,
		name, driver, sealed, origin)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func opens(t *testing.T, s *Server, id int64) bool {
	t.Helper()
	conn, _, err := s.dbConnRow(t.Context(), id)
	return err == nil && !conn.Broken
}

// A reinstall that kept the data but not the .env left every connection
// sealed under the old key. Connecting all used to add `shop-db-2` beside the
// red `shop-db`; the row is repaired instead, and keeps its id.
func TestTheReconcileRepairsAConnectionSealedUnderAnotherKey(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	id := strandRow(t, s, "shop-db", "postgres", "docker:shop-db")

	rec := do(t, r, http.MethodPost, "/databases/sync", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync answered %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Added    []string `json:"added"`
		Repaired []string `json:"repaired"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Repaired, ",") != "shop-db" || strings.Join(out.Added, ",") != "fixture" {
		t.Errorf("repaired %v added %v, want shop-db repaired and only fixture added", out.Repaired, out.Added)
	}
	rows := connectionRows(t, s)
	if _, doubled := rows["shop-db-2"]; doubled || len(rows) != 2 {
		t.Errorf("rows = %v, want shop-db once and fixture", rows)
	}
	if !opens(t, s, id) {
		t.Error("the repaired row does not open under this install's key")
	}
	if trail := auditTrail(t, s); !strings.Contains(trail, `"repaired":["shop-db"]`) {
		t.Errorf("the reconcile's audit entry does not name the repair:\n%s", trail)
	}

	// Converges: a second run repairs and adds nothing.
	again := do(t, r, http.MethodPost, "/databases/sync", "{}")
	if !strings.Contains(again.Body.String(), `"added":[]`) || !strings.Contains(again.Body.String(), `"repaired":[]`) {
		t.Errorf("a second reconcile changed something: %s", again.Body.String())
	}
}

// Connecting the server by hand from Found on this server is the same repair.
func TestConnectingAFoundServerRepairsTheConnectionItLost(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	id := strandRow(t, s, "shop-db", "postgres", "docker:shop-db")

	rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:shop-db"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	if got := readJSON[summaryView](t, rec); got.ID != id || got.Name != "shop-db" || got.Broken {
		t.Errorf("connect answered %+v, want row %d repaired", got, id)
	}
	if rows := connectionRows(t, s); len(rows) != 1 {
		t.Errorf("rows = %v, want the one repaired row", rows)
	}
	if trail := auditTrail(t, s); !strings.Contains(trail, `"repaired":true`) {
		t.Errorf("the connect's audit entry does not say it repaired a row:\n%s", trail)
	}
}

// Nobody has to open the page: the background pass repairs what it can with
// what the servers state, audits it as the system, and does not send a
// refused password again on every pass.
func TestTheBackgroundPassRepairsWithoutARequest(t *testing.T) {
	m := ordinaryMachine()
	m.containers = append(m.containers, fakeContainer{
		id: "aaaaaaaaaaaa0006", name: "stale", image: "postgres:16", state: "running",
		env: []string{"POSTGRES_PASSWORD=stale-pw"}, ports: []fakePort{{5432, 45432, "127.0.0.1"}},
	})
	m.refuse = map[string]string{"stale-pw": `password authentication failed for user "postgres"`}
	s, _ := inventoryRouter(t, auth.RoleAdmin, m)
	shop := strandRow(t, s, "shop-db", "postgres", "docker:shop-db")
	stale := strandRow(t, s, "stale", "postgres", "docker:stale")
	byHand := strandRow(t, s, "by-hand", "postgres", "")

	for range 3 {
		s.repairStranded(t.Context())
	}
	if !opens(t, s, shop) {
		t.Error("shop-db was not repaired")
	}
	if opens(t, s, stale) || opens(t, s, byHand) {
		t.Error("a row was repaired with nothing that signs in to its server")
	}
	if rows := connectionRows(t, s); len(rows) != 3 {
		t.Errorf("rows = %v, want the pass to add nothing", rows)
	}
	dials := 0
	for _, dsn := range m.dials() {
		if strings.Contains(dsn, "stale-pw") {
			dials++
		}
	}
	if dials != 1 {
		t.Errorf("the refused password was sent %d times over three passes, want once", dials)
	}
	if trail := auditTrail(t, s); strings.Count(trail, "database.connection.repair shop-db") != 1 {
		t.Errorf("want one system repair entry for shop-db:\n%s", trail)
	}
}

// Two rows made from one server were two accounts or two databases, and a
// sign-in cannot say which it stands for: neither is overwritten, and each
// says the server is connected again under another name.
func TestTwoConnectionsLostFromOneServerAreNotGuessedBetween(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	first := strandRow(t, s, "shop-admin", "postgres", "docker:shop-db")
	second := strandRow(t, s, "shop-readonly", "postgres", "docker:shop-db")

	do(t, r, http.MethodPost, "/databases/sync", "{}")
	if opens(t, s, first) || opens(t, s, second) {
		t.Error("a row was repaired although two were made from the server")
	}
	list := readJSON[[]summaryView](t, do(t, r, http.MethodGet, "/databases/", ""))
	for _, c := range list {
		if c.ID == first || c.ID == second {
			if !strings.Contains(c.Reason, "connected again as shop-db") {
				t.Errorf("%s says %q, want it to name the connection that replaced it", c.Name, c.Reason)
			}
		}
	}
}

// What a row that cannot be opened says depends on what repairs it.
func TestALostConnectionSaysWhatRepairsIt(t *testing.T) {
	s, r := inventoryRouter(t, auth.RoleAdmin, ordinaryMachine())
	found := strandRow(t, s, "gone-db", "postgres", "docker:gone-db")
	byHand := strandRow(t, s, "by-hand", "postgres", "")

	reasons := map[int64]string{}
	for _, c := range readJSON[[]summaryView](t, do(t, r, http.MethodGet, "/databases/", "")) {
		reasons[c.ID] = c.Reason
	}
	if !strings.Contains(reasons[found], "JD_MASTER_KEY") || !strings.Contains(reasons[found], "Found on this server") {
		t.Errorf("a row made from a found server says %q", reasons[found])
	}
	if !strings.Contains(reasons[byHand], "JD_MASTER_KEY") || !strings.Contains(reasons[byHand], "entered again") {
		t.Errorf("a row typed by hand says %q", reasons[byHand])
	}
	// Its own page says the same.
	if got := readJSON[summaryView](t, do(t, r, http.MethodGet, pathf("/databases/%d", found), "")); got.Error != reasons[found] {
		t.Errorf("summary says %q, list says %q", got.Error, reasons[found])
	}
}

// A server installed on the host states no password, so the operator types
// it; that sign-in repairs the row too.
func TestTypingAHostServersPasswordRepairsTheConnectionItLost(t *testing.T) {
	s, r := inventoryRouter(t, auth.RoleAdmin, ordinaryMachine())
	id := strandRow(t, s, "postgres on this host", "postgres", "host:postgresql@17-main.service")

	rec := do(t, r, http.MethodPost, "/databases/host",
		`{"driver":"postgres","host":"127.0.0.1","port":5438,"user":"postgres","password":"typed","database":"postgres","name":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("host connect = %d %s", rec.Code, rec.Body.String())
	}
	if got := readJSON[summaryView](t, rec); got.ID != id {
		t.Errorf("host connect answered row %d, want %d repaired", got.ID, id)
	}
	if !opens(t, s, id) || len(connectionRows(t, s)) != 1 {
		t.Errorf("rows = %v, want the one row repaired", connectionRows(t, s))
	}
}
