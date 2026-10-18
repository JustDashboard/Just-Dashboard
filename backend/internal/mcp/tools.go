package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The tools, by name. A client lists them, a model picks one by its name and
// its description, so each description says what the tool returns, what it
// will not do, and what bounds it.
const (
	ToolListConnections = "list_connections"
	ToolListTables      = "list_tables"
	ToolDescribeSchema  = "describe_schema"
	ToolSampleRows      = "sample_rows"
	ToolExplain         = "explain"
	ToolRunQuery        = "run_query"
	ToolRunWrite        = "run_write"
)

// The bounds the tools are held to, which their descriptions state.
const (
	SampleDefault = 10
	SampleMax     = 50
	QueryDefault  = 100
	QueryMax      = 500
	// DescribeTables is how many tables one schema read describes before it
	// only names the rest, and DescribeNamed how many a call may name.
	DescribeTables = 100
	DescribeNamed  = 20
	// ListMax bounds a table list.
	ListMax = 1000
)

// Instructions is what a client is told the server is for, once, before any
// tool is chosen.
const Instructions = `Just Dashboard's databases. Start with list_connections: every other tool names a database by the connection it returns. ` +
	`Read the structure with list_tables and describe_schema before writing SQL, and write it for that connection's engine. ` +
	`run_query and sample_rows only read. run_write is offered only for a database this key may change, and runs one statement at a time; ` +
	`a destructive statement still needs the key owner's role to allow it. Protected databases are never reachable here. Every call is recorded in the dashboard's audit log.`

// Tool is one entry of tools/list.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations Annotations    `json:"annotations"`
}

