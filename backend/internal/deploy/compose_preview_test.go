package deploy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func composePreviewAnalysisFixture() *ComposeAnalysis {
	return &ComposeAnalysis{
		Files: []string{"compose.yml"}, PrimaryService: "web",
		Services: []ComposeServicePlan{
			{Name: "web", BuildContext: ".", Ports: []string{"80:8080"}, Mounts: []string{
				"uploads:/uploads", "./nginx.conf:/etc/nginx.conf:ro", "/etc/localtime:/etc/localtime:ro", "/srv/cache:/cache", "/scratch",
			}},
			{Name: "db", Image: "postgres:17", Ports: []string{"5432:5432"}, Mounts: []string{"dbdata:/var/lib/postgresql/data"}},
		},
	}
}

func TestComposePreviewFactsSayWhatEachServiceGets(t *testing.T) {
	facts := composePreviewFacts(BuildPlanConfig{Method: BuildCompose}, composePreviewAnalysisFixture())
	if facts.Public != "web" || len(facts.Refusals) != 0 || len(facts.Services) != 2 {
		t.Fatalf("facts = %+v", facts)
	}
	web, db := facts.Services[0], facts.Services[1]
	if !web.Public || web.Name != "web" || db.Public {
		t.Fatalf("the public service is listed first: %+v", facts.Services)
	}
	if !reflect.DeepEqual(web.Volumes, []string{"/uploads", "/cache", "/scratch"}) ||
		!reflect.DeepEqual(web.Checkout, []string{"/etc/nginx.conf"}) ||
		!reflect.DeepEqual(web.LeftOut, []string{"/etc/localtime"}) ||
		!reflect.DeepEqual(web.Ports, []string{"80:8080"}) {
		t.Fatalf("web = %+v", web)
	}
	if !reflect.DeepEqual(db.Volumes, []string{"/var/lib/postgresql/data"}) || !reflect.DeepEqual(db.Ports, []string{"5432:5432"}) {
		t.Fatalf("db = %+v", db)
	}

	// The operator's own choice of public service outranks the analysis's.
	chosen := composePreviewFacts(BuildPlanConfig{Method: BuildCompose, PrimaryService: "db"}, composePreviewAnalysisFixture())
	if chosen.Public != "db" || !chosen.Services[0].Public || chosen.Services[0].Name != "db" {
		t.Fatalf("chosen = %+v", chosen)
	}

	hostAccess := composePreviewAnalysisFixture()
	hostAccess.Services[1].Advanced = []string{"privileged", "docker_socket", "pid_namespace"}
	refused := composePreviewFacts(BuildPlanConfig{Method: BuildCompose}, hostAccess)
	if !reflect.DeepEqual(refused.Refusals, []string{"service db mounts the Docker socket", "service db runs privileged"}) {
		t.Fatalf("refusals = %v", refused.Refusals)
	}
	if missing := composePreviewFacts(BuildPlanConfig{Method: BuildCompose}, nil); len(missing.Refusals) != 1 {
		t.Fatalf("a plan without analysis was not refused: %+v", missing)
	}
}

