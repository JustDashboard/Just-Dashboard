package dbx

import (
	"fmt"
	"strings"
	"testing"
)

// What the loader decides before it reads a column: which relations it reads
// at all, and which catalogue columns the server it is talking to has.

// The cap on one generation counts what is read. A table partitioned by day
// lists more than a thousand relations within three years and is one model,
// and a view the target will not emit costs nothing.
func TestORMReadPlanCountsWhatIsRead(t *testing.T) {
	tables := []Table{{Schema: "public", Name: "measurements", Type: "p"}}
	partition := map[string]bool{}
	for i := 0; i < 1100; i++ {
		name := fmt.Sprintf("measurements_%04d", i)
		tables = append(tables, Table{Schema: "public", Name: name, Type: "table"})
		partition[ormTableKey("public", name)] = true
	}
	tables = append(tables,
		Table{Schema: "public", Name: "sensors", Type: "table"},
		Table{Schema: "public", Name: "latest", Type: "view"},
		Table{Schema: "public", Name: "hourly", Type: "materialized view"},
		Table{Schema: "public", Name: "ids", Type: "sequence"},
	)

	read, unread := ormReadPlan(tables, partition, true)
	if len(read) != 2 || read[0].Name != "measurements" || read[1].Name != "sensors" {
		t.Errorf("read %d relations, want the parent and the one plain table", len(read))
	}
	kinds := map[ORMTableKind]int{}
	for _, u := range unread {
		kinds[u.Kind]++
		if len(u.Columns) != 0 {
			t.Errorf("%s was not read and has columns", u.Name)
		}
	}
	if kinds[ORMKindPartition] != 1100 || kinds[ORMKindView] != 1 || kinds[ORMKindMatView] != 1 || len(unread) != 1102 {
		t.Errorf("unread = %v", kinds)
	}
	if read, _ := ormReadPlan(tables, partition, false); len(read) != 4 {
		t.Errorf("with views wanted, read %d relations, want 4", len(read))
	}

	// What was left unread is still what the generator reports.
	schema := &ORMSchema{Driver: DriverPostgres, Detailed: true, Tables: unread}
	schema.Tables = append(schema.Tables, ORMTable{
		Schema: "public", Name: "measurements", Kind: ORMKindPartitioned,
		Columns: []ORMColumn{ormColumn("id", "integer")}, PrimaryKey: []string{"id"},
	})
	res := ormGenerate(t, schema, ORMRequest{Target: ORMPrisma})
	ormMustContain(t, "warnings", strings.Join(res.Warnings, "\n"), "1100 partitions were left out")
	if res.Counts.Tables != 1 || res.Counts.Views != 0 {
		t.Errorf("counts = %+v", res.Counts)
	}
	_, err := GenerateORMFiles(&ORMSchema{Driver: DriverPostgres, Tables: unread[1100:]}, ORMOptions{Target: ORMPrisma})
	if err == nil || !strings.Contains(err.Error(), "only 2 view(s); turn on views to include them") {
		t.Errorf("a schema of unread views answered %v", err)
	}

	// The request decides whether views are read: only when they will be written.
	for _, c := range []struct {
		req  ORMRequest
		skip bool
	}{
		{ORMRequest{Target: ORMPrisma}, true},
		{ORMRequest{Target: ORMPrisma, Views: ormYes()}, false},
		{ORMRequest{Target: ORMTypeScript}, false},
		{ORMRequest{Target: ORMTypeScript, Views: ormNo()}, true},
		{ORMRequest{Target: ORMDjango}, true},
	} {
		scope, err := c.req.Scope(DriverPostgres, "")
		if err != nil || scope.SkipViews != c.skip {
			t.Errorf("%s views=%v: SkipViews = %v (%v), want %v", c.req.Target, c.req.Views, scope.SkipViews, err, c.skip)
		}
	}
}

// One catalogue column the server does not have fails the whole query it is
// in, so each query names only what the server's version has.
func TestPostgresCatalogForOlderServers(t *testing.T) {
	current := postgresCatalog{
		partition: "c.relispartition", partKey: "pg_get_partkeydef(c.oid)",
		identity: "a.attidentity::text", generated: "a.attgenerated::text", keyColumns: "ix.indnkeyatts",
	}
	v11, v10, v96 := current, current, current
	v11.generated = "''::text"
	v10.generated, v10.keyColumns = "''::text", "ix.indnatts"
	v96 = v10
	v96.partition, v96.partKey, v96.identity = "false", "''::text", "''::text"
	for version, want := range map[int]postgresCatalog{
		0: current, 160004: current, 120000: current,
		110022: v11, 100023: v10, 90624: v96,
	} {
		if got := postgresCatalogFor(version); got != want {
			t.Errorf("version %d: %+v, want %+v", version, got, want)
		}
	}
}
