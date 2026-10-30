package openapigen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The generator is held to a small hand-written document (testdata/
// fixture.json) whose clients are committed beside it as golden files: a
// change to what the generator writes shows up as a diff of those files in
// review, before it reaches sdk/. JD_UPDATE_SDK=1 rewrites them, as it
// rewrites sdk/.

func fixtureFiles(t *testing.T) Files {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("testdata", "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestTheGeneratedClientsAreTheGoldenOnes(t *testing.T) {
	files := fixtureFiles(t)
	for _, name := range GeneratedFiles {
		path := filepath.Join("testdata", "golden", filepath.FromSlash(name))
		if os.Getenv("JD_UPDATE_SDK") == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, files[name], 0o644); err != nil {
				t.Fatal(err)
			}
		}
		golden, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v; write the golden files with JD_UPDATE_SDK=1 go test ./internal/openapigen", err)
		}
		if !bytes.Equal(golden, files[name]) {
			t.Errorf("%s is not what the generator writes for testdata/fixture.json; review the change and rewrite it with JD_UPDATE_SDK=1 go test ./internal/openapigen", path)
		}
	}
}

// Generating twice writes the same bytes: nothing depends on map order.
func TestGenerationIsDeterministic(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "api", "deploy-openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		again, err := Generate(doc)
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range first {
			if !bytes.Equal(body, again[name]) {
				t.Fatalf("%s differs between two generations", name)
			}
		}
	}
}

// What the fixture says comes out in the clients: names without their
// package unless two share one, enums as constants, nullable values as
// pointers or `| null`, parameters in the path's order, a stream as its
// address, and the error envelope's fields.
func TestTheGeneratedClientsSayWhatTheDocumentSays(t *testing.T) {
	files := fixtureFiles(t)
	goTypes, goOps, goClient := string(files["go/types.go"]), string(files["go/operations.go"]), string(files["go/client.go"])
	tsTypes, tsClient := string(files["typescript/types.ts"]), string(files["typescript/client.ts"])
	for _, want := range []struct{ file, text string }{
		{goTypes, "type DeploySummary struct"},
		{goTypes, "type ImagescanSummary struct"},
		{goTypes, "type EngineRun struct"},
		{goTypes, "type ArchivedDeployment struct"},
		{goTypes, "type RunState string"},
		{goTypes, `RunStateSucceeded RunState = "succeeded"`},
		{goTypes, "EndedAt *time.Time `json:\"endedAt,omitempty\"`"},
		{goTypes, "LastRun *EngineRun `json:\"lastRun,omitempty\"`"},
		{goTypes, "Scan ImagescanSummary `json:\"scan,omitzero\"`"},
		{goTypes, "Deployments []DeploySummary `json:\"deployments\"`"},
		{goTypes, "Counts map[string]int64 `json:\"counts\"`"},
		{goTypes, "CPU DeploySummaryCPU `json:\"cpu,omitzero\"`"},
		{goTypes, "Evidence any `json:\"evidence\"`"},
		{goTypes, "never read back by any route"},
		{goOps, "func (c *Client) DeploymentRunCreate(ctx context.Context, id int64, env int64, body DeploymentRunCreateRequest, opts ...RequestOption) (*EngineRun, error)"},
		{goOps, "func (c *Client) DeploymentVariablePut(ctx context.Context, id int64, env int64, name string, body VariableWrite, params *DeploymentVariablePutParams, opts ...RequestOption) (*DeploymentVariablePutResponse, error)"},
		{goOps, `"/variables/"+url.PathEscape(name)`},
		{goOps, "func (c *Client) DeploymentVariableDelete(ctx context.Context, id int64, env int64, name string, opts ...RequestOption) error"},
		{goOps, "Destructive: it needs a role that may take destructive actions"},
		{goOps, "func (c *Client) DeployList(ctx context.Context, params *DeployListParams, opts ...RequestOption) (json.RawMessage, error)"},
		{goOps, "The answer is one of Fleet or []ArchivedDeployment"},
		{goOps, "func (c *Client) DeploymentRunStreamURL(id int64, run int64) string"},
		{goOps, "func (c *Client) DeploymentRequestExport(ctx context.Context, id int64, params *DeploymentRequestExportParams, opts ...RequestOption) ([]byte, error)"},
		{goOps, `url.PathEscape(string(action))`},
		{goOps, "body *PlayerRequest"},
		{goOps, "Only an interactive session may call it"},
		{goClient, "Field string `json:\"field,omitempty\"`"},
		{goClient, "Retryable bool `json:\"retryable,omitempty\"`"},
		{goClient, `const BasePath = "/api/v1"`},
		{tsTypes, "export interface DeploySummary {"},
		{tsTypes, "lastRun?: EngineRun | null"},
		{tsTypes, "deployments?: DeploySummary[] | null"},
		{tsTypes, "export const RunState = {"},
		{tsTypes, "export type RunState = (typeof RunState)[keyof typeof RunState]"},
		{tsClient, "export function deploymentVariablePut(\n  client: Client,\n  id: number,\n  env: number,\n  name: string,\n  body: VariableWrite,\n  query?: DeploymentVariablePutQuery,"},
		{tsClient, "Promise<Fleet | ArchivedDeployment[] | null>"},
		{tsClient, "export function deploymentRunStreamUrl(client: Client, id: number, run: number): string"},
		{tsClient, "readonly retryable?: boolean"},
		{tsClient, "accept: \"text\",\n      contentType: \"text/csv\","},
	} {
		if !strings.Contains(spaced(want.file), spaced(want.text)) {
			t.Errorf("the generated clients do not say %q", want.text)
		}
	}
	for _, file := range []string{goTypes, goOps, goClient} {
		if !strings.HasPrefix(file, "// Code generated by openapigen") || !strings.Contains(file, "DO NOT EDIT.") || !strings.Contains(file, "JD_UPDATE_SDK=1") {
			t.Error("a Go file does not open with the generated-code line and how to regenerate it")
		}
	}
	for _, file := range []string{tsTypes, tsClient} {
		if !strings.HasPrefix(file, "// Generated by openapigen") || !strings.Contains(file, "JD_UPDATE_SDK=1") {
			t.Error("a TypeScript file does not say it is generated and how to regenerate it")
		}
	}
}

