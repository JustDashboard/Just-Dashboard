package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// A flag is found in `jd help` and in the guide, or it is known only to
// whoever added it: the audit of 26 Nov 2026 found six that `jd help` never
// named and the guide missing the drafts commands. Nothing here is a list
// to keep up: the commands are read from dispatch, and each command's flags
// from the flag set it builds when asked for -h.

// A dispatched command: each word with its aliases, {{"vars", "variables"},
// {"rm", "remove"}} for `jd vars rm`.
type dispatched [][]string

func (d dispatched) String() string {
	words := make([]string, len(d))
	for i, names := range d {
		words[i] = names[0]
	}
	return strings.Join(words, " ")
}

// dispatchedCommands reads dispatch's switch: every case that hands its
// words to a method, and for one that goes through nested, each verb of the
// map it passes. help and version answer in place and build no flag set.
func dispatchedCommands(t *testing.T) []dispatched {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "dispatch" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("main.go has no dispatch")
	}
	var commands []dispatched
	ast.Inspect(body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		var names []string
		for _, expr := range clause.List {
			if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				name, _ := strconv.Unquote(lit.Value)
				names = append(names, name)
			}
		}
		call := handedOn(clause.Body)
		if len(names) == 0 || call == nil {
			return false
		}
		if call.Fun.(*ast.SelectorExpr).Sel.Name != "nested" {
			commands = append(commands, dispatched{names})
			return false
		}
		verbs, ok := call.Args[len(call.Args)-1].(*ast.CompositeLit)
		if !ok {
			t.Fatalf("jd %s: nested is not given a map literal", names[0])
		}
		// Aliases share a handler: "ls": a.varsList, "list": a.varsList.
		var handlers []string
		byHandler := map[string][]string{}
		for _, elt := range verbs.Elts {
			kv := elt.(*ast.KeyValueExpr)
			verb, _ := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
			handler := kv.Value.(*ast.SelectorExpr).Sel.Name
			if _, seen := byHandler[handler]; !seen {
				handlers = append(handlers, handler)
			}
			byHandler[handler] = append(byHandler[handler], verb)
		}
		for _, handler := range handlers {
			commands = append(commands, dispatched{names, byHandler[handler]})
		}
		return false
	})
	if len(commands) < 20 {
		t.Fatalf("read only %d commands from dispatch; has its shape changed?", len(commands))
	}
	return commands
}

// handedOn is the a.method(…) call a case returns, or nil when the case
// answers in place.
func handedOn(body []ast.Stmt) *ast.CallExpr {
	if len(body) != 1 {
		return nil
	}
	ret, ok := body[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "a" {
		return nil
	}
	return call
}

// helpFlags are the flags `jd COMMAND -h` lists: the flag set the command
// builds, the shared flags with it.
func helpFlags(t *testing.T, command dispatched) []string {
	t.Helper()
	var words []string
	for _, names := range command {
		words = append(words, names[0])
	}
	var stdout, stderr strings.Builder
	code := run(append(words, "-h"), strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
	if code != exitOK || !strings.HasPrefix(stdout.String(), "Usage: jd "+command.String()) {
		t.Fatalf("jd %s -h: exit %d\n%s%s", command, code, stdout.String(), stderr.String())
	}
	var flags []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if rest, ok := strings.CutPrefix(line, "  -"); ok {
			flags = append(flags, strings.FieldsFunc(rest, func(r rune) bool { return r == ' ' || r == '\t' })[0])
		}
	}
	if !slices.Contains(flags, "json") {
		t.Fatalf("jd %s -h listed %q, without the shared flags", command, flags)
	}
	return flags
}

// spelled is how the usage text and the guide write a flag: -f, --follow.
func spelled(flag string) string {
	if len(flag) == 1 {
		return "-" + flag
	}
	return "--" + flag
}

// mentions reports whether text names word on its own, so --ack is not
// found inside --ack-warnings, nor jd deploy inside jd deployments.
func mentions(text, word string) bool {
	return regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(word) + `($|[^\w-])`).MatchString(text)
}

// A usage entry is a line indented two spaces and the lines indented further
// under it; its synopsis is the part before the description's column.
type usageEntry struct {
	words []string
	text  string
}

