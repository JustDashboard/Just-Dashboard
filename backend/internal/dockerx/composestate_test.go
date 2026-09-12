package dockerx

import (
	"os"
	"path/filepath"
	"testing"
)

// "0/0 up" was a stack that existed only as a file, reported with a fraction
// whose denominator counted containers. These pin the six states apart.

func stack(declared []string, services ...ComposeService) *ComposeStack {
	st := &ComposeStack{
		Name: "app", Declared: declared, Services: services,
		Orphans: []string{}, ConfigFiles: []string{},
	}
	for _, svc := range services {
		if svc.Missing {
			continue
		}
		st.Containers++
		if svc.State == "running" {
			st.Running++
		}
	}
	return st
}

func TestStackStates(t *testing.T) {
	cases := []struct {
		name  string
		stack *ComposeStack
		want  StackState
	}{
		{
			"fully running",
			stack([]string{"web", "db"},
				ComposeService{Name: "web", State: "running"},
				ComposeService{Name: "db", State: "running"}),
			StackRunning,
		},
		{
			"partially running",
			stack([]string{"web", "db", "worker"},
				ComposeService{Name: "web", State: "running"},
				ComposeService{Name: "db", State: "exited"}),
			StackPartial,
		},
		{
			"degraded",
			stack([]string{"web"},
				ComposeService{Name: "web", State: "running", Health: "unhealthy"}),
			StackDegraded,
		},
		{
			"stopped",
			stack([]string{"web", "db"},
				ComposeService{Name: "web", State: "exited"},
				ComposeService{Name: "db", State: "exited"}),
			StackStopped,
		},
		{
			"file only, never deployed",
			stack([]string{"web", "db", "worker"}),
			StackNotDeployed,
		},
		{
			"containers with no reachable compose file",
			stack(nil, ComposeService{Name: "web", State: "running"}),
			StackUnknown,
		},
		{
			"nothing at all",
			stack(nil),
			StackUnknown,
		},
	}
	for _, c := range cases {
		got, summary := describeStack(c.stack)
		if got != c.want {
			t.Errorf("%s: state = %q, want %q (%s)", c.name, got, c.want, summary)
		}
		if summary == "" {
			t.Errorf("%s: every state needs a sentence", c.name)
		}
	}
}

// The specific regression: a stack on disk that has never been brought up must
// say what it is, not "0/0".
func TestNotDeployedStackCountsDeclaredServices(t *testing.T) {
	st := stack([]string{"web", "db", "worker"})
	resolveStackShapeForTest(t, st)
	if st.Total != 3 {
		t.Errorf("Total counts declared services, got %d", st.Total)
	}
	if st.Deployed {
		t.Error("nothing is deployed")
	}
	if st.Summary != "Not deployed · 3 services defined" {
		t.Errorf("summary = %q", st.Summary)
	}
}

func TestMissingServicesBecomeRows(t *testing.T) {
	st := stack([]string{"web", "db"}, ComposeService{Name: "web", State: "running"})
	resolveStackShapeForTest(t, st)
	found := false
	for _, svc := range st.Services {
		if svc.Name == "db" && svc.Missing {
			found = true
		}
	}
	if !found {
		t.Fatalf("a declared service with no container needs a row: %+v", st.Services)
	}
	if st.State != StackPartial {
		t.Errorf("state = %q, want partial", st.State)
	}
}

func TestOrphansAreNamedNotCountedAsServices(t *testing.T) {
	st := stack([]string{"web"},
		ComposeService{Name: "web", State: "running"},
		ComposeService{Name: "old-worker", State: "running"})
	resolveStackShapeForTest(t, st)
	if len(st.Orphans) != 1 || st.Orphans[0] != "old-worker" {
		t.Fatalf("orphans = %v", st.Orphans)
	}
	if st.Total != 1 {
		t.Errorf("an orphan is not a declared service, Total = %d", st.Total)
	}
	if st.State != StackRunning {
		t.Errorf("every declared service is up, so the stack is running, got %q", st.State)
	}
}

func TestDeclaredServicesFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte(`
services:
  web:
    image: nginx
  db:
    image: postgres
volumes:
  data:
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := declaredServicesFromFile([]string{path})
	if len(got) != 2 || got[0] != "db" || got[1] != "web" {
		t.Fatalf("declaredServicesFromFile = %v", got)
	}
	// A file that is not there, and one that does not parse, are both "no
	// answer" rather than a crash.
	if got := declaredServicesFromFile([]string{filepath.Join(dir, "nope.yml")}); len(got) != 0 {
		t.Errorf("a missing file declares nothing, got %v", got)
	}
	broken := filepath.Join(dir, "broken.yml")
	os.WriteFile(broken, []byte("services: [this is not a map"), 0o644)
	if got := declaredServicesFromFile([]string{broken}); len(got) != 0 {
		t.Errorf("an unparseable file declares nothing, got %v", got)
	}
}

func TestActiveStackStates(t *testing.T) {
	for _, s := range []StackState{StackRunning, StackPartial, StackDegraded, StackUnknown} {
		if !s.Active() {
			t.Errorf("%q holds running containers", s)
		}
	}
	for _, s := range []StackState{StackStopped, StackNotDeployed} {
		if s.Active() {
			t.Errorf("%q holds nothing running", s)
		}
	}
}

// resolveStackShape reads the compose file for its declared list; these
// fixtures set Declared directly, so the file read is skipped by leaving
// ConfigFiles empty and the rest of the function still runs.
func resolveStackShapeForTest(t *testing.T, st *ComposeStack) {
	t.Helper()
	resolveStackShape(st)
}
