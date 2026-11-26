package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// httpx.SetAudit only labels the entry AuditMutations writes, and that
// middleware lets every GET by, so a GET handler that calls it records
// nothing while reading as audited. A read worth a trail writes its own entry
// (s.recordAudit, httpx.AuditRead). The walk only names each route's handler;
// it never calls one.
func TestNoGETHandlerOnlyLabelsAnAuditEntry(t *testing.T) {
	r, ok := testServer(t).Routes().(chi.Routes)
	if !ok {
		t.Fatal("Routes() no longer returns something chi can walk")
	}
	gets := map[string][]string{}
	err := chi.Walk(r, func(method, pattern string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != http.MethodGet {
			return nil
		}
		if name := serverMethodName(handler); name != "" {
			gets[name] = append(gets[name], pattern)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gets) < 100 {
		t.Fatalf("only %d GET handlers named; the walk is not seeing the real router", len(gets))
	}
	for name, file := range serverMethodsCallingSetAudit(t) {
		if patterns, ok := gets[name]; ok {
			t.Errorf("%s (%s) serves GET %s and calls httpx.SetAudit, which no GET records: use s.recordAudit or httpx.AuditRead",
				name, file, strings.Join(patterns, ", "))
		}
	}
}

// serverMethodName is the name of the *Server method a route's handler is,
// or "" for a closure or a handler of another type.
func serverMethodName(handler http.Handler) string {
	fn, ok := handler.(httpx.Handler)
	if !ok {
		return ""
	}
	f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if f == nil {
		return ""
	}
	name := f.Name()
	const marker = ".(*Server)."
	i := strings.LastIndex(name, marker)
	if i < 0 || !strings.HasSuffix(name, "-fm") {
		return ""
	}
	return strings.TrimSuffix(name[i+len(marker):], "-fm")
}

// serverMethodsCallingSetAudit names each *Server method in this package
// whose body, closures included, calls httpx.SetAudit, with its file.
func serverMethodsCallingSetAudit(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]string{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil || !isServerReceiver(fn.Recv) {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SetAudit" {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "httpx" {
						out[fn.Name.Name] = file
					}
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("no *Server method calls httpx.SetAudit; the scan is not reading the package")
	}
	return out
}

func isServerReceiver(recv *ast.FieldList) bool {
	if len(recv.List) != 1 {
		return false
	}
	star, ok := recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "Server"
}
