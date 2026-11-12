package safego

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// discardedPasses are the calls a supervised loop may still make with its
// context and throw the answer of away, each with why.
var discardedPasses = map[string]string{
	"internal/logdrain deliver": "the last flush as a shipper stops; deliver writes its own failure to the drain's status row, which the page reads",
}

// A loop that runs for the life of the process and drops what its pass
// returned fails without a sound: automatic deployments stopped and nothing in
// the log said why. The function a task is started with (the literal handed to
// Go, or the function named there when it is declared in the same package)
// must not assign a call made with the task's context to blank; it reports the
// pass (Passes.Report) or logs the error itself.
func TestSupervisedLoopsDoNotDiscardWhatAPassReturned(t *testing.T) {
	root := filepath.Join("..", "..")
	used := map[string]bool{}
	tasks := 0
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(dir string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			set := token.NewFileSet()
			named := map[string][]*ast.FuncDecl{}
			var files []*ast.File
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				file, err := parser.ParseFile(set, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
				if err != nil {
					return err
				}
				files = append(files, file)
				for _, decl := range file.Decls {
					if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
						named[fn.Name.Name] = append(named[fn.Name.Name], fn)
					}
				}
			}
			rel, _ := filepath.Rel(root, dir)
			rel = filepath.ToSlash(rel)
			check := func(params *ast.FieldList, body *ast.BlockStmt) {
				tasks++
				for _, found := range discards(params, body) {
					key := rel + " " + found.callee
					if _, allowed := discardedPasses[key]; allowed {
						used[key] = true
						continue
					}
					t.Errorf("%s: a supervised loop throws away what %s returned; report it with safego.Passes or log it, or list %q in discardedPasses with the reason",
						set.Position(found.pos), found.callee, key)
				}
			}
			for _, file := range files {
				ast.Inspect(file, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) < 4 || !isSafegoGo(call.Fun) {
						return true
					}
					switch fn := call.Args[3].(type) {
					case *ast.FuncLit:
						check(fn.Type.Params, fn.Body)
					case *ast.SelectorExpr:
						for _, decl := range named[fn.Sel.Name] {
							check(decl.Type.Params, decl.Body)
						}
					case *ast.Ident:
						for _, decl := range named[fn.Name] {
							check(decl.Type.Params, decl.Body)
						}
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if tasks < 40 {
		t.Fatalf("only %d supervised loops read; the walk is not seeing the backend", tasks)
	}
	for key := range discardedPasses {
		if !used[key] {
			t.Errorf("discardedPasses lists %q, which no loop discards any more: remove the entry", key)
		}
	}
}

func isSafegoGo(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Go" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "safego"
}

type discard struct {
	pos    token.Pos
	callee string
}

// discards finds `_ = call(…, ctx, …)` in body, where ctx is one of the
// function's own parameters or a context derived from one inside it. Function
// literals inside are other goroutines' or deferred cleanup, and are not read.
func discards(params *ast.FieldList, body *ast.BlockStmt) []discard {
	contexts := map[string]bool{}
	if params != nil {
		for _, field := range params.List {
			for _, name := range field.Names {
				contexts[name.Name] = true
			}
		}
	}
	var out []discard
	ast.Inspect(body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.AssignStmt:
			if len(stmt.Rhs) != 1 {
				return true
			}
			call, ok := stmt.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			if derivesContext(call) {
				if first, ok := stmt.Lhs[0].(*ast.Ident); ok {
					contexts[first.Name] = true
				}
				return true
			}
			for _, left := range stmt.Lhs {
				if id, ok := left.(*ast.Ident); !ok || id.Name != "_" {
					return true
				}
			}
			for _, arg := range call.Args {
				if id, ok := arg.(*ast.Ident); ok && contexts[id.Name] {
					out = append(out, discard{pos: stmt.Pos(), callee: calleeName(call.Fun)})
					break
				}
			}
		}
		return true
	})
	return out
}

func derivesContext(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "context" && strings.HasPrefix(sel.Sel.Name, "With")
}

func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return "a call"
}