// The golden Go client compiles and passes vet: a generator change that
// writes Go the compiler refuses fails here, before sdk/.
func TestTheGeneratedGoCompiles(t *testing.T) {
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(gobin); err != nil {
		t.Skipf("no go command beside this toolchain: %v", err)
	}
	dir := t.TempDir()
	for name, body := range fixtureFiles(t) {
		if !strings.HasPrefix(name, "go/") {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, strings.TrimPrefix(name, "go/")), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gobin, "vet", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
}

// spaced collapses the runs of spaces and tabs gofmt aligns fields with.
func spaced(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' }), " ")
}

func TestNames(t *testing.T) {
	for _, c := range []struct{ in, exported, unexported, camel, snake string }{
		{"projectId", "ProjectID", "projectID", "projectId", "project_id"},
		{"deploymentRunCreate", "DeploymentRunCreate", "deploymentRunCreate", "deploymentRunCreate", "deployment_run_create"},
		{"HTTPStatus", "HTTPStatus", "httpStatus", "httpStatus", "http_status"},
		{"deploy.EngineRun", "DeployEngineRun", "deployEngineRun", "deployEngineRun", "deploy_engine_run"},
		{"blue_green", "BlueGreen", "blueGreen", "blueGreen", "blue_green"},
		{"run.state", "RunState", "runState", "runState", "run_state"},
		{"p95Ms", "P95Ms", "p95Ms", "p95Ms", "p95_ms"},
		// A run of capitals is one word: APIJSON is not split.
		{"deployOpenAPIJSON", "DeployOpenAPIJSON", "deployOpenAPIJSON", "deployOpenApijson", "deploy_open_apijson"},
		{"502", "X502", "x502", "502", "502"},
	} {
		if got := exported(c.in); got != c.exported {
			t.Errorf("exported(%q) = %q, want %q", c.in, got, c.exported)
		}
		if got := unexported(c.in); got != c.unexported {
			t.Errorf("unexported(%q) = %q, want %q", c.in, got, c.unexported)
		}
		if got := camel(c.in); got != c.camel {
			t.Errorf("camel(%q) = %q, want %q", c.in, got, c.camel)
		}
		if got := Snake(c.in); got != c.snake {
			t.Errorf("Snake(%q) = %q, want %q", c.in, got, c.snake)
		}
	}
}
