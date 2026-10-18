package dbx

import "strings"

// StatementShape is a statement's text with every value replaced by a ?, for
// a record of what ran that must not keep what it ran with: an AI client's
// statement, written to the audit log as its shape, by the rule the statement
// list is held to — a literal is somebody's e-mail address.
//
// It reads the text with the query runner's own lexer for the engine, so what
// is a string here is what the runner and the engine took for one: a string of
// any kind, a dollar-quoted body and a number each become one ?, and a quoted
// name is kept, except MySQL's "…", which is a string unless the server says
// otherwise and so is masked. Comments are left out, since nothing the lexer
// skips can be said to be structure, and every run of space becomes one.
// Text the lexer refuses gives "": the runner refused it too, and a guess at
// its shape is the one answer that could keep a value.
func StatementShape(driver Driver, text string) string {
	tokens, err := lexSQL(lexRulesFor(driver), text)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	for i, t := range tokens {
		if i > 0 && t.start > tokens[i-1].end {
			b.WriteByte(' ')
		}
		if shapeMasks(driver, text, t) {
			b.WriteByte('?')
			continue
		}
		b.WriteString(text[t.start:t.end])
	}
	return clipStatementText(b.String(), 1000)
}

func shapeMasks(driver Driver, text string, t sqlToken) bool {
	switch t.kind {
	case tokNumber, tokDollar:
		return true
	case tokQuoted:
		return text[t.start] == '\'' || text[t.start] == '"' && driver == DriverMySQL
	}
	return false
}
