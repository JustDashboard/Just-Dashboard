package dbx

import (
	"encoding/json"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
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
// ormHostileFixture is a schema where every name, comment, label and default
// tries to. Each carries a marker that would begin a line of its own if the
// newline before it were ever written out raw.
func ormHostileFixture(driver Driver) *ORMSchema {
	s := &ORMSchema{Driver: driver, Detailed: true}
	enumType := "text"
	if driver == DriverPostgres {
		s.Enums = []ORMEnum{{
			Schema: "public", Name: "kind\nINJECTED_ENUM()",
			Values: []string{"plain", "new\nINJECTED_VALUE()", `quo"te`, "apo'strophe", "back`tick${x}", "café", "*/", ""},
		}}
		enumType = "USER-DEFINED"
	}
	if driver == DriverMySQL {
		enumType = "enum('plain','new\nINJECTED_VALUE()','quo\"te','apo''strophe','back`tick${x}','*/')"
	}
	kind := ormColumn("kind", enumType, ormNote("the kind */\nINJECTED_COMMENT()"))
	if driver == DriverPostgres {
		kind.EnumSchema, kind.EnumName = "public", "kind\nINJECTED_ENUM()"
	}
	s.Tables = []ORMTable{
		{
			Schema: "public", Name: "things", Kind: ORMKindTable,
			Comment: "A table */ of things\nINJECTED_COMMENT()\r\n\"\"\" ''' `${x}` \\",
			Columns: []ORMColumn{
				ormColumn("id", "integer"),
				kind,
				ormColumn("col\nINJECTED_COLUMN()", "text", ormNull, ormNote("line one\nINJECTED_COMMENT()")),
				ormColumn(`quo"te'd`+"`col`", "text", ormNull),
				ormColumn("note", "text", ormDef("'x\nINJECTED_DEFAULT()'::text")),
				ormColumn("odd", "weird\nINJECTED_TYPE()", ormNull),
				ormColumn("owner_id", "integer", ormNull),
			},
			PrimaryKey: []string{"id"},
			Indexes: []ORMIndex{
				ormPK("things_pkey", "id"),
				ormUnique("uq\nINJECTED_INDEX()", "note"),
				ormIndex("ix\nINJECTED_INDEX()", "owner_id"),
			},
			ForeignKeys: []ForeignKey{
				ormFK("fk\nINJECTED_FK()", []string{"owner_id"}, "public", "bad\nINJECTED_TABLE()", []string{"id"}, "CASCADE"),
			},
		},
		{
			Schema: "public", Name: "bad\nINJECTED_TABLE()", Kind: ORMKindTable,
			Comment:    "owner\nINJECTED_COMMENT()",
			Columns:    []ORMColumn{ormColumn("id", "integer"), ormColumn("class", "text", ormNull)},
			PrimaryKey: []string{"id"},
			Indexes:    []ORMIndex{ormPK("bad_pkey", "id")},
		},
	}
	if driver != DriverPostgres {
		for i := range s.Tables {
			s.Tables[i].Schema = map[Driver]string{DriverMySQL: "db", DriverSQLite: "main"}[driver]
			for j := range s.Tables[i].ForeignKeys {
				s.Tables[i].ForeignKeys[j].RefSchema = s.Tables[i].Schema
			}
		}
	}
	return s
}

func TestORMGeneratedCodeCannotBeInjected(t *testing.T) {
	python, _ := exec.LookPath("python3")
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite} {
		for _, target := range ORMTargets() {
			if ORMUnsupported(target, driver) != "" {
				continue
			}
			for _, variant := range []ORMRequest{
				{Target: target},
				{Target: target, Naming: ORMNamingCamel},
				{Target: target, Split: ormYes()},
			} {
				opts, err := variant.Options()
				if err != nil {
					// The target has no such option; the plain variant covers it.
					continue
				}
				name := string(driver) + "/" + string(target)
				if variant.Naming != "" {
					name += "/camel"
				}
				if variant.Split != nil {
					name += "/split"
				}
				t.Run(name, func(t *testing.T) {
					res, err := GenerateORMFiles(ormHostileFixture(driver), opts)
					if err != nil {
						t.Fatalf("generate: %v", err)
					}
					for _, f := range res.Files {
						if f.Filename != filepath.Base(f.Filename) || strings.ContainsAny(f.Filename, "\n\r/\\") {
							t.Errorf("file name %q is not a bare, single-line name", f.Filename)
						}
						for i, line := range strings.Split(f.Content, "\n") {
							trimmed := strings.TrimLeft(line, " \t")
							if !strings.HasPrefix(trimmed, "INJECTED_") {
								continue
							}
							// The SQL target quotes a label or a comment as a SQL string,
							// and a string may span lines. Nothing else may.
							if target == ORMSQL && (strings.HasPrefix(trimmed, "INJECTED_VALUE") || strings.HasPrefix(trimmed, "INJECTED_COMMENT")) {
								continue
							}
							t.Errorf("%s line %d begins with text from the database:\n%s", f.Filename, i+1, f.Content)
							break
						}
						switch {
						case strings.HasSuffix(f.Filename, ".go"):
							if _, err := format.Source([]byte(f.Content)); err != nil {
								t.Errorf("%s does not parse: %v\n%s", f.Filename, err, f.Content)
							}
						case strings.HasSuffix(f.Filename, ".json"):
							if !json.Valid([]byte(f.Content)) {
								t.Errorf("%s is not JSON:\n%s", f.Filename, f.Content)
							}
						case strings.HasSuffix(f.Filename, ".py") && python != "":
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

// A name the engine's own quoting refuses — one with a control character —
// cannot be written into SQL at all, so the SQL target leaves the table out
// and says so rather than emitting a statement it cannot vouch for.
func TestORMSQLLeavesOutWhatItCannotQuote(t *testing.T) {
	opts, _ := ORMRequest{Target: ORMSQL}.Options()
	res, err := GenerateORMFiles(ormHostileFixture(DriverPostgres), opts)
	if err != nil {
		t.Fatal(err)
	}
	ormMustNotContain(t, "schema.sql", res.Schema, "INJECTED_TABLE", "INJECTED_COLUMN", "INJECTED_INDEX", "INJECTED_FK", "INJECTED_ENUM")
	ormMustContain(t, "warnings", strings.Join(res.Warnings, "\n"), "cannot be quoted safely and was left out")
}
