package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

type releaseTaskRuntimeFake struct {
	requests []ReleaseTaskRuntimeRequest
	output   []string
	code     int
	block    bool
}

func (f *releaseTaskRuntimeFake) RunReleaseTask(ctx context.Context, request ReleaseTaskRuntimeRequest, emit func(BuildLog) error) (int, error) {
	f.requests = append(f.requests, request)
	for _, line := range f.output {
		if err := emit(BuildLog{Stream: "stdout", Text: line}); err != nil {
			return -1, err
		}
	}
	if f.block {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	if f.code != 0 {
		return f.code, errors.New("release task exited")
	}
	return 0, nil
}

func TestImageReleaseTaskRunsWithTheReleaseVariables(t *testing.T) {
	t.Parallel()
	runner := &releaseTaskRuntimeFake{output: []string{"connected to postgres://app:secret-database-password@db-1.jd.internal/app with task-only on 8000"}}
	logs := []string{}
	evidence, err := runImageReleaseTask(context.Background(), runner, ReleaseTaskRuntimeRequest{
		Image: "sha256:" + strings.Repeat("a", 64), Plan: RuntimePlanConfig{InternalPort: 8000},
		Task:      ReleaseTaskConfig{Name: "migrate", Command: "python manage.py migrate", TimeoutSeconds: 5, Env: []string{"MIGRATION_TOKEN"}, Runner: ReleaseTaskRunnerImage},
		Variables: map[string]string{"DATABASE_URL": "postgres://app:secret-database-password@db-1.jd.internal/app", "SECRET_KEY": "runtime"},
		Secret:    map[string]bool{"DATABASE_URL": true, "SECRET_KEY": true},
	}, map[string]string{"MIGRATION_TOKEN": "task-only"}, func(line BuildLog) error {
		logs = append(logs, line.Text)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := runner.requests[0].Variables
	if sent["DATABASE_URL"] == "" || sent["SECRET_KEY"] != "runtime" || sent["MIGRATION_TOKEN"] != "task-only" || sent["PORT"] != "8000" {
		t.Fatalf("task variables = %v", sent)
	}
	if joined := strings.Join(logs, "\n"); strings.Contains(joined, "secret-database-password") || strings.Contains(joined, "task-only") ||
		!strings.Contains(joined, "on 8000") {
		t.Fatalf("output was not redacted: %v", logs)
	}
	if evidence.Runner != ReleaseTaskRunnerImage || evidence.ExitCode != 0 || !slices.Equal(evidence.VariableNames, []string{"MIGRATION_TOKEN"}) {
		t.Fatalf("evidence = %+v", evidence)
	}

	failing := &releaseTaskRuntimeFake{code: 127}
	if evidence, err := runImageReleaseTask(context.Background(), failing, ReleaseTaskRuntimeRequest{
		Task: ReleaseTaskConfig{Name: "migrate", Command: "npx prisma migrate deploy", TimeoutSeconds: 5},
	}, nil, func(BuildLog) error { return nil }); err == nil || evidence.ExitCode != 127 {
		t.Fatalf("a failing task = %+v, %v", evidence, err)
	}

	slow := &releaseTaskRuntimeFake{block: true}
	started := time.Now()
	if _, err := runImageReleaseTask(context.Background(), slow, ReleaseTaskRuntimeRequest{
		Task: ReleaseTaskConfig{Name: "slow", Command: "sleep 60", TimeoutSeconds: 1},
	}, nil, func(BuildLog) error { return nil }); err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(started) > 10*time.Second {
		t.Fatalf("a task past its timeout = %v", err)
	}
	if _, err := runImageReleaseTask(context.Background(), runner, ReleaseTaskRuntimeRequest{
		Task: ReleaseTaskConfig{Name: "migrate", Command: "true", TimeoutSeconds: 5, Env: []string{"UNKNOWN"}},
	}, map[string]string{}, func(BuildLog) error { return nil }); err == nil {
		t.Fatal("a task with an unavailable variable ran")
	}
}

func TestReleaseTaskCommandShapes(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		command          string
		entrypoint, args []string
	}{
		{"bin/migrate", []string{"bin/migrate"}, []string{}},
		{"python manage.py migrate --noinput", []string{"python"}, []string{"manage.py", "migrate", "--noinput"}},
		{"npx prisma migrate deploy && node seed.js", []string{"/bin/sh", "-c"}, []string{"npx prisma migrate deploy && node seed.js"}},
		{"echo $DATABASE_URL", []string{"/bin/sh", "-c"}, []string{"echo $DATABASE_URL"}},
	} {
		entrypoint, args := releaseTaskArgv(fixture.command)
		if !slices.Equal(entrypoint, fixture.entrypoint) || !slices.Equal(args, fixture.args) {
			t.Fatalf("%q = %v %v", fixture.command, entrypoint, args)
		}
	}
	if tokens := releaseTaskCommandTokens("cd api && env A=1 npx prisma migrate deploy; ./bin/seed | tee log || exec curl -f x"); !slices.Equal(tokens, []string{"npx", "./bin/seed", "tee", "curl"}) {
		t.Fatalf("tokens = %v", tokens)
	}
	for command, wantsApplication := range map[string]bool{
		"npx prisma migrate deploy":            true,
		"bundle exec rails db:migrate":         true,
		"python manage.py migrate":             true,
		"node_modules/.bin/knex migrate":       true,
		"./scripts/migrate.sh":                 true,
		"curl -fsS https://hooks.example":      false,
		"psql \"$DATABASE_URL\" -c 'select 1'": false,
	} {
		if _, needs := releaseTaskNeedsApplication(command); needs != wantsApplication {
			t.Fatalf("%q needs the application = %v", command, needs)
		}
	}
	image := releaseTaskImage(runtimeReleaseSnapshot{Compose: &ResolvedComposeSnapshot{PrimaryService: "web", Services: []ResolvedComposeService{
		{Plan: ComposeServicePlan{Name: "db"}, Reference: "postgres", Digest: "sha256:" + strings.Repeat("1", 64)},
		{Plan: ComposeServicePlan{Name: "web"}, Reference: "app", Digest: "sha256:" + strings.Repeat("2", 64), ConfigDigest: "sha256:" + strings.Repeat("3", 64)},
	}}})
	if image != "sha256:"+strings.Repeat("3", 64) {
		t.Fatalf("Compose release task image = %q", image)
	}
}

func TestPlanValidationKeepsImageTasksToBuildsWithAnImage(t *testing.T) {
	t.Parallel()
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{{Name: "migrate", Command: "bin/migrate", TimeoutSeconds: 60, Env: []string{}, Runner: ReleaseTaskRunnerImage}}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
	}
	if err := configuration.Validate(); err == nil || !strings.Contains(err.Error(), "produces none") {
		t.Fatalf("an image task without an image = %v", err)
	}
	configuration.Build.Method = BuildDockerfile
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	configuration.Build.ReleaseTasks[0].Runner = "ssh"
	if err := configuration.Validate(); err == nil {
		t.Fatal("an unknown runner was accepted")
	}
	configuration.Build.ReleaseTasks[0].Runner = ""
	configuration.Build.Target = "production"
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	configuration.Build.Target = "prod --push"
	if err := configuration.Validate(); err == nil {
		t.Fatal("a malformed target was accepted")
	}
}
