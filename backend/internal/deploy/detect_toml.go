package deploy

import (
	"strconv"
	"strings"
)

// tomlEntry is one assignment of a TOML document as a line reader sees it.
// This is not a TOML parser: it reads the shapes deployment manifests use —
// tables, arrays of tables, strings, numbers, booleans, string arrays and
// flat inline tables — and skips anything else. An unusual layout yields
// fewer facts, never wrong ones, which is the standard every manifest reader
// in detection holds (pyproject, Cargo.toml).
type tomlEntry struct {
	table string
	// index is the ordinal of an [[array]] table among those of its name,
	// or -1 for a plain table.
	index int
	key   string
	value tomlValue
}

type tomlValue struct {
	text string
	list []string
	// isList distinguishes an empty array from a missing value.
	isList bool
}

// tomlMaxValueLines bounds a multi-line string or array. A value that runs
// past it, or to the end of the file unterminated, ends the document: what
// follows would be read from inside the value.
const tomlMaxValueLines = 256

func readTOML(content []byte) []tomlEntry {
	lines := strings.Split(string(manifestText(content)), "\n")
	var entries []tomlEntry
	table := ""
	index := -1
	ordinals := map[string]int{}
	for position := 0; position < len(lines) && len(entries) < 4096; position++ {
		line := strings.TrimSpace(stripTOMLComment(lines[position]))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			table = normalizeTOMLKey(strings.TrimSpace(line[2 : len(line)-2]))
			index = ordinals[table]
			ordinals[table]++
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = normalizeTOMLKey(strings.TrimSpace(line[1 : len(line)-1]))
			index = -1
			continue
		}
		rawKey, rawValue, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key := normalizeTOMLKey(strings.TrimSpace(rawKey))
		rawValue = strings.TrimSpace(rawValue)
		// A multi-line value is gathered line by line, each line scanned once:
		// rescanning the whole value for every line it grew by made a single
		// unterminated array in a 64 KiB file cost seconds.
		switch {
		case strings.HasPrefix(rawValue, `"""`) || strings.HasPrefix(rawValue, `'''`):
			quote := rawValue[:3]
			var body strings.Builder
			body.WriteString(rawValue[3:])
			closed := strings.Contains(rawValue[3:], quote)
			for extra := 0; !closed; extra++ {
				if extra == tomlMaxValueLines || position+1 >= len(lines) {
					return entries
				}
				position++
				body.WriteString("\n" + lines[position])
				closed = strings.Contains(lines[position], quote)
			}
			text, _, _ := strings.Cut(body.String(), quote)
			entries = append(entries, tomlEntry{table: table, index: index, key: key, value: tomlValue{text: strings.TrimPrefix(text, "\n")}})
		case strings.HasPrefix(rawValue, "["):
			var scan tomlArrayScan
			var body strings.Builder
			body.WriteString(rawValue)
			balanced := scan.feed(rawValue)
			for extra := 0; !balanced; extra++ {
				if extra == tomlMaxValueLines || position+1 >= len(lines) {
					return entries
				}
				position++
				line := " " + strings.TrimSpace(stripTOMLComment(lines[position]))
				body.WriteString(line)
				balanced = scan.feed(line)
			}
			entries = append(entries, tomlEntry{table: table, index: index, key: key, value: tomlValue{list: tomlStrings(body.String()), isList: true}})
		case strings.HasPrefix(rawValue, "{"):
			body := strings.TrimSuffix(strings.TrimPrefix(rawValue, "{"), "}")
			for _, pair := range splitTOMLInline(body) {
				innerKey, innerValue, ok := strings.Cut(pair, "=")
				if !ok {
					continue
				}
				value := tomlValue{text: tomlScalar(strings.TrimSpace(innerValue))}
				// Cargo's `features = ["a", "b"]` inside a dependency's inline
				// table is an array, not text.
				if strings.HasPrefix(strings.TrimSpace(innerValue), "[") {
					value = tomlValue{list: tomlStrings(innerValue), isList: true}
				}
				entries = append(entries, tomlEntry{table: table, index: index,
					key: key + "." + normalizeTOMLKey(strings.TrimSpace(innerKey)), value: value})
			}
		default:
			entries = append(entries, tomlEntry{table: table, index: index, key: key, value: tomlValue{text: tomlScalar(rawValue)}})
		}
	}
	return entries
}

