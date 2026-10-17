package dbx

import (
	"testing"
	"time"
)

func TestVersionEndOfLife(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		driver  Driver
		flavor  string
		version string
		release string
		past    bool
		known   bool
	}{
		{DriverPostgres, FlavorPostgres, "PostgreSQL 13.14 on x86_64-pc-linux-gnu", "13", true, true},
		{DriverPostgres, FlavorPostgres, "PostgreSQL 14.11 (Debian 14.11-1)", "14", false, true},
		{DriverPostgres, FlavorPostgres, "PostgreSQL 16.15 on x86_64-pc-linux-musl", "16", false, true},
		{DriverPostgres, FlavorPostgres, "PostgreSQL 18.6", "18", false, true},
		{DriverPostgres, FlavorPostgres, "PostgreSQL 9.6.24 on x86_64", "9.6", true, true},
		{DriverPostgres, FlavorPostgres, "PostgreSQL 99.1", "", false, false},
		// TimescaleDB is PostgreSQL with an extension: the server's own
		// release is what stops being maintained.
		{DriverPostgres, FlavorTimescaleDB, "PostgreSQL 17.11 on x86_64-pc-linux-musl", "17", false, true},
		{DriverMySQL, FlavorMySQL, "8.0.36-0ubuntu0.22.04.1", "8.0", true, true},
		{DriverMySQL, FlavorMySQL, "8.4.11", "8.4", false, true},
		{DriverMySQL, FlavorMySQL, "9.7.2", "9.7", false, true},
		{DriverMySQL, FlavorMySQL, "9.4.0", "9.4", true, true},
		{DriverMySQL, FlavorMySQL, "5.7.44-log", "5.7", true, true},
		{DriverMySQL, FlavorMariaDB, "11.8.9-MariaDB-ubu2404", "11.8", false, true},
		{DriverMySQL, FlavorMariaDB, "12.3.3-MariaDB-ubu2404", "12.3", false, true},
		{DriverMySQL, FlavorMariaDB, "10.5.23-MariaDB", "10.5", true, true},
		{DriverMySQL, FlavorMariaDB, "10.11.6-MariaDB-0+deb12u1", "10.11", false, true},
		{DriverMongo, FlavorMongoDB, "7.0.12", "7.0", false, true},
		{DriverMongo, FlavorMongoDB, "6.0.29", "6.0", true, true},
		{DriverMongo, FlavorMongoDB, "8.0.34", "8.0", false, true},
		{DriverMongo, FlavorMongoDB, "2.4.14", "", false, false},
		{DriverMSSQL, FlavorSQLServer, "Microsoft SQL Server 2016 (SP3) - 13.0.6300.2 (X64)", "2016", true, true},
		{DriverMSSQL, FlavorSQLServer, "Microsoft SQL Server 2022 (RTM-CU12) - 16.0.4115.5", "2022", false, true},
		{DriverOracle, FlavorOracle, "Oracle Database 19c Enterprise Edition Release 19.0.0.0.0 - Production", "19", false, true},
		{DriverOracle, FlavorOracle, "Oracle Database 11g Enterprise Edition Release 11.2.0.4.0", "11", true, true},
		{DriverOracle, FlavorOracle, "Oracle Database 23ai Free Release 23.0.0.0.0", "23", false, true},
		{DriverClickHouse, FlavorClickHouse, "ClickHouse 24.8.14.39", "24.8", true, true},
		{DriverClickHouse, FlavorClickHouse, "25.8.33.6", "25.8", true, true},
		{DriverClickHouse, FlavorClickHouse, "ClickHouse 26.8.1.1", "26.8", false, true},
		// A release that is not a long-term one is maintained until three
		// newer ones are out, which no table can say ahead of the day.
		{DriverClickHouse, FlavorClickHouse, "26.9.20.23", "", false, false},
		{DriverSQLite, FlavorSQLite, "SQLite 3.46.0", "", false, false},
		{DriverRedis, FlavorRedis, "7.4.1", "", false, false},
		{DriverMySQL, FlavorMySQL, "something else", "", false, false},
		// The release is read where the engine's own number stands. A beta has
		// no minor, and the first pair of numbers in its banner is the
		// compiler's.
		{DriverPostgres, FlavorPostgres, "PostgreSQL 18beta1 on x86_64-pc-linux-gnu, compiled by gcc (Debian 12.2.0-14) 12.2.0, 64-bit", "18", false, true},
		{DriverPostgres, FlavorPostgres, "PostgreSQL 17rc1 on aarch64-unknown-linux-gnu, compiled by gcc (GCC) 11.4.1", "17", false, true},
		{DriverPostgres, FlavorPostgres, "EnterpriseDB 12.2.0 compiled by gcc 12.2", "", false, false},
		// A fork answers with the version of the engine it is compatible with,
		// which says nothing about the fork's own maintenance.
		{DriverPostgres, FlavorPostgres, "PostgreSQL 11.2-YB-2.20.1.3-b0 on x86_64-pc-linux-gnu", "", false, false},
		{DriverPostgres, FlavorCockroachDB, "CockroachDB CCL v23.2.4 (x86_64-pc-linux-gnu, built 2024/04/08)", "", false, false},
		{DriverPostgres, FlavorPostgres, "PostgreSQL 12.12 (Greenplum Database 7.0.0 build commit:abc) on x86_64", "", false, false},
		{DriverMySQL, FlavorTiDB, "8.0.11-TiDB-v7.5.0", "", false, false},
		{DriverMySQL, FlavorMySQL, "5.7.25-TiDB-v6.1.0", "", false, false},
		{DriverMySQL, FlavorMySQL, "8.0.30-Vitess", "", false, false},
		// FerretDB answers buildInfo with the MongoDB release it emulates, and
		// only its flavour says so.
		{DriverMongo, FlavorFerretDB, "7.0.77", "", false, false},
		// MariaDB's replication-compatible prefix is not its version.
		{DriverMySQL, FlavorMariaDB, "5.5.5-10.6.12-MariaDB-log", "10.6", true, true},
	} {
		got, ok := VersionEndOfLife(c.driver, c.flavor, c.version, now)
		if ok != c.known {
			t.Errorf("%s %q: known %v, want %v", c.driver, c.version, ok, c.known)
			continue
		}
		if !ok {
			continue
		}
		if got.Release != c.release || got.Past != c.past {
			t.Errorf("%s %q: release %q past %v (date %s), want %q %v", c.driver, c.version, got.Release, got.Past, got.Date.Format("2006-01-02"), c.release, c.past)
		}
		if got.Past != (got.DaysLeft < 0) && got.DaysLeft != 0 {
			t.Errorf("%s %q: past %v with %d days left", c.driver, c.version, got.Past, got.DaysLeft)
		}
	}
}

