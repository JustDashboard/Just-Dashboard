package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeUpdate records every step in order. A step named in fail fails, once: the
// second health check is the rollback's.
type fakeUpdate struct {
	calls  []string
	fail   map[string]bool
	images []string
}

func (f *fakeUpdate) step(name string) error {
	f.calls = append(f.calls, name)
	if f.fail[name] {
		delete(f.fail, name)
		return errors.New(name + " failed")
	}
	return nil
}

func (f *fakeUpdate) steps() updateSteps {
	return updateSteps{
		docker: func(args ...string) error { return f.step(strings.Join(args, " ")) },
		images: func() []string { return f.images },
		build:  func() error { return f.step("build") },
		start:  func() error { return f.step("start") },
		health: func(_ string, window time.Duration) error {
			return f.step("health " + window.String())
		},
		restore: func(from string) error { return f.step("restore " + from) },
	}
}

const (
	saveBackend      = "image tag jd-backend:latest jd-backend:previous"
	saveFrontend     = "image tag jd-frontend:latest jd-frontend:previous"
	putBackendBack   = "image tag jd-backend:previous jd-backend:latest"
	putFrontendBack  = "image tag jd-frontend:previous jd-frontend:latest"
	upOnTheRelease   = "compose -f docker-compose.yml up -d --no-build"
	upOnTheCheckout  = "compose -f docker-compose.yml up -d --no-build --remove-orphans"
	restoreCommit    = "restore abc123"
	probeForward     = "health 5m0s"
	probeTheRollback = "health 3m0s"
)

func runFakeUpdate(t *testing.T, f *fakeUpdate, health string) error {
	t.Helper()
	if f.images == nil {
		f.images = []string{"jd-backend:latest", "jd-frontend:latest"}
	}
	store := NewStore(t.TempDir())
	run := &Run{ID: "x", FromVersion: "0.5", ToVersion: "0.6", Dir: "/opt/jd", FromCommit: "abc123", Health: health}
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	return replaceStack(store, run, "docker-compose.yml", f.steps(), &out)
}

func checkCalls(t *testing.T, f *fakeUpdate, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("steps:\n  %s\nwant:\n  %s", strings.Join(f.calls, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestASuccessfulUpdateSavesImagesAndGoesNoFurther(t *testing.T) {
	f := &fakeUpdate{}
	if err := runFakeUpdate(t, f, "http://x/healthz"); err != nil {
		t.Fatal(err)
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build", "start", probeForward)
}

func TestAFailedHealthCheckGoesBack(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{probeForward: true}}
	err := runFakeUpdate(t, f, "http://x/healthz")
	if err == nil || !strings.Contains(err.Error(), "health 5m0s failed") || !strings.Contains(err.Error(), "was rolled back") {
		t.Fatalf("error = %v", err)
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build", "start", probeForward,
		putBackendBack, putFrontendBack, upOnTheRelease, restoreCommit, upOnTheCheckout, probeTheRollback)
}

func TestAFailedStartGoesBack(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{"start": true}}
	if err := runFakeUpdate(t, f, "http://x/healthz"); err == nil || !strings.Contains(err.Error(), "was rolled back") {
		t.Fatalf("error = %v", err)
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build", "start",
		putBackendBack, putFrontendBack, upOnTheRelease, restoreCommit, upOnTheCheckout, probeTheRollback)
}

// A failed build has touched no container, so the release's compose file is
// never run against the stack that is still serving.
func TestAFailedBuildDoesNotStartTheStackOnTheNewComposeFile(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{"build": true}}
	if err := runFakeUpdate(t, f, "http://x/healthz"); err == nil || !strings.Contains(err.Error(), "was rolled back") {
		t.Fatalf("error = %v", err)
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build",
		putBackendBack, putFrontendBack, restoreCommit, upOnTheCheckout, probeTheRollback)
}

// The first `up` runs the release's compose file, which the old images may not
// suit. It is not the last word: the one after the checkout is.
func TestAFailedFirstRestartStillRestoresAndTriesAgain(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{probeForward: true, upOnTheRelease: true}}
	if err := runFakeUpdate(t, f, "http://x/healthz"); err == nil || !strings.Contains(err.Error(), "was rolled back") {
		t.Fatalf("error = %v", err)
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build", "start", probeForward,
		putBackendBack, putFrontendBack, upOnTheRelease, restoreCommit, upOnTheCheckout, probeTheRollback)
}

