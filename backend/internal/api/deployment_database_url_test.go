package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestApplicationConnectionURLPreservesCredentialsAndDatabase(t *testing.T) {
	for _, tc := range []struct {
		driver        dbx.Driver
		dsn, expected string
	}{
		{dbx.DriverPostgres, "postgres://app:p%40ss@127.0.0.1:5432/app?sslmode=disable", "postgres://app:p%40ss@172.17.0.4:5432/app?sslmode=disable"},
		{dbx.DriverMySQL, "app:p@ss@tcp(127.0.0.1:3306)/myapp", "mysql://app:p%40ss@172.17.0.4:5432/myapp"},
		{dbx.DriverMongo, "mongodb://app:secret@127.0.0.1:27017/app?authSource=admin", "mongodb://app:secret@172.17.0.4:5432/app?authSource=admin"},
		{dbx.DriverRedis, "redis://:secret@127.0.0.1:6379/2", "redis://:secret@172.17.0.4:5432/2"},
	} {
		t.Run(string(tc.driver), func(t *testing.T) {
			info, err := dbx.ParseDSN(tc.driver, tc.dsn)
			if err != nil {
				t.Fatal(err)
			}
			got, err := applicationConnectionURL(tc.driver, tc.dsn, info, "172.17.0.4", "5432")
			if err != nil || got != tc.expected {
				t.Fatalf("got %q, %v; want %q", got, err, tc.expected)
			}
		})
	}
}

