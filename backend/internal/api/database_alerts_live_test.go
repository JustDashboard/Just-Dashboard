package api

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The recorder's read of real servers, as the alerts judge it. Every rule
// here watches every database, so each server is read for everything its
// engine can be watched for; nothing is written to any of them.
func TestLiveDatabaseAlertObservations(t *testing.T) {
	for _, c := range []struct {
		driver dbx.Driver
		envs   []string
		// engineDisk: the engine reports its own disk, wherever it runs.
		engineDisk bool
	}{
		{dbx.DriverPostgres, []string{"JD_TEST_POSTGRES_DSN"}, false},
		{dbx.DriverMySQL, []string{"JD_TEST_MYSQL8_DSN"}, false},
		{dbx.DriverMySQL, []string{"JD_TEST_MARIADB_DSN"}, false},
		{dbx.DriverClickHouse, []string{"JD_TEST_CLICKHOUSE_DSN"}, true},
		{dbx.DriverMongo, []string{"JD_TEST_MONGO_RS_DSN", "JD_TEST_MONGO_DSN"}, true},
		{dbx.DriverRedis, []string{"JD_TEST_REDIS_DSN"}, false},
		{dbx.DriverMSSQL, []string{"JD_TEST_MSSQL_DSN"}, true},
		{dbx.DriverOracle, []string{"JD_TEST_ORACLE_ADMIN_DSN"}, false},
	} {
		t.Run(c.envs[0], func(t *testing.T) {
			dsn := opsLiveAnyDSN(t, c.envs...)
			s := testServer(t)
			// Where the Docker socket answers, a server in a container is
			// followed to its volume, as in an install; the measuring itself
			// needs the root the dashboard runs as.
			docker := dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
			if _, err := docker.ListContainers(context.Background(), false); err == nil {
				s.modules.docker = docker
			}
			id := saveOpsConnection(t, s, "jdt_alerts_live", c.driver, dsn)
			a := s.modules.dbAlerts
			ctx := context.Background()
			for _, kind := range []string{dbx.AlertDown, dbx.AlertDisk, dbx.AlertConnections, dbx.AlertReplication} {
				if _, err := a.CreateRule(ctx, dbAlertWrite{Kind: kind}, nil); err != nil {
					t.Fatal(err)
				}
			}
			rules, err := a.Rules(ctx, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
			if err != nil {
				t.Fatal(err)
			}
			pass, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			// A fixture that is named and stopped skips, as every live test
			// does; one that answers here must answer the recorder too.
			conn, dsn, err := s.openDBConn(rec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.dialConnection(pass, conn, dsn); err != nil {
				t.Skipf("%s unreachable: %v", c.envs[0], err)
			}
			obs := s.recordDatabaseMetric(pass, rec, rules)
			if !obs.Answered || obs.Error != "" || obs.Unread != "" {
				t.Fatalf("the server did not answer the recorder: %+v", obs)
			}
			if obs.Sessions == nil || obs.Sessions.Limit <= 0 || obs.Sessions.Held <= 0 || obs.Sessions.Held > obs.Sessions.Limit {
				t.Fatalf("connections against the limit: %+v", obs.Sessions)
			}
			kinds := dbx.AlertKinds(c.driver, obs.Conn.Flavor)
			if replicates := slices.Contains(kinds, dbx.AlertReplication); replicates != (obs.Replication != nil) || obs.ReplicationUnknown != "" ||
				(replicates && obs.Replication.Stopped != "") {
				t.Fatalf("replication of a fixture that replicates nothing: %+v, %q", obs.Replication, obs.ReplicationUnknown)
			}
			switch {
			case s.modules.docker != nil && !c.engineDisk && obs.Disk == nil &&
				!strings.Contains(obs.DiskUnknown, "the volume ") && !strings.Contains(obs.DiskUnknown, "Docker's disk"):
				t.Fatalf("a server in a container was not followed to its volume: %q", obs.DiskUnknown)
			case c.engineDisk && (obs.Disk == nil || obs.Disk.Total <= 0):
				t.Fatalf("the engine's own disk: %+v, %q", obs.Disk, obs.DiskUnknown)
			case obs.Disk == nil && obs.DiskUnknown == "":
				t.Fatal("the disk was neither read nor said to be unreadable")
			case obs.Disk != nil && (obs.Disk.Used <= 0 || obs.Disk.Used > obs.Disk.Total || obs.Disk.Where == ""):
				t.Fatalf("disk: %+v", obs.Disk)
			}
			t.Logf("disk: %+v %s", obs.Disk, obs.DiskUnknown)
			// What a pass makes of it: a reading for every rule, and none of
			// them a reason to tell anybody.
			conns := []dbAlertConn{obs.Conn}
			a.Evaluate(ctx, conns, map[int64]dbAlertObservation{id: obs})
			subjects, err := a.Subjects(ctx, &id)
			if err != nil || len(subjects) != len(kinds)-2 {
				t.Fatalf("subjects: %+v, %v", subjects, err)
			}
			for _, subject := range subjects {
				if subject.State == "firing" || subject.State == "pending" {
					t.Fatalf("a healthy fixture is %s: %+v", subject.State, subject)
				}
			}
		})
	}
}