// A checkout that will not move is reported, and the stack still comes back.
func TestACheckoutThatWillNotMoveIsReportedWithTheCommandsToRun(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{"start": true, restoreCommit: true}}
	err := runFakeUpdate(t, f, "http://x/healthz")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"start failed", "rollback did not finish", "restore abc123 failed", "/opt/jd",
		"git reset --keep abc123", "docker image tag jd-backend:previous jd-backend:latest", "docker compose -f docker-compose.yml up -d"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--hard") {
		t.Errorf("suggests discarding work: %v", err)
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build", "start",
		putBackendBack, putFrontendBack, upOnTheRelease, restoreCommit, upOnTheCheckout, probeTheRollback)
}

func TestAPreviousVersionThatDoesNotAnswerIsReported(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{"start": true, probeTheRollback: true}}
	err := runFakeUpdate(t, f, "http://x/healthz")
	if err == nil || !strings.Contains(err.Error(), "the previous version is not answering") {
		t.Fatalf("error = %v", err)
	}
}

// Without every old image there is nothing to restart onto. Once containers have
// been replaced, moving only the checkout would make the two disagree.
func TestWithoutTheOldImagesAReplacedStackIsLeftAlone(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{saveFrontend: true, "start": true}}
	err := runFakeUpdate(t, f, "http://x/healthz")
	if err == nil || !strings.Contains(err.Error(), "stay on the failed update") {
		t.Fatalf("error = %v", err)
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build", "start", putBackendBack)
}

// Before anything is replaced, the checkout can still go back, and restarting
// would pick up whichever image could not be saved.
func TestWithoutTheOldImagesAFailedBuildRestartsNothing(t *testing.T) {
	f := &fakeUpdate{fail: map[string]bool{saveFrontend: true, "build": true}}
	if err := runFakeUpdate(t, f, "http://x/healthz"); err == nil {
		t.Fatal("no error")
	}
	checkCalls(t, f, saveBackend, saveFrontend, "build", putBackendBack, restoreCommit)
}

// An update that cannot save its images still runs, as it did before there was
// anything to save.
func TestImagesThatCannotBeSavedDoNotStopAnUpdate(t *testing.T) {
	f := &fakeUpdate{images: []string{}}
	if err := runFakeUpdate(t, f, "http://x/healthz"); err != nil {
		t.Fatal(err)
	}
	checkCalls(t, f, "build", "start", probeForward)
}

func TestPreviousImage(t *testing.T) {
	for image, want := range map[string]string{
		"just-dashboard-backend:latest": "just-dashboard-backend:previous",
		"just-dashboard-backend":        "just-dashboard-backend:previous",
		"registry:5000/jd/backend":      "registry:5000/jd/backend:previous",
		"registry:5000/jd/backend:1.2":  "registry:5000/jd/backend:previous",
		"jd/backend@sha256:abc":         "",
		"":                              "",
	} {
		if got := PreviousImage(image); got != want {
			t.Errorf("%q: got %q, want %q", image, got, want)
		}
	}
}

func TestBuiltImagesLeaveOutWhatIsPulled(t *testing.T) {
	got := builtImages([]byte(`{"name":"jd","services":{
		"proxy":{"image":"caddy:2-alpine"},
		"frontend":{"image":"just-dashboard-frontend:latest","build":{"context":"./frontend"}},
		"backend":{"image":"just-dashboard-backend:latest","build":{"context":"./backend"}},
		"extra":{"build":{"context":"./extra"}}}}`))
	want := []string{"jd-extra", "just-dashboard-backend:latest", "just-dashboard-frontend:latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if builtImages([]byte("not json")) != nil {
		t.Fatal("read images out of nothing")
	}
}

