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
	// leftBehind reports a container that could not be removed.
	leftBehind bool
}

func (f *releaseTaskRuntimeFake) RunReleaseTask(ctx context.Context, request ReleaseTaskRuntimeRequest, emit func(BuildLog) error) (int, bool, error) {
	f.requests = append(f.requests, request)
	for _, line := range f.output {
		if err := emit(BuildLog{Stream: "stdout", Text: line}); err != nil {
			return -1, !f.leftBehind, err
		}
	}
	if f.block {
		<-ctx.Done()
		return -1, !f.leftBehind, ctx.Err()
	}
	if f.code != 0 {
		return f.code, !f.leftBehind, errors.New("release task exited")
	}
	return 0, !f.leftBehind, nil
}

func TestImageReleaseTaskRunsWithTheReleaseVariables(t *testing.T) {
	t.Parallel()
	runner := &releaseTaskRuntimeFake{output: []string{"connected to postgres://app:secret-database-password@db-1.jd.internal/app with task-only on 8000"}}
	logs := []string{}
	evidence, cleanup, err := runImageReleaseTask(context.Background(), runner, ReleaseTaskRuntimeRequest{
		Run: EngineRun{ID: 9}, Release: Release{EnvironmentID: 4}, Index: 1,
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
	if recorded := cleanup.(map[string]any); recorded["container"] != "jd-e4-run9-task2" || recorded["removed"] != true {
		t.Fatalf("cleanup = %v", cleanup)
	}
	stuck := &releaseTaskRuntimeFake{leftBehind: true}
	if _, cleanup, _ := runImageReleaseTask(context.Background(), stuck, ReleaseTaskRuntimeRequest{
		Task: ReleaseTaskConfig{Name: "migrate", Command: "true", TimeoutSeconds: 5},
	}, nil, func(BuildLog) error { return nil }); cleanup.(map[string]any)["removed"] != false {
		t.Fatalf("a container that could not be removed was reported removed: %v", cleanup)
	}

	failing := &releaseTaskRuntimeFake{code: 127}
	if evidence, _, err := runImageReleaseTask(context.Background(), failing, ReleaseTaskRuntimeRequest{
		Task: ReleaseTaskConfig{Name: "migrate", Command: "npx prisma migrate deploy", TimeoutSeconds: 5},
	}, nil, func(BuildLog) error { return nil }); err == nil || evidence.ExitCode != 127 {
		t.Fatalf("a failing task = %+v, %v", evidence, err)
	}

	slow := &releaseTaskRuntimeFake{block: true}
	started := time.Now()
	if _, _, err := runImageReleaseTask(context.Background(), slow, ReleaseTaskRuntimeRequest{
		Task: ReleaseTaskConfig{Name: "slow", Command: "sleep 60", TimeoutSeconds: 1},
	}, nil, func(BuildLog) error { return nil }); err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(started) > 10*time.Second {
		t.Fatalf("a task past its timeout = %v", err)
	}
	if _, _, err := runImageReleaseTask(context.Background(), runner, ReleaseTaskRuntimeRequest{
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
		{" \t ", nil, nil},
	} {
		entrypoint, args := releaseTaskArgv(fixture.command)
		if !slices.Equal(entrypoint, fixture.entrypoint) || !slices.Equal(args, fixture.args) {
			t.Fatalf("%q = %v %v", fixture.command, entrypoint, args)
		}
	}
	if tokens := releaseTaskCommandTokens("cd api && env A=1 npx prisma migrate deploy; ./bin/seed | tee log || exec curl -f x"); !slices.Equal(tokens, []string{"npx", "./bin/seed", "tee", "curl"}) {
		t.Fatalf("tokens = %v", tokens)
	}
	if tools := releaseTaskHostTools([]ReleaseTaskConfig{{Command: "set -e; if [ -n \"$X\" ]; then echo ok; fi; export A=1; curl -f x"}}); !slices.Equal(tools, []string{"curl"}) {
		t.Fatalf("host tools = %v", tools)
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
	configuration.Build.Method, configuration.Build.Target = BuildRecipe, "production"
	if canonicalConfiguration(configuration).Build.Target != "" {
		t.Fatal("a Dockerfile stage outlived the switch to a recipe")
	}
}

func TestReleaseTaskContainerFollowsTheReleaseNetwork(t *testing.T) {
	t.Parallel()
	request := ReleaseTaskRuntimeRequest{
		Run: EngineRun{ID: 3}, Release: Release{ID: 5, EnvironmentID: 7}, Image: "sha256:" + strings.Repeat("a", 64),
		Task: ReleaseTaskConfig{Name: "migrate", Command: "bin/migrate"}, Variables: map[string]string{"DATABASE_URL": "x"},
	}
	spec, err := releaseTaskContainerSpec(request, []string{"jd-db-1"})
	if err != nil || spec.NetworkMode != "" || !slices.Equal(spec.Networks, []string{"jd-db-1"}) || spec.Name != "jd-e7-run3-task1" {
		t.Fatalf("bridge task = %+v, %v", spec, err)
	}
	labelled := false
	for _, label := range spec.Labels {
		labelled = labelled || (label.Name == releaseTaskLabel && label.Value == "migrate")
	}
	if !labelled {
		t.Fatalf("the task container is not marked as one: %+v", spec.Labels)
	}
	// On the host's network, as the release itself runs, so localhost and
	// host-bound services are the ones the application reaches.
	request.Plan.HostNetwork = true
	spec, err = releaseTaskContainerSpec(request, []string{"jd-db-1"})
	if err != nil || spec.NetworkMode != "host" || len(spec.Networks) != 0 {
		t.Fatalf("host-network task = %+v, %v", spec, err)
	}
	request.Task.Command = "  "
	if _, err := releaseTaskContainerSpec(request, nil); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("a blank stored command = %v", err)
	}

	runner := &releaseTaskRuntimeFake{}
	if _, _, err := runImageReleaseTask(context.Background(), runner, ReleaseTaskRuntimeRequest{
		Plan: RuntimePlanConfig{InternalPort: 8000, HostNetwork: true},
		Task: ReleaseTaskConfig{Name: "migrate", Command: "true", TimeoutSeconds: 5},
	}, nil, func(BuildLog) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, set := runner.requests[0].Variables["PORT"]; set {
		t.Fatal("a host-network task was given the container port as PORT, which the release is not")
	}
}

func TestPlanValidationRefusesBlankReleaseTaskCommands(t *testing.T) {
	t.Parallel()
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildDockerfile, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{{Name: "migrate", Command: " \n\t", TimeoutSeconds: 60, Env: []string{}}}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
	}
	if err := configuration.Validate(); err == nil {
		t.Fatal("a whitespace-only release task command was accepted")
	}
}
