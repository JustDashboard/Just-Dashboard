package dbx

import (
	"fmt"
	"sort"
	"strings"
)

// The class-based TypeScript targets (TypeORM, MikroORM, Sequelize) are each
// written as a list of units — one per entity, one for the enums — and then
// laid out either as one file or as a file per unit. Keeping the layout out of
// the generators is what lets "one file per model" be a switch rather than a
// second generator: the units are the same, only their imports differ.

type tsUnit struct {
	// file is the unit's file name without extension when files are split.
	// Units that share one are written to the same file.
	file string
	// symbols are what the unit declares for other units to use.
	symbols []string
	body    string
	// imports are what the unit takes from packages: module -> names. A name
	// may carry a "type " prefix.
	imports map[string]map[string]bool
	// refs are symbols the unit uses that another unit declares.
	refs map[string]bool
}

func newTSUnit(file string, symbols ...string) *tsUnit {
	return &tsUnit{file: file, symbols: symbols, imports: map[string]map[string]bool{}, refs: map[string]bool{}}
}

func (u *tsUnit) use(module string, names ...string) {
	if u.imports[module] == nil {
		u.imports[module] = map[string]bool{}
	}
	for _, n := range names {
		u.imports[module][n] = true
	}
}

func (u *tsUnit) ref(symbol string) { u.refs[symbol] = true }

// tsPackageImports renders the package imports of some units, modules in
// alphabetical order.
func tsPackageImports(units []*tsUnit) string {
	merged := map[string]map[string]bool{}
	for _, u := range units {
		for module, names := range u.imports {
			if merged[module] == nil {
				merged[module] = map[string]bool{}
			}
			for n := range names {
				merged[module][n] = true
			}
		}
	}
	modules := make([]string, 0, len(merged))
	for m := range merged {
		modules = append(modules, m)
	}
	sort.Strings(modules)
	var b strings.Builder
	for _, m := range modules {
		// A name imported as a value somewhere does not also need importing as
		// a type.
		for n := range merged[m] {
			if strings.HasPrefix(n, "type ") && merged[m][strings.TrimPrefix(n, "type ")] {
				delete(merged[m], n)
			}
		}
		b.WriteString(tsImport(tsSortedImports(merged[m]), m))
	}
	return b.String()
}

// tsLayout writes units as one file, or as one file per unit plus an index
// that re-exports them all. The index comes first: it is the entry point, and
// the first file is the one a caller that wants a single answer is given.
func tsLayout(header string, units []*tsUnit, split bool, single string) []ORMFile {
	if !split {
		var b strings.Builder
		b.WriteString(header)
		if imports := tsPackageImports(units); imports != "" {
			b.WriteString("\n" + imports)
		}
		for _, u := range units {
			b.WriteString("\n" + u.body)
		}
		return []ORMFile{{Filename: single, Content: b.String()}}
	}

	owner := map[string]string{}
	var order []string
	byFile := map[string][]*tsUnit{}
	for _, u := range units {
		if _, seen := byFile[u.file]; !seen {
			order = append(order, u.file)
		}
		byFile[u.file] = append(byFile[u.file], u)
		for _, s := range u.symbols {
			owner[s] = u.file
		}
	}

	var index strings.Builder
	index.WriteString(header + "\n")
	files := []ORMFile{{Filename: "index.ts"}}
	for _, file := range order {
		fmt.Fprintf(&index, "export * from %s\n", jsString("./"+file))

		group := byFile[file]
		var b strings.Builder
		b.WriteString(header)
		imports := tsPackageImports(group)
		// What this file needs from its siblings, one import line per file.
		siblings := map[string]map[string]bool{}
		for _, u := range group {
			for s := range u.refs {
				if from, ok := owner[s]; ok && from != file {
					if siblings[from] == nil {
						siblings[from] = map[string]bool{}
					}
					siblings[from][s] = true
				}
			}
		}
		from := make([]string, 0, len(siblings))
		for f := range siblings {
			from = append(from, f)
		}
		sort.Strings(from)
		for _, f := range from {
			imports += tsImport(tsSortedImports(siblings[f]), "./"+f)
		}
		if imports != "" {
			b.WriteString("\n" + imports)
		}
		for _, u := range group {
			b.WriteString("\n" + u.body)
		}
		files = append(files, ORMFile{Filename: file + ".ts", Content: b.String()})
	}
	files[0].Content = index.String()
	return files
}

// tsEnumMembers gives each enum label a member name for a TypeScript enum.
func tsEnumMembers(values []string) [][2]string {
	used := newORMNamer(false)
	out := make([][2]string, 0, len(values))
	for _, v := range values {
		name := pascal(v)
		if strings.Trim(sanitizeIdent(v), "_") == "" {
			name = "Empty"
		}
		out = append(out, [2]string{used.take(name), v})
	}
	return out
}

// tsEnumMember is the member a label was given, for a default that names it.
func tsEnumMember(values []string, value string) (string, bool) {
	for _, m := range tsEnumMembers(values) {
		if m[1] == value {
			return m[0], true
		}
	}
	return "", false
}

// tsEnumUnit declares every enum as a TypeScript enum, in one unit.
func tsEnumUnit(g *ormGen, n *ormNaming) *tsUnit {
	if len(g.enums) == 0 {
		return nil
	}
	u := newTSUnit("enums")
	var b strings.Builder
	for i, e := range g.enums {
		if i > 0 {
			b.WriteString("\n")
		}
		u.symbols = append(u.symbols, n.enum[e])
		fmt.Fprintf(&b, "export enum %s {\n", n.enum[e])
		for _, m := range tsEnumMembers(e.Values) {
			fmt.Fprintf(&b, "  %s = %s,\n", m[0], jsString(m[1]))
		}
		b.WriteString("}\n")
	}
	u.body = b.String()
	return u
}

// tsClassRules names things for a class-based target: a singular PascalCase
// class per table, members that are always identifiers.
func tsClassRules(g *ormGen, reservedTop ...string) ormNameRules {
	rules := ormNameRules{
		model:        func(s string) string { return pascal(singular(s)) },
		field:        sanitizeIdent,
		enum:         pascal,
		escapeTop:    tsTypeName,
		escapeMember: jsMember,
		top:          append([]string(nil), reservedTop...),
		fold:         true,
	}
	if g.opts.Naming == ORMNamingCamel {
		rules.field = ormCamel
	}
	if g.opts.Split {
		// Split, a class is also a file name, and the layout has file names of
		// its own: on a file system that folds case, a class called Index
		// would be written over index.ts.
		rules.top = append(rules.top, "index", "enums")
	}
	return rules
}

// jsObject renders an object literal from already-rendered "key: value" parts,
// or "" when there are none.
func jsObject(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// tsDocComment renders a database comment as a JSDoc line.
func tsDocComment(indent, text string) string {
	if text == "" {
		return ""
	}
	return indent + "/** " + ormBlockComment(text) + " */\n"
}
