package dbx

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var testAliases = map[string]relationRef{
	"o": {"public", "orders"},
	"c": {"public", "customers"},
}

func usesOf(condition string) []string {
	var out []string
	for _, u := range conditionUses(condition, testAliases) {
		out = append(out, u.relation.table+"."+u.column+":"+u.use)
	}
	return out
}

func TestConditionUses(t *testing.T) {
	cases := []struct {
		name, condition string
		want            []string
	}{
		{"an equality on a text column", `(o.status = 'open'::text)`, []string{"orders.status:equality"}},
		{"an and of an equality and a range", `((o.total > '100'::numeric) AND (o.status = 'open'::text))`,
			[]string{"orders.total:range", "orders.status:equality"}},
		{"a cast on the column side", `((o.status)::text = 'open'::text)`, []string{"orders.status:equality"}},
		{"a multi-word type is one cast", `(o.created_at >= '2026-01-01 00:00:00'::timestamp without time zone)`,
			[]string{"orders.created_at:range"}},
		{"a constant on the left", `('open'::text = o.status)`, []string{"orders.status:equality"}},
		{"in a list", `(o.status = ANY ('{open,paid}'::text[]))`, []string{"orders.status:equality"}},
		{"is null", `(o.shipped_at IS NULL)`, []string{"orders.shipped_at:equality"}},
		{"is not null says nothing", `(o.shipped_at IS NOT NULL)`, nil},
		{"a join", `(o.customer_id = c.id)`, []string{"orders.customer_id:join", "customers.id:join"}},
		{"a range between two tables is not a join an index serves", `(o.created_at < c.id)`, nil},
		{"a parameter", `(o.customer_id = $1)`, []string{"orders.customer_id:equality"}},
		{"a sub-plan", `(o.customer_id = (SubPlan 1))`, []string{"orders.customer_id:equality"}},
		{"an init plan", `(o.customer_id = (InitPlan 1).col1)`, []string{"orders.customer_id:equality"}},
		{"a function of constants", `(o.created_at > (now() - '7 days'::interval))`, []string{"orders.created_at:range"}},
		{"a function of the column is an expression", `(lower(o.status) = 'open'::text)`, nil},
		{"arithmetic on the column", `((o.total + 1) = 5)`, nil},
		{"a column against an expression of columns", `(o.total = (c.id + 1))`, nil},
		{"or is passed over", `((o.status = 'open'::text) OR (o.status = 'paid'::text))`, nil},
		{"not is passed over", `(NOT (o.status = 'open'::text))`, nil},
		{"not equal says nothing", `(o.status <> 'open'::text)`, nil},
		{"like says nothing", `(o.status ~~ 'op%'::text)`, nil},
		{"an alias that is not a table", `(x.status = 'open'::text)`, nil},
		{"a quoted column with a quote in it", `(o."Sta""tus" = 'open'::text)`, []string{`orders.Sta"tus:equality`}},
		{"a string holding an and", `(o.status = 'a AND b'::text)`, []string{"orders.status:equality"}},
		{"an unterminated string", `(o.status = 'open`, nil},
		{"an or inside one conjunct", `((o.total > 1) AND ((o.status = 'a'::text) OR (o.status = 'b'::text)))`,
			[]string{"orders.total:range"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usesOf(tc.condition); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("uses of %s = %v, want %v", tc.condition, got, tc.want)
			}
		})
	}
}

// A plan taken with VERBOSE from PostgreSQL 17, trimmed to the keys the reader
// looks at.
const joinPlan = `[{"Plan": {
  "Node Type": "Limit",
  "Plans": [{
    "Node Type": "Sort",
    "Sort Key": ["o.created_at DESC"],
    "Plans": [{
      "Node Type": "Hash Join",
      "Hash Cond": "(o.customer_id = c.id)",
      "Plans": [
        {"Node Type": "Seq Scan", "Relation Name": "orders", "Schema": "public", "Alias": "o",
         "Total Cost": 2236.00,
         "Filter": "((o.total > '100'::numeric) AND (o.status = 'open'::text))"},
        {"Node Type": "Hash", "Plans": [
          {"Node Type": "Seq Scan", "Relation Name": "customers", "Schema": "public", "Alias": "c",
           "Filter": "(c.region = 'eu'::text)"}]}
      ]}]}]}}]`

