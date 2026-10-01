package dbx

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

// Names.
//
// A database name can be anything — "Mixed Case Table", "select", "2fa" — and
// each target has its own idea of what an identifier is and which words it
// keeps for itself. The rules below turn one into the other. They never lose
// the original: every target that renames also writes the mapping back (@map,
// name:, db_column, a struct tag), so the generated code still addresses the
// column the database actually has.

// sanitizeIdent replaces characters an identifier cannot hold (anything but
// letters, digits and underscore; a leading digit) so the generated file
// parses; the original name is preserved through a mapping at the call site
// when it differs.
func sanitizeIdent(name string) string {
	var b strings.Builder
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "field"
	}
	return s
}

func camelLower(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// lowerFirst is camelLower for a name that has a case to lower into. A name in
// capitals throughout (Oracle's CUSTOMER) is left alone: cUSTOMER is not a
// spelling anybody chose.
func lowerFirst(s string) string {
	if s == strings.ToUpper(s) {
		return s
	}
	return camelLower(s)
}

// ormIdentWords splits a name into the words a casing rule rearranges: on
// anything that is not a letter or digit, and at a lower-to-upper boundary so
// "orderItems" is two words like "order_items".
func ormIdentWords(name string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	var prev rune
	for _, r := range name {
		ok := r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
		if !ok {
			flush()
			prev = 0
			continue
		}
		if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
			flush()
		}
		b.WriteRune(r)
		prev = r
	}
	flush()
	return words
}