// readUsage splits usageText into the commands' entries and what holds for
// every command: the shared flags' own lines, and the paragraphs (--env).
func readUsage() (entries []usageEntry, shared []string, general string) {
	var current *usageEntry
	for _, line := range strings.Split(usageText, "\n") {
		switch {
		case strings.HasPrefix(line, "   ") && current != nil:
			current.text += "\n" + line
		case strings.HasPrefix(line, "  -"):
			current = nil
			shared = append(shared, strings.Fields(line)[0])
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   "):
			// The words a command is spelled with: "canary promote|abort APP"
			// is canary, promote and abort.
			synopsis, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
			var words []string
			for _, word := range strings.FieldsFunc(synopsis, func(r rune) bool { return strings.ContainsRune(" |[],", r) }) {
				if word == strings.ToLower(word) && !strings.HasPrefix(word, "-") {
					words = append(words, word)
				}
			}
			entries = append(entries, usageEntry{words: words, text: line})
			current = &entries[len(entries)-1]
		default:
			current = nil
			general += line + "\n"
		}
	}
	return entries, shared, general
}

// Every flag a command registers is in `jd help`, on that command's own
// entry unless it holds for every command.
func TestEveryFlagIsInTheUsageText(t *testing.T) {
	entries, shared, general := readUsage()
	for _, command := range dispatchedCommands(t) {
		var own []usageEntry
		for _, entry := range entries {
			if len(entry.words) == 0 || !slices.Contains(command[0], entry.words[0]) {
				continue
			}
			if len(command) == 1 || slices.ContainsFunc(command[1], func(verb string) bool {
				return slices.Contains(entry.words[1:], verb)
			}) {
				own = append(own, entry)
			}
		}
		if len(own) == 0 {
			t.Errorf("jd help has no line for jd %s", command)
			continue
		}
		for _, flag := range helpFlags(t, command) {
			if slices.Contains(shared, spelled(flag)) || mentions(general, spelled(flag)) {
				continue
			}
			if !slices.ContainsFunc(own, func(entry usageEntry) bool { return mentions(entry.text, spelled(flag)) }) {
				t.Errorf("jd %s registers %s, which its lines in jd help do not name", command, spelled(flag))
			}
		}
	}
}

// namesCommand reports whether text names the command as it is typed, by
// any of its aliases: jd vars set, jd variables set.
func namesCommand(text string, command dispatched) bool {
	for _, name := range command[0] {
		if len(command) == 1 {
			if mentions(text, "jd "+name) {
				return true
			}
			continue
		}
		for _, verb := range command[1] {
			if mentions(text, "jd "+name+" "+verb) {
				return true
			}
		}
	}
	return false
}

// flagRows are the rows of the guide's Flags table: the commands a row is
// for, and the flags it names for them.
func flagRows(t *testing.T, guide string) [][2]string {
	t.Helper()
	_, table, ok := strings.Cut(guide, "\n## Flags\n")
	if !ok {
		t.Fatal("docs/guides/cli.md has no Flags section")
	}
	table, _, _ = strings.Cut(table, "\n## ")
	var rows [][2]string
	for _, line := range strings.Split(table, "\n") {
		if cells := strings.Split(line, "|"); len(cells) == 4 && strings.TrimSpace(cells[0]) == "" {
			rows = append(rows, [2]string{cells[1], cells[2]})
		}
	}
	return rows
}

// The guide names every command and every flag, so what `jd help` lists is
// in the documentation too, and a command's own flags on its row of the
// Flags table, as `jd help` names them on its own lines.
func TestTheGuideNamesEveryCommandAndFlag(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/guides/cli.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(raw)
	_, shared, general := readUsage()
	rows := flagRows(t, guide)
	missing := map[string]bool{}
	for _, command := range dispatchedCommands(t) {
		if !namesCommand(guide, command) {
			t.Errorf("docs/guides/cli.md does not name jd %s", command)
		}
		var own []string
		for _, row := range rows {
			if namesCommand(row[0], command) {
				own = append(own, row[1])
			}
		}
		for _, flag := range helpFlags(t, command) {
			// A shared flag is reported once, not for every command.
			if !mentions(guide, spelled(flag)) && !missing[flag] {
				missing[flag] = true
				t.Errorf("docs/guides/cli.md does not name %s (jd %s has it)", spelled(flag), command)
			}
			if slices.Contains(shared, spelled(flag)) || mentions(general, spelled(flag)) {
				continue
			}
			if !slices.ContainsFunc(own, func(cell string) bool { return mentions(cell, spelled(flag)) }) {
				t.Errorf("docs/guides/cli.md's Flags table has no row for jd %s that names %s", command, spelled(flag))
			}
		}
	}
}
