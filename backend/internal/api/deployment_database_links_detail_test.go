package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

type databaseLinkView struct {
	ConnectionID int64  `json:"connectionId"`
	Name         string `json:"name"`
	Driver       string `json:"driver"`
	Database     string `json:"database"`
	Hostname     string `json:"hostname"`
	Status       string `json:"status"`
	Detail       string `json:"detail"`
}

// A linked database is reported with the engine it runs and the database it
// opens, and — when reconciliation could not repair it — with the reason.
// Without the reason the settings page had one sentence for every cause.
func TestDatabaseLinkReportsEngineAndReconciliationReason(t *testing.T) {
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

	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "database-link-detail-reader", auth.RoleReadOnly)}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/database-links", projectID, environmentID)

	read := func() databaseLinkView {
		t.Helper()
		got := reader.do(http.MethodGet, path, "", nil)
		if got.Code != http.StatusOK {
			t.Fatalf("database-links status=%d %s", got.Code, got.Body.String())
		}
		var items []databaseLinkView
		if err := json.Unmarshal(got.Body.Bytes(), &items); err != nil {
			t.Fatalf("decode %v: %s", err, got.Body.String())
		}
		if len(items) != 1 {
			t.Fatalf("want one link, got %d: %s", len(items), got.Body.String())
		}
		if strings.Contains(got.Body.String(), "private-") {
			t.Fatalf("credential material in link report: %s", got.Body.String())
		}
		return items[0]
	}

	connected := read()
	if connected.Driver != "postgres" || connected.Database != "orders" {
		t.Fatalf("engine identity missing: %+v", connected)
	}
	if connected.Hostname != databaseDNSName(connectionID) {
		t.Fatalf("hostname=%q", connected.Hostname)
	}
	// A connected binding has no reason to carry.
	if connected.Detail != "" {
		t.Fatalf("connected binding carried a reason: %q", connected.Detail)
	}

	networks := &deploymentDatabaseNetworks{server: s}
	networks.markUnavailable(context.Background(), environmentID, connectionID,
		fmt.Errorf("the saved database port belongs to a different container; reconnect the database explicitly"))

	unavailable := read()
	if unavailable.Status != "unavailable" {
		t.Fatalf("status=%q", unavailable.Status)
	}
	if !strings.Contains(unavailable.Detail, "belongs to a different container") {
		t.Fatalf("reason was not reported: %q", unavailable.Detail)
	}

	// A stale observation is about the age of the last pass, not about a
	// failure, so the reason of an older pass is not shown as its cause.
	if _, err := s.Store.DB.Exec(`UPDATE deploy_database_bindings SET status='connected',checked_at=1`); err != nil {
		t.Fatal(err)
	}
	stale := read()
	if stale.Status != "stale" || stale.Detail != "" {
		t.Fatalf("stale link=%+v", stale)
	}
}

// A Docker failure is not written to a length this column should hold.
func TestDatabaseLinkReconciliationReasonIsBounded(t *testing.T) {
	s := testServer(t)
	_, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	sealed, err := s.Sealer.Seal("postgres://app:pw@127.0.0.1:5432/app")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES('bounded-db','postgres',?,1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := res.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_networks(environment_id,network_name,owner_key,created_at) VALUES(?,'bounded-network','token',1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_bindings(environment_id,connection_id,container_name,status) VALUES(?,?,'app-db','connected')`, environmentID, connectionID); err != nil {
		t.Fatal(err)
	}
	networks := &deploymentDatabaseNetworks{server: s}
	networks.markUnavailable(context.Background(), environmentID, connectionID, fmt.Errorf("%s", strings.Repeat("x", 4096)))
	var stored string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM deploy_database_bindings WHERE environment_id=? AND connection_id=?`, environmentID, connectionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 300 {
		t.Fatalf("reason was not bounded: %d characters", len(stored))
	}
}

// A truncated reason must still decode: a byte slice through a multi-byte
// character would store text no reader can read back.
func TestDatabaseLinkReconciliationReasonStaysValidUTF8(t *testing.T) {
	s := testServer(t)
	_, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	sealed, err := s.Sealer.Seal("postgres://app:pw@127.0.0.1:5432/app")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES('utf8-db','postgres',?,1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := res.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_networks(environment_id,network_name,owner_key,created_at) VALUES(?,'utf8-network','token',1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_database_bindings(environment_id,connection_id,container_name,status) VALUES(?,?,'app-db','connected')`, environmentID, connectionID); err != nil {
		t.Fatal(err)
	}
	networks := &deploymentDatabaseNetworks{server: s}
	// 150 two-byte runes: the 300-byte cut lands mid-character.
	networks.markUnavailable(context.Background(), environmentID, connectionID,
		fmt.Errorf("%s", strings.Repeat("é", 200)))
	var stored string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM deploy_database_bindings WHERE environment_id=? AND connection_id=?`, environmentID, connectionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(stored) {
		t.Fatalf("stored reason is not valid UTF-8: %q", stored)
	}
	if stored == "" || len(stored) > 300 {
		t.Fatalf("reason length=%d", len(stored))
	}
}
