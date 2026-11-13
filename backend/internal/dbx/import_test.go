package dbx

import (
	"context"
	"strings"
	"testing"
)

func TestImportCSVRoundTrip(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	csv := "id,email,name\n10,j@x.io,Jo\n11,k@x.io,\n"
	res, err := ImportCSV(ctx, db, DriverSQLite, strings.NewReader(csv), ImportOptions{
		Table: "users", HasHeader: true,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Inserted != 2 || res.Failed != 0 {
		t.Fatalf("inserted=%d failed=%d errors=%v", res.Inserted, res.Failed, res.Errors)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("user count = %d, want 4", n)
	}
}

func TestImportJSONRoundTrip(t *testing.T) {
	db, _ := openTestDB(t)
	res, err := ImportJSON(context.Background(), db, DriverSQLite,
		strings.NewReader(`[{"id":20,"email":"a1@x.io","name":"A"},{"id":21,"email":"a2@x.io"}]`),
		ImportOptions{Table: "users"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Inserted != 2 {
		t.Fatalf("inserted=%d errors=%v", res.Inserted, res.Errors)
	}
}

func TestImportRollsBackOnStopOnError(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	// The second row duplicates a primary key. With StopOnError the whole load
	// must roll back — a half-applied import is the failure mode this guards.
	csv := "id,email,name\n30,new@x.io,New\n1,dupe@x.io,Dupe\n"
	if _, err := ImportCSV(ctx, db, DriverSQLite, strings.NewReader(csv), ImportOptions{
		Table: "users", HasHeader: true, StopOnError: true,
	}); err == nil {
		t.Fatal("expected the duplicate key to fail the import")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 30`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("row from a failed import survived: found %d", n)
	}
}

func TestImportSkipsBadRowsWhenAsked(t *testing.T) {
	db, _ := openTestDB(t)
	csv := "id,email,name\n40,ok@x.io,Ok\n1,dupe@x.io,Dupe\n41,ok2@x.io,Ok2\n"
	res, err := ImportCSV(context.Background(), db, DriverSQLite, strings.NewReader(csv), ImportOptions{
		Table: "users", HasHeader: true,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Inserted != 2 || res.Failed != 1 {
		t.Errorf("inserted=%d failed=%d, want 2/1 (%v)", res.Inserted, res.Failed, res.Errors)
	}
}

func TestImportTruncateReplaces(t *testing.T) {
	db, _ := openTestDB(t)
	// posts is referenced by nothing, so emptying it is allowed.
	res, err := ImportCSV(context.Background(), db, DriverSQLite,
		strings.NewReader("id,title,author_id\n99,Only,1\n"),
		ImportOptions{Table: "posts", HasHeader: true, Truncate: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Inserted != 1 {
		t.Fatalf("inserted = %d", res.Inserted)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("after truncate+import count = %d, want 1", n)
	}
}

func TestImportTruncateBlockedByForeignKeyChangesNothing(t *testing.T) {
	db, _ := openTestDB(t)
	// users is referenced by posts, so the engine refuses to empty it. The
	// whole import must roll back rather than leave the table emptied, or the
	// new rows inserted, or any mixture of the two.
	if _, err := ImportCSV(context.Background(), db, DriverSQLite,
		strings.NewReader("id,email,name\n99,only@x.io,Only\n"),
		ImportOptions{Table: "users", HasHeader: true, Truncate: true}); err == nil {
		t.Fatal("expected the referenced-table truncate to be refused")
	}
	var users, posts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&posts); err != nil {
		t.Fatal(err)
	}
	if users != 2 || posts != 2 {
		t.Errorf("failed import left users=%d posts=%d, want 2/2", users, posts)
	}
}

func TestImportNullAs(t *testing.T) {
	db, _ := openTestDB(t)
	if _, err := ImportCSV(context.Background(), db, DriverSQLite,
		strings.NewReader("id,email,name\n50,n@x.io,\\N\n"),
		ImportOptions{Table: "users", HasHeader: true, NullAs: `\N`}); err != nil {
		t.Fatalf("import: %v", err)
	}
	var name *string
	if err := db.QueryRow(`SELECT name FROM users WHERE id = 50`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != nil {
		t.Errorf("expected NULL name, got %q", *name)
	}
}
