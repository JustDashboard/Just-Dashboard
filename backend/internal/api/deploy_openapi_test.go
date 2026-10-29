package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The description is generated from the router and the handlers' types, and
// these tests are what keeps it honest: every route is described and nothing
// described is gone (like the deploy key decision), its security is what the
// gate really answers, the document is well-formed OpenAPI 3.1, the closed
// vocabularies are the constants in the source, and the committed copy in
// docs/api is the one this build serves.

// deployOpenAPIDocument builds a server's routes and returns the description
// Routes stored for them.
func deployOpenAPIDocument(t *testing.T, s *Server) *openapiDocument {
	t.Helper()
	s.Routes()
	src := s.deployOpenAPI.Load()
	if src == nil {
		t.Fatal("Routes kept no deployment API description")
	}
	return src.document()
}

// Every deployment route in the router has a line in deployOperations and
// every line names a route; a route added without a line fails here, with the
// line to write.
func TestEveryDeployRouteIsDescribed(t *testing.T) {
	doc := deployOpenAPIDocument(t, testServer(t))
	for _, problem := range doc.Problems {
		t.Error(problem)
	}
	tags := map[string]bool{}
	for _, tag := range openapiTags {
		tags[tag.Name] = true
	}
	for key, op := range deployOperations {
		if !strings.HasSuffix(op.Summary, ".") || len(op.Summary) < 10 {
			t.Errorf("%s: the summary is not a sentence: %q", key, op.Summary)
		}
		if !tags[op.Tag] {
			t.Errorf("%s: tag %q is not in openapiTags", key, op.Tag)
		}
		if op.Response != nil && (op.Stream || op.Content != "") {
			t.Errorf("%s: a stream or non-JSON answer describes no JSON response", key)
		}
	}
	paths := doc.Document["paths"].(map[string]any)
	if len(paths) < 120 {
		t.Fatalf("only %d paths described; the walk is not seeing the deployment routes", len(paths))
	}
}

// What the description says about who may call a route is what the gate
// answers: an ordinary key is refused session_required exactly where apiKey is
// missing, and a deploy key is admitted exactly where deployKey is present.
func TestTheDeployOpenAPISecurityMatchesTheGate(t *testing.T) {
	b := newKeyBench(t)
	src := b.s.deployOpenAPI.Load()
	if src == nil {
		t.Fatal("no description")
	}
	paths := src.document().Document["paths"].(map[string]any)
	checked := 0
	for _, rt := range apiRoutes(t, b.h) {
		if !strings.HasPrefix(rt.pattern, "/api/v1/deploy/") {
			continue
		}
		pattern := strings.TrimPrefix(rt.pattern, "/api/v1")
		item, _ := paths[pattern].(map[string]any)
		op, _ := item[strings.ToLower(rt.method)].(map[string]any)
		if op == nil {
			t.Errorf("%s %s is not in the description", rt.method, pattern)
			continue
		}
		schemes := map[string]bool{}
		for _, requirement := range op["security"].([]any) {
			for name := range requirement.(map[string]any) {
				schemes[name] = true
			}
		}
		if pattern == "/deploy/{id}/hooks/{triggerID}" {
			if !schemes["webhookSignature"] || schemes["session"] || schemes["apiKey"] || schemes["deployKey"] {
				t.Errorf("%s: the hook's security is %v", pattern, schemes)
			}
			continue
		}
		checked++
		if !schemes["session"] {
			t.Errorf("%s %s: a session is not listed", rt.method, pattern)
		}
		ordinary := keyErrorCode(b.call(b.ordinary, rt.method, rt.path, "{}"))
		if sessionOnly := ordinary == "session_required"; sessionOnly == schemes["apiKey"] {
			t.Errorf("%s %s: an ordinary key gets %q but the description lists apiKey=%v", rt.method, pattern, ordinary, schemes["apiKey"])
		}
		key := b.call(b.deploy, rt.method, rt.path, "{}")
		code := keyErrorCode(key)
		admitted := code != "key_scope" && code != "session_required"
		if admitted != schemes["deployKey"] {
			t.Errorf("%s %s: a deploy key gets %d %s but the description lists deployKey=%v", rt.method, pattern, key.Code, code, schemes["deployKey"])
		}
		note, _ := op["x-deploy-key"].(string)
		if strings.HasPrefix(note, "allowed") != schemes["deployKey"] {
			t.Errorf("%s %s: x-deploy-key %q disagrees with deployKey=%v", rt.method, pattern, note, schemes["deployKey"])
		}
	}
	if checked < 150 {
		t.Fatalf("only %d routes checked", checked)
	}
}

