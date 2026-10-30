// Package openapigen writes the clients of the deployment API from its
// OpenAPI description: a Go package and a TypeScript module, each with a type
// per schema and a call per operation. It reads only the document — the one
// the dashboard generates from its router (docs/api/deploy-openapi.json) — so
// what the clients say is what that build serves, and a route added to the
// router is a method in both after the next generation.
//
// The output is deterministic: declarations, fields and operations are
// written in the order of their names, and nothing in it depends on the time,
// the version or the machine. TestTheCommittedSDKsAreCurrent (internal/api)
// regenerates both clients in memory and fails when the committed sdk/
// differs; JD_UPDATE_SDK=1 rewrites it.
package openapigen

import "fmt"

// GoPackage is the Go client's package name. Its directory is sdk/go, and
// "go" is a keyword, so the package is named for the command line it sits
// beside.
const GoPackage = "jd"

// Files is generated output by its path under sdk/.
type Files map[string][]byte

// Generate writes both clients from a document.
func Generate(document []byte) (Files, error) {
	api, err := Parse(document)
	if err != nil {
		return nil, err
	}
	out := Files{}
	goFiles, err := generateGo(api, GoPackage)
	if err != nil {
		return nil, err
	}
	for name, body := range goFiles {
		out["go/"+name] = body
	}
	tsFiles, err := generateTypeScript(api)
	if err != nil {
		return nil, err
	}
	for name, body := range tsFiles {
		out["typescript/"+name] = body
	}
	if len(out) != len(GeneratedFiles) {
		return nil, fmt.Errorf("generated %d files, want %d", len(out), len(GeneratedFiles))
	}
	return out, nil
}

// GeneratedFiles is every file Generate writes, by its path under sdk/. The
// rest of sdk/ — go.mod, the tests, tsconfig.json, the README — is written by
// hand and left alone.
var GeneratedFiles = []string{
	"go/client.go",
	"go/operations.go",
	"go/types.go",
	"typescript/client.ts",
	"typescript/types.ts",
}
