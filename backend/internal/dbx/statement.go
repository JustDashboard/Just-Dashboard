package dbx

import (
	"fmt"
	"strings"
)

// Split SQL at statement boundaries, never inside a quoted value or comment.
// Ambiguous dialect-specific escaping is refused: this is an authorization
// boundary, not a best-effort syntax highlighter.
func sqlStatements(query string) ([]string, error) {
	var statements []string
	start := 0
	for i := 0; i < len(query); {
		switch c := query[i]; {
		case c == '\'' || c == '"' || c == '`' || c == '[':
			end := c
			if c == '[' {
				end = ']'
			}
			i++
			closed := false
			for i < len(query) {
				if query[i] == '\\' && i+1 < len(query) && (query[i+1] == end || query[i+1] == '\\') {
					return nil, fmt.Errorf("use doubled quotes instead of ambiguous backslash escapes")
				}
				if query[i] == end {
					i++
					if i < len(query) && query[i] == end {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated SQL quote")
			}
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			if i+2 < len(query) && query[i+2] > ' ' {
				return nil, fmt.Errorf("ambiguous SQL comment: add whitespace after --")
			}
			for i < len(query) && query[i] != '\n' && query[i] != '\r' {
				i++
			}
		case c == '#':
			return nil, fmt.Errorf("use standard SQL comments in the query runner")
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			mariaDBExecutable := i+3 < len(query) && (query[i+2] == 'M' || query[i+2] == 'm') && query[i+3] == '!'
			if mariaDBExecutable || i+2 < len(query) && (query[i+2] == '!' || query[i+2] == '+') {
				return nil, fmt.Errorf("executable SQL comments are not supported")
			}
			i += 2
			depth := 1
			for i < len(query) && depth > 0 {
				if i+1 < len(query) && query[i:i+2] == "/*" {
					return nil, fmt.Errorf("nested SQL comments are not supported")
				} else if i+1 < len(query) && query[i:i+2] == "*/" {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("unterminated SQL comment")
			}
		case c == '$':
			j := i + 1
			for j < len(query) && (query[j] == '_' || query[j] >= 'a' && query[j] <= 'z' || query[j] >= 'A' && query[j] <= 'Z' || j > i+1 && query[j] >= '0' && query[j] <= '9') {
				j++
			}
			if j < len(query) && query[j] == '$' {
				return nil, fmt.Errorf("dollar quoted SQL is not supported by the query runner")
			} else {
				i++
			}
		case c == ';':
			if strings.TrimSpace(normaliseSQL(query[start:i])) != "" {
				statements = append(statements, query[start:i])
			}
			i++
			start = i
		default:
			i++
		}
	}
	if strings.TrimSpace(normaliseSQL(query[start:])) != "" {
		statements = append(statements, query[start:])
	}
	return statements, nil
}

func SingleStatement(query string) (string, error) {
	statements, err := sqlStatements(query)
	if err != nil {
		return "", err
	}
	if len(statements) != 1 {
		return "", fmt.Errorf("submit exactly one SQL statement at a time")
	}
	return strings.TrimSpace(statements[0]), nil
}

// Explain accepts a statement, never caller-provided EXPLAIN options. Keeping
// ANALYZE outside this grammar prevents a read-only plan from executing it.
func ExplainStatement(query string) (string, error) {
	stmt, err := SingleStatement(query)
	if err != nil {
		return "", err
	}
	leader := strings.ToLower(strings.Fields(normaliseSQL(stmt))[0])
	switch leader {
	case "select", "with", "insert", "update", "delete", "merge", "values", "table":
		return stmt, nil
	default:
		return "", fmt.Errorf("this statement cannot be explained safely")
	}
}