// The message once named the health URL as the directory to run compose in.
func TestAHealthFailureNamesTheDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	var out bytes.Buffer
	err := waitHealthy(context.Background(), server.URL, "/opt/jd", time.Millisecond, &out)
	if err == nil || !strings.Contains(err.Error(), "in /opt/jd to see why") || strings.Contains(err.Error(), "in "+server.URL+" to") {
		t.Fatalf("error = %v", err)
	}
}

func head(t *testing.T, dir, rev string) string {
	t.Helper()
	return strings.TrimSpace(git(t, dir, "rev-parse", rev))
}

func TestATagIsInstalledEvenWhenTheBranchHasMovedOn(t *testing.T) {
	upstream, checkout := installed(t)
	tagged := head(t, upstream, "HEAD")
	git(t, upstream, "tag", "v0.6")
	release(t, upstream, "0.7")

	var log bytes.Buffer
	commit, version, err := fastForward(context.Background(), checkout, "main", "0.6", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if commit != tagged || version != "0.6" {
		t.Fatalf("landed on %s as %s, want %s as 0.6", commit, version, tagged)
	}
}

// release.yml makes lightweight tags today, and one made by hand may be annotated.
func TestAnAnnotatedTagIsPeeled(t *testing.T) {
	upstream, checkout := installed(t)
	tagged := head(t, upstream, "HEAD")
	git(t, upstream, "tag", "-a", "v0.6", "-m", "0.6")
	release(t, upstream, "0.7")
	var log bytes.Buffer
	commit, _, err := fastForward(context.Background(), checkout, "main", "0.6", &log)
	if err != nil || commit != tagged {
		t.Fatalf("commit %s, err %v\n%s", commit, err, log.String())
	}
}

func TestABranchThatMovedOnWithoutATagIsRefused(t *testing.T) {
	upstream, checkout := installed(t)
	before := head(t, checkout, "HEAD")
	release(t, upstream, "0.7")
	var log bytes.Buffer
	_, _, err := fastForward(context.Background(), checkout, "main", "0.6", &log)
	if err == nil || !strings.Contains(err.Error(), "has moved on to 0.7") {
		t.Fatalf("error = %v", err)
	}
	if head(t, checkout, "HEAD") != before {
		t.Fatal("the checkout moved")
	}
}

func TestABranchThatIsNotTheAnnouncedReleaseIsRefused(t *testing.T) {
	_, checkout := installed(t)
	before := head(t, checkout, "HEAD")
	var log bytes.Buffer
	_, _, err := fastForward(context.Background(), checkout, "main", "0.7", &log)
	if err == nil || !strings.Contains(err.Error(), "does not contain release 0.7") {
		t.Fatalf("error = %v", err)
	}
	if head(t, checkout, "HEAD") != before {
		t.Fatal("the checkout moved")
	}
}

func TestATargetWithNoReadableChangelogIsRefused(t *testing.T) {
	upstream, checkout := installed(t)
	write(t, filepath.Join(upstream, ManifestPath), "not json")
	git(t, upstream, "add", ".")
	git(t, upstream, "commit", "-q", "-m", "broken")
	var log bytes.Buffer
	if _, _, err := fastForward(context.Background(), checkout, "main", "0.6", &log); err == nil {
		t.Fatal("installed a commit whose version could not be confirmed")
	}
}

// A tag only the operator has means nothing about the release.
func TestALocalTagCannotStandInForTheRelease(t *testing.T) {
	_, checkout := installed(t)
	git(t, checkout, "tag", "v0.6")
	var log bytes.Buffer
	if _, version, err := fastForward(context.Background(), checkout, "main", "0.6", &log); err != nil || version != "0.6" {
		t.Fatalf("version %q, err %v\n%s", version, err, log.String())
	}
	if head(t, checkout, "HEAD") == head(t, checkout, "v0.6") {
		t.Fatal("followed the operator's own tag")
	}
}

func TestRestoringTheCheckoutKeepsAnUnrelatedEdit(t *testing.T) {
	_, checkout := installed(t)
	from := head(t, checkout, "HEAD")
	var log bytes.Buffer
	commit, _, err := fastForward(context.Background(), checkout, "main", "0.6", &log)
	if err != nil {
		t.Fatal(err)
	}
	edit := filepath.Join(checkout, "docker-compose.yml")
	write(t, edit, "name: just-dashboard\n# mine\n")
	if err := restoreCheckout(context.Background(), checkout, from, commit, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if head(t, checkout, "HEAD") != from {
		t.Fatal("the checkout did not go back")
	}
	got, _ := os.ReadFile(edit)
	if !strings.Contains(string(got), "# mine") {
		t.Fatal("the operator's edit was lost")
	}
	version, _ := os.ReadFile(filepath.Join(checkout, "version.txt"))
	if strings.TrimSpace(string(version)) != "0.5" {
		t.Fatalf("version.txt = %q", version)
	}
}

// handBack's chown touches each file the merge wrote without changing its
// contents, which the index then disagrees with. Found on a real update: that
// was enough to stop reset --keep. A new mtime makes the index just as stale.
func TestRestoringTheCheckoutSeesThroughAStaleIndex(t *testing.T) {
	_, checkout := installed(t)
	from := head(t, checkout, "HEAD")
	var log bytes.Buffer
	commit, _, err := fastForward(context.Background(), checkout, "main", "0.6", &log)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(checkout, "version.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	if err := restoreCheckout(context.Background(), checkout, from, commit, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if head(t, checkout, "HEAD") != from {
		t.Fatal("the checkout did not go back")
	}
}

func TestRestoringTheCheckoutRefusesToOverwriteACollidingEdit(t *testing.T) {
	_, checkout := installed(t)
	from := head(t, checkout, "HEAD")
	var log bytes.Buffer
	commit, _, err := fastForward(context.Background(), checkout, "main", "0.6", &log)
	if err != nil {
		t.Fatal(err)
	}
	edit := filepath.Join(checkout, "version.txt")
	write(t, edit, "mine\n")
	if err := restoreCheckout(context.Background(), checkout, from, commit, &log); err == nil {
		t.Fatal("restored over a local edit")
	}
	if head(t, checkout, "HEAD") != commit {
		t.Fatal("the checkout moved anyway")
	}
	got, _ := os.ReadFile(edit)
	if strings.TrimSpace(string(got)) != "mine" {
		t.Fatalf("the edit was lost: %q", got)
	}
}

func TestRestoringLeavesACheckoutSomebodyElseMoved(t *testing.T) {
	_, checkout := installed(t)
	from := head(t, checkout, "HEAD")
	var log bytes.Buffer
	commit, _, err := fastForward(context.Background(), checkout, "main", "0.6", &log)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(checkout, "mine.txt"), "mine\n")
	git(t, checkout, "add", ".")
	git(t, checkout, "commit", "-q", "-m", "mine")
	moved := head(t, checkout, "HEAD")
	if err := restoreCheckout(context.Background(), checkout, from, commit, &log); err == nil {
		t.Fatal("undid a commit that was made during the update")
	}
	if head(t, checkout, "HEAD") != moved {
		t.Fatal("the checkout moved")
	}
}

func TestRestoringWithNoRecordedCommitChangesNothing(t *testing.T) {
	_, checkout := installed(t)
	before := head(t, checkout, "HEAD")
	var log bytes.Buffer
	if err := restoreCheckout(context.Background(), checkout, "", "", &log); err == nil {
		t.Fatal("no error")
	}
	if head(t, checkout, "HEAD") != before {
		t.Fatal("the checkout moved")
	}
}
