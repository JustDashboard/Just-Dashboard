package dbx

import (
	"encoding/json"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A database's names and comments are data, and on a shared server they are
// somebody else's data. A table can be called anything a quoted identifier can
// hold — a newline included — and a comment can say `*/`. Generated source is
// read and then run, so nothing taken from the catalogue may be able to end the
// string, the comment or the identifier it was written into and continue as
// code.
//
// The fixtures below are schemas where every name, comment, label, type and
// default tries to. Each attempt ends one kind of literal or comment and then
// calls ormHostileCall, and the test reads every generated file with a lexer
// for its language: the file must lex to the end, and the call must never be
// found outside a string or a comment.

// ormHostileCall is what an escaped value would run. The argument is there so
// the call cannot be confused with an identifier a generator made out of a
// hostile name, which keeps the letters and loses the parentheses.
const ormHostileCall = "(1337)"

type ormHostileMode int

const (
	// ormHostileControl is every way out, line breaks included. No engine's
	// identifier quoting accepts those names, so the SQL target leaves the
	// tables out.
	ormHostileControl ormHostileMode = iota
	// ormHostilePrintable is every way out that needs no control character: the
	// names can be quoted, so the SQL target has to write them.
	ormHostilePrintable
	// ormHostileBenign has the same shape and nothing hostile in it. A script
	// generated from it has the number of statements the hostile one must have.
	ormHostileBenign
)

// ormHostileValues are the enum's labels, and between them the ways out of
// every literal and comment the generators write.
func ormHostileValues(mode ormHostileMode) []string {
	all := []string{
		"plain",
		"new\nINJECTED_NL(1337)",
		"cr\rINJECTED_CR(1337)",
		"ls\u2028INJECTED_LS(1337)",
		`dq"); INJECTED_DQ(1337); ("`,
		`sq'); INJECTED_SQ(1337); ('`,
		"bt`); INJECTED_BT(1337); (`",
		"tpl${INJECTED_TPL(1337)}",
		"php{$x->INJECTED_PHP(1337)}",
		// A line break is what makes the PHP writer choose double quotes, and
		// inside those a dollar sign interpolates.
		"both\n{$x->INJECTED_BOTH(1337)} ${y}",
		"*/ INJECTED_BLOCK(1337) /*",
		"$$; INJECTED_DOLLAR(1337); --",
		"$jd$; INJECTED_TAG(1337); --",
		"?> INJECTED_CLOSE(1337) <?php",
		`""" INJECTED_TDQ(1337) """`,
		"''' INJECTED_TSQ(1337) '''",
		// A backslash that is not escaped takes the closing quote with it, and
		// the label after it is then outside the string.
		`back\`,
		`, INJECTED_BSDQ(1337), "`,
		`back2\`,
		`, INJECTED_BSSQ(1337), '`,
		"café",
		"",
	}
	out := make([]string, 0, len(all))
	for i, v := range all {
		switch {
		case mode == ormHostileBenign:
			out = append(out, fmt.Sprintf("v%d", i))
		case mode == ormHostilePrintable && strings.ContainsAny(v, "\n\r"):
		default:
			out = append(out, v)
		}
	}
	return out
}

// ormHostileName is a name that tries to leave whatever it is written into.
// The printable form has a call after each delimiter in turn, so whichever one
// a generator fails to escape, the call that follows it is outside. It stays
// within the 128 characters an identifier may have.
func ormHostileName(mode ormHostileMode, base string) string {
	marker := "INJECTED_" + strings.ToUpper(base)
	switch mode {
	case ormHostileBenign:
		return base + "_x"
	case ormHostilePrintable:
		name := base + " " + marker
		for _, way := range []string{`"`, `'`, "`", "]", "$$", "*/", "?>", `\"`, "--"} {
			name += " " + way + "X" + ormHostileCall + ";"
		}
		return name
	}
	return base + "\n" + marker + ormHostileCall
}

func ormHostileFixture(driver Driver, mode ormHostileMode) *ORMSchema {
	s := &ORMSchema{Driver: driver, Detailed: true}
	values := ormHostileValues(mode)
	prose := strings.Join(values, " ")

	// A string default, as each engine's catalogue prints one.
	def := func(v string) func(*ORMColumn) {
		if driver == DriverMySQL {
			// Bare: MySQL prints the value and nothing else.
			return ormDef(v)
		}
		lit := ormSQLString(v)
		if driver == DriverPostgres {
			lit += "::text"
		}
		return ormDef(lit)
	}

	enumName := ormHostileName(mode, "kind")
	enumType := "text"
	switch driver {
	case DriverPostgres:
		s.Enums = []ORMEnum{{Schema: "public", Name: enumName, Values: values}}
		enumType = "USER-DEFINED"
	case DriverMySQL:
		// As MySQL prints a column type: backslashes escaped, quotes doubled.
		labels := make([]string, 0, len(values))
		for _, v := range values {
			if v != "" {
				labels = append(labels, ormMySQLString(v))
			}
		}
		enumType = "enum(" + strings.Join(labels, ",") + ")"
	}
	kind := ormColumn("kind", enumType, ormNote(prose))
	if driver == DriverPostgres {
		kind.EnumSchema, kind.EnumName = "public", enumName
	}
	odd := ormColumn("odd", "weird; "+ormHostileName(mode, "type"), ormNull)
	if mode == ormHostileBenign {
		odd.Type = "weird"
	}

	bad := ormHostileName(mode, "bad")
	things := ORMTable{
		Schema: "public", Name: "things", Kind: ORMKindTable,
		Comment: prose,
		Columns: []ORMColumn{
			ormColumn("id", "integer"),
			kind,
			ormColumn(ormHostileName(mode, "col"), "text", ormNull, ormNote(prose)),
			ormColumn("note", "text", def("x")),
			ormColumn("owner_id", "integer", ormNull),
			// Names a language keeps for itself without reserving them.
			ormColumn("__proto__", "text", ormNull),
			ormColumn("constructor", "text", ormNull),
			ormColumn("__tablename__", "text", ormNull),
			ormColumn("__table_args__", "text", ormNull),
		},
		PrimaryKey: []string{"id"},
		Indexes: []ORMIndex{
			ormPK("things_pkey", "id"),
			ormUnique(ormHostileName(mode, "uq"), "owner_id", "id"),
			ormIndex(ormHostileName(mode, "ix"), "owner_id"),
		},
		ForeignKeys: []ForeignKey{
			ormFK(ormHostileName(mode, "fk"), []string{"owner_id"}, "public", bad, []string{"id"}, "CASCADE"),
		},
	}
	// Two columns for each way out, defaulting to it: one where the default is
	// a string to the target, and one — a date — where it is an expression the
	// target has to pass through as SQL.
	for i, v := range values {
		things.Columns = append(things.Columns,
			ormColumn(fmt.Sprintf("d%d", i), "text", ormNull, def(v)),
			ormColumn(fmt.Sprintf("e%d", i), "date", ormNull, def(v)))
	}
	// And a computed column whose expression holds them all.
	things.Columns = append(things.Columns,
		ormColumn("computed", "text", ormNull, ormGenerated("lower("+ormSQLString(prose)+")")))
	s.Tables = []ORMTable{
		things,
		{
			Schema: "public", Name: bad, Kind: ORMKindTable,
			Comment:    prose,
			Columns:    []ORMColumn{ormColumn("id", "integer"), ormColumn("class", "text", ormNull)},
			PrimaryKey: []string{"id"},
			Indexes:    []ORMIndex{ormPK("bad_pkey", "id")},
		},
		// The type that cannot be copied into SQL has a table to itself, so
		// leaving it out takes nothing else with it.
		{
			Schema: "public", Name: "typed", Kind: ORMKindTable,
			Columns:    []ORMColumn{ormColumn("id", "integer"), odd},
			PrimaryKey: []string{"id"},
			Indexes:    []ORMIndex{ormPK("typed_pkey", "id")},
		},
	}
	if driver == DriverPostgres {
		// A second schema, hostile by name, with a table named like one in the
		// first: schema handles and qualified names are written too.
		other := ormHostileName(mode, "sch")
		s.Tables = append(s.Tables, ORMTable{
			Schema: other, Name: "things", Kind: ORMKindTable,
			Columns:    []ORMColumn{ormColumn("id", "integer"), ormColumn("thing_id", "integer", ormNull)},
			PrimaryKey: []string{"id"},
			Indexes:    []ORMIndex{ormPK("things_pkey", "id")},
			ForeignKeys: []ForeignKey{
				ormFK(ormHostileName(mode, "fk2"), []string{"thing_id"}, "public", "things", []string{"id"}, "NO ACTION"),
			},
		})
	} else {
		for i := range s.Tables {
			s.Tables[i].Schema = map[Driver]string{DriverMySQL: "db", DriverSQLite: "main"}[driver]
			for j := range s.Tables[i].ForeignKeys {
				s.Tables[i].ForeignKeys[j].RefSchema = s.Tables[i].Schema
			}
		}
	}
	return s
}

// --- reading the output back ------------------------------------------------

// ormLexString is one kind of string literal a language has.
type ormLexString struct {
	open, close string
	// backslash: a backslash takes the next character with it.
	backslash bool
	// doubled: the closing delimiter twice is one of it, not the end.
	doubled bool
	// multiline: a line break inside it is not an error.
	multiline bool
	// template: ${ inside it starts code (a JavaScript template literal).
	template bool
	// dollar: an unescaped $ inside it interpolates (PHP's double quotes).
	dollar bool
}

type ormLexLang struct {
	line  []string
	block [2]string
	strs  []ormLexString // longer openers first
	// dollarQuotes is PostgreSQL's $tag$ … $tag$. The generators use it for
	// one thing, a DO block, whose body is code and is read as code.
	dollarQuotes bool
	// php: `?>` ends the program wherever it is not inside a string or a block
	// comment, a line comment included, and `#[` opens an attribute.
	php bool
}

var (
	ormLexDQ = ormLexString{open: `"`, close: `"`, backslash: true}
	ormLexSQ = ormLexString{open: `'`, close: `'`, backslash: true}
)

func ormLexLangFor(filename string, driver Driver) (ormLexLang, bool) {
	c := [2]string{"/*", "*/"}
	switch filepath.Ext(filename) {
	case ".ts":
		return ormLexLang{line: []string{"//"}, block: c, strs: []ormLexString{
			ormLexDQ, ormLexSQ, {open: "`", close: "`", backslash: true, multiline: true, template: true},
		}}, true
	case ".prisma":
		return ormLexLang{line: []string{"//"}, strs: []ormLexString{ormLexDQ}}, true
	case ".graphql":
		return ormLexLang{line: []string{"#"}, strs: []ormLexString{
			{open: `"""`, close: `"""`, backslash: true, multiline: true}, ormLexDQ,
		}}, true
	case ".py":
		return ormLexLang{line: []string{"#"}, strs: []ormLexString{
			{open: `"""`, close: `"""`, backslash: true, multiline: true},
			{open: `'''`, close: `'''`, backslash: true, multiline: true},
			ormLexDQ, ormLexSQ,
		}}, true
	case ".php":
		return ormLexLang{line: []string{"//", "#"}, block: c, php: true, strs: []ormLexString{
			{open: `'`, close: `'`, backslash: true, multiline: true},
			{open: `"`, close: `"`, backslash: true, multiline: true, dollar: true},
		}}, true
	case ".rs":
		// A single quote opens a character or a lifetime in Rust, never a
		// string, and the generator writes neither from catalogue text.
		return ormLexLang{line: []string{"//"}, block: c, strs: []ormLexString{
			{open: `"`, close: `"`, backslash: true, multiline: true},
		}}, true
	case ".sql":
		sq := ormLexString{open: `'`, close: `'`, doubled: true, multiline: true}
		dq := ormLexString{open: `"`, close: `"`, doubled: true, multiline: true}
		bt := ormLexString{open: "`", close: "`", doubled: true, multiline: true}
		switch driver {
		case DriverPostgres:
			return ormLexLang{line: []string{"--"}, block: c, dollarQuotes: true, strs: []ormLexString{sq, dq}}, true
		case DriverMySQL:
			sq.backslash, dq.backslash = true, true
			return ormLexLang{line: []string{"--", "#"}, block: c, strs: []ormLexString{sq, dq, bt}}, true
		case DriverSQLite:
			return ormLexLang{line: []string{"--"}, block: c, strs: []ormLexString{
				sq, dq, bt, {open: "[", close: "]", multiline: true},
			}}, true
		}
	}
	return ormLexLang{}, false
}

// ormLex reads source and returns what is left of it once strings and comments
// are taken out, with whatever stopped it reading the file as that language.
func ormLex(lang ormLexLang, src string) (code string, problems []string) {
	var b strings.Builder
	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	lineOf := func(i int) int { return strings.Count(src[:i], "\n") + 1 }
	// Every character one of these languages ends a line comment at.
	lineEnd := func(s string) int {
		if i := strings.IndexAny(s, "\n\r\u2028\u2029"); i >= 0 {
			return i
		}
		return len(s)
	}
	word := func(c byte) bool {
		return c == '_' || c == '$' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}

scan:
	for i := 0; i < len(src); {
		rest := src[i:]
		if lang.php && strings.HasPrefix(rest, "?>") {
			problem("line %d: ?> outside a string ends the PHP program", lineOf(i))
		}
		for _, lc := range lang.line {
			if !strings.HasPrefix(rest, lc) || (lang.php && strings.HasPrefix(rest, "#[")) {
				continue
			}
			end := lineEnd(rest)
			if lang.php && strings.Contains(rest[:end], "?>") {
				problem("line %d: ?> inside a line comment ends the PHP program", lineOf(i))
			}
			i += end
			continue scan
		}
		if lang.block[0] != "" && strings.HasPrefix(rest, lang.block[0]) {
			end := strings.Index(rest[len(lang.block[0]):], lang.block[1])
			if end < 0 {
				problem("line %d: a block comment is never closed", lineOf(i))
				break
			}
			i += len(lang.block[0]) + end + len(lang.block[1])
			continue
		}
		if lang.dollarQuotes && rest[0] == '$' && (i == 0 || !word(src[i-1])) {
			j := 1
			for j < len(rest) && word(rest[j]) && rest[j] != '$' {
				j++
			}
			if j < len(rest) && rest[j] == '$' {
				tag := rest[:j+1]
				end := strings.Index(rest[len(tag):], tag)
				if end < 0 {
					problem("line %d: the %s quote is never closed", lineOf(i), tag)
					break
				}
				body, inner := ormLex(lang, rest[len(tag):len(tag)+end])
				for _, p := range inner {
					problem("inside the %s block at line %d: %s", tag, lineOf(i), p)
				}
				b.WriteString(" " + body + " ")
				i += len(tag) + end + len(tag)
				continue
			}
		}
		for _, st := range lang.strs {
			if !strings.HasPrefix(rest, st.open) {
				continue
			}
			start := i
			i += len(st.open)
			closed := false
			for i < len(src) {
				switch {
				case st.backslash && src[i] == '\\':
					i += 2
					continue
				case strings.HasPrefix(src[i:], st.close):
					if st.doubled && strings.HasPrefix(src[i+len(st.close):], st.close) {
						i += 2 * len(st.close)
						continue
					}
					i += len(st.close)
					closed = true
				case !st.multiline && (src[i] == '\n' || src[i] == '\r'):
					problem("line %d: a line break inside a %s string", lineOf(start), st.open)
					closed = true
				case st.template && strings.HasPrefix(src[i:], "${"):
					problem("line %d: ${ inside a template literal runs what follows it", lineOf(i))
					i++
					continue
				case st.dollar && src[i] == '$':
					problem("line %d: $ inside a double-quoted PHP string interpolates", lineOf(i))
					i++
					continue
				default:
					i++
					continue
				}
				break
			}
			if !closed {
				problem("line %d: a %s string is never closed", lineOf(start), st.open)
			}
			b.WriteString(`""`)
			continue scan
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String(), problems
}

// ormLexGo is ormLex for Go, read by Go's own scanner.
func ormLexGo(src string) (code string, problems []string) {
	fset := token.NewFileSet()
	file := fset.AddFile("models.go", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), func(pos token.Position, msg string) {
		problems = append(problems, fmt.Sprintf("line %d: %s", pos.Line, msg))
	}, 0)
	var b strings.Builder
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		switch {
		case tok == token.STRING || tok == token.CHAR:
			b.WriteString(`""`)
		case lit != "":
			b.WriteString(lit)
		default:
			b.WriteString(tok.String())
		}
	}
	return b.String(), problems
}

// ormHostileCheck reads one generated file as its language and reports what a
// value from the database managed to do to it.
func ormHostileCheck(t *testing.T, driver Driver, target ORMTarget, f ORMFile) {
	t.Helper()
	if f.Filename != filepath.Base(f.Filename) || strings.ContainsAny(f.Filename, "\n\r/\\") {
		t.Errorf("file name %q is not a bare, single-line name", f.Filename)
	}
	var code string
	var problems []string
	switch ext := filepath.Ext(f.Filename); ext {
	case ".json":
		if !json.Valid([]byte(f.Content)) {
			t.Errorf("%s is not JSON:\n%s", f.Filename, f.Content)
		}
		return
	case ".go":
		code, problems = ormLexGo(f.Content)
		if _, err := format.Source([]byte(f.Content)); err != nil {
			problems = append(problems, "does not parse: "+err.Error())
		}
		if target == ORMGoStructs {
			if err := ormGoTypeChecks(f.Content); err != nil {
				problems = append(problems, "does not type-check: "+err.Error())
			}
		}
	default:
		lang, ok := ormLexLangFor(f.Filename, driver)
		if !ok {
			t.Fatalf("%s: no lexer for %s files", f.Filename, ext)
		}
		code, problems = ormLex(lang, f.Content)
	}
	if strings.Contains(code, ormHostileCall) {
		problems = append(problems, "a value from the database is outside every string and comment")
	}
	if len(problems) > 0 {
		t.Errorf("%s: %s\n%s", f.Filename, strings.Join(problems, "\n"), f.Content)
	}
}

// ormHostileVariants are the option sets that change how names and values are
// written: the default, camelCase names, one file per model, and the guarded
// form of the SQL script.
func ormHostileVariants(target ORMTarget) map[string]ORMOptions {
	out := map[string]ORMOptions{}
	for name, req := range map[string]ORMRequest{
		"default":     {Target: target},
		"camel":       {Target: target, Naming: ORMNamingCamel},
		"split":       {Target: target, Split: ormYes()},
		"ifNotExists": {Target: target, IfNotExists: ormYes()},
		"views":       {Target: target, Views: ormYes()},
	} {
		// A target without the option is covered by its default variant.
		if opts, err := req.Options(); err == nil {
			out[name] = opts
		}
	}
	return out
}

func TestORMGeneratedCodeCannotBeInjected(t *testing.T) {
	python, _ := exec.LookPath("python3")
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite} {
		for _, target := range ORMTargets() {
			if ORMUnsupported(target, driver) != "" {
				continue
			}
			variants := ormHostileVariants(target)
			names := make([]string, 0, len(variants))
			for name := range variants {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, variant := range names {
				for mode, label := range map[ormHostileMode]string{ormHostileControl: "control", ormHostilePrintable: "printable"} {
					opts := variants[variant]
					t.Run(fmt.Sprintf("%s/%s/%s/%s", driver, target, variant, label), func(t *testing.T) {
						res, err := GenerateORMFiles(ormHostileFixture(driver, mode), opts)
						if err != nil {
							t.Fatalf("generate: %v", err)
						}
						for _, f := range res.Files {
							ormHostileCheck(t, driver, target, f)
							if strings.HasSuffix(f.Filename, ".py") && python != "" {
								path := filepath.Join(t.TempDir(), "models.py")
								if err := os.WriteFile(path, []byte(f.Content), 0o644); err != nil {
									t.Fatal(err)
								}
								cmd := exec.Command(python, "-B", "-c", "import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')", path)
								if out, err := cmd.CombinedOutput(); err != nil {
									t.Errorf("%s does not compile: %v\n%s\n%s", f.Filename, err, out, f.Content)
								}
							}
						}
					})
				}
			}
		}
	}
}

// The lexers above are the test's own, so they are shown the breakouts they
// exist to catch: a check that passes everything proves nothing.
func TestORMHostileLexerCatchesBreakouts(t *testing.T) {
	for _, c := range []struct {
		name, file string
		driver     Driver
		src        string
	}{
		{"a quote that ends a TypeScript string", "a.ts", "", `const a = ["x"); INJECTED(1337); ("y"]`},
		{"a newline that ends a line comment", "a.ts", "", "// about\nINJECTED(1337)\n"},
		{"a line separator that ends a line comment", "a.ts", "", "// about\u2028INJECTED(1337)\n"},
		{"a block comment closed early", "a.ts", "", "/** a */ INJECTED(1337) /* b */"},
		{"an interpolation in a template", "a.ts", "", "const a = sql`${INJECTED(1337)}`"},
		{"a backslash that takes the closing quote", "a.ts", "", `const a = ["back\", ", INJECTED(1337), ""]`},
		{"a line break inside a string", "a.prisma", "", "model a {\n  b String @default(\"x\ny\")\n}"},
		{"a block string closed early", "a.graphql", "", `""" a """ INJECTED(1337) """ b """`},
		{"a triple-quoted Python string closed early", "a.py", "", `"""a""" + INJECTED(1337) + """b"""`},
		{"a PHP close tag in a line comment", "a.php", "", "<?php\n// a ?> b\n"},
		{"a PHP interpolation", "a.php", "", "<?php\n$a = \"x{$b->c(1)}\";\n"},
		{"a Rust string closed early", "a.rs", "", `#[diesel(postgres_type(name = "x"))] INJECTED(1337); #[x = ""]`},
		{"a dollar quote closed by its body", "a.sql", DriverPostgres,
			"DO $$ BEGIN\n  CREATE TYPE k AS ENUM ('a', 'x$$; INJECTED(1337); --');\nEND $$;"},
		{"a MySQL string ended by a backslash", "a.sql", DriverMySQL, `CREATE TABLE t (a text DEFAULT 'x\', b text DEFAULT ', INJECTED(1337), ''');`},
		{"a quoted identifier closed early", "a.sql", DriverPostgres, `CREATE TABLE "a"; INJECTED(1337); --" (id integer);`},
	} {
		lang, ok := ormLexLangFor(c.file, c.driver)
		if !ok {
			t.Fatalf("%s: no lexer", c.name)
		}
		code, problems := ormLex(lang, c.src)
		if len(problems) == 0 && !strings.Contains(code, ormHostileCall) {
			t.Errorf("%s was not caught:\n%s", c.name, c.src)
		}
	}
	if code, problems := ormLexGo("package a\n\nvar b = \"x\"; INJECTED(1337); var c = \"\"\n"); len(problems) != 0 || !strings.Contains(code, ormHostileCall) {
		t.Errorf("a Go string closed early was not caught (%v)", problems)
	}
	// And what is escaped properly is not a breakout.
	for file, src := range map[string]string{
		"a.ts":      `const a = ["x\"); INJECTED(1337); (\"y", "back\\", 'it\'s'] // INJECTED(1337)` + "\nconst b = sql`a \\${INJECTED(1337)} \\``\n",
		"a.py":      `a = "x\"\"\" INJECTED(1337)"  # INJECTED(1337)` + "\n",
		"a.php":     "<?php\n/* ?> INJECTED(1337) */\n$a = 'it\\'s ?> {$x} INJECTED(1337)';\n$b = \"\\$x INJECTED(1337)\";\n",
		"a.rs":      `#[diesel(postgres_type(name = "x\"; INJECTED(1337); \""))] struct A<'a>(&'a str);`,
		"a.graphql": `"""a \""" INJECTED(1337)""" type A { b: String } # INJECTED(1337)`,
	} {
		lang, _ := ormLexLangFor(file, "")
		if code, problems := ormLex(lang, src); len(problems) != 0 || strings.Contains(code, ormHostileCall) {
			t.Errorf("%s: escaped text was taken for a breakout (%v):\n%s", file, problems, src)
		}
	}
	pg, _ := ormLexLangFor("a.sql", DriverPostgres)
	if code, problems := ormLex(pg, "DO $jd$ BEGIN\n  CREATE TYPE \"k\"\"\" AS ENUM ('it''s', 'x$$; INJECTED(1337); --');\nEND $jd$;"); len(problems) != 0 || strings.Contains(code, ormHostileCall) {
		t.Errorf("a well-quoted DO block was taken for a breakout (%v)", problems)
	}
}

// The SQL target has a harder job than the others: what it writes is run
// against a database, by an operator who trusted the dashboard for it. Every
// name here can be quoted, so every table is written, and the script must still
// be the statements a harmless schema of the same shape produces — no more.
func TestORMSQLScriptHasNoStatementItDidNotWrite(t *testing.T) {
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite} {
		for _, guard := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ifNotExists=%t", driver, guard), func(t *testing.T) {
				req := ORMRequest{Target: ORMSQL}
				if guard {
					req.IfNotExists = ormYes()
				}
				hostile := ormGenerate(t, ormHostileFixture(driver, ormHostilePrintable), req)
				benign := ormGenerate(t, ormHostileFixture(driver, ormHostileBenign), req)

				got := splitSQLStatements(driver, hostile.Schema)
				want := splitSQLStatements(driver, benign.Schema)
				// The one table whose type is not a type is left out, and said.
				if left := len(want) - len(got); left != 1 {
					t.Errorf("the hostile script has %d statements and the harmless one %d; exactly one table should be missing\n%s",
						len(got), len(want), hostile.Schema)
				}
				ormMustContain(t, "warnings", strings.Join(hostile.Warnings, "\n"), "has a type that cannot be copied into a statement")
				ormMustNotContain(t, "schema.sql", hostile.Schema, "INJECTED_TYPE")
				// Everything else was written, hostile names and all.
				ormMustContain(t, "schema.sql", hostile.Schema, "INJECTED_BAD", "INJECTED_COL")

				if driver != DriverPostgres {
					return
				}
				if !guard {
					ormMustNotContain(t, "schema.sql", hostile.Schema, "DO $")
					return
				}
				ormMustContain(t, "schema.sql", hostile.Schema, "INJECTED_FK", "INJECTED_KIND", "INJECTED_SCH")
				// A DO block's quote is opened once and closed once.
				blocks := 0
				for _, stmt := range strings.Split(hostile.Schema, "\n\n") {
					stmt = strings.TrimSpace(stmt)
					if !strings.HasPrefix(stmt, "DO $") {
						continue
					}
					blocks++
					tag := stmt[len("DO "):strings.Index(stmt, " BEGIN")]
					if n := strings.Count(stmt, tag); n != 2 || !strings.HasSuffix(stmt, "END "+tag+";") {
						t.Errorf("the %s quote appears %d times in its own block:\n%s", tag, n, stmt)
					}
				}
				// The enum type and both foreign keys.
				if blocks != 3 {
					t.Errorf("%d DO blocks, want 3\n%s", blocks, hostile.Schema)
				}
				ormMustContain(t, "schema.sql", hostile.Schema, "DO $jd1$ BEGIN\n  CREATE TYPE")
			})
		}
	}
}

