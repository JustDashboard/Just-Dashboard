package dbx

import (
	"strings"
	"testing"
)

func TestReplicationConninfoIsRebuiltFromTheParts(t *testing.T) {
	for name, c := range map[string]struct{ dsn, want string }{
		"url": {
			"postgres://reader:s3cret@db.example.com:6543/shop?sslmode=require",
			"host='db.example.com' port='6543' dbname='shop' user='reader' password='s3cret' sslmode='require'",
		},
		"keyword string": {
			"host=db.example.com port=5432 user=reader password='s e c' dbname=shop sslmode=verify-full",
			"host='db.example.com' port='5432' dbname='shop' user='reader' password='s e c' sslmode='verify-full'",
		},
		"no password and no ssl mode": {
			"postgres://reader@db.example.com/shop",
			"host='db.example.com' port='5432' dbname='shop' user='reader'",
		},
	} {
		got, err := ReplicationConninfo(c.dsn)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, c.want)
		}
	}
}

// The server that dials the string is the target, and a connection string can
// make it read files or run a command.
func TestReplicationConninfoDropsWhatMakesTheTargetReadFiles(t *testing.T) {
	got, err := ReplicationConninfo("postgres://reader:pw@db.example.com/shop?sslmode=require&sslrootcert=/etc/shadow&sslkey=/root/key&passfile=/root/.pgpass&service=other&options=-c%20log_statement=all")
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"sslrootcert", "sslkey", "passfile", "service", "options", "/etc/shadow"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the conninfo carries %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "sslmode='require'") {
		t.Errorf("the ssl mode was dropped: %s", got)
	}
}

func TestReplicationConninfoQuotesWhatLibpqReadsSpecially(t *testing.T) {
	got, err := ReplicationConninfo(`postgres://reader:it%27s%5Cthe%20one@db.example.com/shop`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `password='it\'s\\the one'`; !strings.Contains(got, want) {
		t.Errorf("password is not quoted for libpq: %s", got)
	}
	if _, err := ReplicationConninfo("postgres://db.example.com/shop"); err == nil {
		t.Error("a string with no user was accepted")
	}
	if _, err := ReplicationConninfo("postgres://reader@db.example.com"); err == nil {
		t.Error("a string with no database was accepted")
	}
}

func TestSSLModeIsOnlyOneLibpqKnows(t *testing.T) {
	for dsn, want := range map[string]string{
		"postgres://u@h/d?sslmode=require":     "require",
		"postgres://u@h/d?sslmode=bogus":       "",
		"postgres://u@h/d":                     "",
		"host=h user=u sslmode=verify-ca":      "verify-ca",
		"host=h user=u sslmode='disable'":      "disable",
		"host=h password='a b' sslmode=prefer": "prefer",
	} {
		if got := sslModeOf(dsn); got != want {
			t.Errorf("%s: %q, want %q", dsn, got, want)
		}
	}
}

func TestPublicationSQLQuotesEveryName(t *testing.T) {
	got := PublicationSQL("jd_import_shop", []ReplicationTable{
		{Schema: "public", Name: "orders"},
		{Schema: "My Schema", Name: `we"ird`},
	})
	want := `CREATE PUBLICATION "jd_import_shop" FOR TABLE "public"."orders", "My Schema"."we""ird";`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := PublicationSQL("p", nil); got != `CREATE PUBLICATION "p" FOR ALL TABLES;` {
		t.Errorf("no tables: %s", got)
	}
}

func TestImportNamesAreIdentifiersTheThreeObjectsShare(t *testing.T) {
	for name, ok := range map[string]bool{
		"jd_import_shop": true, "_x": true, "a": true,
		"": false, "Shop": false, "1shop": false, "shop-1": false, "shop; drop": false,
		strings.Repeat("a", 50): true, strings.Repeat("a", 51): false,
	} {
		if ImportNameRe.MatchString(name) != ok {
			t.Errorf("%q: accepted = %v, want %v", name, !ok, ok)
		}
	}
}

func TestOnlyEnginesThatRestoreUnderAnotherNameCanBeCopiedBetweenServers(t *testing.T) {
	for driver, want := range map[Driver]bool{
		DriverPostgres: true, DriverMySQL: true, DriverMSSQL: true, DriverMongo: true, DriverRedis: true,
		DriverClickHouse: false, DriverOracle: false, DriverSQLite: false,
	} {
		if CanCopyBetween(driver) != want {
			t.Errorf("%s: CanCopyBetween = %v, want %v", driver, !want, want)
		}
	}
}

func TestACopyTrackerCountsTablesAndRows(t *testing.T) {
	var seen []CopyProgress
	tracker := newCopyTracker(CopyOptions{Report: func(p CopyProgress) { seen = append(seen, p) }}, "copying")
	trace := tracker.trace()
	trace.planned([]Table{{Name: "a", Rows: 100}, {Name: "b", Rows: -1}, {Name: "c", Rows: 20}})
	trace.wrote("a", 90)
	trace.wrote("b", 5)
	last := seen[len(seen)-1]
	if last.Tables != 3 || last.TablesDone != 2 || last.Rows != 95 || last.Table != "b" {
		t.Errorf("progress = %+v", last)
	}
	// A table the planner never counted adds nothing to the estimate rather
	// than taking one off.
	if last.EstimatedRows != 120 {
		t.Errorf("estimated rows = %d, want 120", last.EstimatedRows)
	}
}

func TestACopyIsRefusedForAnEngineThatCannotRestoreUnderAnotherName(t *testing.T) {
	_, err := CopyBetween(t.Context(), DriverClickHouse, "clickhouse://a/b", "clickhouse://c/d", CopyOptions{TargetDatabase: "x"})
	if err == nil || !strings.Contains(err.Error(), "cannot be copied to another server") {
		t.Errorf("err = %v", err)
	}
	// Asking an engine to leave out what it cannot leave out is refused
	// before either server is dialled.
	_, err = CopyBetween(t.Context(), DriverMongo, "mongodb://a/b", "mongodb://c/d", CopyOptions{TargetDatabase: "x", SchemaOnly: true})
	if err == nil || !strings.Contains(err.Error(), "cannot leave the data out") {
		t.Errorf("err = %v", err)
	}
}
