package dbx

import (
	"strings"
)

// Reading a PostgreSQL plan for the columns its conditions test.
//
// This is the first half of the Explain view's "What if?" action: from a plan
// taken with VERBOSE, find the columns a filter, a join and an ORDER BY …
// LIMIT lean on, so that an index over them can be proposed. Nothing here
// talks to a server, and nothing here is run unasked — the caller is a request
// the operator made from the Explain view.
//
// A plan prints its conditions as the deparsed expression it will evaluate,
// `((o.status = 'open'::text) AND (o.total > '10'::numeric))`, so the reader
// below is a small tokenizer and a splitter on AND, not a SQL parser. What it
// does not recognise it leaves alone: a condition that is not a plain column
// compared with a constant, or with another column, proposes nothing. An index
// is a cost on every write, and a wrong suggestion is worse than a missing one.

// How a column is used by the plan.
const (
	UseEquality = "equality"
	UseRange    = "range"
	UseJoin     = "join"
	UseSort     = "sort"
)

// IndexCandidate is an index the plan's conditions would be able to use.
type IndexCandidate struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	// Uses says what each column does in the plan, in the order of Columns.
	Uses []string `json:"uses"`
	// Statement is the CREATE INDEX the dashboard would run for it.
	Statement string `json:"statement,omitempty"`
}

// maxCandidateColumns is how many columns a proposed index leads with. Past
// the third the index is mostly a copy of the table.
const maxCandidateColumns = 3

type relationRef struct{ schema, table string }

type columnUse struct {
	relation relationRef
	column   string
	use      string
}

// planColumnUses walks a plan document, as Explanation.Plan holds it, and
// returns the columns its conditions test. The plan must have been taken with
// VERBOSE: only then is every column qualified with its alias and every
// relation named with its schema.
func planColumnUses(plan any) []columnUse {
	roots, ok := plan.([]any)
	if !ok || len(roots) == 0 {
		return nil
	}
	root, ok := roots[0].(map[string]any)
	if !ok {
		return nil
	}
	top, ok := root["Plan"].(map[string]any)
	if !ok {
		return nil
	}
	aliases := map[string]relationRef{}
	ambiguous := map[string]bool{}
	var collect func(node map[string]any)
	collect = func(node map[string]any) {
		if table, ok := node["Relation Name"].(string); ok {
			ref := relationRef{schema: stringOf(node["Schema"]), table: table}
			alias := stringOf(node["Alias"])
			if alias == "" {
				alias = table
			}
			if held, seen := aliases[alias]; seen && held != ref {
				ambiguous[alias] = true
			}
			aliases[alias] = ref
		}
		for _, child := range childPlans(node) {
			collect(child)
		}
	}
	collect(top)
	for alias := range ambiguous {
		delete(aliases, alias)
	}

	var uses []columnUse
	var walk func(node map[string]any, limited bool)
	walk = func(node map[string]any, limited bool) {
		for _, key := range []string{"Filter", "Join Filter", "Hash Cond", "Merge Cond"} {
			if condition, ok := node[key].(string); ok {
				uses = append(uses, conditionUses(condition, aliases)...)
			}
		}
		// A sort is only worth an index when something above it stops reading:
		// sorting every row is not what an index is for.
		if keys, ok := node["Sort Key"].([]any); ok && limited {
			uses = append(uses, sortUses(keys, aliases)...)
		}
		below := limited || node["Node Type"] == "Limit"
		for _, child := range childPlans(node) {
			walk(child, below)
		}
	}
	walk(top, false)
	return uses
}