func stripTOMLComment(line string) string {
	inString := byte(0)
	for index := 0; index < len(line); index++ {
		switch character := line[index]; {
		case inString != 0:
			if character == '\\' && inString == '"' {
				index++
			} else if character == inString {
				inString = 0
			}
		case character == '"' || character == '\'':
			inString = character
		case character == '#':
			return line[:index]
		}
	}
	return line
}

func normalizeTOMLKey(key string) string {
	parts := strings.Split(key, ".")
	for index, part := range parts {
		parts[index] = strings.Trim(strings.TrimSpace(part), `"'`)
	}
	return strings.Join(parts, ".")
}

// tomlArrayScan follows an array's brackets and strings across the lines it
// is fed, so a line is scanned once however long the array grows.
type tomlArrayScan struct {
	depth    int
	inString byte
}

// feed scans the next part of the array and says whether it has closed.
func (a *tomlArrayScan) feed(text string) bool {
	for index := 0; index < len(text); index++ {
		switch character := text[index]; {
		case a.inString != 0:
			if character == '\\' && a.inString == '"' {
				index++
			} else if character == a.inString {
				a.inString = 0
			}
		case character == '"' || character == '\'':
			a.inString = character
		case character == '[':
			a.depth++
		case character == ']':
			a.depth--
		}
	}
	return a.depth <= 0
}

// tomlStrings reads the string elements of an array, in order.
func tomlStrings(body string) []string {
	var result []string
	for index := 0; index < len(body) && len(result) < 256; index++ {
		quote := body[index]
		if quote != '"' && quote != '\'' {
			continue
		}
		end := index + 1
		for end < len(body) && body[end] != quote {
			if body[end] == '\\' && quote == '"' {
				end++
			}
			end++
		}
		if end >= len(body) {
			break
		}
		result = append(result, unescapeTOML(body[index+1:end], quote))
		index = end
	}
	return result
}

// splitTOMLInline splits an inline table's pairs at the commas outside
// strings and nested arrays or tables.
func splitTOMLInline(body string) []string {
	var parts []string
	inString := byte(0)
	depth := 0
	start := 0
	for index := 0; index < len(body); index++ {
		switch character := body[index]; {
		case inString != 0:
			if character == '\\' && inString == '"' {
				index++
			} else if character == inString {
				inString = 0
			}
		case character == '"' || character == '\'':
			inString = character
		case character == '[' || character == '{':
			depth++
		case character == ']' || character == '}':
			depth--
		case character == ',' && depth == 0:
			parts = append(parts, body[start:index])
			start = index + 1
		}
	}
	return append(parts, body[start:])
}

// tomlScalar returns a string's contents, or a bare value's text.
func tomlScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		return unescapeTOML(raw[1:len(raw)-1], raw[0])
	}
	return raw
}

func unescapeTOML(value string, quote byte) string {
	if quote == '\'' || !strings.Contains(value, `\`) {
		return value
	}
	if unquoted, err := strconv.Unquote(`"` + value + `"`); err == nil {
		return unquoted
	}
	return value
}

// tomlLookup returns the first value of key in table (any array index).
func tomlLookup(entries []tomlEntry, table, key string) (tomlValue, bool) {
	for _, entry := range entries {
		if entry.table == table && entry.key == key {
			return entry.value, true
		}
	}
	return tomlValue{}, false
}

func tomlText(entries []tomlEntry, table, key string) string {
	value, _ := tomlLookup(entries, table, key)
	return value.text
}

// tomlTable returns every key of a plain table, in order.
func tomlTable(entries []tomlEntry, table string) []tomlEntry {
	var result []tomlEntry
	for _, entry := range entries {
		if entry.table == table {
			result = append(result, entry)
		}
	}
	return result
}

// tomlArrayTables groups an [[array]] table's entries by ordinal.
func tomlArrayTables(entries []tomlEntry, table string) []map[string]tomlValue {
	var result []map[string]tomlValue
	for _, entry := range entries {
		if entry.table != table || entry.index < 0 {
			continue
		}
		for len(result) <= entry.index {
			result = append(result, map[string]tomlValue{})
		}
		result[entry.index][entry.key] = entry.value
	}
	return result
}
