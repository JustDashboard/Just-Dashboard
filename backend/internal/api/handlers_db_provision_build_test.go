package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

// stubRecipeBuilds stands in for the docker binary: each build records what
// it was asked, prints one line, and exits with the code the test sends.
type stubRecipeBuilds struct {
	mu          sync.Mutex
	asked       []dockerx.BuildOptions
	dockerfiles []string
	exit        chan int
}

func stubRecipeBuild(t *testing.T) *stubRecipeBuilds {
	t.Helper()
	stub := &stubRecipeBuilds{exit: make(chan int)}
	original := buildRecipeImage
	buildRecipeImage = func(ctx context.Context, _ *dockerx.Client, opts dockerx.BuildOptions, out chan<- dockerx.LogLine) (int, error) {
		dockerfile, err := os.ReadFile(filepath.Join(opts.Dir, "Dockerfile"))
		if err != nil {
			return -1, err
		}
		stub.mu.Lock()
		stub.asked = append(stub.asked, opts)
		stub.dockerfiles = append(stub.dockerfiles, string(dockerfile))
		stub.mu.Unlock()
		out <- dockerx.LogLine{Stream: "stderr", Text: "#6 [2/2] RUN apt-get install postgresql-18-postgis-3"}
		select {
		case code := <-stub.exit:
			return code, nil
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}
	t.Cleanup(func() { buildRecipeImage = original })
	return stub
}

func awaitJob(t *testing.T, s *Server, id string) (jobs.Job, []jobs.Line) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, lines, ok := s.modules.jobs.Get(id)
		if !ok {
			t.Fatalf("job %s is gone", id)
		}
		if job.Status != jobs.StatusRunning {
			return job, lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is still running", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The full template's image is built as a job: the recipe in a directory of
// its own, the base image pulled fresh, the digest passed in for the label.
// Asking again while it runs answers the same job, a second image waits its
// turn, and the trail says who started which.
func TestTheFullImageIsBuiltAsAJobThatIsAttachedTo(t *testing.T) {
	s := testServer(t)
	provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})
	stub := stubRecipeBuild(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	build := func(body string) (int, jobs.Job, string) {
		t.Helper()
		rec := admin.do(http.MethodPost, "/api/v1/databases/provision/build", body, nil)
		if rec.Code != http.StatusAccepted {
			return rec.Code, jobs.Job{}, rec.Body.String()
		}
		return rec.Code, readJSON[jobs.Job](t, rec), rec.Body.String()
	}

	code, first, body := build(`{"engine":"postgres-full","version":"18"}`)
	if code != http.StatusAccepted || first.Status != jobs.StatusRunning ||
		first.Kind != "database.image.build.postgres-full.18" || first.Target != "jd-postgres-full:18" || first.StartedBy != "tester" {
		t.Fatalf("build = %d %+v %s", code, first, body)
	}
	if code, again, _ := build(`{"engine":"postgres-full"}`); code != http.StatusAccepted || again.ID != first.ID {
		t.Errorf("asking again while it builds = %d %+v, want the running job %s", code, again, first.ID)
	}
	if code, _, body := build(`{"engine":"postgres-full","version":"17"}`); code != http.StatusConflict || !strings.Contains(body, "build_running") {
		t.Errorf("a second image while one builds = %d %s", code, body)
	}
	// The page that comes back finds the build and attaches to it.
	options := readJSON[[]provisionedOption](t, admin.do(http.MethodGet, "/api/v1/databases/provision/options", "", nil))
	for _, o := range options {
		for _, v := range o.Versions {
			if o.Engine == "postgres-full" && v.Version == "18" && (v.Build == nil || v.Build.Job != first.ID || v.Build.Ready) {
				t.Errorf("options while it builds = %+v", v.Build)
			}
		}
	}

	stub.exit <- 0
	done, lines := awaitJob(t, s, first.ID)
	if done.Status != jobs.StatusSucceeded {
		t.Fatalf("build ended %s: %s", done.Status, done.Error)
	}
	var printed []string
	for _, line := range lines {
		printed = append(printed, line.Stream+": "+line.Text)
	}
	if text := strings.Join(printed, "\n"); !strings.Contains(text, "stderr: #6 [2/2] RUN apt-get install") ||
		!strings.Contains(text, "status: Built jd-postgres-full:18") {
		t.Errorf("the job's output = %s", text)
	}
	stub.mu.Lock()
	asked, dockerfile := stub.asked[0], stub.dockerfiles[0]
	stub.mu.Unlock()
	digest := recipeDigest(postgresFullRecipe)
	if asked.Tag != "jd-postgres-full:18" || !asked.Pull || asked.NoCache || asked.Dockerfile != "" ||
		asked.BuildArgs["PG_MAJOR"] != "18" || asked.BuildArgs["JD_RECIPE"] != digest || len(asked.BuildArgs) != 2 {
		t.Errorf("build options = %+v", asked)
	}
	if dockerfile != postgresFullRecipe {
		t.Error("the build was given something other than the recipe")
	}
	if _, err := os.Stat(asked.Dir); !os.IsNotExist(err) {
		t.Errorf("the recipe's directory %s was left behind: %v", asked.Dir, err)
	}

	// A build that fails says so, with the code, and the next one may start.
	code, failing, body := build(`{"engine":"postgres-full","version":"17"}`)
	if code != http.StatusAccepted || failing.ID == first.ID {
		t.Fatalf("a build after the first finished = %d %s", code, body)
	}
	stub.exit <- 1
	if failed, _ := awaitJob(t, s, failing.ID); failed.Status != jobs.StatusFailed || !strings.Contains(failed.Error, "exited with status 1") {
		t.Errorf("a failed build = %s %q", failed.Status, failed.Error)
	}

	var started, attached int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'database.image.build' AND detail LIKE '%"attached":false%'`).Scan(&started)
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'database.image.build' AND detail LIKE '%"attached":true%' AND target = 'jd-postgres-full:18'`).Scan(&attached)
	if started != 2 || attached != 1 {
		t.Errorf("audit: %d builds started and %d attached to, want 2 and 1", started, attached)
	}
}

// Building is the first part of provisioning and needs what provisioning
// needs; and only a template made from a recipe has anything to build.
func TestImageBuildsAreAnAdministratorsAndOnlyForRecipes(t *testing.T) {
	s := testServer(t)
	provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})
	stub := stubRecipeBuild(t)
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, string(role)+"-user", role)}
		if rec := c.do(http.MethodPost, "/api/v1/databases/provision/build", `{"engine":"postgres-full"}`, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s: build = %d %s", role, rec.Code, rec.Body.String())
		}
	}
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for _, body := range []string{
		`{"engine":"postgres"}`,
		`{"engine":"timescaledb","version":"18"}`,
		`{"engine":"postgres-full","version":"16"}`,
		`{"engine":"postgres-full","version":"18; docker run evil"}`,
		`{"engine":"nope"}`,
	} {
		if rec := admin.do(http.MethodPost, "/api/v1/databases/provision/build", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("build %s = %d %s", body, rec.Code, rec.Body.String())
		}
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.asked) != 0 {
		t.Errorf("a refused request built %d images", len(stub.asked))
	}
}