// Annotations say how a tool behaves. Every hint is written out, because a
// hint left out is read as its default and the defaults are the cautious
// ones: destructive, and reaching the open world.
type Annotations struct {
	Title           string `json:"title"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

func object(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

var (
	connectionProperty = map[string]any{
		"type":        "string",
		"description": "The database, by the connection name list_connections gives.",
	}
	schemaProperty = map[string]any{
		"type":        "string",
		"description": "One schema (on MySQL and MariaDB, one database) to look in. Leave it out for the connection's own.",
	}
	sqlProperty = map[string]any{
		"type":        "string",
		"description": "Exactly one SQL statement, in the connection's engine's dialect.",
	}
)

func reading(title string) Annotations {
	return Annotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}
}

var catalogue = []Tool{
	{
		Name:  ToolListConnections,
		Title: "List databases",
		Description: "List the databases this key reaches: each one's connection name, engine, database, environment, and whether this key may change it (access read or write). " +
			"Protected databases and engines the tools do not speak are named under unavailable, with why.",
		InputSchema: object(nil, map[string]any{}),
		Annotations: reading("List databases"),
	},
	{
		Name:  ToolListTables,
		Title: "List tables",
		Description: fmt.Sprintf("List the tables and views of a database, with each one's schema, kind and the engine's row estimate (-1 where it has none). "+
			"Without a schema it lists every schema the connection's account can see. At most %d are returned.", ListMax),
		InputSchema: object([]string{"connection"}, map[string]any{"connection": connectionProperty, "schema": schemaProperty}),
		Annotations: reading("List tables"),
	},
	{
		Name:  ToolDescribeSchema,
		Title: "Describe tables",
		Description: fmt.Sprintf("Describe tables as structure only: each column's name and type, whether it may be null, primary and unique keys, and the column a foreign key references. "+
			"Name up to %d tables to describe exactly those; without tables, the first %d of the schema are described and the rest only named. No rows are read.", DescribeNamed, DescribeTables),
		InputSchema: object([]string{"connection"}, map[string]any{
			"connection": connectionProperty,
			"schema":     schemaProperty,
			"tables": map[string]any{
				"type": "array", "maxItems": DescribeNamed,
				"items":       map[string]any{"type": "string"},
				"description": "Tables to describe, by name or as schema.table.",
			},
		}),
		Annotations: reading("Describe tables"),
	},
	{
		Name:        ToolSampleRows,
		Title:       "Sample rows",
		Description: fmt.Sprintf("Read the first rows of one table or view as it stores them, %d by default and at most %d. Long values are cut and counted as clipped.", SampleDefault, SampleMax),
		InputSchema: object([]string{"connection", "table"}, map[string]any{
			"connection": connectionProperty,
			"schema":     schemaProperty,
			"table":      map[string]any{"type": "string", "description": "The table or view."},
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": SampleMax},
		}),
		Annotations: reading("Sample rows"),
	},
	{
		Name:        ToolExplain,
		Title:       "Explain a statement",
		Description: "Show the plan the engine would use for one SELECT, INSERT, UPDATE, DELETE or MERGE, as the engine prints it. The statement is planned, never run.",
		InputSchema: object([]string{"connection", "sql"}, map[string]any{"connection": connectionProperty, "sql": sqlProperty}),
		Annotations: reading("Explain a statement"),
	},
	{
		Name:  ToolRunQuery,
		Title: "Run a read-only query",
		Description: fmt.Sprintf("Run one SQL statement that only reads and return its rows, %d by default and at most %d, with long values cut. "+
			"A statement that could change anything is refused before it is sent, and the statement runs inside the engine's own read-only scope. It stops after 30 seconds.", QueryDefault, QueryMax),
		InputSchema: object([]string{"connection", "sql"}, map[string]any{
			"connection": connectionProperty,
			"sql":        sqlProperty,
			"maxRows":    map[string]any{"type": "integer", "minimum": 1, "maximum": QueryMax},
		}),
		Annotations: reading("Run a read-only query"),
	},
	{
		Name:  ToolRunWrite,
		Title: "Run a statement that changes data",
		Description: "Run one SQL statement that changes data or structure — INSERT, UPDATE, DELETE, CREATE, ALTER — on a database this key may change, and return how many rows it changed (and the rows a RETURNING clause gives back). " +
			"It is held to what the key's owner may run in the dashboard's SQL editor: a statement that deletes, overwrites or drops needs an administrator's role. It stops after 60 seconds.",
		InputSchema: object([]string{"connection", "sql"}, map[string]any{"connection": connectionProperty, "sql": sqlProperty}),
		Annotations: Annotations{Title: "Run a statement that changes data", DestructiveHint: true},
	},
}

// Tools is what tools/list answers for a key: run_query only for one whose
// owner may run statements at all, run_write only for one that may change
// some database. The order is fixed, so a client's cache of it holds.
func Tools(query, write bool) []Tool {
	out := make([]Tool, 0, len(catalogue))
	for _, t := range catalogue {
		if (t.Name == ToolRunQuery && !query) || (t.Name == ToolRunWrite && !write) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Known reports whether a name is a tool at all, offered to this key or not.
func Known(name string) bool {
	for _, t := range catalogue {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Arguments are every tool's arguments; each reads the ones it takes.
type Arguments struct {
	Connection string   `json:"connection"`
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Tables     []string `json:"tables"`
	SQL        string   `json:"sql"`
	Limit      int      `json:"limit"`
	MaxRows    int      `json:"maxRows"`
}

// DecodeArguments reads a call's arguments for one tool, refusing a name the
// tool does not take: a model that misspells one is better told than ignored.
func DecodeArguments(tool string, raw json.RawMessage) (Arguments, error) {
	var args Arguments
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	var names map[string]json.RawMessage
	if err := json.Unmarshal(raw, &names); err != nil {
		return args, errors.New("arguments must be an object")
	}
	allowed := map[string]bool{}
	for _, t := range catalogue {
		if t.Name == tool {
			for name := range t.InputSchema["properties"].(map[string]any) {
				allowed[name] = true
			}
		}
	}
	for name := range names {
		if !allowed[name] {
			return args, fmt.Errorf("%s takes no argument %q", tool, name)
		}
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("the arguments do not fit %s: %v", tool, err)
	}
	for _, name := range requiredOf(tool) {
		if _, ok := names[name]; !ok {
			return args, fmt.Errorf("%s needs %q", tool, name)
		}
	}
	args.Connection = strings.TrimSpace(args.Connection)
	return args, nil
}

func requiredOf(tool string) []string {
	for _, t := range catalogue {
		if t.Name == tool {
			required, _ := t.InputSchema["required"].([]string)
			return required
		}
	}
	return nil
}

// Result is a tool's answer: the value as JSON text, which is what a model
// reads, and the same value as structured content for a client that parses.
func Result(v any) map[string]any {
	text, err := json.Marshal(v)
	if err != nil {
		return Failure("the answer could not be written: " + err.Error())
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": v,
		"isError":           false,
	}
}

// Failure is a tool that ran into something the model can act on: a refusal,
// a mistake in its arguments, the engine's own error. It is a result rather
// than a protocol error so that the model sees it.
func Failure(message string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": message}},
		"isError": true,
	}
}
