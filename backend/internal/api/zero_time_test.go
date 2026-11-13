package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// yearOneLeft is the number of value time.Time JSON fields without omitzero
// that each of these packages may still have. Everywhere else it is none.
//
// These packages digest, sign or compare a JSON encoding of their own types
// (a plan's digest, a state file the drift check renders again, a protocol
// with another product), so leaving a zero time out of one changes bytes that
// something stored before the change is held against. Each field needs its
// struct checked against that before its tag changes. A number here goes
// down; a new field takes omitzero instead of raising it.
var yearOneLeft = map[string]int{
	"internal/accountlink": 8,
	"internal/assistant":   3,
	"internal/backups":     9,
	"internal/dbx":         14,
	"internal/deploy":      80,
	"internal/dnsservice":  11,
	"internal/hublink":     6,
	"internal/netvantage":  12,
	"internal/netx":        53,
}

// A time that was never set used to leave the server as
// "0001-01-01T00:00:00Z": 436 value time.Time fields had no omitzero, and 25
// carried omitempty, which never omits a struct. "Never" then had three
// spellings (absent, null, year one), an integrator who parsed the third got a
// date two thousand years ago, and the page tested for the string "0001" in
// six places. A zero time is left out now.
func TestAZeroTimeIsLeftOutNotSentAsYearOne(t *testing.T) {
	plain := map[string][]string{}
	fset := token.NewFileSet()
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			pkg := filepath.ToSlash(strings.TrimPrefix(filepath.Dir(path), "../../"))
			ast.Inspect(file, func(n ast.Node) bool {
				field, ok := n.(*ast.Field)
				if !ok || field.Tag == nil || !isTimeValue(field.Type) {
					return true
				}
				tag, ok := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Lookup("json")
				if !ok || tag == "-" {
					return true
				}
				name, options, _ := strings.Cut(tag, ",")
				options = "," + options + ","
				where := fset.Position(field.Pos()).String()
				if strings.Contains(options, ",omitempty,") {
					t.Errorf("%s: %s is a time.Time with omitempty, which never omits a struct: write omitzero", where, name)
				}
				if !strings.Contains(options, ",omitzero,") {
					plain[pkg] = append(plain[pkg], where+" "+name)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	packages := make([]string, 0, len(plain))
	for pkg := range plain {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	for _, pkg := range packages {
		fields := plain[pkg]
		switch allowed := yearOneLeft[pkg]; {
		case len(fields) > allowed && allowed == 0:
			t.Errorf("%s: a time.Time that is never set leaves as 0001-01-01T00:00:00Z; give it `json:\"name,omitzero\"`:\n  %s", pkg, strings.Join(fields, "\n  "))
		case len(fields) > allowed:
			t.Errorf("%s has %d time.Time fields without omitzero and %d are allowed: the new one takes `json:\"name,omitzero\"`:\n  %s", pkg, len(fields), allowed, strings.Join(fields, "\n  "))
		case len(fields) < allowed:
			t.Logf("%s is down to %d time.Time fields without omitzero: lower yearOneLeft from %d", pkg, len(fields), allowed)
		}
	}
}

// isTimeValue reports a field typed time.Time itself: a pointer is null when
// unset and a list is empty.
func isTimeValue(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Time" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}

// The two the page worked around by name: an account that never signed in,
// and a site whose file could not be read.
func TestAnAccountThatNeverSignedInHasNoLastLogin(t *testing.T) {
	for name, value := range map[string]any{
		"an account":  auth.User{Username: "new"},
		"a site file": proxysvc.VHost{Name: "app"},
	} {
		out, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "0001-01-01") {
			t.Errorf("%s still sends year one: %s", name, out)
		}
	}
}
