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
	reads := "list_deployments list_runs read_run_log list_releases"
	for _, c := range []struct {
		offer Offer
		want  string
	}{
		{Offer{Databases: true}, base},
		{Offer{Databases: true, Query: true}, base + " run_query"},
		{Offer{Databases: true, Query: true, Write: true}, base + " run_query run_write"},
		// A key whose databases were all forgotten still says it reaches none.
		{Offer{}, base},
		// A key for the deployments alone is offered nothing about databases.
		{Offer{Query: true, Deployments: true}, reads},
		{Offer{Deployments: true, Deploy: true}, reads + " deploy"},
		{Offer{Deployments: true, Deploy: true, Rollback: true}, reads + " deploy rollback"},
		{Offer{Databases: true, Query: true, Deployments: true, Deploy: true}, base + " run_query " + reads + " deploy"},
		// Deploying is never offered without reading.
		{Offer{Databases: true, Deploy: true, Rollback: true}, base},
	} {
		if got := names(Tools(c.offer)); got != c.want {
			t.Errorf("%+v: %s", c.offer, got)
		}
	}
	for _, name := range []string{ToolRunWrite, ToolRunQuery, ToolListConnections, ToolDeploy, ToolReadRunLog} {
		if !Known(name) {
			t.Errorf("%s is not known", name)
		}
	}
	if Known("drop_database") {
		t.Error("an invented tool is known")
	}
}

// A hint left out is read as its default, and the defaults are destructive
// and open-world: every one is written, only run_write and rollback say they
// may destroy, and only they and deploy say they change anything.
func TestEveryToolSaysHowItBehaves(t *testing.T) {
	all := Tools(Offer{Databases: true, Query: true, Write: true, Deployments: true, Deploy: true, Rollback: true})
	if len(all) != len(catalogue) {
		t.Fatalf("every grant offers %d of %d tools", len(all), len(catalogue))
	}
	for _, tool := range all {
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
		destroys := tool.Name == ToolRunWrite || tool.Name == ToolRollback
		changes := destroys || tool.Name == ToolDeploy
		if tool.Annotations.DestructiveHint != destroys || tool.Annotations.ReadOnlyHint == changes {
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
	deploy, err := DecodeArguments(ToolDeploy, json.RawMessage(`{"deployment":" shop ","ref":" v2 "}`))
	if err != nil || deploy.Deployment != "shop" || deploy.Ref != "v2" || deploy.Revision != "" {
		t.Fatalf("deploy arguments = %+v %v", deploy, err)
	}
	for _, c := range []struct {
		tool, raw, says string
	}{
		{ToolRunQuery, `{"connection":"shop","query":"SELECT 1"}`, `no argument "query"`},
		{ToolRunQuery, `{"connection":"shop"}`, `needs "sql"`},
		{ToolSampleRows, `{"connection":"shop","table":"orders","limit":"ten"}`, "do not fit"},
		{ToolListTables, `[]`, "must be an object"},
		{ToolListConnections, `{"connection":"shop"}`, `no argument "connection"`},
		{ToolListDeployments, `{"deployment":"shop"}`, `no argument "deployment"`},
		{ToolDeploy, `{"deployment":"shop","branch":"main"}`, `no argument "branch"`},
		{ToolRollback, `{"release":3}`, `needs "deployment"`},
		{ToolReadRunLog, `{"deployment":"shop","run":"latest"}`, "do not fit"},
		{ToolListRuns, `{"connection":"shop","deployment":"shop"}`, `no argument "connection"`},
	} {
		if _, err := DecodeArguments(c.tool, json.RawMessage(c.raw)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s %s: %v, want %q", c.tool, c.raw, err, c.says)
		}
	}
}

// The client is told about what its key reaches, and every client hears that
// its calls are audited.
func TestTheInstructionsAreForWhatTheKeyReaches(t *testing.T) {
	for _, c := range []struct {
		offer             Offer
		databases, deploy bool
	}{
		{Offer{Databases: true}, true, false},
		{Offer{}, true, false},
		{Offer{Deployments: true}, false, true},
		{Offer{Databases: true, Deployments: true, Deploy: true}, true, true},
	} {
		text := Instructions(c.offer)
		if strings.Contains(text, "list_connections") != c.databases || strings.Contains(text, "list_deployments") != c.deploy ||
			!strings.HasSuffix(text, "audit log.") {
			t.Errorf("%+v: %s", c.offer, text)
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
