package dbx

import (
	"encoding/json"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Generated code has to be code.
//
// Text assertions cannot tell a missing bracket from a present one, so what a
// generator writes is handed to the language's own tools wherever this machine
// has them: Go's parser and type checker in-process, Python's compiler when
// python3 is on the PATH, a JSON decoder, and — when JD_ORM_TS_PROJECT names a
// directory holding the real packages — TypeScript's compiler in strict mode.
// Each case is every golden case, so an option that only breaks one layout is
// checked too.

// ormGoStd is one importer for every case: it reads the standard library from
// source, and remembers what it has read.
var ormGoStd = sync.OnceValue(func() types.Importer {
	return importer.ForCompiler(token.NewFileSet(), "source", nil)
})

// ormGoTypeChecks compiles generated Go as far as its types. Only the plain
// structs can be taken this far: they import nothing but the standard library.
func ormGoTypeChecks(src string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "models.go", src, parser.AllErrors)
	if err != nil {
		return err
	}
	conf := types.Config{Importer: ormGoStd()}
	_, err = conf.Check("models", fset, []*ast.File{file}, nil)
	return err
}

func TestORMGeneratedGoCompiles(t *testing.T) {
	for _, c := range ormGoldenCases() {
		if c.req.Target != ORMGorm && c.req.Target != ORMGoStructs {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			res := ormGenerateCase(t, c)
			src := res.Schema
			if _, err := parser.ParseFile(token.NewFileSet(), "models.go", src, parser.AllErrors); err != nil {
				t.Fatalf("does not parse: %v\n%s", err, src)
			}
			formatted, err := format.Source([]byte(src))
			if err != nil || string(formatted) != src {
				t.Errorf("is not gofmt-clean (err %v)", err)
			}
			for _, w := range res.Warnings {
				if strings.Contains(w, "could not be formatted") {
					t.Errorf("the generator could not format its own output: %s", w)
				}
			}
			if c.req.Target != ORMGoStructs {
				// The GORM models import gorm.io/datatypes and lib/pq, which this
				// module does not have; parsing is as far as they go here.
				return
			}
			// The plain structs import only the standard library, so they are
			// type-checked whole: an undeclared Null type or a field of a type
			// that does not exist fails here.
			if err := ormGoTypeChecks(src); err != nil {
				t.Errorf("does not type-check: %v\n%s", err, src)
			}
		})
	}
}

func TestORMGeneratedPythonCompiles(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on the PATH")
	}
	for _, c := range ormGoldenCases() {
		if c.req.Target != ORMSQLAlchemy && c.req.Target != ORMDjango {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			res := ormGenerateCase(t, c)
			path := filepath.Join(t.TempDir(), "models.py")
			if err := os.WriteFile(path, []byte(res.Schema), 0o644); err != nil {
				t.Fatal(err)
			}
			// -B: compile, and leave no bytecode behind.
			cmd := exec.Command(python, "-B", "-c", "import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')", path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("does not compile: %v\n%s\n%s", err, out, res.Schema)
			}
		})
	}
}

func TestORMGeneratedJSONSchemaIsJSON(t *testing.T) {
	for _, c := range ormGoldenCases() {
		if c.req.Target != ORMJSONSchema {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			res := ormGenerateCase(t, c)
			var doc struct {
				Schema     string                     `json:"$schema"`
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Defs       map[string]struct {
					Type       any                        `json:"type"`
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
					Enum       []string                   `json:"enum"`
				} `json:"$defs"`
			}
			if err := json.Unmarshal([]byte(res.Schema), &doc); err != nil {
				t.Fatalf("is not JSON: %v\n%s", err, res.Schema)
			}
			if doc.Schema == "" || doc.Type != "object" || len(doc.Defs) == 0 || len(doc.Properties) == 0 {
				t.Errorf("is not a schema document: %+v", doc)
			}
			for name, def := range doc.Defs {
				// Every required key is a declared property, and every $ref
				// points at a definition that exists.
				for _, req := range def.Required {
					if _, ok := def.Properties[req]; !ok {
						t.Errorf("%s requires %q, which it does not declare", name, req)
					}
				}
			}
			for _, ref := range strings.Split(res.Schema, `"$ref": "#/$defs/`)[1:] {
				target := ref[:strings.IndexByte(ref, '"')]
				if _, ok := doc.Defs[target]; !ok {
					t.Errorf("a $ref points at %q, which is not defined", target)
				}
			}
		})
	}
}

// TestORMGeneratedTypeScriptCompiles type-checks every TypeScript output
// against the real packages. It needs a directory with them installed:
//
//	mkdir orm-check && cd orm-check && bun init -y
//	bun add typescript @types/node drizzle-orm typeorm sequelize kysely zod \
//	    @mikro-orm/core @mikro-orm/decorators reflect-metadata
//	JD_ORM_TS_PROJECT=$PWD go test ./internal/dbx -run TestORMGeneratedTypeScriptCompiles
func TestORMGeneratedTypeScriptCompiles(t *testing.T) {
	project := os.Getenv("JD_ORM_TS_PROJECT")
	if project == "" {
		t.Skip("set JD_ORM_TS_PROJECT to a directory with typescript and the ORM packages installed")
	}
	tsc := filepath.Join(project, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skipf("no TypeScript compiler at %s", tsc)
	}
	dir, err := os.MkdirTemp(project, "orm-gen-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	files := 0
	for _, c := range ormGoldenCases() {
		res := ormGenerateCase(t, c)
		for _, f := range res.Files {
			// prisma.config.ts imports the Prisma CLI's own package, which the
			// Prisma check covers.
			if !strings.HasSuffix(f.Filename, ".ts") || c.req.Target == ORMPrisma {
				continue
			}
			sub := filepath.Join(dir, c.name)
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, f.Filename), []byte(f.Content), 0o644); err != nil {
				t.Fatal(err)
			}
			files++
		}
	}
	config := `{
  "compilerOptions": {
    "strict": true, "noEmit": true, "target": "ES2022", "module": "ESNext",
    "moduleResolution": "bundler", "skipLibCheck": true, "types": ["node"],
    "experimentalDecorators": true, "emitDecoratorMetadata": true, "noUnusedLocals": true
  },
  "include": ["./**/*.ts"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tsc, "-p", filepath.Join(dir, "tsconfig.json"))
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("%d generated TypeScript files do not type-check: %v\n%s", files, err, out)
	}
}
