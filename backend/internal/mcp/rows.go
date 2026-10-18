package mcp

import (
	"encoding/json"
	"unicode/utf8"
)

// What a model is handed of a result set is bounded twice: each value, so one
// document in a JSON column cannot be the whole answer, and the answer, so a
// hundred wide rows stay under what a client passes on to its model.
const (
	// CellBytes is the most of one value a result carries.
	CellBytes = 1000
	// ResultBytes is the most a result's rows come to.
	ResultBytes = 48 << 10
)

// Rows is a result set as a tool returns it.
type Rows struct {
	Columns []string `json:"columns"`
	Types   []string `json:"types,omitempty"`
	Rows    [][]any  `json:"rows"`
	// RowCount is how many rows are here.
	RowCount int `json:"rowCount"`
	// Truncated says there were more rows than are here, by the row limit
	// or by ResultBytes.
	Truncated bool `json:"truncated,omitempty"`
	// Clipped counts the values cut to CellBytes.
	Clipped int `json:"clippedValues,omitempty"`
}

// BoundRows bounds what the engine returned. truncated is whether the engine's
// read already stopped at its limit.
func BoundRows(columns, types []string, rows [][]any, truncated bool) Rows {
	out := Rows{Columns: columns, Types: types, Rows: [][]any{}, Truncated: truncated}
	if out.Columns == nil {
		out.Columns = []string{}
	}
	total := 0
	for _, row := range rows {
		bounded := make([]any, len(row))
		size := 0
		for i, v := range row {
			var clipped bool
			bounded[i], clipped = boundValue(v)
			if clipped {
				out.Clipped++
			}
			size += valueSize(bounded[i])
		}
		if total+size > ResultBytes && len(out.Rows) > 0 {
			out.Truncated = true
			break
		}
		total += size
		out.Rows = append(out.Rows, bounded)
	}
	out.RowCount = len(out.Rows)
	return out
}

// boundValue keeps a value that is short as it is, and turns a long one into
// text cut on a character boundary.
func boundValue(v any) (any, bool) {
	switch t := v.(type) {
	case nil, bool, json.Number, int, int32, int64, uint64, float32, float64:
		return v, false
	case string:
		if len(t) <= CellBytes {
			return t, false
		}
		return clip(t), true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	if len(b) <= CellBytes {
		return v, false
	}
	return clip(string(b)), true
}

func clip(s string) string {
	n := CellBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func valueSize(v any) int {
	if s, ok := v.(string); ok {
		return len(s) + 3
	}
	b, _ := json.Marshal(v)
	return len(b) + 1
}
