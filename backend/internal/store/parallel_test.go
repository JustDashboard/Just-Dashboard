package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A test that takes the 0.6.6 fixture through Open migrates twice, a fresh
// database and the old one, and under the race detector one Open is about six
// seconds. Twenty of them in a row were two thirds of this package's ten
// minutes there, which is all `go test` gives a package: the race gate failed
// on three pushes to release branches in November 2026 with no race at all,
// only "test timed out after 10m0s" under whichever test was running when the
// time ran out. Each of these tests owns the directories it opens and the
// package keeps no state between stores, so they run beside each other, and
// this holds the next one to the same.
func TestTestsThatMigrateTheOldFixtureRunInParallel(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		// This file names the fixture too, to look for it.
		if !strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "parallel_test.go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if !migratesTheOldFixture(fn.Body) {
				continue
			}
			checked++
			if !callsParallel(fn.Body) {
				t.Errorf("%s: %s migrates testdata/0.6.6.sql and does not call t.Parallel()", entry.Name(), fn.Name.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no test that migrates testdata/0.6.6.sql, so this checked nothing")
	}
}

func migratesTheOldFixture(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "0.6.6.sql") {
			found = true
		}
		return !found
	})
	return found
}

// callsParallel reports a t.Parallel() among the test's own statements, where
// it pauses the test before any of its work.
func callsParallel(body *ast.BlockStmt) bool {
	for _, statement := range body.List {
		expression, ok := statement.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := expression.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		if method, ok := call.Fun.(*ast.SelectorExpr); ok && method.Sel.Name == "Parallel" {
			return true
		}
	}
	return false
}
