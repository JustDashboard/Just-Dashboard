package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate/selfupdatetest"
)

// serviceFor is an install of version current that follows repo and reads it
// from url, in a checkout this process can see.
func serviceFor(t *testing.T, repo, url, current string) *Service {
	t.Helper()
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(checkout, "docker-compose.yml"), "name: jd\n")
	return New(Options{
		Current: current, DataDir: t.TempDir(), UpdateDir: checkout,
		Repo: repo, Ref: DefaultRef, CheckOnline: true,
		BaseURL: url, DockerHost: "unix:///var/run/docker.sock", Log: quiet(),
	})
}

// stubDocker stands in for the docker CLI, so starting an update here starts
// nothing on this machine.
func stubDocker(t *testing.T) func() []string {
	t.Helper()
	return selfupdatetest.Docker(t)
}

// What the page is told, which is all it has to go on: what is on offer,
// whether that is a release, and what is on the branch and not released yet.
func TestTheReportSaysWhetherWhatIsOfferedIsARelease(t *testing.T) {
	srv, _ := repository(t, threeReleases, "v0.6")

	own := serviceFor(t, DefaultRepo, srv.URL, "0.5").Report(context.Background(), Forced)
	if !own.Available || own.Latest != "0.6" || !own.Tagged || own.Pending != "0.7" || !own.Check.TagsOnly {
		t.Fatalf("the project's own repository reported latest %q tagged %v pending %q tagsOnly %v",
			own.Latest, own.Tagged, own.Pending, own.Check.TagsOnly)
	}

	fork := serviceFor(t, "someone/fork", srv.URL, "0.5").Report(context.Background(), Forced)
	if !fork.Available || fork.Latest != "0.7" || fork.Tagged || fork.Pending != "" || fork.Check.TagsOnly {
		t.Fatalf("a fork reported latest %q tagged %v pending %q tagsOnly %v",
			fork.Latest, fork.Tagged, fork.Pending, fork.Check.TagsOnly)
	}

	// Nothing on offer is not "an untagged version on offer".
	current := serviceFor(t, DefaultRepo, srv.URL, "0.6").Report(context.Background(), Forced)
	if current.Available || current.Tagged || current.Latest != "0.6" || current.Pending != "0.7" {
		t.Fatalf("an install on the newest release reported latest %q available %v tagged %v pending %q",
			current.Latest, current.Available, current.Tagged, current.Pending)
	}
}

// A build that is not a release tells the page which commit it is, and a
// release tells it nothing, so the two stop reading alike on the Version page.
func TestTheReportNamesTheCommitOfABuildThatIsNotARelease(t *testing.T) {
	srv, _ := repository(t, threeReleases, "v0.6")
	checkout := t.TempDir()
	options := Options{
		Current: "0.6", DataDir: t.TempDir(), UpdateDir: checkout, Repo: DefaultRepo, Ref: DefaultRef,
		CheckOnline: true, BaseURL: srv.URL, Log: quiet(),
	}
	if got := New(options).Report(context.Background(), Forced); got.Version != "0.6" || got.Commit != "" {
		t.Fatalf("a release reported version %q commit %q", got.Version, got.Commit)
	}
	options.Commit = "98c9349\n"
	if got := New(options).Report(context.Background(), Forced); got.Version != "0.6" || got.Commit != "98c9349" {
		t.Fatalf("a build from 98c9349 reported version %q commit %q", got.Version, got.Commit)
	}
}

// The updater decides what to install from the record alone, so the gate has
// to be written into it by the backend that knows which repository this is.
func TestAnUpdateFromTheProjectsOwnRepositoryIsRecordedAsTagOnly(t *testing.T) {
	srv, _ := repository(t, threeReleases, "v0.6")
	for repo, want := range map[string]bool{DefaultRepo: true, "someone/fork": false} {
		calls := stubDocker(t)
		service := serviceFor(t, repo, srv.URL, "0.5")
		rep := service.Report(context.Background(), Forced)
		run, err := service.Install(context.Background(), rep.Latest, "tester")
		if err != nil {
			t.Fatalf("%s: %v", repo, err)
		}
		if run.RequireTag != want {
			t.Errorf("%s: requireTag %v, want %v", repo, run.RequireTag, want)
		}
		transcript := service.Transcript()
		if want != strings.Contains(transcript, "origin's tag v0.6, or nothing") {
			t.Errorf("%s: the transcript's first lines are %q", repo, transcript)
		}
		if got := strings.Join(calls(), "\n"); !strings.Contains(got, "run --detach") {
			t.Errorf("%s: the updater was not started: %s", repo, got)
		}
	}
}