func childPlans(node map[string]any) []map[string]any {
	children, _ := node["Plans"].([]any)
	out := make([]map[string]any, 0, len(children))
	for _, child := range children {
		if m, ok := child.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// conditionUses reads one printed condition.
func conditionUses(condition string, aliases map[string]relationRef) []columnUse {
	tokens, ok := lexCondition(condition)
	if !ok {
		return nil
	}
	var uses []columnUse
	splitConjuncts(tokens, func(leaf []condToken) {
		uses = append(uses, leafUses(leaf, aliases)...)
	})
	return uses
}

// sortSuffixes are the words that follow a column in a Sort Key.
var sortSuffixes = map[string]bool{"ASC": true, "DESC": true, "NULLS": true, "USING": true}

// sortUses reads a Sort Key. The keys are an index's columns only while they
// are plain columns of one relation; the first expression ends the run.
func sortUses(keys []any, aliases map[string]relationRef) []columnUse {
	var uses []columnUse
	for _, key := range keys {
		text, _ := key.(string)
		tokens, ok := lexCondition(text)
		if !ok {
			break
		}
		for i, t := range tokens {
			if t.kind == condIdent && !t.quoted && sortSuffixes[strings.ToUpper(t.text)] {
				tokens = tokens[:i]
				break
			}
		}
		column, ok := asColumn(tokens, aliases)
		if !ok || (len(uses) > 0 && uses[0].relation != column.relation) {
			break
		}
		column.use = UseSort
		uses = append(uses, column)
	}
	return uses
}

// --- tokens ------------------------------------------------------------------

type condKind int

const (
	condIdent condKind = iota
	condString
	condNumber
	condOp
	condOpen
	condClose
	condDot
	condComma
	condCast
	condOther
)

type condToken struct {
	kind   condKind
	text   string
	quoted bool
}

// castStops end the words of a type name after `::`, because a multi-word
// type (`character varying`, `timestamp without time zone`) is followed by
// whatever the condition says next.
var castStops = map[string]bool{
	"AND": true, "OR": true, "NOT": true, "IS": true, "IN": true, "LIKE": true, "ILIKE": true,
	"BETWEEN": true, "ANY": true, "ALL": true, "SOME": true, "NULL": true, "DISTINCT": true,
	"FROM": true, "ASC": true, "DESC": true, "NULLS": true, "USING": true, "COLLATE": true,
}

const operatorChars = "+-*/<>=~!@#%^&|?"

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || c == '$' || (c >= '0' && c <= '9') }

// lexCondition splits a printed condition into tokens. It reports false for
// text it cannot read to the end — an unterminated quote — rather than guess.
func lexCondition(s string) ([]condToken, bool) {
	var tokens []condToken
	i := 0
	quoted := func(q byte) (string, bool) {
		var b strings.Builder
		for i++; i < len(s); i++ {
			if s[i] == q {
				if i+1 < len(s) && s[i+1] == q {
					b.WriteByte(q)
					i++
					continue
				}
				i++
				return b.String(), true
			}
			b.WriteByte(s[i])
		}
		return "", false
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			tokens = append(tokens, condToken{kind: condOpen, text: "("})
			i++
		case c == ')':
			tokens = append(tokens, condToken{kind: condClose, text: ")"})
			i++
		case c == ',':
			tokens = append(tokens, condToken{kind: condComma, text: ","})
			i++
		case c == '.' && !(i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'):
			tokens = append(tokens, condToken{kind: condDot, text: "."})
			i++
		case c == ':' && i+1 < len(s) && s[i+1] == ':':
			i += 2
			var ok bool
			if i, ok = skipTypeName(s, i); !ok {
				return nil, false
			}
			tokens = append(tokens, condToken{kind: condCast, text: "::"})
		case c == '\'':
			text, ok := quoted('\'')
			if !ok {
				return nil, false
			}
			tokens = append(tokens, condToken{kind: condString, text: text})
		case c == '"':
			text, ok := quoted('"')
			if !ok {
				return nil, false
			}
			tokens = append(tokens, condToken{kind: condIdent, text: text, quoted: true})
		case c == '$' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			j := i + 1
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			tokens = append(tokens, condToken{kind: condNumber, text: s[i:j]})
			i = j
		case c >= '0' && c <= '9' || (c == '.' && i+1 < len(s)):
			j := i + 1
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.' || s[j] == 'e' || s[j] == 'E') {
				j++
			}
			tokens = append(tokens, condToken{kind: condNumber, text: s[i:j]})
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			tokens = append(tokens, condToken{kind: condIdent, text: s[i:j]})
			i = j
		case strings.IndexByte(operatorChars, c) >= 0:
			j := i + 1
			for j < len(s) && strings.IndexByte(operatorChars, s[j]) >= 0 {
				j++
			}
			tokens = append(tokens, condToken{kind: condOp, text: s[i:j]})
			i = j
		default:
			tokens = append(tokens, condToken{kind: condOther, text: string(c)})
			i++
		}
	}
	return tokens, true
}