func decodePlan(t *testing.T, text string) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var plan any
	if err := dec.Decode(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlanColumnUsesAndCandidates(t *testing.T) {
	plan := decodePlan(t, joinPlan)
	var got []string
	for _, u := range planColumnUses(plan) {
		got = append(got, u.relation.table+"."+u.column+":"+u.use)
	}
	// The order is the walk's: the sort and the join are met before the scans.
	want := []string{
		"orders.created_at:sort",
		"orders.customer_id:join", "customers.id:join",
		"orders.total:range", "orders.status:equality",
		"customers.region:equality",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uses = %v\nwant %v", got, want)
	}

	candidates := candidatesFromUses(planColumnUses(plan))
	type short struct {
		table   string
		columns string
		uses    string
	}
	var have []short
	for _, c := range candidates {
		have = append(have, short{c.Table, strings.Join(c.Columns, ","), strings.Join(c.Uses, ",")})
	}
	expect := []short{
		{"orders", "status,total", "equality,range"},
		{"orders", "customer_id", "join"},
		{"customers", "region", "equality"},
		{"customers", "id", "join"},
	}
	if !reflect.DeepEqual(have, expect) {
		t.Errorf("candidates = %v\nwant %v", have, expect)
	}
}

func TestASortIsOnlyWorthAnIndexUnderALimit(t *testing.T) {
	plan := decodePlan(t, `[{"Plan": {"Node Type": "Sort", "Sort Key": ["o.created_at"],
	  "Plans": [{"Node Type": "Seq Scan", "Relation Name": "orders", "Schema": "public", "Alias": "o"}]}}]`)
	if uses := planColumnUses(plan); len(uses) != 0 {
		t.Errorf("a sort of everything proposed %v", uses)
	}
	limited := decodePlan(t, `[{"Plan": {"Node Type": "Limit", "Plans": [{"Node Type": "Sort",
	  "Sort Key": ["o.created_at DESC NULLS LAST", "o.id", "lower(o.name)", "o.total"],
	  "Plans": [{"Node Type": "Seq Scan", "Relation Name": "orders", "Schema": "public", "Alias": "o"}]}]}}]`)
	var got []string
	for _, u := range planColumnUses(limited) {
		got = append(got, u.column)
	}
	// The expression ends the run: columns after it cannot lead an index.
	if want := []string{"created_at", "id"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sort columns = %v, want %v", got, want)
	}
}

func TestAnAliasUsedForTwoRelationsIsNotResolved(t *testing.T) {
	plan := decodePlan(t, `[{"Plan": {"Node Type": "Append", "Plans": [
	  {"Node Type": "Seq Scan", "Relation Name": "a", "Schema": "public", "Alias": "t", "Filter": "(t.x = 1)"},
	  {"Node Type": "Seq Scan", "Relation Name": "b", "Schema": "public", "Alias": "t", "Filter": "(t.y = 1)"}]}}]`)
	if uses := planColumnUses(plan); len(uses) != 0 {
		t.Errorf("an ambiguous alias proposed %v", uses)
	}
}

func TestACandidateIsLimitedToThreeColumnsAndNeverRepeatsOne(t *testing.T) {
	var uses []columnUse
	ref := relationRef{"public", "t"}
	for _, c := range []string{"a", "b", "a", "c", "d"} {
		uses = append(uses, columnUse{ref, c, UseEquality})
	}
	got := candidatesFromUses(uses)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Columns, []string{"a", "b", "c"}) {
		t.Errorf("candidates = %+v", got)
	}
}

func TestIndexServes(t *testing.T) {
	eq := IndexCandidate{Columns: []string{"status", "customer_id"}, Uses: []string{UseEquality, UseEquality}}
	ranged := IndexCandidate{Columns: []string{"status", "total"}, Uses: []string{UseEquality, UseRange}}
	cases := []struct {
		name      string
		index     Index
		candidate IndexCandidate
		want      bool
	}{
		{"the same columns", Index{Columns: []string{"status", "customer_id"}}, eq, true},
		{"equality columns in another order", Index{Columns: []string{"customer_id", "status"}}, eq, true},
		{"a longer index that leads with them", Index{Columns: []string{"customer_id", "status", "total"}}, eq, true},
		{"a range column must be where the candidate puts it", Index{Columns: []string{"total", "status"}}, ranged, false},
		{"a range in order", Index{Columns: []string{"status", "total", "id"}}, ranged, true},
		{"only a prefix of the candidate", Index{Columns: []string{"status"}}, eq, false},
		{"a hash index cannot be ranged or composed", Index{Columns: []string{"status", "customer_id"}, Method: "hash"}, eq, false},
		{"a partial index serves some rows only", Index{Columns: []string{"status", "customer_id"}, Predicate: "(x)"}, eq, false},
		{"an expression index", Index{Columns: []string{"status", "customer_id"}, Expression: true}, eq, false},
		{"an invalid index", Index{Columns: []string{"status", "customer_id"}, Invalid: true}, eq, false},
		{"a btree named as such", Index{Columns: []string{"status", "customer_id"}, Method: "btree"}, eq, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := indexServes(tc.index, tc.candidate); got != tc.want {
				t.Errorf("indexServes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanCostAndTheIndexItUses(t *testing.T) {
	plan := decodePlan(t, `[{"Plan": {"Node Type": "Limit", "Total Cost": 12.50, "Plans": [
	  {"Node Type": "Index Scan", "Index Name": "<13551>btree_orders_status"}]}}]`)
	if got := planTotalCost(plan); got != 12.5 {
		t.Errorf("cost = %v", got)
	}
	if !planUsesIndex(plan, "<13551>btree_orders_status") || planUsesIndex(plan, "orders_pkey") {
		t.Error("planUsesIndex did not find exactly the index it was given")
	}
	if planTotalCost(nil) != 0 || planTotalCost("x") != 0 {
		t.Error("a document that is not a plan costs nothing")
	}
}