func TestDatabaseURLTargetIsExplicitAndDoesNotChangeSavedConnection(t *testing.T) {
	s, router := dbTestRouter(t)
	dsn := "postgres://app:unique-password@db.example.test:5432/app?sslmode=require"
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`, "app-db", "postgres", sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	for _, target := range []string{"host", "container"} {
		response := do(t, router, http.MethodGet, pathf("/databases/%d/url", id)+"?target="+target, "")
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("target %s: status %d", target, response.Code)
		}
		if !strings.Contains(response.Body.String(), "unique-password") {
			t.Fatal("explicit read did not return connection")
		}
	}
	_, stored, err := s.dbConnRow(t.Context(), id)
	if err != nil || stored != dsn {
		t.Fatal("application read changed saved connection")
	}
	response := do(t, router, http.MethodGet, pathf("/databases/%d/url", id)+"?target=arbitrary", "")
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "unique-password") {
		t.Fatal("unknown target did not fail closed")
	}
}

func TestDatabaseApplicationURLRefusesUnreachableHostLoopback(t *testing.T) {
	s := testServer(t)
	s.modules.docker = nil
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		u := url.URL{Scheme: "postgres", Host: host + ":5432", Path: "/app", User: url.UserPassword("app", "unique-password")}
		value, err := s.databaseApplicationURL(t.Context(), &dbConnection{Driver: dbx.DriverPostgres}, u.String())
		if err == nil || value != "" || strings.Contains(err.Error(), "unique-password") {
			t.Fatalf("loopback %s did not fail safely", host)
		}
	}
}

func TestApplicationConnectionURLKeepsRemoteDiscoveryAndTransport(t *testing.T) {
	for _, tc := range []struct {
		driver dbx.Driver
		dsn    string
	}{
		{dbx.DriverPostgres, "postgres://app:secret@db.example.test/app?sslmode=verify-full"},
		{dbx.DriverMongo, "mongodb+srv://app:secret@cluster.example.test/app?tls=true"},
		{dbx.DriverMongo, "mongodb://app:secret@db1.example.test:27017,db2.example.test:27017/app?replicaSet=rs0&tls=true"},
		{dbx.DriverRedis, "rediss://:secret@redis.example.test/2"},
	} {
		info, err := dbx.ParseDSN(tc.driver, tc.dsn)
		if err != nil {
			t.Fatal(err)
		}
		got, err := applicationConnectionURL(tc.driver, tc.dsn, info, info.Host, info.Port)
		if err != nil || got != tc.dsn {
			t.Fatalf("remote connection changed: %v", err)
		}
	}
	for _, dsn := range []string{"app:secret@tcp(db.example.test:3306)/app?tls=true", "app:secret@unix(/var/run/mysql.sock)/app"} {
		info, err := dbx.ParseDSN(dbx.DriverMySQL, dsn)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := applicationConnectionURL(dbx.DriverMySQL, dsn, info, info.Host, info.Port); err == nil || got != "" {
			t.Fatal("driver-specific options were silently discarded")
		}
	}
}

func TestDatabaseLoopbackBindingMatchesSavedAddress(t *testing.T) {
	for _, tc := range []struct {
		saved, observed string
		want            bool
	}{
		{"127.0.0.1", "127.0.0.1", true},
		{"127.0.0.2", "127.0.0.1", false},
		{"127.0.0.1", "127.0.0.2", false},
		{"localhost", "127.0.0.1", true},
		{"localhost", "::1", true},
		{"localhost", "127.0.0.2", false},
		{"[::1]", "::1", true},
		{"127.0.0.1", "0.0.0.0", false},
	} {
		if got := sameDatabaseLoopback(tc.saved, tc.observed); got != tc.want {
			t.Fatalf("saved %s, observed %s: got %v, want %v", tc.saved, tc.observed, got, tc.want)
		}
	}
}

// A consumer that parses JDBC or ADO.NET gets the same connection in its own
// shape, and the typed reference records the shape so the release resolves it
// the same way.
func TestDatabaseURLRendersTheConsumersFormat(t *testing.T) {
	s, router := dbTestRouter(t)
	dsn := "postgres://app:unique-password@db.example.test:5432/app?sslmode=require"
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`, "app-db", "postgres", sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	for _, test := range []struct {
		query, url, reference string
	}{
		{"&format=jdbc", "jdbc:postgresql://db.example.test:5432/app?password=unique-password&sslmode=require&user=app", pathf("${{database.%d.jdbc}}", id)},
		{"&format=adonet", "Host=db.example.test;Port=5432;Database=app;Username=app;Password=unique-password", pathf("${{database.%d.adonet}}", id)},
		{"&database=app_cache", "postgres://app:unique-password@db.example.test:5432/app_cache?sslmode=require", pathf("${{database.%d.url.app_cache}}", id)},
		{"&format=url", dsn, pathf("${{database.%d}}", id)},
	} {
		response := do(t, router, http.MethodGet, pathf("/databases/%d/url", id)+"?target=container"+test.query, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", test.query, response.Code, response.Body.String())
		}
		var body struct{ URL, Reference string }
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.URL != test.url || body.Reference != test.reference {
			t.Fatalf("%s: url %q reference %q", test.query, body.URL, body.Reference)
		}
		if _, err := deploy.ParseVariableReference(body.Reference); err != nil {
			t.Fatalf("%s: reference %q does not parse: %v", test.query, body.Reference, err)
		}
	}
	for _, query := range []string{"&format=odbc", "&database=../etc", "&format=mysql2"} {
		response := do(t, router, http.MethodGet, pathf("/databases/%d/url", id)+"?target=container"+query, "")
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "unique-password") {
			t.Fatalf("%s: status %d", query, response.Code)
		}
	}
}

func TestDatabaseReferenceShapeIsReadOnlyAfterANumericID(t *testing.T) {
	for target, want := range map[string][3]string{
		"5.jdbc":          {"5", "jdbc", ""},
		"5.url.app_cache": {"5", "url", "app_cache"},
		"5.adonet":        {"5", "adonet", ""},
		"my.db.jdbc":      {},
		"5.jdbc.../x":     {},
		"5":               {},
		"5.url":           {"5", "url", ""},
	} {
		match := databaseReferenceShapeRE.FindStringSubmatch(target)
		got := [3]string{}
		if match != nil {
			got = [3]string{match[1], match[2], match[3]}
		}
		if got != want {
			t.Fatalf("%s = %v, want %v", target, got, want)
		}
	}
}