// skipTypeName steps over the type that follows `::`: dotted and quoted
// names, a word list like `double precision`, an argument list and array
// brackets.
func skipTypeName(s string, i int) (int, bool) {
	words := 0
	for {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		switch {
		case i < len(s) && s[i] == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j >= len(s) {
				return i, false
			}
			i = j + 1
		case i < len(s) && isIdentStart(s[i]):
			j := i
			for j < len(s) && (isIdentPart(s[j]) || s[j] == '.') {
				j++
			}
			if words > 0 && castStops[strings.ToUpper(s[i:j])] {
				return i, true
			}
			i = j
		default:
			return i, words > 0
		}
		words++
		if i < len(s) && s[i] == '(' {
			depth := 0
			for ; i < len(s); i++ {
				if s[i] == '(' {
					depth++
				} else if s[i] == ')' {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
			}
			if depth != 0 {
				return i, false
			}
		}
		for i+1 < len(s) && s[i] == '[' && s[i+1] == ']' {
			i += 2
		}
	}
}

// --- conditions --------------------------------------------------------------

func isWord(t condToken, word string) bool {
	return t.kind == condIdent && !t.quoted && strings.EqualFold(t.text, word)
}

// matching returns the index of the parenthesis that closes tokens[open], or -1.
func matching(tokens []condToken, open int) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch tokens[i].kind {
		case condOpen:
			depth++
		case condClose:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// unwrap takes off parentheses that enclose the whole of tokens.
func unwrap(tokens []condToken) []condToken {
	for len(tokens) >= 2 && tokens[0].kind == condOpen && matching(tokens, 0) == len(tokens)-1 {
		tokens = tokens[1 : len(tokens)-1]
	}
	return tokens
}

// splitConjuncts calls emit for each leaf of a tree of ANDs. A condition with
// an OR at its top level is passed over: an index serves one branch of it at
// best, and which is for the planner to say.
func splitConjuncts(tokens []condToken, emit func([]condToken)) {
	tokens = unwrap(tokens)
	if len(tokens) == 0 {
		return
	}
	depth := 0
	start := 0
	var parts [][]condToken
	for i, t := range tokens {
		switch t.kind {
		case condOpen:
			depth++
		case condClose:
			depth--
		}
		if depth != 0 {
			continue
		}
		if isWord(t, "OR") {
			return
		}
		if isWord(t, "AND") {
			parts = append(parts, tokens[start:i])
			start = i + 1
		}
	}
	if len(parts) == 0 {
		emit(tokens)
		return
	}
	parts = append(parts, tokens[start:])
	for _, part := range parts {
		splitConjuncts(part, emit)
	}
}

// asColumn reads tokens as one column, `alias.name`, perhaps in parentheses and
// perhaps cast. The qualifier must name a relation of the plan: a CTE or a
// subquery's alias is not a table an index can be made on.
func asColumn(tokens []condToken, aliases map[string]relationRef) (columnUse, bool) {
	for {
		tokens = unwrap(tokens)
		if n := len(tokens); n > 0 && tokens[n-1].kind == condCast {
			tokens = tokens[:n-1]
			continue
		}
		break
	}
	if len(tokens) != 3 || tokens[0].kind != condIdent || tokens[1].kind != condDot || tokens[2].kind != condIdent {
		return columnUse{}, false
	}
	relation, ok := aliases[tokens[0].text]
	if !ok {
		return columnUse{}, false
	}
	return columnUse{relation: relation, column: tokens[2].text}, true
}

// constants are the words that may stand in the other side of a comparison
// without it being a column.
var constants = map[string]bool{
	"ANY": true, "ALL": true, "SOME": true, "ARRAY": true, "NULL": true, "TRUE": true, "FALSE": true,
	"SUBPLAN": true, "INITPLAN": true, "HASHED": true,
}

// isConstant reports whether tokens reads no column: literals, parameters,
// casts, function calls over those, and sub-plans.
func isConstant(tokens []condToken) bool {
	for i, t := range tokens {
		if t.kind != condIdent {
			continue
		}
		if i+1 < len(tokens) && tokens[i+1].kind == condDot {
			return false
		}
		if i > 0 && tokens[i-1].kind == condDot {
			// The column of an InitPlan's result: `(InitPlan 1).col1`.
			continue
		}
		if t.quoted {
			return false
		}
		if constants[strings.ToUpper(t.text)] || (i+1 < len(tokens) && tokens[i+1].kind == condOpen) {
			continue
		}
		return false
	}
	return true
}

// leafUses reads one comparison.
func leafUses(tokens []condToken, aliases map[string]relationRef) []columnUse {
	tokens = unwrap(tokens)
	if len(tokens) == 0 || isWord(tokens[0], "NOT") {
		return nil
	}
	depth, at := 0, -1
	for i, t := range tokens {
		switch t.kind {
		case condOpen:
			depth++
		case condClose:
			depth--
		case condOp:
			if depth == 0 {
				at = i
			}
		case condIdent:
			if depth == 0 && isWord(t, "IS") {
				at = i
			}
		}
		if at >= 0 {
			break
		}
	}
	if at <= 0 || at == len(tokens)-1 {
		return nil
	}
	left, op, right := tokens[:at], tokens[at], tokens[at+1:]

	if isWord(op, "IS") {
		if column, ok := asColumn(left, aliases); ok && len(right) == 1 && isWord(right[0], "NULL") {
			column.use = UseEquality
			return []columnUse{column}
		}
		return nil
	}
	use := ""
	switch op.text {
	case "=":
		use = UseEquality
	case "<", "<=", ">", ">=":
		use = UseRange
	default:
		return nil
	}
	l, lok := asColumn(left, aliases)
	r, rok := asColumn(right, aliases)
	switch {
	case lok && rok:
		if use != UseEquality || l.relation == r.relation {
			return nil
		}
		l.use, r.use = UseJoin, UseJoin
		return []columnUse{l, r}
	case lok && isConstant(right):
		l.use = use
		return []columnUse{l}
	case rok && isConstant(left):
		r.use = use
		return []columnUse{r}
	}
	return nil
}

// --- candidates --------------------------------------------------------------

type tableUses struct {
	relation relationRef
	// Each is in the order the plan first used the column.
	equality, join, rng, sort []string
}

func appendOnce(list []string, name string) []string {
	for _, held := range list {
		if held == name {
			return list
		}
	}
	return append(list, name)
}

// candidatesFromUses proposes indexes for the columns a plan used. Per table
// there are at most two: one for its filters — the equality columns, then the
// range column, or the sort if there is no range, which is the order a b-tree
// can use them in — and one for its join columns.
func candidatesFromUses(uses []columnUse) []IndexCandidate {
	var order []relationRef
	byTable := map[relationRef]*tableUses{}
	for _, u := range uses {
		t := byTable[u.relation]
		if t == nil {
			t = &tableUses{relation: u.relation}
			byTable[u.relation] = t
			order = append(order, u.relation)
		}
		switch u.use {
		case UseEquality:
			t.equality = appendOnce(t.equality, u.column)
		case UseJoin:
			t.join = appendOnce(t.join, u.column)
		case UseRange:
			t.rng = appendOnce(t.rng, u.column)
		case UseSort:
			t.sort = appendOnce(t.sort, u.column)
		}
	}
	var out []IndexCandidate
	for _, relation := range order {
		t := byTable[relation]
		filter := IndexCandidate{Schema: relation.schema, Table: relation.table}
		add := func(c *IndexCandidate, column, use string) {
			for _, held := range c.Columns {
				if held == column {
					return
				}
			}
			if len(c.Columns) < maxCandidateColumns {
				c.Columns = append(c.Columns, column)
				c.Uses = append(c.Uses, use)
			}
		}
		for _, column := range t.equality {
			add(&filter, column, UseEquality)
		}
		if len(t.rng) > 0 {
			add(&filter, t.rng[0], UseRange)
		} else {
			for _, column := range t.sort {
				add(&filter, column, UseSort)
			}
		}
		if len(filter.Columns) > 0 {
			out = append(out, filter)
		}
		join := IndexCandidate{Schema: relation.schema, Table: relation.table}
		for _, column := range t.join {
			add(&join, column, UseJoin)
		}
		if len(join.Columns) > 0 && !sameColumns(join.Columns, filter.Columns) {
			out = append(out, join)
		}
	}
	return out
}

func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// indexServes reports whether an existing index already leads with the columns
// of a candidate. Equality columns may come in any order; a range or a sort
// column has to be where the candidate puts it.
func indexServes(index Index, candidate IndexCandidate) bool {
	if index.Invalid || index.Predicate != "" || index.Expression {
		return false
	}
	if index.Method != "" && !strings.EqualFold(index.Method, "btree") {
		return false
	}
	n := len(candidate.Columns)
	if len(index.Columns) < n {
		return false
	}
	lead := index.Columns[:n]
	ordered := false
	for _, use := range candidate.Uses {
		if use == UseRange || use == UseSort {
			ordered = true
		}
	}
	if ordered {
		return sameColumns(lead, candidate.Columns)
	}
	held := map[string]bool{}
	for _, column := range lead {
		held[column] = true
	}
	for _, column := range candidate.Columns {
		if !held[column] {
			return false
		}
	}
	return true
}
