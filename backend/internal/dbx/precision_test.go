package dbx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestSQLValuePreservesExactNumbers(t *testing.T) {
	for _, number := range []string{
		"9007199254740993", "-9223372036854775808", "18446744073709551615",
		"12345678901234567890.12345678901234567890", "1e10000", "1e-10000", "-0.00",
	} {
		t.Run(number, func(t *testing.T) {
			got, err := SQLValue(json.Number(number))
			if err != nil || got != number {
				t.Fatalf("SQLValue = (%#v, %v), want exact %q", got, err, number)
			}
		})
	}
	got, err := SQLValue(uint64(math.MaxUint64))
	if err != nil || got != "18446744073709551615" {
		t.Fatalf("SQLValue uint64 = (%#v, %v)", got, err)
	}
	text := "a text column need not contain a number"
	if got, err := SQLValue(text); err != nil || got != text {
		t.Fatalf("SQLValue text = (%#v, %v)", got, err)
	}
}

func TestSQLValueRejectsInvalidOrAlreadyLossyNumbers(t *testing.T) {
	for _, invalid := range []any{
		json.Number(""), json.Number("01"), json.Number("+1"), json.Number("NaN"),
		json.Number("Infinity"), json.Number("1; DROP TABLE users"), json.Number("1e"),
		math.NaN(), math.Inf(1), float32(math.Inf(-1)), float64(9007199254740992),
	} {
		if _, err := SQLValue(invalid); err == nil {
			t.Errorf("accepted %#v (%T)", invalid, invalid)
		}
	}
	if got, err := SQLValue(1.25); err != nil || got != 1.25 {
		t.Fatalf("ordinary float = (%#v, %v)", got, err)
	}
}

func TestNormaliseExactDriverValuesIncludingContainers(t *testing.T) {
	integer, ok := new(big.Int).SetString("340282366920938463463374607431768211455", 10)
	if !ok {
		t.Fatal("invalid fixture")
	}
	amount := decimal.RequireFromString("12345678901234567890.1234567890123456789")
	for _, tc := range []struct {
		name  string
		value any
		want  any
	}{
		{"signed", int64(math.MinInt64), "-9223372036854775808"},
		{"unsigned", uint64(math.MaxUint64), "18446744073709551615"},
		{"mysql and mssql decimal bytes", []byte("12345678901234567890.123456789"), "12345678901234567890.123456789"},
		{"postgres and oracle decimal text", "12345678901234567890.123456789", "12345678901234567890.123456789"},
		{"clickhouse decimal", amount, amount.String()},
		{"clickhouse wide integer", integer, integer.String()},
		{"wide integer value", *integer, integer.String()},
		{"integer array", []uint64{math.MaxUint64}, []any{"18446744073709551615"}},
		{"integer map", map[string]int64{"id": 9007199254740993}, map[string]any{"id": "9007199254740993"}},
		{"nested array", [][]uint64{{math.MaxUint64}}, []any{[]any{"18446744073709551615"}}},
		{"integer map key", map[uint64]int64{math.MaxUint64: 9007199254740993}, map[string]any{"18446744073709551615": "9007199254740993"}},
		{"nonfinite float", math.Inf(1), "+Inf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normaliseValue(tc.value)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normaliseValue = %#v, want %#v", got, tc.want)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Fatalf("wire value cannot be encoded: %v", err)
			}
		})
	}
}

