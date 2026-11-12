package safego

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// bareStarts are the functions named Start… that may still start a goroutine
// with a bare `go`, each with why. Everything else a Start function launches
// goes through Go or Once: that is how the work the dashboard begins at boot,
// and keeps running while nobody is looking, survives a panic in itself.
var bareStarts = map[string]string{
	"internal/netflows/observer_recorder_linux.go Start": "run recovers itself (Recover) and must not be started twice: a kernel session is attached once",
	"internal/deploy/engine.go startWorker":              "one worker per run, recovered by Recover; the schedule's sweep reclaims a run whose worker died",
	"internal/api/handlers_backups.go startBackupRun":    "one goroutine per backup an operator asked for, not a loop",
	"internal/jobs/jobs.go start":                        "one goroutine per job an operator asked for, not a loop",
	"internal/deploy/rehearsal.go start":                 "one goroutine per rehearsal an operator asked for, not a loop",
}

// A background loop is started from a function called Start in every package
// that has one, which is what makes this cheap to hold: a new loop written
// the old way fails here, with the line to change.
func TestStartFunctionsStartSupervisedGoroutines(t *testing.T) {
	root := filepath.Join("..", "..")
	used := map[string]bool{}
	files := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files++
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || !strings.HasPrefix(strings.ToLower(fn.Name.Name), "start") {
					continue
				}
				key := rel + " " + fn.Name.Name
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					stmt, ok := n.(*ast.GoStmt)
					if !ok {
						return true
					}
					if _, allowed := bareStarts[key]; allowed {
						used[key] = true
						return true
					}
					t.Errorf("%s: %s starts a goroutine with a bare `go`; start it with safego.Go (a loop) or safego.Once, or list it in bareStarts with the reason",
						set.Position(stmt.Pos()), fn.Name.Name)
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 500 {
		t.Fatalf("only %d files read; the walk is not seeing the backend", files)
	}
	for key := range bareStarts {
		if !used[key] {
			t.Errorf("bareStarts lists %q, which no longer starts a bare goroutine: remove the entry", key)
		}
	}
}
