package dbx

import (
	"fmt"
	"strings"
	"testing"
)

func TestAdvisorReportsUnassessedTablesAndNativeTimestamp(t *testing.T) {
	db, _ := openTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxAdvisedTables+1; i++ {
		if _, err := tx.Exec(fmt.Sprintf("CREATE TABLE advisor_%03d (id INTEGER PRIMARY KEY)", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	report, err := Advise(t.Context(), db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.TablesChecked != maxAdvisedTables || report.TablesOmitted != 3 || len(report.Silences) != 1 || !strings.Contains(report.Silences[0], "not assessed") || report.CheckedAt.IsZero() {
		t.Fatalf("partial report: %+v", report)
	}
}
