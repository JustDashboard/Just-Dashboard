package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The one tool the model is offered. It reads structure — the same names,
// types and keys the conversation began with, for tables the first read left
// out — and nothing in a table, and it runs only when the person allows it.
var DescribeTables = Tool{
	Name: "describe_tables",
	Description: "Read the columns, types, keys and references of tables or views in the database the person is working on. " +
		"Call it when answering needs a table whose columns you were not given: one named in the schema without its columns, " +
		"or one the person names that the schema does not list. It returns the structure only, never rows. " +
		"The person approves each call before it runs and may decline it.",
	Schema: json.RawMessage(`{"type":"object","properties":{"tables":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":10,` +
		`"description":"Table or view names as the schema writes them, schema-qualified (schema.table) when they are not in the schema in focus."}},` +
		`"required":["tables"],"additionalProperties":false}`),
}

const maxToolTables = 10

// ToolCall is one call the model asked for, as the person is asked to allow it.
// A describe_tables call names its tables; any other tool is named with its
// checked input.
type ToolCall struct {
	ID     string   `json:"id"`
	Tables []string `json:"tables,omitempty"`
	// Name and Input are a tool other than describe_tables, its input as
	// the tool's own check read it.
	Name  string          `json:"tool,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ErrMalformedCall is an answer that asked for something no tool here does,
// or asked in a shape that cannot be read. Nothing is run for it.
var ErrMalformedCall = errors.New("the assistant asked to read the database in a way this dashboard does not offer")

// Calls reads the tool calls an answer ended on. The input of a call was
// streamed rather than checked by the provider, so it is checked here, every
// field, before anybody is asked to allow it.
func Calls(blocks []Block) ([]ToolCall, error) {
	var calls []ToolCall
	for _, b := range blocks {
		if b.Type != BlockToolUse {
			continue
		}
		if b.ToolName != DescribeTables.Name || b.ToolID == "" {
			return nil, ErrMalformedCall
		}
		var input struct {
			Tables []string `json:"tables"`
		}
		dec := json.NewDecoder(bytes.NewReader(b.Input))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || len(input.Tables) == 0 || len(input.Tables) > maxToolTables {
			return nil, ErrMalformedCall
		}
		call := ToolCall{ID: b.ToolID}
		seen := map[string]bool{}
		for _, name := range input.Tables {
			name = strings.TrimSpace(name)
			if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") {
				return nil, ErrMalformedCall
			}
			if !seen[name] {
				seen[name] = true
				call.Tables = append(call.Tables, name)
			}
		}
		calls = append(calls, call)
	}
	if len(calls) == 0 {
		return nil, ErrMalformedCall
	}
	return calls, nil
}

// maxToolResult bounds what one call answers with, as the schema text is
// bounded: ten wide tables are still a known size.
const maxToolResult = 30_000

// DescribedText is a call's answer: the tables as the schema text writes
// them, and a line for each name nothing answered to.
func DescribedText(tables []Table, missing []string, focus string) string {
	var b strings.Builder
	for _, t := range tables {
		text := tableText(t, focus)
		if b.Len()+len(text) > maxToolResult {
			fmt.Fprintf(&b, "(%s and the rest were left out: the answer was long enough)\n", qualified(t.Schema, t.Name, focus))
			break
		}
		b.WriteString(text)
		b.WriteString("\n")
	}
	for _, name := range missing {
		fmt.Fprintf(&b, "No table or view is called %s.\n", name)
	}
	return strings.TrimSpace(b.String())
}

// Declined is what a call is answered with when the person did not allow it.
const Declined = "The person declined to share the structure of these tables. Answer with what you have, and say what is missing."

// ToolResults answers every pending call, with what was read for it or with
// the person's refusal.
func ToolResults(calls []ToolCall, answer func(ToolCall) (string, bool)) []Block {
	blocks := make([]Block, 0, len(calls))
	for _, call := range calls {
		text, ok := answer(call)
		blocks = append(blocks, Block{Type: BlockToolResult, ToolID: call.ID, Text: text, IsError: !ok})
	}
	return blocks
}