// A release named by the catalogue is the same line a server of it reports,
// and gets the same date.
func TestReleaseEndOfLifeAgreesWithTheServersBanner(t *testing.T) {
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		flavor, release string
		driver          Driver
		banner          string
		date            string
	}{
		{FlavorPostgres, "18", DriverPostgres, "PostgreSQL 18.6 on x86_64-pc-linux-musl", "2030-11-14"},
		{FlavorTimescaleDB, "16", DriverPostgres, "PostgreSQL 16.15", "2028-11-09"},
		{FlavorMySQL, "8.0", DriverMySQL, "8.0.46", "2026-04-30"},
		{FlavorMariaDB, "11.8", DriverMySQL, "11.8.9-MariaDB-ubu2404", "2028-06-04"},
		{FlavorMongoDB, "7.0", DriverMongo, "7.0.45", "2027-08-31"},
		{FlavorClickHouse, "26.3", DriverClickHouse, "26.3.53.4", "2027-03-26"},
	} {
		named, ok := ReleaseEndOfLife(c.flavor, c.release, now)
		if !ok || named.Date.Format("2006-01-02") != c.date {
			t.Errorf("%s %s = %+v, %v; want %s", c.flavor, c.release, named, ok, c.date)
			continue
		}
		read, ok := VersionEndOfLife(c.driver, c.flavor, c.banner, now)
		if !ok || *read != *named {
			t.Errorf("%s %s: the server's banner reads as %+v, the catalogue's name as %+v", c.flavor, c.release, read, named)
		}
	}
	for _, c := range []struct{ flavor, release string }{
		{FlavorRedis, "8"}, {FlavorValkey, "8"}, {FlavorKeyDB, "latest"}, {FlavorPostgres, "latest"},
		{FlavorCockroachDB, "23.2"}, {FlavorFerretDB, "2.1"}, {FlavorClickHouse, "26.9"},
	} {
		if got, ok := ReleaseEndOfLife(c.flavor, c.release, now); ok {
			t.Errorf("%s %s has a date it was never given: %+v", c.flavor, c.release, got)
		}
	}
	// The day itself is still maintained; the day after is not.
	last := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	if on, _ := ReleaseEndOfLife(FlavorMySQL, "8.0", last); on.Past {
		t.Errorf("on its last day 8.0 reads as past: %+v", on)
	}
	if after, _ := ReleaseEndOfLife(FlavorMySQL, "8.0", last.Add(24*time.Hour)); !after.Past || after.DaysLeft != -1 {
		t.Errorf("the day after its last 8.0 reads as %+v", after)
	}
}
