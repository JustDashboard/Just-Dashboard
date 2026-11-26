package media

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

// recyclarrFake is the Recyclarr container: a file, and a command whose
// reports are what Recyclarr 8.8.1 printed in the live run for a first sync
// and for one with nothing to do. A first sync of a guide profile adds it
// to the stand-in application, as the real one does.
type recyclarrFake struct {
	*stackstest.Fake
	t     *testing.T
	stack *arrFakeStack

	fileWrites []string
	commands   []string
	synced     map[string]bool
	// busy is how many times a sync is found already running.
	busy int
	// report and exit replace what a sync of a service prints and returns;
	// broken makes running it fail outright.
	report map[string]string
	exit   map[string]int
	broken error
	// createsNothing is a sync that reports success without the profile.
	createsNothing bool
}

func newRecyclarrFake(t *testing.T) *recyclarrFake {
	t.Helper()
	stack := newArrFakeStack(t)
	fake := &recyclarrFake{Fake: stack.env, t: t, stack: stack, synced: map[string]bool{}, report: map[string]string{}, exit: map[string]int{}}
	stack.env.ExecFunc = fake.exec
	return fake
}

func (f *recyclarrFake) WriteFile(ctx context.Context, service, path string, content []byte, mode int64, uid, gid int) error {
	f.fileWrites = append(f.fileWrites, fmt.Sprintf("%s %s %o %d:%d", service, path, mode, uid, gid))
	return f.Fake.WriteFile(ctx, service, path, content, mode, uid, gid)
}

func (f *recyclarrFake) exec(service, user string, argv []string) (stacks.ExecResult, error) {
	if service != "recyclarr" || user != "" {
		f.t.Errorf("exec in %s as %q", service, user)
	}
	command := strings.Join(argv, " ")
	f.commands = append(f.commands, command)
	if command == "pgrep -f recyclarr sync" {
		if f.busy > 0 {
			f.busy--
			return stacks.ExecResult{Stdout: "1235\n"}, nil
		}
		return stacks.ExecResult{ExitCode: 1}, nil
	}
	target, isSync := strings.CutPrefix(command, "recyclarr sync ")
	target, onlyTheStacksFile := strings.CutSuffix(target, " --config "+recyclarrConfigPath)
	if !isSync || !onlyTheStacksFile || (target != "radarr" && target != "sonarr") {
		f.t.Errorf("unexpected command %q", command)
		return stacks.ExecResult{ExitCode: 127}, nil
	}
	if f.broken != nil {
		return stacks.ExecResult{ExitCode: -1}, f.broken
	}
	if report, replaced := f.report[target]; replaced {
		return stacks.ExecResult{Stdout: report, ExitCode: f.exit[target]}, nil
	}
	config := string(f.Files["recyclarr:"+recyclarrConfigPath])
	quality, profile := "720p", ""
	for name, choice := range recyclarrChoices {
		trashID, guideName := choice.radarrTrashID, choice.radarrGuideName
		if target == "sonarr" {
			trashID, guideName = choice.sonarrTrashID, choice.sonarrGuideName
		}
		if trashID != "" && strings.Contains(config, "trash_id: "+trashID) {
			quality, profile = name, guideName+" (Just Dashboard)"
		}
	}
	if quality == "2160p" {
		// The live run recorded 720p and 1080p; a 2160p report differs only
		// in the names, which the step does not read.
		quality = "1080p"
	}
	pass := "again"
	if !f.synced[target+config] {
		f.synced[target+config] = true
		pass = "first"
		if profile != "" && !f.createsNothing {
			app := map[string]*arrFake{"radarr": f.stack.radarr, "sonarr": f.stack.sonarr}[target]
			app.mu.Lock()
			app.profiles = append(app.profiles, map[string]any{"id": len(app.profiles) + 1, "name": profile})
			app.mu.Unlock()
		}
	}
	return stacks.ExecResult{Stdout: string(arrFixture(f.t, "recyclarr-"+quality+"-"+pass+"-"+target+".txt"))}, nil
}

