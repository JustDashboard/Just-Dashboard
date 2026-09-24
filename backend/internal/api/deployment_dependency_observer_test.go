package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// A dependency row links to the page that owns the resource: a backup job's
// own page, and Databases with the connection selected. A link is only that
// specific once the resource is known to exist; until then it opens the list.
func TestDependencyDeepLinksOpenTheOwningPage(t *testing.T) {
	s := testServer(t)
	_, _, backupID := insertDeploymentConfigurationAPI(t, s)
	sealed, err := s.Sealer.Seal("postgres://app:private@127.0.0.1:5432/orders")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES('orders-db','postgres',?,1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := res.LastInsertId()
	observer := newDeploymentDependencyObserver(s.Store, s.modules.backupStore, nil)
	observed, err := observer.ObserveDependencies(t.Context(), []deploy.PlannedDependency{
		{Kind: "backup", ResourceKind: "backup_job", ResourceID: fmt.Sprint(backupID)},
		{Kind: "backup", ResourceKind: "backup_job", ResourceID: "999999"},
		{Kind: "database", ResourceKind: "database_connection", ResourceID: fmt.Sprint(connectionID)},
		{Kind: "database", ResourceKind: "database_connection", ResourceID: "999999"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		fmt.Sprintf("/backups/%d", backupID), "/backups",
		fmt.Sprintf("/databases/connection?conn=%d", connectionID), "/databases",
	}
	for index, link := range want {
		if observed[index].DeepLink != link {
			t.Errorf("dependency %d deep link = %q, want %q", index, observed[index].DeepLink, link)
		}
	}
	if !observed[0].Available || observed[1].Available || !observed[2].Available || observed[3].Available {
		t.Fatalf("availability = %#v", observed)
	}
}

// A linked PostgreSQL reports the schema extensions it offers, so preflight
// can refuse a pgvector schema before its first migration; a server that
// cannot be asked reports nothing rather than an empty list.
func TestDependencyObserverReportsDatabaseExtensions(t *testing.T) {
	s := testServer(t)
	sealed, err := s.Sealer.Seal("postgres://app:private@127.0.0.1:5432/orders")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES('vectors','postgres',?,1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := res.LastInsertId()
	answers := map[int64][]string{connectionID: {"postgis"}}
	observer := newDeploymentDependencyObserver(s.Store, s.modules.backupStore, nil).withExtensionProbe(
		func(_ context.Context, id int64) ([]string, error) {
			if available, ok := answers[id]; ok {
				return available, nil
			}
			return nil, errors.New("unreachable")
		})
	observed, err := observer.ObserveDependencies(t.Context(), []deploy.PlannedDependency{
		{Kind: "database", ResourceKind: "database_connection", ResourceID: fmt.Sprint(connectionID)},
	})
	if err != nil || len(observed) != 1 || !reflect.DeepEqual(observed[0].Extensions, []string{"postgis"}) {
		t.Fatalf("observed = %#v, %v", observed, err)
	}
	delete(answers, connectionID)
	observed, _ = observer.ObserveDependencies(t.Context(), []deploy.PlannedDependency{
		{Kind: "database", ResourceKind: "database_connection", ResourceID: fmt.Sprint(connectionID)},
	})
	if observed[0].Extensions != nil || !observed[0].Available {
		t.Fatalf("an unreachable probe = %#v", observed[0])
	}
}

// Each archived row carries what it deployed, read beside the project record.
func TestArchivedListCarriesEachDeploymentsSourceAndBuildFacts(t *testing.T) {
	s := testServer(t)
	id, _, _ := insertDeploymentConfigurationAPI(t, s)
	if _, err := s.modules.deployStore.Archive(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "archived-facts-admin", auth.RoleAdmin)}
	res := admin.do(http.MethodGet, "/api/v1/deploy/?view=archived", "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("archived list: %d %s", res.Code, res.Body.String())
	}
	var rows []struct {
		ID          int64              `json:"id"`
		Name        string             `json:"name"`
		ArchivedAt  string             `json:"archivedAt"`
		SourceKind  deploy.SourceKind  `json:"sourceKind"`
		BuildMethod deploy.BuildMethod `json:"buildMethod"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id || rows[0].Name != "api-config-app" || rows[0].ArchivedAt == "" ||
		rows[0].SourceKind != deploy.SourceGit || rows[0].BuildMethod != deploy.BuildNone {
		t.Fatalf("archived rows = %s", res.Body.String())
	}
}