// pascal renders a table name as a type name: user_profiles -> UserProfiles.
func pascal(name string) string {
	parts := strings.FieldsFunc(sanitizeIdent(name), func(r rune) bool { return r == '_' })
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if len(p) > 1 && p == strings.ToUpper(p) && p != strings.ToLower(p) {
			// A shouted name, as Oracle stores them: CUSTOMERS is one word,
			// not an acronym to keep in capitals.
			p = strings.ToLower(p)
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	if b.Len() == 0 {
		return "Row"
	}
	s := b.String()
	if s[0] >= '0' && s[0] <= '9' {
		// A type name cannot open with a digit any more than a variable can.
		return "_" + s
	}
	return s
}

// ormCamel renders a name in lowerCamelCase: created_at -> createdAt. A word that
// is all capitals is treated as an acronym and lowered whole when it leads, so
// ID becomes id rather than iD.
func ormCamel(name string) string {
	words := ormIdentWords(name)
	var b strings.Builder
	for i, w := range words {
		switch {
		case i == 0 && w == strings.ToUpper(w):
			b.WriteString(strings.ToLower(w))
		case i == 0:
			b.WriteString(strings.ToLower(w[:1]) + w[1:])
		default:
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	s := b.String()
	if s == "" {
		return "field"
	}
	if s[0] >= '0' && s[0] <= '9' {
		return "_" + s
	}
	return s
}

// ormSnake renders a name in lower_snake_case, the way Python and Rust spell a
// member.
func ormSnake(name string) string {
	words := ormIdentWords(name)
	for i, w := range words {
		words[i] = strings.ToLower(w)
	}
	s := strings.Join(words, "_")
	if s == "" {
		return "field"
	}
	if s[0] >= '0' && s[0] <= '9' {
		return "_" + s
	}
	return s
}

// ormUninflected are plurals that are also their own singular, or that the
// shallow rules below would mangle.
var ormUninflected = map[string]bool{
	"series": true, "species": true, "news": true, "data": true, "media": true,
	"metadata": true, "settings": true,
}

// singular is a deliberately shallow de-pluraliser. A table called `users`
// should produce a `User`, and getting that right for the common cases is worth
// more than an inflection library; anything it gets wrong is one rename away
// and the generated file is meant to be read before it is used.
func singular(name string) string {
	lower := strings.ToLower(name)
	switch {
	case ormUninflected[lower], strings.HasSuffix(lower, "us"), strings.HasSuffix(lower, "is"):
		// status, campus, analysis: a trailing s that is not a plural.
		return name
	case strings.HasSuffix(lower, "ies") && len(name) > 3:
		return name[:len(name)-3] + "y"
	case strings.HasSuffix(lower, "sses"), strings.HasSuffix(lower, "shes"), strings.HasSuffix(lower, "ches"),
		strings.HasSuffix(lower, "xes"):
		return name[:len(name)-2]
	case strings.HasSuffix(lower, "s") && !strings.HasSuffix(lower, "ss"):
		return name[:len(name)-1]
	}
	return name
}

// ormPlural is singular's counterpart, used to name the "many" side of a relation.
func ormPlural(name string) string {
	lower := strings.ToLower(name)
	switch {
	case name == "", ormUninflected[lower]:
		return name
	case strings.HasSuffix(lower, "s") && !strings.HasSuffix(lower, "ss") && !strings.HasSuffix(lower, "us") &&
		!strings.HasSuffix(lower, "is"):
		// Already plural, as table names usually are.
		return name
	case strings.HasSuffix(lower, "y") && len(name) > 1 && !strings.ContainsRune("aeiou", rune(lower[len(lower)-2])):
		return name[:len(name)-1] + "ies"
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "x"), strings.HasSuffix(lower, "ch"),
		strings.HasSuffix(lower, "sh"):
		return name + "es"
	}
	return name + "s"
}

// pluralFieldName is the name Prisma's back-relations have always been given
// here. It is kept as it was, quirks included, because a schema generated last
// month and one generated today should call the same field the same thing.
func pluralFieldName(table string) string {
	n := camelLower(table)
	if strings.HasSuffix(n, "s") {
		return n
	}
	return n + "s"
}

// relationBase names the "one" side of a relation after the column that holds
// it: author_id -> author, authorId -> author. A column that gives nothing to
// work with (it is called id, or it has no id suffix to drop) falls back to the
// table it points at — unless the table has several keys to that same parent,
// when only the column can tell them apart: from_account -> from_account_ref.
func relationBase(r *ormRel, siblings int) string {
	if len(r.fk.Columns) == 1 {
		col := r.fk.Columns[0]
		base := col
		switch lower := strings.ToLower(col); {
		case strings.HasSuffix(lower, "_id") && len(col) > 3:
			base = col[:len(col)-3]
		case strings.HasSuffix(col, "Id") || strings.HasSuffix(col, "ID"):
			base = col[:len(col)-2]
		case strings.HasSuffix(lower, "id") && len(col) > 2:
			// userid: only when what is left names the table it references.
			rest := lower[:len(lower)-2]
			if rest == strings.ToLower(r.to.Name) || rest == strings.ToLower(singular(r.to.Name)) {
				base = col[:len(col)-2]
			}
		}
		base = strings.TrimRight(base, "_")
		if base != "" && base != col {
			return lowerFirst(base)
		}
		if siblings > 1 {
			return lowerFirst(col) + "_ref"
		}
	}
	return lowerFirst(singular(r.to.Name))
}

// ormNamer hands out names that are unique within one scope: a file's
// top-level declarations, or one class's members.
type ormNamer struct {
	used map[string]bool
	// fold makes the scope case-insensitive, as PHP's class names and a
	// case-insensitive file system's file names are.
	fold bool
}

func newORMNamer(fold bool, reserved ...string) *ormNamer {
	n := &ormNamer{used: map[string]bool{}, fold: fold}
	for _, r := range reserved {
		n.reserve(r)
	}
	return n
}

func (n *ormNamer) key(s string) string {
	if n.fold {
		return strings.ToLower(s)
	}
	return s
}

func (n *ormNamer) reserve(s string) { n.used[n.key(s)] = true }

// take returns the first candidate that is free, or the first candidate with a
// number on the end if none is.
func (n *ormNamer) take(candidates ...string) string {
	for _, c := range candidates {
		if c != "" && !n.used[n.key(c)] {
			n.used[n.key(c)] = true
			return c
		}
	}
	base := "field"
	if len(candidates) > 0 && candidates[0] != "" {
		base = candidates[0]
	}
	for i := 2; ; i++ {
		c := base + strconv.Itoa(i)
		if !n.used[n.key(c)] {
			n.used[n.key(c)] = true
			return c
		}
	}
}

// ormNameRules is one target's naming: how a table, a column, a relation and
// an enum are spelt, and which names are not free to use.
type ormNameRules struct {
	model func(table string) string
	field func(column string) string
	// rel spells a relation member from its lower-camel base.
	rel  func(base string) string
	enum func(name string) string
	// top are names the file's own declarations and imports already hold;
	// member are names a class's members may not take.
	top    []string
	member []string
	// escape rewrites a name the language reserves, for a top-level name and
	// for a member.
	escapeTop    func(string) string
	escapeMember func(string) string
	fold         bool
	// modelSuffix is what a model's name gains when the bare name is taken by
	// something the file already declares.
	modelSuffix string
	// manyBase overrides how the "many" side is named, for the one target
	// whose existing output fixes it.
	manyBase func(table string) string
}

// ormNaming is the names one target gave everything it emits.
type ormNaming struct {
	model map[*ormTable]string
	field map[*ormCol]string
	fwd   map[*ormRel]string
	back  map[*ormRel]string
	enum  map[*ormEnum]string
	// top is the file-level scope, left open for the declarations a generator
	// adds after the models: relation blocks, schema handles, helpers.
	top *ormNamer
}

func (g *ormGen) names(r ormNameRules) *ormNaming {
	ident := func(s string) string { return s }
	if r.escapeTop == nil {
		r.escapeTop = ident
	}
	if r.escapeMember == nil {
		r.escapeMember = ident
	}
	if r.rel == nil {
		r.rel = r.field
	}
	if r.enum == nil {
		r.enum = r.model
	}
	n := &ormNaming{
		model: map[*ormTable]string{}, field: map[*ormCol]string{},
		fwd: map[*ormRel]string{}, back: map[*ormRel]string{}, enum: map[*ormEnum]string{},
	}
	top := newORMNamer(r.fold, r.top...)
	n.top = top

	// A name shared by tables in two schemas goes to the one in the default
	// schema, or failing that to the first; the others carry their schema in
	// front.
	bare := map[string]*ormTable{}
	for _, m := range g.models {
		key := top.key(r.model(m.Name))
		if owner := bare[key]; owner == nil || (m.Schema == g.defaultSchema && owner.Schema != g.defaultSchema) {
			bare[key] = m
		}
	}
	for _, m := range g.models {
		name := r.model(m.Name)
		if bare[top.key(name)] != m && m.Schema != "" {
			name = r.model(m.Schema + "_" + m.Name)
		}
		name = r.escapeTop(name)
		if r.modelSuffix != "" {
			n.model[m] = top.take(name, name+r.modelSuffix)
		} else {
			n.model[m] = top.take(name)
		}
	}
	bareEnum := map[string]*ormEnum{}
	for _, e := range g.enums {
		key := top.key(r.enum(e.Name))
		if owner := bareEnum[key]; owner == nil || (e.Schema == g.defaultSchema && owner.Schema != g.defaultSchema) {
			bareEnum[key] = e
		}
	}
	for _, e := range g.enums {
		name := r.enum(e.Name)
		if bareEnum[top.key(name)] != e && e.Schema != "" {
			name = r.enum(e.Schema + "_" + e.Name)
		}
		n.enum[e] = top.take(r.escapeTop(name), r.escapeTop(r.enum(e.Name+"_enum")))
	}

	for _, m := range g.models {
		members := newORMNamer(false, r.member...)
		for _, c := range m.cols {
			n.field[c] = members.take(r.escapeMember(r.field(c.Name)))
		}
		siblings := map[*ormTable]int{}
		for _, rel := range m.rels {
			siblings[rel.to]++
		}
		for _, rel := range m.rels {
			base := relationBase(rel, siblings[rel.to])
			n.fwd[rel] = members.take(r.escapeMember(r.rel(base)), r.escapeMember(r.rel(base+"_ref")))
		}
		// Two keys from the same child need telling apart, and then both are
		// named by their columns: calling one "messages" and the other
		// "messages_recipient_id" would say the first is the real one.
		pairs := map[*ormTable]int{}
		for _, rel := range m.back {
			pairs[rel.from]++
		}
		for _, rel := range m.back {
			var base string
			switch {
			case r.manyBase != nil && !rel.unique:
				base = r.manyBase(rel.from.Name)
			case rel.unique:
				base = lowerFirst(singular(rel.from.Name))
			default:
				base = lowerFirst(ormPlural(rel.from.Name))
			}
			byColumns := base + "_" + strings.Join(rel.fk.Columns, "_")
			if pairs[rel.from] > 1 {
				n.back[rel] = members.take(r.escapeMember(r.rel(byColumns)))
			} else {
				n.back[rel] = members.take(r.escapeMember(r.rel(base)), r.escapeMember(r.rel(byColumns)))
			}
		}
	}
	return n
}

// --- reserved words -------------------------------------------------------

func ormWordSet(words string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(words) {
		set[w] = true
	}
	return set
}

var jsReserved = ormWordSet(`break case catch class const continue debugger default delete do else enum
	export extends false finally for function if implements import in instanceof interface let new
	null package private protected public return static super switch this throw true try typeof var
	void while with yield await arguments eval`)

// tsGlobals are the built-in types a generated class or interface must not
// shadow: a table called "dates" would otherwise declare a Date of its own and
// break every timestamp column in the same file.
var tsGlobals = ormWordSet(`Array Boolean Buffer Date Error Function Map Number Object Promise Record
	RegExp Set String Symbol Partial Required Readonly Pick Omit Exclude Extract Uint8Array`)

var pyReserved = ormWordSet(`False None True and as assert async await break class continue def del elif
	else except finally for from global if import in is lambda nonlocal not or pass raise return try
	while with yield match case type`)

var rustReserved = ormWordSet(`as break const continue crate else enum extern false fn for if impl in let
	loop match mod move mut pub ref return self Self static struct super trait true type unsafe use
	where while async await dyn abstract become box do final macro override priv typeof unsized
	virtual yield try gen`)

var phpReserved = ormWordSet(`abstract and array as break callable case catch class clone const continue
	declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile
	enum eval exit extends final finally fn for foreach function global goto if implements include
	instanceof insteadof interface isset list match namespace new or print private protected public
	readonly require return static switch throw trait try unset use var while xor yield int float
	bool string true false null void iterable object mixed never self parent model`)

// jsObjectMembers are what every JavaScript object inherits. A column may be
// called any of them; where an object's type is checked key by key, the
// inherited function is found under that name unless the object says otherwise.
var jsObjectMembers = ormWordSet(`constructor toString toLocaleString valueOf hasOwnProperty
	isPrototypeOf propertyIsEnumerable`)

// jsIdent makes a name usable as a JavaScript binding.
func jsIdent(name string) string {
	if jsReserved[name] {
		return name + "_"
	}
	return name
}

// jsMember makes a name usable as a member of a class or of an object whose
// keys are free to choose. Neither name below is a reserved word, which is why
// the reserved list does not catch them: one is every instance's link to its
// class, and assigning the other replaces the instance's prototype.
func jsMember(name string) string {
	if name == "constructor" || name == "__proto__" {
		return name + "_"
	}
	return name
}

// tsTypeName keeps a generated type from shadowing a built-in one.
func tsTypeName(name string) string {
	if tsGlobals[name] || jsReserved[name] {
		return name + "Record"
	}
	return name
}

func pyIdent(name string) string {
	if pyReserved[name] {
		return name + "_"
	}
	return name
}

// pyMember is pyIdent for a name declared in a class body, where two leading
// underscores are not a spelling but an instruction: Python rewrites __x to
// _Class__x, and __x__ belongs to whatever reads the class — a column called
// __tablename__ would replace a declarative model's table name.
func pyMember(name string) string {
	if strings.HasPrefix(name, "__") {
		return "col" + name
	}
	return pyIdent(name)
}

// --- literals -------------------------------------------------------------

// jsString renders a JavaScript string literal. JSON's string syntax is a
// subset of JavaScript's and of Python's, so the one encoder serves TypeScript,
// Python and GraphQL alike; HTML escaping is turned off because the output is
// source code, not markup.
func jsString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return `""`
	}
	return strings.TrimRight(b.String(), "\n")
}