func TestSQLiteExactNumericMutationBrowseAndExport(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	// SQLite INTEGER really stores int64. TEXT intentionally retains the
	// decimal: SQLite NUMERIC affinity can itself store an inexact REAL.
	if _, err := db.Exec(`CREATE TABLE exact_values (id INTEGER PRIMARY KEY, amount TEXT, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	const id = "9007199254740993"
	const neighbor = "9007199254740992"
	const amount = "12345678901234567890.12345678901234567890"
	for _, key := range []string{neighbor, id} {
		if _, err := InsertRow(ctx, db, DriverSQLite, "", "exact_values", map[string]any{
			"id": json.Number(key), "amount": json.Number(amount), "note": "original",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := UpdateRow(ctx, db, DriverSQLite, "", "exact_values",
		map[string]any{"note": "updated"}, map[string]any{"id": json.Number(id)}); err != nil {
		t.Fatal(err)
	}
	result, err := Browse(ctx, db, DriverSQLite, BrowseOptions{Table: "exact_values", OrderBy: "id", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{neighbor, amount, "original"}, {id, amount, "updated"}}
	if !reflect.DeepEqual(result.Rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.Rows, want)
	}
	if count, err := Count(ctx, db, DriverSQLite, BrowseOptions{Table: "exact_values"}); err != nil || count != 2 {
		t.Fatalf("Count = (%d, %v)", count, err)
	}
	for _, format := range []ExportFormat{ExportJSON, ExportCSV} {
		var out bytes.Buffer
		count, truncated, err := StreamExport(ctx, db, `SELECT id, amount, note FROM exact_values WHERE id = ?`,
			[]any{json.Number(id)}, format, &out, 10)
		if err != nil || count != 1 || truncated {
			t.Fatalf("export %s = (%d, %v, %v)", format, count, truncated, err)
		}
		if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), amount) || strings.Contains(out.String(), neighbor) {
			t.Fatalf("export %s lost precision or selected neighbor: %s", format, &out)
		}
		if format == ExportJSON {
			var rows []map[string]any
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil || rows[0]["id"] != id || rows[0]["amount"] != amount {
				t.Fatalf("JSON export must encode exact strings: %s (%v)", &out, err)
			}
		}
	}
	if _, err := DeleteRow(ctx, db, DriverSQLite, "", "exact_values", map[string]any{"id": json.Number(id)}); err != nil {
		t.Fatal(err)
	}
	remaining, err := RunQuery(ctx, db, `SELECT id FROM exact_values`, 10)
	if err != nil || !reflect.DeepEqual(remaining.Rows, [][]any{{neighbor}}) {
		t.Fatalf("delete removed wrong primary key: %#v (%v)", remaining, err)
	}
}

func TestRowInsertSQLExactNumbersAcrossDialects(t *testing.T) {
	const integer = "18446744073709551615"
	const amount = "12345678901234567890.12345678901234567890"
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverOracle, DriverClickHouse} {
		t.Run(string(driver), func(t *testing.T) {
			statement, err := RowInsertSQL(driver, "", "exact_values", map[string]any{
				"id": json.Number(integer), "amount": json.Number(amount), "text": amount,
			})
			if err != nil || !strings.Contains(statement, integer) || !strings.Contains(statement, amount) || !strings.Contains(statement, "'"+amount+"'") {
				t.Fatalf("rendered statement = %q (%v)", statement, err)
			}
			for _, invalid := range []any{json.Number("1); DROP TABLE exact_values; --"), math.Inf(1), float64(9007199254740992)} {
				if _, err := RowInsertSQL(driver, "", "exact_values", map[string]any{"id": invalid}); err == nil {
					t.Errorf("rendered unsafe numeric literal %#v", invalid)
				}
			}
		})
	}
}

func TestLiveExactNumericMutations(t *testing.T) {
	for _, fixture := range sqlFixtures() {
		if fixture.driver != DriverPostgres && fixture.driver != DriverMySQL {
			continue
		}
		t.Run(string(fixture.driver), func(t *testing.T) {
			db := liveSQL(t, fixture.driver, fixture.env, fixture.dsn)
			ctx := context.Background()
			// Independent go test processes must not share a mutable fixture.
			// rand.Text contains only letters and digits, safe in this identifier.
			table := "jd_exact_precision_" + strings.ToLower(rand.Text())
			if _, err := db.Exec("CREATE TABLE " + table + " (id BIGINT PRIMARY KEY, amount DECIMAL(40,20), note VARCHAR(40))"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = db.Exec("DROP TABLE " + table) })
			const id = "9007199254740993"
			const neighbor = "9007199254740992"
			const amount = "12345678901234567890.12345678901234567890"
			for _, key := range []string{neighbor, id} {
				if _, err := InsertRow(ctx, db, fixture.driver, fixture.schema, table, map[string]any{
					"id": json.Number(key), "amount": json.Number(amount), "note": "original",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := UpdateRow(ctx, db, fixture.driver, fixture.schema, table,
				map[string]any{"note": "updated", "amount": amount}, map[string]any{"id": id}); err != nil {
				t.Fatal(err)
			}
			result, err := Browse(ctx, db, fixture.driver, BrowseOptions{Schema: fixture.schema, Table: table, OrderBy: "id", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			want := [][]any{{neighbor, amount, "original"}, {id, amount, "updated"}}
			if !reflect.DeepEqual(result.Rows, want) {
				t.Fatalf("numeric CRUD = %#v, want %#v", result.Rows, want)
			}
			for _, format := range []ExportFormat{ExportJSON, ExportCSV} {
				var out bytes.Buffer
				count, truncated, err := ExportTable(ctx, db, fixture.driver, fixture.schema, table, format, &out, 10)
				if err != nil || count != 2 || truncated || !strings.Contains(out.String(), amount) || !strings.Contains(out.String(), id) {
					t.Fatalf("%s export = (%d, %v, %v): %s", format, count, truncated, err, &out)
				}
			}
			if _, err := DeleteRow(ctx, db, fixture.driver, fixture.schema, table, map[string]any{"id": json.Number(id)}); err != nil {
				t.Fatal(err)
			}
			remaining, err := RunQuery(ctx, db, "SELECT id FROM "+table, 10)
			if err != nil || !reflect.DeepEqual(remaining.Rows, [][]any{{neighbor}}) {
				t.Fatalf("delete removed wrong primary key: %#v (%v)", remaining, err)
			}
		})
	}
}