func (f *recyclarrFake) syncs() []string {
	var syncs []string
	for _, command := range f.commands {
		if strings.HasPrefix(command, "recyclarr sync") {
			syncs = append(syncs, command)
		}
	}
	return syncs
}

func TestRecyclarrSyncWritesItsFileOnceAndReportsTheProfiles(t *testing.T) {
	fake := newRecyclarrFake(t)

	first := arrRun(t, fake, "recyclarr.sync")
	if !slices.Equal(fake.fileWrites, []string{"recyclarr /config/recyclarr.yml 640 1000:1000"}) {
		t.Fatalf("file writes: %q", fake.fileWrites)
	}
	if !slices.Equal(fake.syncs(), []string{"recyclarr sync radarr --config /config/recyclarr.yml", "recyclarr sync sonarr --config /config/recyclarr.yml"}) || fake.commands[0] != "pgrep -f recyclarr sync" {
		t.Fatalf("commands: %q", fake.commands)
	}
	want := map[string]string{
		"quality": "1080p", "radarrProfileId": "7", "radarrProfileName": "HD Bluray + WEB (Just Dashboard)",
		"sonarrProfileId": "7", "sonarrProfileName": "WEB-1080p (Just Dashboard)",
	}
	for key, value := range want {
		if first.Outputs[key] != value {
			t.Errorf("output %s: got %q, want %q", key, first.Outputs[key], value)
		}
	}
	if !strings.Contains(first.Message, "Recyclarr synced") || !slices.Contains(fake.Lines, "recyclarr: Created 41 New Custom Formats") {
		t.Fatalf("first run: %q, %q", first.Message, fake.Lines)
	}

	// The file is Recyclarr 8's: both applications at their Compose names,
	// keys from the environment, nothing deleted, nobody's scores reset.
	content := fake.Files["recyclarr:"+recyclarrConfigPath]
	for _, key := range []string{fake.stack.radarr.key, fake.stack.sonarr.key} {
		if strings.Contains(string(content), key) {
			t.Fatal("the file holds an API key")
		}
	}
	if !strings.Contains(string(content), "api_key: !env_var RADARR_API_KEY\n") || !strings.Contains(string(content), "api_key: !env_var SONARR_API_KEY\n") {
		t.Fatalf("the keys are not read from the environment:\n%s", content)
	}
	var file map[string]map[string]struct {
		BaseURL  string                `yaml:"base_url"`
		Delete   *bool                 `yaml:"delete_old_custom_formats"`
		Sizes    struct{ Type string } `yaml:"quality_definition"`
		Profiles []struct {
			TrashID string `yaml:"trash_id"`
			Name    string
			Reset   struct{ Enabled *bool } `yaml:"reset_unmatched_scores"`
		} `yaml:"quality_profiles"`
	}
	if err := yaml.Unmarshal(content, &file); err != nil {
		t.Fatalf("the file is not YAML: %v\n%s", err, content)
	}
	movies, series := file["radarr"][recyclarrMovies], file["sonarr"][recyclarrSeries]
	if movies.BaseURL != "http://radarr:7878" || series.BaseURL != "http://sonarr:8989" || movies.Sizes.Type != "movie" || series.Sizes.Type != "series" ||
		movies.Delete == nil || *movies.Delete || series.Delete == nil || *series.Delete || len(movies.Profiles) != 1 || len(series.Profiles) != 1 {
		t.Fatalf("file: %+v", file)
	}
	if profile := movies.Profiles[0]; profile.TrashID != "d1d67249d3890e49bc12e275d989a7e9" || profile.Name != want["radarrProfileName"] || profile.Reset.Enabled == nil || *profile.Reset.Enabled {
		t.Fatalf("films profile: %+v", profile)
	}
	if profile := series.Profiles[0]; profile.TrashID != "72dae194fc92bf828f32cde7744e51a1" || profile.Name != want["sonarrProfileName"] || profile.Reset.Enabled == nil || *profile.Reset.Enabled {
		t.Fatalf("series profile: %+v", profile)
	}

	fake.fileWrites, fake.commands = nil, nil
	fake.stack.forget()
	second := arrRun(t, fake, "recyclarr.sync")
	if len(fake.fileWrites) != 0 || len(fake.syncs()) != 2 || !strings.Contains(second.Message, "already in step") {
		t.Fatalf("second run: %q, file writes %q", second.Message, fake.fileWrites)
	}
	for key, value := range want {
		if second.Outputs[key] != value {
			t.Errorf("second run, output %s: got %q, want %q", key, second.Outputs[key], value)
		}
	}
	// The step itself writes nothing to either application: Recyclarr does.
	arrWant(t, fake.stack.written(), nil)
	if len(fake.stack.radarr.profiles) != 7 || len(fake.stack.sonarr.profiles) != 7 {
		t.Fatal("a profile was made twice")
	}

	// Another quality: the file is written again, and the earlier profile
	// stays in the applications.
	fake.fileWrites = nil
	fake.OptionsMap[OptionQuality] = "2160p"
	third := arrRun(t, fake, "recyclarr.sync")
	if len(fake.fileWrites) != 1 || third.Outputs["radarrProfileName"] != "UHD Bluray + WEB (Just Dashboard)" || third.Outputs["sonarrProfileName"] != "WEB-2160p (Just Dashboard)" ||
		third.Outputs["radarrProfileId"] != "8" || len(fake.stack.radarr.profiles) != 8 {
		t.Fatalf("2160p: %+v", third)
	}
	content = fake.Files["recyclarr:"+recyclarrConfigPath]
	if !strings.Contains(string(content), "64fb5f9858489bdac2af690e27c8f42f") || !strings.Contains(string(content), "d1498e7d189fbe6c7110ceaabb7473e6") || strings.Contains(string(content), "d1d67249d3890e49bc12e275d989a7e9") {
		t.Fatalf("2160p file:\n%s", content)
	}
}

