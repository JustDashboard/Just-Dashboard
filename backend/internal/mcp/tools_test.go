package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func names(tools []Tool) string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return strings.Join(out, " ")
}

func TestAKeyIsOfferedTheToolsItMayCall(t *testing.T) {
	base := "list_connections list_tables describe_schema sample_rows explain"
	for _, c := range []struct {
		query, write bool
		want         string
	}{
		{false, false, base},
		{true, false, base + " run_query"},
		{true, true, base + " run_query run_write"},
	} {
		if got := names(Tools(c.query, c.write)); got != c.want {
			t.Errorf("query=%v write=%v: %s", c.query, c.write, got)
		}
	}
	for _, name := range []string{ToolRunWrite, ToolRunQuery, ToolListConnections} {
		if !Known(name) {
			t.Errorf("%s is not known", name)
		}
	}
	if Known("drop_database") {
		t.Error("an invented tool is known")
	}
}

// A hint left out is read as its default, and the defaults are destructive
// and open-world: every one is written, and only run_write says it may destroy.
func TestEveryToolSaysHowItBehaves(t *testing.T) {
	for _, tool := range Tools(true, true) {
		b, _ := json.Marshal(tool)
		var decoded map[string]any
		_ = json.Unmarshal(b, &decoded)
		hints, _ := decoded["annotations"].(map[string]any)
		for _, hint := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := hints[hint]; !ok {
				t.Errorf("%s leaves out %s", tool.Name, hint)
			}
		}
		if tool.Annotations.OpenWorldHint {
			t.Errorf("%s says it reaches the open world", tool.Name)
		}
		if writes := tool.Name == ToolRunWrite; tool.Annotations.DestructiveHint != writes || tool.Annotations.ReadOnlyHint == writes {
			t.Errorf("%s: %+v", tool.Name, tool.Annotations)
		}
		schema := tool.InputSchema
		if schema["type"] != "object" || schema["additionalProperties"] != false || tool.Description == "" {
			t.Errorf("%s: schema %+v", tool.Name, schema)
		}
	}
}

func TestArgumentsAreReadForTheToolTheyAreFor(t *testing.T) {
	args, err := DecodeArguments(ToolRunQuery, json.RawMessage(`{"connection":" shop ","sql":"SELECT 1","maxRows":20}`))
	if err != nil || args.Connection != "shop" || args.SQL != "SELECT 1" || args.MaxRows != 20 {
		t.Fatalf("run_query arguments = %+v %v", args, err)
	}
	if _, err := DecodeArguments(ToolListConnections, nil); err != nil {
		t.Errorf("no arguments for a tool that takes none: %v", err)
	}
	for _, c := range []struct {
		tool, raw, says string
	}{
		{ToolRunQuery, `{"connection":"shop","query":"SELECT 1"}`, `no argument "query"`},
		{ToolRunQuery, `{"connection":"shop"}`, `needs "sql"`},
		{ToolSampleRows, `{"connection":"shop","table":"orders","limit":"ten"}`, "do not fit"},
		{ToolListTables, `[]`, "must be an object"},
		{ToolListConnections, `{"connection":"shop"}`, `no argument "connection"`},
	} {
		if _, err := DecodeArguments(c.tool, json.RawMessage(c.raw)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s %s: %v, want %q", c.tool, c.raw, err, c.says)
		}
	}
}

func TestAResultIsTextAModelReadsAndAFailureIsOneItSees(t *testing.T) {
	ok := Result(map[string]any{"rows": 2})
	content := ok["content"].([]map[string]any)
	if ok["isError"] != false || content[0]["type"] != "text" || content[0]["text"] != `{"rows":2}` || ok["structuredContent"] == nil {
		t.Errorf("result = %+v", ok)
	}
	failed := Failure("shop is protected")
	if failed["isError"] != true || failed["content"].([]map[string]any)[0]["text"] != "shop is protected" {
		t.Errorf("failure = %+v", failed)
	}
}

func TestBoundRowsCutsLongValuesAndStopsAtTheBudget(t *testing.T) {
	long := strings.Repeat("é", CellBytes)
	rows := BoundRows([]string{"id", "body", "doc"}, nil, [][]any{
		{1, long, map[string]any{"k": strings.Repeat("v", 2*CellBytes)}},
		{2, "short", nil},
	}, false)
	if rows.RowCount != 2 || rows.Clipped != 2 || rows.Truncated {
		t.Fatalf("bounded = %+v", rows)
	}
	cut, _ := rows.Rows[0][1].(string)
	if !utf8.ValidString(cut) || len(cut) > CellBytes+len("…") || !strings.HasSuffix(cut, "…") {
		t.Errorf("a long value was cut to %d bytes", len(cut))
	}
	if doc, ok := rows.Rows[0][2].(string); !ok || len(doc) > CellBytes+len("…") {
		t.Errorf("a long document was not cut: %T", rows.Rows[0][2])
	}
	if rows.Rows[1][1] != "short" || rows.Rows[1][2] != nil {
		t.Errorf("a short row changed: %+v", rows.Rows[1])
	}

	many := make([][]any, 200)
	for i := range many {
		many[i] = []any{i, strings.Repeat("x", CellBytes)}
	}
	bounded := BoundRows([]string{"id", "pad"}, nil, many, false)
	if !bounded.Truncated || bounded.RowCount == 0 || bounded.RowCount >= 200 {
		t.Fatalf("a result past the budget kept %d rows, truncated %v", bounded.RowCount, bounded.Truncated)
	}
	if b, _ := json.Marshal(bounded.Rows); len(b) > ResultBytes+CellBytes*2 {
		t.Errorf("the rows came to %d bytes", len(b))
	}
	if empty := BoundRows(nil, nil, nil, true); empty.Columns == nil || empty.Rows == nil || !empty.Truncated {
		t.Errorf("an empty result = %+v", empty)
	}
}

func TestResolveFindsTheTableAModelMeant(t *testing.T) {
	known := []TableName{
		{Schema: "public", Name: "orders"}, {Schema: "public", Name: "Users"},
		{Schema: "audit", Name: "orders"}, {Schema: "public", Name: "v1.events"},
	}
	for _, c := range []struct {
		raw, schema string
		want        TableName
		ok          bool
	}{
		{"orders", "public", known[0], true},
		{"audit.orders", "", known[2], true},
		{"users", "public", known[1], true},
		{"v1.events", "public", known[3], true},
		// Two schemas hold one: the model has to say which.
		{"orders", "", TableName{}, false},
		{"customers", "public", TableName{}, false},
	} {
		got, ok := Resolve(c.raw, c.schema, known)
		if ok != c.ok || got != c.want {
			t.Errorf("Resolve(%q, %q) = %+v %v", c.raw, c.schema, got, ok)
		}
	}
}
