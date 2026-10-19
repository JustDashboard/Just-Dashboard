package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// An environment given its database's address written out — this machine's
// address and the port the database container is published on, not the
// loopback the dashboard saved it at — has no dependency row and no binding,
// so the links reported nothing and Settings → Databases said no database was
// linked to an application that was using one. The addresses route names the
// saved database and the variables holding it, and nothing of their values.
func TestDeploymentDatabaseAddressesNameTheDatabaseALiteralURLReaches(t *testing.T) {
	h := newConnHarness(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, h.s)
	h.engine.containers = []*fakeDBContainer{
		{id: "db0000000001", name: "shop-db", image: "postgres:17-alpine", state: "running", hostIP: "0.0.0.0", hostPort: 59435, port: 5432},
		{id: "db0000000002", name: "other-db", image: "postgres:17-alpine", state: "running", hostIP: "0.0.0.0", hostPort: 59436, port: 5432},
	}
	shop := h.add("shop", dbx.DriverPostgres, "postgres://admin:saved-secret@127.0.0.1:59435/app?sslmode=disable&connect_timeout=1")
	h.add("other", dbx.DriverPostgres, "postgres://admin:saved-secret@127.0.0.1:59436/other?sslmode=disable&connect_timeout=1")

	for name, value := range map[string]string{
		"DATABASE_URL": "postgres://app:variable-secret@host.docker.internal:59435/app?sslmode=disable",
		// A port that only begins with the other database's is not its address.
		"CACHE_URL": "postgres://app:variable-secret@host.docker.internal:594361/cache",
		// A host and a port kept apart name it too.
		"DB_HOST": "host.docker.internal",
		"DB_PORT": "59435",
	} {
		sealed, err := h.s.Sealer.Seal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.s.Store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,value_enc,created_at,value_mode) VALUES(?,?,1,?,1,'literal')`, environmentID, name, sealed); err != nil {
			t.Fatal(err)
		}
	}

	reader := &client{t: t, h: h.s.Routes(), cookie: signInAs(t, h.s, "database-address-reader", auth.RoleReadOnly)}
	got := reader.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-addresses", projectID, environmentID), "", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("database-addresses status=%d %s", got.Code, got.Body.String())
	}
	if strings.Contains(got.Body.String(), "secret") || strings.Contains(got.Body.String(), "59435") {
		t.Fatalf("a value left the server: %s", got.Body.String())
	}
	items := readJSON[[]struct {
		ConnectionID int64    `json:"connectionId"`
		Name         string   `json:"name"`
		Driver       string   `json:"driver"`
		Database     string   `json:"database"`
		Variables    []string `json:"variables"`
	}](t, got)
	if len(items) != 1 {
		t.Fatalf("want only shop, got %+v", items)
	}
	item := items[0]
	if item.ConnectionID != shop || item.Name != "shop" || item.Driver != "postgres" || item.Database != "app" {
		t.Errorf("address = %+v, want shop's identity", item)
	}
	if strings.Join(item.Variables, ",") != "DATABASE_URL,DB_HOST,DB_PORT" {
		t.Errorf("variables = %v, want DATABASE_URL, DB_HOST and DB_PORT", item.Variables)
	}
}
