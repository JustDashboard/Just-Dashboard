package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// TestLiveDatabaseContentsSaysWhenADatabaseIsNew provisions a PostgreSQL and
// a MySQL database through the API and asks whether each holds rows, the
// question a build answers before it loads a project's seed: a new one does
// not; one with only the migration tool's own records and empty tables still
// does not; one with a single row of the application's does. JD_DEPLOY_LIVE=1.
func TestLiveDatabaseContentsSaysWhenADatabaseIsNew(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	for _, test := range []struct {
		engine      string
		bookkeeping []string
		table       string
		row         string
	}{
		{"postgres", []string{
			`CREATE SCHEMA drizzle`,
			`CREATE TABLE drizzle.__drizzle_migrations (id serial PRIMARY KEY, hash text NOT NULL)`,
			`INSERT INTO drizzle.__drizzle_migrations(hash) VALUES ('0000')`,
			`CREATE TABLE _prisma_migrations (id text PRIMARY KEY)`,
			`INSERT INTO _prisma_migrations VALUES ('0000')`,
		}, `CREATE TABLE "Posts" (id serial PRIMARY KEY, slug text NOT NULL)`, `INSERT INTO "Posts"(slug) VALUES ('hello')`},
		{"mysql", []string{
			`CREATE TABLE knex_migrations (id int PRIMARY KEY, name varchar(255))`,
			`INSERT INTO knex_migrations VALUES (1, '0000')`,
		}, "CREATE TABLE `posts` (id int PRIMARY KEY AUTO_INCREMENT, slug varchar(255) NOT NULL)", "INSERT INTO `posts`(slug) VALUES ('hello')"},
	} {
		t.Run(test.engine, func(t *testing.T) {
			s, router := dbTestRouter(t)
			s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
			pullEngineImage(t, s.modules.docker, test.engine)
			name := fmt.Sprintf("jd-contents-%s-%d", test.engine, time.Now().UnixNano())
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = s.modules.docker.RemoveContainer(ctx, name, true, true)
				_ = s.modules.docker.RemoveVolume(ctx, name+"-data", false)
			})
			body, _ := json.Marshal(map[string]string{"engine": test.engine, "name": name, "database": "app", "exposure": "local"})
			if created := do(t, router, http.MethodPost, "/databases/provision", string(body)); created.Code != http.StatusAccepted {
				t.Fatalf("provision failed: HTTP %d: %s", created.Code, created.Body)
			}
			var conn dbConnection
			deadline := time.Now().Add(2 * time.Minute)
			for {
				adopted := do(t, router, http.MethodPost, "/databases/adopt", fmt.Sprintf(`{"container":%q}`, name))
				if adopted.Code >= 200 && adopted.Code < 300 && json.Unmarshal(adopted.Body.Bytes(), &conn) == nil {
					ping := do(t, router, http.MethodGet, pathf("/databases/%d/ping", conn.ID), "")
					var health struct {
						OK bool `json:"ok"`
					}
					if json.Unmarshal(ping.Body.Bytes(), &health) == nil && health.OK {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s did not accept connections: %s", test.engine, describeFixtureContainer(s.modules.docker, name))
				}
				time.Sleep(time.Second)
			}
			contents := deploymentDatabaseContents{server: s}
			hasRows := func() bool {
				t.Helper()
				present, err := contents.DatabaseHasRows(t.Context(), conn.ID)
				if err != nil {
					t.Fatal(err)
				}
				return present
			}
			exec := func(statements ...string) {
				t.Helper()
				row, dsn, err := s.dbConnRow(t.Context(), conn.ID)
				if err != nil {
					t.Fatal(err)
				}
				pool, err := s.modules.dbs.Pool(t.Context(), row.ID, row.Driver, dsn)
				if err != nil {
					t.Fatal(err)
				}
				for _, statement := range statements {
					if _, err := pool.ExecContext(t.Context(), statement); err != nil {
						t.Fatalf("%s: %v", statement, err)
					}
				}
			}
			if hasRows() {
				t.Fatal("a new database holds rows")
			}
			exec(append(test.bookkeeping, test.table)...)
			if hasRows() {
				t.Fatal("migration records and an empty table were counted as rows")
			}
			exec(test.row)
			if !hasRows() {
				t.Fatal("a row of the application's was not seen")
			}
		})
	}
}