func insertComposePreviewProduction(t *testing.T, f *automationFixture, analysis *ComposeAnalysis) {
	t.Helper()
	runtime := RuntimePlanConfig{Strategy: StrategyStopFirst, InternalPort: 8080, HostPort: 80}
	build := BuildPlanConfig{Method: BuildCompose, PrimaryService: "web", Secrets: []BuildSecretConfig{{Variable: "TOKEN", Step: "install"}},
		ReleaseTasks: []ReleaseTaskConfig{{Name: "migrate", Command: "./migrate", TimeoutSeconds: 60, Runner: ReleaseTaskRunnerImage}}}
	evidence := StoredBuildEvidence{Compose: analysis}
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) SELECT environment_id,2,kind,config_json,identity_json,digest,created_at FROM deploy_sources WHERE environment_id=?`,
		`UPDATE deploy_environments SET desired_revision=2 WHERE id=?`,
	} {
		if _, err := f.store.DB.Exec(statement, f.environmentID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_build_plans(environment_id,revision,method,config_json,evidence_json,preview,digest,created_at) VALUES(?,2,'compose',?,?,'','build',1)`, f.environmentID, string(mustJSON(build)), string(mustJSON(evidence))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at) VALUES(?,2,?,'','runtime',1)`, f.environmentID, string(mustJSON(runtime))); err != nil {
		t.Fatal(err)
	}
}

// A Compose stack previews like one container: the plan keeps the stack,
// drops what production publishes, its secrets and its release tasks, and
// runs stop-first, since one project per environment holds one release.
func TestComposePreviewPlanIsIsolatedAndStopFirst(t *testing.T) {
	f := newAutomationFixture(t)
	insertComposePreviewProduction(t, f, composePreviewAnalysisFixture())
	created, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Review", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 4, PreviewRef: "refs/pull/4/head", Revision: strings.Repeat("c", 40)}
	approvePreviewFixture(t, f, &created.Trigger, event)
	preview, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	var method, buildRaw, runtimeRaw, strategy string
	if err := f.store.DB.QueryRow(`SELECT b.method,b.config_json,r.config_json,e.strategy FROM deploy_build_plans b JOIN deploy_runtime_plans r ON r.environment_id=b.environment_id AND r.revision=b.revision JOIN deploy_environments e ON e.id=b.environment_id WHERE b.environment_id=?`, preview.EnvironmentID).Scan(&method, &buildRaw, &runtimeRaw, &strategy); err != nil {
		t.Fatal(err)
	}
	var build BuildPlanConfig
	var runtime RuntimePlanConfig
	if json.Unmarshal([]byte(buildRaw), &build) != nil || json.Unmarshal([]byte(runtimeRaw), &runtime) != nil {
		t.Fatal("malformed preview plan")
	}
	build.Method = BuildMethod(method)
	if build.Method != BuildCompose || build.PrimaryService != "web" || len(build.ReleaseTasks) != 0 || len(build.Secrets) != 0 {
		t.Fatalf("preview build = %s", buildRaw)
	}
	if !runtime.PreviewIsolation || runtime.HostPort != 0 || runtime.BindAddress != "127.0.0.1" || runtime.Strategy != StrategyStopFirst || strategy != string(StrategyStopFirst) {
		t.Fatalf("preview runtime = %s (strategy %s)", runtimeRaw, strategy)
	}
	if err := validatePreviewPlan(preview.EnvironmentID, build, runtime); err != nil {
		t.Fatal(err)
	}
	// A compatibility Compose deployment has no release engine to isolate.
	if err := validatePreviewPlan(preview.EnvironmentID, BuildPlanConfig{Method: BuildLegacyCompose}, runtime); !errors.Is(err, ErrPreviewIsolation) {
		t.Fatalf("legacy Compose preview accepted: %v", err)
	}
	facts, err := NewPlanningStore(f.store, nil, nil).ComposePreviewFacts(t.Context(), f.environmentID)
	if err != nil || facts == nil || facts.Public != "web" {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
}

func TestComposePreviewOfAStackNeedingTheServerIsRefused(t *testing.T) {
	f := newAutomationFixture(t)
	analysis := composePreviewAnalysisFixture()
	analysis.Services[0].Advanced = []string{"docker_socket"}
	insertComposePreviewProduction(t, f, analysis)
	created, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{Name: "Review", Kind: TriggerGitHub, Provider: "github", Enabled: true, Config: TriggerConfig{Repository: "acme/app", Ref: "main", Preview: true}})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: 5, PreviewRef: "refs/pull/5/head", Revision: strings.Repeat("d", 40)}
	approvePreviewFixture(t, f, &created.Trigger, event)
	if _, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event); !errors.Is(err, ErrPreviewIsolation) || !strings.Contains(err.Error(), "service web mounts the Docker socket") {
		t.Fatalf("a stack mounting the Docker socket was previewed: %v", err)
	}
	var environments int
	_ = f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_environments WHERE kind='preview'`).Scan(&environments)
	if environments != 0 {
		t.Fatalf("a refused preview left %d environments", environments)
	}
}

// The pull request's files are what a preview runs, so they are read before
// Compose is: a file Compose would read on this server outside the commit,
// through a link or by an absolute path, refuses the preview.
func TestComposePreviewSourcesRefuseFilesOutsideTheCommit(t *testing.T) {
	write := func(t *testing.T, root, name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const stack = "services:\n  web:\n    image: caddy:2-alpine\n    env_file: [app.env]\n    configs: [site]\n    volumes: [\"/etc/localtime:/etc/localtime:ro\", \"./site.txt:/srv/site.txt:ro\"]\nconfigs:\n  site:\n    file: ./site.txt\n"
	fixture := func(t *testing.T, compose string) string {
		root := t.TempDir()
		write(t, root, "compose.yml", compose)
		write(t, root, "app.env", "GREETING=hello\n")
		write(t, root, "site.txt", "hello\n")
		return root
	}
	if err := composePreviewSources(fixture(t, stack), []string{"compose.yml"}); err != nil {
		t.Fatalf("the commit's own stack was refused: %v", err)
	}
	for name, setup := range map[string]func(t *testing.T) string{
		"a linked env file": func(t *testing.T) string {
			root := fixture(t, stack)
			_ = os.Remove(filepath.Join(root, "app.env"))
			if err := os.Symlink("/etc/hostname", filepath.Join(root, "app.env")); err != nil {
				t.Fatal(err)
			}
			return root
		},
		"a linked config file": func(t *testing.T) string {
			root := fixture(t, stack)
			_ = os.Remove(filepath.Join(root, "site.txt"))
			if err := os.Symlink("/etc/hostname", filepath.Join(root, "site.txt")); err != nil {
				t.Fatal(err)
			}
			return root
		},
		"an absolute env file": func(t *testing.T) string {
			return fixture(t, strings.Replace(stack, "env_file: [app.env]", "env_file: [/etc/hostname]", 1))
		},
		"a linked Compose file": func(t *testing.T) string {
			root := fixture(t, stack)
			write(t, root, "real/compose.yml", stack)
			_ = os.Remove(filepath.Join(root, "compose.yml"))
			if err := os.Symlink(filepath.Join(root, "real", "compose.yml"), filepath.Join(root, "compose.yml")); err != nil {
				t.Fatal(err)
			}
			return root
		},
		"a privileged service": func(t *testing.T) string {
			return fixture(t, strings.Replace(stack, "image: caddy:2-alpine", "image: caddy:2-alpine\n    privileged: true", 1))
		},
		"an include": func(t *testing.T) string {
			return fixture(t, "include: [other.yml]\n"+stack)
		},
		"a file named by a variable": func(t *testing.T) string {
			return fixture(t, strings.Replace(stack, "env_file: [app.env]", "env_file: [\"${ENV_FILE}\"]", 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := composePreviewSources(setup(t), []string{"compose.yml"})
			if !errors.Is(err, ErrPreviewIsolation) {
				t.Fatalf("refusal = %v", err)
			}
			t.Log(err)
		})
	}
}