// The structural rules of OpenAPI 3.1 the generator can break: the required
// top-level fields, every $ref resolving, every {param} declared as a path
// parameter and no path parameter without its {param}, unique operationIds,
// known tags and security schemes, well-formed responses and schema types.
func TestTheDeployOpenAPIDocumentIsStructurallyValid(t *testing.T) {
	doc := deployOpenAPIDocument(t, testServer(t))
	// Validate the JSON as a client reads it, not the Go maps.
	var document map[string]any
	if err := json.Unmarshal(doc.JSON, &document); err != nil {
		t.Fatal(err)
	}
	if document["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", document["openapi"])
	}
	info, _ := document["info"].(map[string]any)
	if info["title"] == "" || info["version"] == "" || info["title"] == nil || info["version"] == nil {
		t.Errorf("info lacks a title or version: %v", info)
	}
	tags := map[string]bool{}
	for _, tag := range document["tags"].([]any) {
		tags[tag.(map[string]any)["name"].(string)] = true
	}
	components := document["components"].(map[string]any)
	schemes := components["securitySchemes"].(map[string]any)
	ids := map[string]string{}
	param := regexp.MustCompile(`\{([^}]+)\}`)
	status := regexp.MustCompile(`^([1-5][0-9][0-9]|[1-5]XX|default)$`)
	for path, raw := range document["paths"].(map[string]any) {
		item := raw.(map[string]any)
		declared := map[string]bool{}
		if params, ok := item["parameters"].([]any); ok {
			for _, p := range params {
				one := p.(map[string]any)
				if one["in"] == "path" {
					if one["required"] != true {
						t.Errorf("%s: path parameter %v is not required", path, one["name"])
					}
					declared[one["name"].(string)] = true
				}
			}
		}
		inPath := map[string]bool{}
		for _, m := range param.FindAllStringSubmatch(path, -1) {
			inPath[m[1]] = true
			if !declared[m[1]] {
				t.Errorf("%s: {%s} has no path parameter", path, m[1])
			}
		}
		for name := range declared {
			if !inPath[name] {
				t.Errorf("%s: path parameter %s is not in the path", path, name)
			}
		}
		for method, raw := range item {
			if method == "parameters" {
				continue
			}
			if !slices.Contains([]string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}, method) {
				t.Errorf("%s: %q is not an HTTP method", path, method)
				continue
			}
			op := raw.(map[string]any)
			id, _ := op["operationId"].(string)
			if id == "" {
				t.Errorf("%s %s: no operationId", method, path)
			} else if other, taken := ids[id]; taken {
				t.Errorf("%s %s: operationId %q is also %s", method, path, id, other)
			}
			ids[id] = method + " " + path
			if summary, _ := op["summary"].(string); summary == "" {
				t.Errorf("%s %s: no summary", method, path)
			}
			for _, tag := range op["tags"].([]any) {
				if !tags[tag.(string)] {
					t.Errorf("%s %s: tag %v is not declared", method, path, tag)
				}
			}
			responses, _ := op["responses"].(map[string]any)
			if len(responses) == 0 {
				t.Errorf("%s %s: no responses", method, path)
			}
			for code := range responses {
				if !status.MatchString(code) {
					t.Errorf("%s %s: response key %q", method, path, code)
				}
			}
			for _, requirement := range op["security"].([]any) {
				for name := range requirement.(map[string]any) {
					if _, ok := schemes[name]; !ok {
						t.Errorf("%s %s: security scheme %q is not declared", method, path, name)
					}
				}
			}
			if params, ok := op["parameters"].([]any); ok {
				for _, p := range params {
					if one := p.(map[string]any); one["in"] != "query" {
						t.Errorf("%s %s: an operation parameter in %v; path parameters belong to the path item", method, path, one["in"])
					}
				}
			}
		}
	}

	// Every $ref resolves inside the document, and every schema type is one
	// JSON Schema knows.
	types := map[string]bool{"null": true, "boolean": true, "object": true, "array": true, "number": true, "string": true, "integer": true}
	var walk func(node any, where string)
	walk = func(node any, where string) {
		switch value := node.(type) {
		case map[string]any:
			// A "properties" map's keys are property names, and a security
			// scheme's type is its own vocabulary: neither is a schema.
			container := strings.HasSuffix(where, "/properties") || strings.Contains(where, "/securitySchemes/")
			if ref, ok := value["$ref"].(string); ok {
				if !strings.HasPrefix(ref, "#/") {
					t.Errorf("%s: $ref %q is not local", where, ref)
				} else if resolve(document, strings.TrimPrefix(ref, "#/")) == nil {
					t.Errorf("%s: $ref %q resolves to nothing", where, ref)
				}
			}
			if typ, ok := value["type"]; ok && !container {
				switch spelled := typ.(type) {
				case string:
					if !types[spelled] {
						t.Errorf("%s: type %q", where, spelled)
					}
				case []any:
					for _, one := range spelled {
						if s, _ := one.(string); !types[s] {
							t.Errorf("%s: type %v", where, one)
						}
					}
				default:
					t.Errorf("%s: type is %T", where, typ)
				}
			}
			if enum, ok := value["enum"].([]any); ok && len(enum) == 0 {
				t.Errorf("%s: an empty enum", where)
			}
			for _, k := range sortedKeysOf(value) {
				walk(value[k], where+"/"+k)
			}
		case []any:
			for i, one := range value {
				walk(one, where+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(document, "#")
	if len(components["schemas"].(map[string]any)) < 50 {
		t.Errorf("only %d component schemas", len(components["schemas"].(map[string]any)))
	}
}

func sortedKeysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolve follows a JSON pointer (without its leading #/) through decoded JSON.
func resolve(document any, pointer string) any {
	node := document
	for _, part := range strings.Split(pointer, "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch value := node.(type) {
		case map[string]any:
			node = value[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(value) {
				return nil
			}
			node = value[i]
		default:
			return nil
		}
		if node == nil {
			return nil
		}
	}
	return node
}

// The description never carries a value: no examples, nothing that looks like
// a key, and every field the write-only table names is marked so. A secret
// accepted by a write is described as a shape and never read back.
func TestTheDeployOpenAPIDescriptionCarriesShapesNotValues(t *testing.T) {
	doc := deployOpenAPIDocument(t, testServer(t))
	var document map[string]any
	if err := json.Unmarshal(doc.JSON, &document); err != nil {
		t.Fatal(err)
	}
	var walk func(node any, where string)
	walk = func(node any, where string) {
		switch value := node.(type) {
		case map[string]any:
			for _, k := range sortedKeysOf(value) {
				// A property named example or default is a name, not a value;
				// the responses' default is the error answer.
				if !strings.HasSuffix(where, "/properties") && (k == "example" || k == "examples" || k == "default" && !strings.HasSuffix(where, "/responses")) {
					t.Errorf("%s/%s: the description carries a value", where, k)
				}
				walk(value[k], where+"/"+k)
			}
		case []any:
			for i, one := range value {
				walk(one, where+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(document, "#")
	// API keys begin vpsd_, and the session cookie no longer does: the
	// document names none.
	if strings.Contains(string(doc.JSON), "vpsd_") {
		t.Error("the description mentions a key")
	}
	// Every line of the write-only table matched a field of a type the
	// description reaches; a renamed field would otherwise leave a secret
	// unmarked with its line still there.
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	reached := newOpenAPISchemas()
	for _, op := range deployOperations {
		if op.Request != nil {
			reached.schemaForValue(op.Request)
		}
		if op.Response != nil {
			reached.schemaForValue(op.Response)
		}
	}
	for key := range openapiWriteOnly {
		if !reached.writeOnlyApplied[key] {
			t.Errorf("%s is in openapiWriteOnly but no field of a described type matched it", key)
		}
	}
	// A representative: the value a variable write carries is write-only,
	// while the digest a list carries is not.
	variable := schemas["deploy.VariableWriteRequest"].(map[string]any)["properties"].(map[string]any)
	if variable["value"].(map[string]any)["writeOnly"] != true {
		t.Errorf("deploy.VariableWriteRequest.value is not writeOnly: %v", variable["value"])
	}
	listed := schemas["deploy.DeploymentVariable"].(map[string]any)["properties"].(map[string]any)
	if _, has := listed["value"]; has {
		t.Errorf("deploy.DeploymentVariable carries a value property: %v", listed["value"])
	}
}

// Every closed vocabulary the description lists is the type's constants in the
// source, and every named string type the description reaches that has
// constants in the source is listed. go/parser reads the constants, so a new
// constant or a new vocabulary fails here until openapiEnums says it.
func TestOpenAPIEnumsMatchTheConstants(t *testing.T) {
	s := testServer(t)
	s.Routes()
	schemas := newOpenAPISchemas()
	for _, op := range deployOperations {
		if op.Request != nil {
			schemas.schemaForValue(op.Request)
		}
		if op.Response != nil {
			schemas.schemaForValue(op.Response)
		}
	}
	constants := map[string]map[string][]string{} // package dir -> type -> values
	read := func(dir string) map[string][]string {
		if have, ok := constants[dir]; ok {
			return have
		}
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", dir, err)
		}
		out := map[string][]string{}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, decl := range file.Decls {
					gen, ok := decl.(*ast.GenDecl)
					if !ok || gen.Tok != token.CONST {
						continue
					}
					for _, spec := range gen.Specs {
						vs := spec.(*ast.ValueSpec)
						ident, ok := vs.Type.(*ast.Ident)
						if !ok || len(vs.Values) != 1 {
							continue
						}
						lit, ok := vs.Values[0].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						value, err := strconv.Unquote(lit.Value)
						if err != nil {
							continue
						}
						out[ident.Name] = append(out[ident.Name], value)
					}
				}
			}
		}
		constants[dir] = out
		return out
	}
	dirOf := func(pkgPath string) string {
		const internal = "github.com/Wayy01/Just-Dashboard/backend/internal/"
		if !strings.HasPrefix(pkgPath, internal) {
			return ""
		}
		return filepath.Join("..", strings.TrimPrefix(pkgPath, internal))
	}
	for typ, listed := range openapiEnums {
		dir := dirOf(typ.PkgPath())
		if dir == "" {
			t.Errorf("%s is outside this repository", typ)
			continue
		}
		want := read(dir)[typ.Name()]
		sortedWant, sortedListed := slices.Clone(want), slices.Clone(listed)
		sort.Strings(sortedWant)
		sort.Strings(sortedListed)
		if !slices.Equal(sortedWant, sortedListed) {
			t.Errorf("openapiEnums[%s] lists %v; the constants in %s are %v", typ, listed, dir, want)
		}
	}
	for typ := range schemas.scalars {
		if typ.Kind() != reflect.String || typ.Name() == "" {
			continue
		}
		if _, listed := openapiEnums[typ]; listed {
			continue
		}
		dir := dirOf(typ.PkgPath())
		if dir == "" {
			continue
		}
		if values := read(dir)[typ.Name()]; len(values) > 0 {
			t.Errorf("%s has constants %v and is in the description without an enum: add it to openapiEnums", typ, values)
		}
	}
}

// The committed copy, docs/api/deploy-openapi.json, is what this build serves
// (its info.version aside, which a release bumps). It fails when a change to
// a route or a type left the file behind, and JD_UPDATE_OPENAPI=1 rewrites it.
func TestTheCommittedDeployOpenAPIDescriptionIsCurrent(t *testing.T) {
	doc := deployOpenAPIDocument(t, testServer(t))
	path := filepath.Join("..", "..", "..", "docs", "api", "deploy-openapi.json")
	if os.Getenv("JD_UPDATE_OPENAPI") == "1" {
		if err := os.WriteFile(path, doc.JSON, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; generate it with: cd backend && JD_UPDATE_OPENAPI=1 go test ./internal/api -run TestTheCommittedDeployOpenAPIDescriptionIsCurrent", err)
	}
	if !bytes.Equal(withoutVersion(t, committed), withoutVersion(t, doc.JSON)) {
		t.Fatalf("%s is not the description this build serves. Regenerate it with:\n  cd backend && JD_UPDATE_OPENAPI=1 go test ./internal/api -run TestTheCommittedDeployOpenAPIDescriptionIsCurrent\nand commit it with the change.", path)
	}
}

// withoutVersion is a description with its info.version blanked, re-encoded
// the way the generator encodes, so two builds compare on content alone.
func withoutVersion(t *testing.T, raw []byte) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if info, ok := document["info"].(map[string]any); ok {
		info["version"] = ""
	}
	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The description is read by any signed-in account and any key — a deploy
// key included, since it is what the key's holder generates a client from —
// and by nobody without one.
func TestTheDeployOpenAPIRouteAnswersEveryCredential(t *testing.T) {
	b := newKeyBench(t)
	for _, variant := range []struct{ path, contentType, starts string }{
		{"/api/v1/deploy/openapi.json", "application/json", `{`},
		{"/api/v1/deploy/openapi.yaml", "application/yaml", `"components":`},
	} {
		for name, call := range map[string]func() *httptest.ResponseRecorder{
			"session":      func() *httptest.ResponseRecorder { return b.inSession(http.MethodGet, variant.path, nil) },
			"ordinary key": func() *httptest.ResponseRecorder { return b.call(b.ordinary, http.MethodGet, variant.path, nil) },
			"deploy key":   func() *httptest.ResponseRecorder { return b.call(b.deploy, http.MethodGet, variant.path, nil) },
		} {
			w := call()
			if w.Code != http.StatusOK {
				t.Errorf("%s %s = %d %s", name, variant.path, w.Code, strings.TrimSpace(w.Body.String()))
				continue
			}
			if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, variant.contentType) {
				t.Errorf("%s %s: Content-Type %q", name, variant.path, got)
			}
			if !strings.HasPrefix(w.Body.String(), variant.starts) {
				t.Errorf("%s %s: body starts %q", name, variant.path, w.Body.String()[:40])
			}
		}
		if w := b.call("vpsd_nobody", http.MethodGet, variant.path, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("no credential at %s = %d", variant.path, w.Code)
		}
		if w := b.call(b.mcp, http.MethodGet, variant.path, nil); keyErrorCode(w) != "key_scope" {
			t.Errorf("an MCP key at %s = %d %s", variant.path, w.Code, w.Body.String())
		}
	}
	// The JSON is the document, and the YAML says the same version.
	w := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/openapi.json", nil)
	var document struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.1.0" || len(document.Paths) < 120 {
		t.Errorf("openapi %q with %d paths", document.OpenAPI, len(document.Paths))
	}
	yaml := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/openapi.yaml", nil).Body.String()
	if !strings.Contains(yaml, fmt.Sprintf("\"version\": %q", document.Info.Version)) || !strings.Contains(yaml, `"openapi": "3.1.0"`) {
		t.Errorf("the YAML does not carry the version and openapi fields:\n%.300s", yaml)
	}
}

// The YAML emitter double-quotes every string and key, so a value YAML would
// otherwise read as a boolean, a number or null stays the string it is, and a
// multi-line text stays one value; lists and maps nest by indentation.
func TestWriteYAMLQuotesEveryScalar(t *testing.T) {
	var out bytes.Buffer
	writeYAML(&out, map[string]any{
		"b": []any{"yes", "null", "1e3", map[string]any{"k": "two\nlines", "n": float64(3)}, []any{}},
		"a": map[string]any{},
		"c": true,
		"d": nil,
		"e": float64(1000000),
	}, 0)
	want := strings.Join([]string{
		`"a": {}`,
		`"b":`,
		`  - "yes"`,
		`  - "null"`,
		`  - "1e3"`,
		`  - "k": "two\nlines"`,
		`    "n": 3`,
		`  - []`,
		`"c": true`,
		`"d": null`,
		`"e": 1000000`,
		``,
	}, "\n")
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}
