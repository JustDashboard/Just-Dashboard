package api

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/openapigen"
)

// The clients in sdk/ are generated from the deployment API's description
// (internal/openapigen), so a route, a type a handler decodes or encodes, or a
// line of the description's tables changed without regenerating them fails
// here, beside the description's own staleness test. They are generated from
// the document this build serves, which TestTheCommittedDeployOpenAPIDescriptionIsCurrent
// holds equal to the committed one; the generator reads nothing else, and no
// version, so a release bump alone changes neither.

const regenerateSDK = "cd backend && JD_UPDATE_SDK=1 go test ./internal/api -run TestTheCommittedSDKsAreCurrent"

func TestTheCommittedSDKsAreCurrent(t *testing.T) {
	doc := deployOpenAPIDocument(t, testServer(t))
	files, err := openapigen.Generate(doc.JSON)
	if err != nil {
		t.Fatalf("the clients cannot be generated from this build's description: %v", err)
	}
	root := filepath.Join("..", "..", "..", "sdk")
	update := os.Getenv("JD_UPDATE_SDK") == "1"
	var stale []string
	for _, name := range openapigen.GeneratedFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		if update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, files[name], 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s", path)
		}
		committed, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(committed, files[name]) {
			stale = append(stale, "sdk/"+name)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("%s %s not what this build's description generates. Regenerate the clients with:\n  %s\nand commit sdk/ with the change.",
			strings.Join(stale, ", "), map[bool]string{true: "is", false: "are"}[len(stale) == 1], regenerateSDK)
	}
}

// The committed Go client compiles, passes vet and passes its own tests (the
// request builder against a test server): the Go a generation writes is held
// to the compiler, not only to the generator's golden files. The TypeScript
// client's equivalent, tsc and bun test in sdk/typescript, is run by
// scripts/test-changed.sh for any change under sdk/.
func TestTheCommittedGoSDKBuildsAndPassesItsTests(t *testing.T) {
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(gobin); err != nil {
		t.Skipf("no go command beside this toolchain: %v", err)
	}
	cmd := exec.Command(gobin, "test", "-count=1", "./...")
	cmd.Dir = filepath.Join("..", "..", "..", "sdk", "go")
	// The SDK is its own module with no dependencies: nothing is fetched, and
	// the race detector or any other flag of this run is not its business.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test in sdk/go: %v\n%s", err, out)
	}
}
