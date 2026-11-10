package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// The link report read its preview policies and each link's database name
// while its own cursor still held a connection. On the store's real pool that
// works until a few such requests run at once; on one connection it can never
// finish, so the route is asked with a link present and a deadline.
func TestDatabaseLinksAreReadOnOneConnection(t *testing.T) {
	s := testServer(t)
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	sealed, err := s.Sealer.Seal("postgres://app:private-database-password@127.0.0.1:5432/orders")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES('orders-db','postgres',?,1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := res.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_networks(environment_id,network_name,network_id,owner_key,created_at) VALUES(?,'fixture-network','network-id','private-owner-token',1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_bindings(environment_id,connection_id,container_name,status,checked_at) VALUES(?,?,'app-db','connected',?)`, environmentID, connectionID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	cookie := signInAs(t, s, "database-link-pool-reader", auth.RoleReadOnly)
	routes := s.Routes()
	storetest.OneConnection(s.Store)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-links", projectID, environmentID), nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Cookie", cookie)
	got := httptest.NewRecorder()
	routes.ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("database-links on a single connection: status=%d %s", got.Code, got.Body.String())
	}
	var items []databaseLinkView
	if err := json.Unmarshal(got.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode %v: %s", err, got.Body.String())
	}
	if len(items) != 1 || items[0].Database != "orders" {
		t.Fatalf("links = %+v, want the one link with its database", items)
	}
}