func TestPostgresDollarTagIsNotInTheBody(t *testing.T) {
	for body, want := range map[string]string{
		"CREATE TYPE k AS ENUM ('a')":          "$$",
		"CREATE TYPE k AS ENUM ('price$')":     "$$",
		"CREATE TYPE k AS ENUM ('a$$b')":       "$jd$",
		"CREATE TYPE k AS ENUM ('$$', '$jd$')": "$jd1$",
		"ADD CONSTRAINT \"$$ $jd$ $jd1$\"":     "$jd2$",
	} {
		if got := postgresDollarTag(body); got != want || strings.Contains(body, got) {
			t.Errorf("postgresDollarTag(%q) = %q, want %q", body, got, want)
		}
	}
}

// A name the engine's own quoting refuses — one with a control character —
// cannot be written into SQL at all, so the SQL target leaves the table out
// and says so rather than emitting a statement it cannot vouch for.
func TestORMSQLLeavesOutWhatItCannotQuote(t *testing.T) {
	opts, _ := ORMRequest{Target: ORMSQL}.Options()
	res, err := GenerateORMFiles(ormHostileFixture(DriverPostgres, ormHostileControl), opts)
	if err != nil {
		t.Fatal(err)
	}
	ormMustNotContain(t, "schema.sql", res.Schema, "INJECTED_BAD", "INJECTED_COL", "INJECTED_IX", "INJECTED_FK", "INJECTED_KIND", "INJECTED_SCH")
	ormMustContain(t, "warnings", strings.Join(res.Warnings, "\n"), "cannot be quoted safely and was left out")
	// And what was left out is not counted as written: every table here has a
	// name, a column or a type that could not be, and so has the enum.
	if res.Counts != (ORMCounts{}) || strings.Contains(res.Schema, "CREATE T") {
		t.Errorf("counts = %+v for a script that creates nothing:\n%s", res.Counts, res.Schema)
	}
}

func TestSQLFragment(t *testing.T) {
	for text, want := range map[string]bool{
		"integer":                      true,
		"character varying(20)":        true,
		"timestamp(3) with time zone":  true,
		`"My;Type"`:                    true,
		`"Geo"."point--3d"[]`:          true,
		"enum('a;b','it''s')":          true,
		`Enum8('it\'s; x' = 1)`:        true,
		"integer; DROP TABLE t":        false,
		"integer -- rest":              false,
		"integer /* rest":              false,
		"integer\nDROP":                false,
		`"never closed`:                false,
		"enum('a') ; DROP TABLE t; --": false,
	} {
		if got := sqlFragment(text); got != want {
			t.Errorf("sqlFragment(%q) = %v, want %v", text, got, want)
		}
	}
}
