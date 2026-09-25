package api

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// The host account path against a real native PostgreSQL, run as root on a
// machine that has one:
//
//	JD_TEST_HOST_PG_PORT=5432 sudo -E go test ./internal/api -run TestLiveHostPostgresAccount -v
//
// It makes (or resets) a throwaway role over the Unix socket as the host's
// postgres account, proves the generated password works over TCP, and drops
// the role again. Skipped, never failed, where there is no server or no root.
func TestLiveHostPostgresAccount(t *testing.T) {
	port, _ := strconv.Atoi(os.Getenv("JD_TEST_HOST_PG_PORT"))
	if port == 0 {
		t.Skip("JD_TEST_HOST_PG_PORT not set")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to switch to the postgres account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	password, err := generatePassword()
	if err != nil {
		t.Fatal(err)
	}
	const role = "jd_live_test_role"
	if _, err := hostPostgresAccount(ctx, port, role, password, true); err != nil {
		t.Fatalf("make account: %v", err)
	}
	// Twice: the second run is the reset path, which must not fail on an
	// account that already exists.
	if _, err := hostPostgresAccount(ctx, port, role, password, false); err != nil {
		t.Fatalf("reset account: %v", err)
	}
	dsn := dbx.BuildDSN(dbx.Candidate{
		Driver: dbx.DriverPostgres, Host: "127.0.0.1", Port: port, User: role, Database: "postgres",
	}, password)
	version, err := dbx.Probe(ctx, dbx.DriverPostgres, dsn)
	if err != nil {
		t.Fatalf("the account was made but TCP sign-in failed: %v", err)
	}
	t.Logf("signed in over TCP as %s: %s", role, version)

	pg, err := hostAccount("postgres")
	if err != nil {
		t.Fatal(err)
	}
	_ = pg
	cleanup, err := hostPostgresDrop(ctx, port, role)
	if err != nil {
		t.Fatalf("drop: %v (%s)", err, cleanup)
	}
}
