package selfupdate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate/selfupdatetest"
)

// An updater killed part-way: the checkout fast-forwarded, the backend image
// already built over, the containers still on the old version. This is the
// state a Docker daemon restart during the frontend build leaves, and it is
// the record a backend finds when it starts afterwards.
func interrupted(t *testing.T) (checkout, from string, store *Store, installer *Installer) {
	t.Helper()
	_, checkout = installed(t)
	from = head(t, checkout, "HEAD")
	var log bytes.Buffer
	landed, _, err := forward(checkout, "0.6", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	dir := t.TempDir()
	store = NewStore(dir)
	if err := store.Save(&Run{
		ID: "x", Status: StatusRunning, Phase: PhaseBuilding,
		FromVersion: "0.5", ToVersion: "0.6", Ref: "main",
		Dir: checkout, Compose: "docker-compose.yml",
		FromCommit: from, ToCommit: landed,
		SavedImages: []SavedImage{
			{Current: "jd-backend:latest", Previous: "jd-backend:previous"},
			{Current: "jd-frontend:latest", Previous: "jd-frontend:previous"},
		},
		DatabaseCopy: filepath.Join(dir, PreUpdateDir, "vpsd-0.5-x.db"),
		StartedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if f, err := store.OpenLog(); err == nil {
		f.WriteString("#12 [frontend build 4/5] RUN bun run build\n")
		f.Close()
	}
	return checkout, from, store, NewInstaller(store, dir, testDockerHost, quiet())
}

const testDockerHost = "unix:///srv/jd/docker.sock"

// It used to mark the run failed and leave everything where the dead updater
// dropped it, so the next thing to recreate the stack brought up half of each
// version.
func TestABackendThatFindsTheUpdaterGonePutsBackWhatItChanged(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	checkout, from, store, installer := interrupted(t)

	installer.Reconcile(context.Background(), "0.5", lister(Sibling{Name: UpdaterContainer, State: "exited"}))

	// The tags, on the daemon this dashboard manages, and nothing else: no
	// container is recreated from inside one of them.
	if diff := selfupdatetest.AimedAt(calls(), testDockerHost,
		"image tag jd-backend:previous jd-backend:latest",
		"image tag jd-frontend:previous jd-frontend:latest"); diff != "" {
		t.Fatal(diff)
	}
	if at := head(t, checkout, "HEAD"); at != from {
		t.Fatalf("the checkout is on %s, want it back on %s", at, from)
	}
	run, _ := store.Load()
	if run.Status != StatusFailed || run.FinishedAt == nil {
		t.Fatalf("status %s", run.Status)
	}
	for _, want := range []string{
		"the updater stopped before 0.6 was running", "still on 0.5",
		"What it had changed was put back",
		"jd-backend:latest and jd-frontend:latest again name the 0.5 images",
		"the checkout at " + checkout + " is on " + from[:12],
		"docker compose -f docker-compose.yml up -d --no-build",
		run.DatabaseCopy,
	} {
		if !strings.Contains(run.Error, want) {
			t.Errorf("the run's error does not say %q:\n%s", want, run.Error)
		}
	}
	// What the dead updater printed is kept, and what was done since follows it.
	transcript := store.Transcript()
	for _, want := range []string{"[frontend build 4/5]", "The updater is gone", "$ docker image tag jd-backend:previous jd-backend:latest", "$ git reset --keep " + from} {
		if !strings.Contains(transcript, want) {
			t.Errorf("the transcript does not show %q:\n%s", want, transcript)
		}
	}
	if err := CheckLifecycleIdle(filepath.Dir(store.Path())); err != nil {
		t.Fatalf("the settled run still holds the lifecycle: %v", err)
	}
}

// An operator's edit in the way is left alone, and so is everything else that
// cannot be done: the message is then the commands, as every rollback's is.
func TestWhatCannotBePutBackIsSpelledOutAsCommands(t *testing.T) {
	selfupdatetest.Docker(t)
	checkout, from, store, installer := interrupted(t)
	write(t, filepath.Join(checkout, "version.txt"), "mine\n")
	landed := head(t, checkout, "HEAD")

	installer.Reconcile(context.Background(), "0.5", lister())

	if head(t, checkout, "HEAD") != landed {
		t.Fatal("the checkout was moved over a local edit")
	}
	if got, _ := os.ReadFile(filepath.Join(checkout, "version.txt")); strings.TrimSpace(string(got)) != "mine" {
		t.Fatalf("the edit was lost: %q", got)
	}
	run, _ := store.Load()
	for _, want := range []string{
		"Putting back what it had changed did not finish", "could not move the checkout back",
		"To finish it by hand, run these in " + checkout,
		"docker image tag jd-backend:previous jd-backend:latest",
		"git reset --keep " + from, "docker compose -f docker-compose.yml up -d",
	} {
		if !strings.Contains(run.Error, want) {
			t.Errorf("the run's error does not say %q:\n%s", want, run.Error)
		}
	}
	if strings.Contains(run.Error, "--hard") {
		t.Errorf("suggests discarding work:\n%s", run.Error)
	}
}

// A checkout the update never recorded moving is not this run's to move.
func TestACheckoutTheUpdateDidNotRecordMovingIsLeftWhereItIs(t *testing.T) {
	selfupdatetest.Docker(t)
	checkout, _, store, installer := interrupted(t)
	landed := head(t, checkout, "HEAD")
	if err := store.Update(func(r *Run) { r.ToCommit = "" }); err != nil {
		t.Fatal(err)
	}

	installer.Reconcile(context.Background(), "0.5", lister())

	if head(t, checkout, "HEAD") != landed {
		t.Fatal("moved a checkout it could not know it had moved")
	}
	if run, _ := store.Load(); !strings.Contains(run.Error, "did not record moving the checkout") {
		t.Fatalf("error:\n%s", run.Error)
	}
}

// An updater killed before it changed anything leaves nothing to put back,
// and the run says so rather than listing commands nobody needs.
func TestAnUpdaterThatDiedBeforeChangingAnythingLeavesNothingToDo(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	checkout, from, store, installer := interrupted(t)
	git(t, checkout, "reset", "-q", "--hard", from)
	if err := store.Update(func(r *Run) { r.ToCommit, r.SavedImages = "", nil }); err != nil {
		t.Fatal(err)
	}

	installer.Reconcile(context.Background(), "0.5", lister())

	if got := calls(); len(got) != 0 {
		t.Fatalf("docker was asked for %q with no image saved", got)
	}
	run, _ := store.Load()
	if !strings.Contains(run.Error, "It had not replaced any image") || strings.Contains(run.Error, "did not finish") {
		t.Fatalf("error:\n%s", run.Error)
	}
}

// With no daemon to ask, the tags cannot be moved. The checkout still can.
func TestWithoutDockerTheTagsAreLeftAndTheCommandsGiven(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	checkout, from, store, installer := interrupted(t)
	if err := store.Update(func(r *Run) { r.StartedAt = time.Now().Add(-3 * time.Hour).UTC() }); err != nil {
		t.Fatal(err)
	}

	installer.Reconcile(context.Background(), "0.5", nil)

	if got := calls(); len(got) != 0 {
		t.Fatalf("docker was called with no daemon answering: %q", got)
	}
	if head(t, checkout, "HEAD") != from {
		t.Fatal("the checkout was not moved back")
	}
	run, _ := store.Load()
	if !strings.Contains(run.Error, "Docker could not be asked") || !strings.Contains(run.Error, "docker image tag jd-frontend:previous jd-frontend:latest") {
		t.Fatalf("error:\n%s", run.Error)
	}
}

// The updater writes which images it saved before the build can replace one,
// because from then on the record may be all that is left of it.
func TestTheImagesAnUpdateSavedAreInItsRecordBeforeTheBuild(t *testing.T) {
	store := NewStore(t.TempDir())
	run := &Run{ID: "x", FromVersion: "0.5", ToVersion: "0.6", Dir: "/opt/jd", FromCommit: "abc123"}
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	var atBuild []SavedImage
	f := &fakeUpdate{images: []string{"jd-backend:latest", "jd-frontend:latest"}}
	steps := f.steps()
	steps.build = func() error {
		recorded, _ := store.Load()
		atBuild = recorded.SavedImages
		return nil
	}
	var out bytes.Buffer
	if err := replaceStack(store, run, "docker-compose.yml", steps, &out); err != nil {
		t.Fatal(err)
	}
	if len(atBuild) != 2 || atBuild[0] != (SavedImage{Current: "jd-backend:latest", Previous: "jd-backend:previous"}) {
		t.Fatalf("when the build started the record held %+v", atBuild)
	}
}