// jsKey renders an object-literal key: bare where the name is an identifier,
// quoted where it is not. __proto__ is the one key a literal does not define —
// bare or quoted it sets the object's prototype — so it is written computed,
// which does define it.
func jsKey(name string) string {
	if name == "__proto__" {
		return `["__proto__"]`
	}
	return tsPropertyName(name)
}

// tsPropertyName quotes a key that is not a bare JS identifier, rather than
// renaming it: the key has to match what the database actually returns.
func tsPropertyName(name string) string {
	if name == "" {
		return `""`
	}
	for i, r := range name {
		ok := r == '_' || r == '$' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			return jsString(name)
		}
	}
	return name
}

func jsStrings(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = jsString(v)
	}
	return out
}

// phpString renders a PHP string. Single quotes, in which only the backslash
// and the quote itself are special, are what Laravel code uses; a value with a
// control character in it is written double-quoted instead, where the
// character can be escaped rather than left to break the line.
func phpString(s string) string {
	control := false
	for _, r := range s {
		control = control || r < 0x20 || r == 0x7f
	}
	if !control {
		s = strings.ReplaceAll(s, `\`, `\\`)
		return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\' || r == '$':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x` + strconv.FormatInt(int64(r)>>4, 16) + strconv.FormatInt(int64(r)&0xf, 16))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// rustString renders a Rust string literal.
func rustString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\u{` + strconv.FormatInt(int64(r), 16) + `}`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// rustBytes renders a Rust byte-string literal, which may hold only ASCII:
// anything else is written as the bytes it is.
func rustBytes(s string) string {
	var b strings.Builder
	b.WriteString(`b"`)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20 || c > 0x7e:
			b.WriteString(`\x` + strconv.FormatInt(int64(c)>>4, 16) + strconv.FormatInt(int64(c)&0xf, 16))
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ormOneLine folds text taken from the database — a comment, but also a table
// or column name, which may hold a newline as readily — onto one line, so that
// written into a line comment it cannot start a line of code after it.
func ormOneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ormBlockComment is ormOneLine for text going inside a block comment, which
// it must not be able to close either.
func ormBlockComment(s string) string {
	return strings.ReplaceAll(ormOneLine(s), "*/", "* /")
}

func mapFields(cols []string, f func(string) string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = f(c)
	}
	return out
}