func TestRecyclarrSyncUsesTheBuiltInProfileFor720p(t *testing.T) {
	fake := newRecyclarrFake(t)
	fake.OptionsMap[OptionQuality] = "720p"
	result := arrRun(t, fake, "recyclarr.sync")
	content := string(fake.Files["recyclarr:"+recyclarrConfigPath])
	if strings.Contains(content, "quality_profiles") || strings.Contains(content, "trash_id") || !strings.Contains(content, "quality_definition:\n      type: movie") {
		t.Fatalf("720p file:\n%s", content)
	}
	want := map[string]string{"quality": "720p", "radarrProfileId": "3", "radarrProfileName": "HD-720p", "sonarrProfileId": "3", "sonarrProfileName": "HD-720p"}
	for key, value := range want {
		if result.Outputs[key] != value {
			t.Errorf("output %s: got %q, want %q", key, result.Outputs[key], value)
		}
	}
	if !strings.Contains(result.Message, "have no 720p profile") || !strings.Contains(result.Message, `built-in "HD-720p"`) {
		t.Fatalf("message: %q", result.Message)
	}
	if len(fake.stack.radarr.profiles) != 6 {
		t.Fatal("a profile was made for 720p")
	}

	// The person removed the built-in profile: nothing is made in its place.
	fake.stack.sonarr.profiles = fake.stack.sonarr.profiles[:2]
	if err := arrFail(t, fake, "recyclarr.sync"); !stacks.IsPermanent(err) || !strings.Contains(err.Error(), `Sonarr no longer has its built-in "HD-720p"`) {
		t.Fatalf("no built-in profile: %v", err)
	}

	fake.OptionsMap[OptionQuality] = "480p"
	fake.commands = nil
	if err := arrFail(t, fake, "recyclarr.sync"); !stacks.IsPermanent(err) || len(fake.commands) != 0 {
		t.Fatalf("an unknown quality: %v after %q", err, fake.commands)
	}
	// No quality at all is the manifest's default.
	fake.OptionsMap[OptionQuality] = ""
	if result := arrRun(t, fake, "recyclarr.sync"); result.Outputs["quality"] != "1080p" {
		t.Fatalf("default quality: %+v", result)
	}
}

