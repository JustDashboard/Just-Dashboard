package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// A schema form's ?preview= shows the statement and runs nothing. The flag was
// read as "1 or true, anything else off", so ?preview=yes ran the change it
// was meant to show; on the grant and privilege routes, which tested for "1"
// alone, so did ?preview=true. Now every spelling that is not a boolean stops
// the request before any handler under /databases reads it.
func TestAPreviewThatCannotBeReadIsNeverTheChange(t *testing.T) {
	s, router, db := schemaRouter(t, auth.RoleAdmin)
	before := schemaText(t, db)

	for _, spelling := range []string{"1", "true", "TRUE", "True"} {
		rec, shown := schemaSend(t, router, http.MethodPost, "/databases/1/ddl/trigger?preview="+spelling, stampTrigger)
		if rec.Code != http.StatusOK || shown["preview"] != true {
			t.Errorf("?preview=%s = %d %s, want the statement shown", spelling, rec.Code, rec.Body.String())
		}
	}
	for _, query := range []string{"preview=yes", "preview=on", "preview=2", "preview=", "preview", "preview=1&preview=0"} {
		for _, route := range []struct{ method, path, body string }{
			{http.MethodPost, "/databases/1/ddl/trigger", stampTrigger},
			{http.MethodDelete, "/databases/1/ddl/view", `{"name":"big_orders"}`},
			// Read on these two as "1" alone until now: what ?preview=true reached.
			{http.MethodPost, "/databases/1/server/roles/app/grant", `{"level":"read","database":"shop"}`},
			{http.MethodPost, "/databases/1/server/roles/app/privileges", `{"level":"table","schema":"main","table":"orders","privileges":["SELECT"]}`},
			{http.MethodPost, "/databases/1/server/roles/app/privileges/revoke", `{"level":"table","schema":"main","table":"orders","privileges":["SELECT"]}`},
		} {
			rec, body := schemaSend(t, router, route.method, route.path+"?"+query, route.body)
			refusal, _ := body["error"].(map[string]any)
			if rec.Code != http.StatusBadRequest || refusal["code"] != "bad_request" || refusal["field"] != "preview" {
				t.Errorf("%s %s?%s = %d %s, want 400 naming preview", route.method, route.path, query, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
	if after := schemaText(t, db); after != before {
		t.Errorf("a preview that could not be read changed the schema:\n%s", after)
	}
	if schemaHas(t, db, "trigger", "orders_stamp") || !schemaHas(t, db, "view", "big_orders") {
		t.Error("a preview that could not be read created the trigger or dropped the view")
	}
	// The readable previews showed and recorded nothing; each unreadable one is
	// a refused change and is on the trail as one.
	var shown, refused int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE status = 200`).Scan(&shown); err != nil || shown != 0 {
		t.Errorf("%d previews were audited as changes, %v", shown, err)
	}
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE status = 400`).Scan(&refused); err != nil || refused != 30 {
		t.Errorf("%d refused requests on the trail, want 30 (%v)", refused, err)
	}
}

// ?since=tomorrow on the audit log used to answer the whole log with a 200,
// and limit=abc the default page: an answer that looks complete to a question
// nobody asked.
func TestTheAuditListRefusesAFilterItCannotRead(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	for query, field := range map[string]string{
		"since=tomorrow":           "since",
		"since=2026-11-01":         "since",
		"until=yesterday":          "until",
		"limit=abc":                "limit",
		"limit=-5":                 "limit",
		"limit=0":                  "limit",
		"limit=501":                "limit",
		"offset=-1":                "offset",
		"failed=yes":               "failed",
		"failed=1&failed=0":        "failed",
		"limit=10&since=last-week": "since",
	} {
		rec := admin.do(http.MethodGet, "/api/v1/audit/?"+query, "", nil)
		body := jsonBody(t, rec.Body.String())
		refusal, _ := body["error"].(map[string]any)
		if rec.Code != http.StatusBadRequest || refusal["code"] != "bad_request" || refusal["field"] != field {
			t.Errorf("?%s = %d %s, want 400 naming %s", query, rec.Code, strings.TrimSpace(rec.Body.String()), field)
		}
	}
	for _, query := range []string{
		"", "limit=1", "limit=500", "failed=1", "failed=true", "failed=0", "failed=false",
		"since=" + url.QueryEscape("2026-11-01T00:00:00Z"), "since=" + url.QueryEscape("2026-11-01T00:00:00.000Z"),
		"since=&until=&limit=",
	} {
		if rec := admin.do(http.MethodGet, "/api/v1/audit/?"+query, "", nil); rec.Code != http.StatusOK {
			t.Errorf("?%s = %d %s, want 200", query, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	// A window in the future holds nothing, which is how a window that was
	// applied reads apart from one that was dropped.
	var page struct {
		Total int `json:"total"`
	}
	decodeOps(t, admin.do(http.MethodGet, "/api/v1/audit/?since="+url.QueryEscape("2099-01-01T00:00:00Z"), "", nil), &page)
	if page.Total != 0 {
		t.Errorf("a window that starts in 2099 holds %d entries", page.Total)
	}
}

// stringComparedFlag is a query parameter tested against a spelling of true or
// false: the three readings (`== "true"`, `== "1"`, either) that made one flag
// mean different things on different routes.
var stringComparedFlag = regexp.MustCompile(`\.Get\("[^"]+"\)\s*(==|!=)\s*"(?i:true|false|1|0|yes|no|on|off)"`)

// Every flag goes through httpx.QueryBool or a httpx.QueryReader. A handler
// that compares the parameter with a string again brings back a spelling that
// reads as "off" and lets the change through.
func TestNoHandlerReadsAFlagByComparingStrings(t *testing.T) {
	for _, file := range apiSourceFiles(t) {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(source), "\n") {
			if stringComparedFlag.MatchString(line) {
				t.Errorf("%s:%d reads a flag by comparing strings; use httpx.QueryBool, which refuses what it cannot read:\n\t%s",
					file, number+1, strings.TrimSpace(line))
			}
		}
	}
}

// A httpx.QueryReader answers defaults after a refusal, so a function that
// makes one and never asks Err would act on a half-read address: the lenient
// reading again, by another name.
func TestEveryQueryReaderIsAskedForItsError(t *testing.T) {
	fset := token.NewFileSet()
	for _, file := range apiSourceFiles(t) {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for _, name := range unaskedReaders(fn.Body) {
				t.Errorf("%s: %s makes the query reader %q and never asks %s.Err()",
					fset.Position(fn.Pos()), fn.Name.Name, name, name)
			}
		}
	}
}

// unaskedReaders is the readers a function body makes with httpx.Query or
// httpx.QueryOf whose Err is never called in it.
func unaskedReaders(body *ast.BlockStmt) []string {
	made, asked := []string{}, map[string]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			for i, value := range n.Rhs {
				if i < len(n.Lhs) && makesQueryReader(value) {
					made = append(made, fmt.Sprint(n.Lhs[i]))
				}
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Err" {
				asked[fmt.Sprint(sel.X)] = true
			}
		}
		return true
	})
	var unasked []string
	for _, name := range made {
		if !asked[name] {
			unasked = append(unasked, name)
		}
	}
	return unasked
}

func makesQueryReader(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "httpx" && (sel.Sel.Name == "Query" || sel.Sel.Name == "QueryOf")
}

// apiSourceFiles is this package's own source, tests left out.
func apiSourceFiles(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, file := range all {
		if !strings.HasSuffix(file, "_test.go") {
			files = append(files, file)
		}
	}
	if len(files) < 100 {
		t.Fatalf("found %d source files; the test is not running in the api package's directory", len(files))
	}
	return files
}
