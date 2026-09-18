package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// Where a database is reachable from is read off its port binding, and the
// binding is the one thing the four spellings of "every interface" agree on.
func TestExposureReadsTheBinding(t *testing.T) {
	for _, c := range []struct {
		ip   string
		want dbExposure
	}{
		{"127.0.0.1", exposureLocal},
		{"::1", exposureLocal},
		{"0.0.0.0", exposurePublic},
		{"", exposurePublic},
		{"::", exposurePublic},
		{"10.0.0.5", exposurePrivate},
		{"100.64.0.7", exposurePrivate},
	} {
		got := exposureOf(&dbServer{binding: &dockerx.Port{IP: c.ip, PrivatePort: 5432, PublicPort: 5432}})
		if got != c.want {
			t.Errorf("binding on %q read as %s, want %s", c.ip, got, c.want)
		}
	}
	if got := exposureOf(&dbServer{}); got != exposureLocal {
		t.Errorf("a container reached at its own address read as %s, want local", got)
	}
}

// The string handed to a laptop is the saved one with the server's address in
// place of loopback and nothing else changed — the password, the database and
// the options are what make it work.
func TestPublicConnectionURLKeepsEverythingButTheHost(t *testing.T) {
	dsn := "postgres://jd:s3cret@127.0.0.1:5433/app?sslmode=disable"
	info, err := dbx.ParseDSN(dbx.DriverPostgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	got, err := publicConnectionURL(dbx.DriverPostgres, dsn, info, "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://jd:s3cret@203.0.113.9:5433/app?sslmode=disable"; got != want {
		t.Errorf("public URL = %q, want %q", got, want)
	}

	// An IPv6 address has to be bracketed before a port can follow it.
	got, err = publicConnectionURL(dbx.DriverPostgres, dsn, info, "2001:db8::9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "@[2001:db8::9]:5433/") {
		t.Errorf("IPv6 public URL = %q, want the address bracketed", got)
	}

	// MySQL's driver form is not something another program can paste, so it
	// goes out as a URL.
	mysql := "jd:s3cret@tcp(127.0.0.1:3306)/app"
	info, err = dbx.ParseDSN(dbx.DriverMySQL, mysql)
	if err != nil {
		t.Fatal(err)
	}
	got, err = publicConnectionURL(dbx.DriverMySQL, mysql, info, "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if want := "mysql://jd:s3cret@203.0.113.9:3306/app"; got != want {
		t.Errorf("MySQL public URL = %q, want %q", got, want)
	}

	if _, err := publicConnectionURL(dbx.DriverPostgres, dsn, info, ""); err == nil {
		t.Error("a host with no public address must refuse rather than hand back loopback")
	}
	if _, err := publicConnectionURL(dbx.DriverSQLite, "/data/app.db", &dbx.ConnInfo{Database: "/data/app.db"}, "203.0.113.9"); err == nil {
		t.Error("a SQLite file has no public form")
	}
}

func TestPreferredPublicAddressIsIPv4WhereThereIsOne(t *testing.T) {
	if got := preferredPublicAddress([]string{"2001:db8::9", "203.0.113.9"}); got != "203.0.113.9" {
		t.Errorf("got %q, want the IPv4 address", got)
	}
	if got := preferredPublicAddress([]string{"2001:db8::9"}); got != "2001:db8::9" {
		t.Errorf("got %q, want the only address there is", got)
	}
	if got := preferredPublicAddress(nil); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

// A connection to a server somewhere else is nobody's to publish from here,
// and the sync would never re-add it — so forgetting it is a real act, and
// the page keeps the button for it.
func TestAccessOfARemoteServerIsRemoteAndNotDetected(t *testing.T) {
	s := testServer(t)
	conn := &dbConnection{ID: 1, Name: "prod", Driver: dbx.DriverPostgres}
	got := s.describeDBAccess(context.Background(), conn, "postgres://app:pw@db.example.com:5432/shop?sslmode=require")
	if got.Exposure != exposureRemote || got.Detected || got.Managed || got.Container != "" {
		t.Errorf("remote server described as %+v", got)
	}
	if got.PublicAddresses == nil {
		t.Error("publicAddresses must be a list, never null, so the page can read its length")
	}
}

// The access change is refused, with a reason, where no container on this
// host is behind the connection — before any confirmation, since a binding is
// changed back the same way it was changed.
func TestAccessChangeNeedsAContainer(t *testing.T) {
	_, router := dbTestRouter(t)
	req := httptest.NewRequest(http.MethodPut, "/databases/1/access", strings.NewReader(`{"exposure":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no container") {
		t.Fatalf("access change with no container = %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	req = httptest.NewRequest(http.MethodPut, "/databases/1/access", strings.NewReader(`{"exposure":"everyone"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown exposure = %d, want 400", rec.Code)
	}
}

// Deleting with the container asks for the same phrase as dropping, and is
// then refused where no container is behind the connection rather than
// falling through to a DROP the operator did not ask for.
func TestRemovingTheContainerIsConfirmedThenNeedsAContainer(t *testing.T) {
	_, router := dbTestRouter(t)
	rec := driveWithoutConfirmation(t, router, confirmCase{
		http.MethodDelete, "/databases/1/database", `{"removeContainer":true}`, "",
	})
	if !strings.Contains(rec.Body.String(), "confirmation") {
		t.Fatalf("container removal ran without a phrase: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	req := httptest.NewRequest(http.MethodDelete, "/databases/1/database", strings.NewReader(`{"removeContainer":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Confirm", "never-opened.db")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no container") {
		t.Fatalf("container removal with no container = %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