func TestRecyclarrSyncReportsWhatRecyclarrSaidWentWrong(t *testing.T) {
	for name, test := range map[string]struct {
		target, fixture string
		exit            int
		want            string
	}{
		"a refused key":         {"radarr", "recyclarr-unauthorized.txt", 1, "Recyclarr could not sync Radarr: HTTP 401: Unauthorized - check your api_key"},
		"an unreachable app":    {"radarr", "recyclarr-unreachable.txt", 1, "Recyclarr could not sync Radarr: Exiting due to fatal error: Connection refused"},
		"a file it cannot read": {"sonarr", "recyclarr-unparsable.txt", 0, "Recyclarr could not sync Sonarr: Config parsing failed"},
		"no report at all":      {"sonarr", "", 0, "it exited with 0 and did not report Sonarr as completed"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newRecyclarrFake(t)
			fake.report[test.target], fake.exit[test.target] = "", test.exit
			if test.fixture != "" {
				fake.report[test.target] = string(arrFixture(t, test.fixture))
			}
			err := arrFail(t, fake, "recyclarr.sync")
			if stacks.IsPermanent(err) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v", err)
			}
		})
	}

	fake := newRecyclarrFake(t)
	fake.broken = context.DeadlineExceeded
	err := arrFail(t, fake, "recyclarr.sync")
	if stacks.IsPermanent(err) || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "carries on in its container") {
		t.Fatalf("a sync that outlives the command: %v", err)
	}

	fake = newRecyclarrFake(t)
	fake.createsNothing = true
	if err := arrFail(t, fake, "recyclarr.sync"); !strings.Contains(err.Error(), `Radarr has no quality profile named "HD Bluray + WEB (Just Dashboard)"`) {
		t.Fatalf("a finished sync without the profile: %v", err)
	}

	fake = newRecyclarrFake(t)
	fake.stack.radarr.fail["GET qualityprofile"] = 503
	if err := arrFail(t, fake, "recyclarr.sync"); stacks.IsPermanent(err) || !strings.Contains(err.Error(), "Radarr answered 503") {
		t.Fatalf("an upstream error: %v", err)
	}
}

func TestRecyclarrSyncWaitsForASyncAlreadyRunning(t *testing.T) {
	fake := newRecyclarrFake(t)
	fake.busy = 3
	arrRun(t, fake, "recyclarr.sync")
	if !slices.Equal(fake.commands[:4], slices.Repeat([]string{"pgrep -f recyclarr sync"}, 4)) || len(fake.syncs()) != 2 {
		t.Fatalf("commands: %q", fake.commands)
	}
	if !slices.Contains(fake.Lines, "Recyclarr is already running a sync; waiting for it to finish") {
		t.Fatalf("lines: %q", fake.Lines)
	}

	fake = newRecyclarrFake(t)
	fake.busy = 1 << 30
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stackstest.Run(ctx, fake, arrStep(t, "recyclarr.sync")); err == nil || !strings.Contains(err.Error(), "still running an earlier sync") || len(fake.syncs()) != 0 {
		t.Fatalf("out of time while waiting: %v", err)
	}
}

func TestRecyclarrReportReadsOnlyItsOwnInstance(t *testing.T) {
	report := recyclarrRead(string(arrFixture(t, "recyclarr-1080p-first-radarr.txt")), recyclarrMovies)
	if !report.completed || !report.changed || len(report.errors) != 0 || !slices.Contains(report.summary, `Created 1 Profiles: ["HD Bluray + WEB (Just Dashboard)"]`) {
		t.Fatalf("first: %+v", report)
	}
	if other := recyclarrRead(string(arrFixture(t, "recyclarr-1080p-first-radarr.txt")), recyclarrSeries); other.completed || len(other.summary) != 0 {
		t.Fatalf("another instance's lines were read: %+v", other)
	}
	if again := recyclarrRead(string(arrFixture(t, "recyclarr-1080p-again-sonarr.txt")), recyclarrSeries); !again.completed || again.changed {
		t.Fatalf("again: %+v", again)
	}
}
