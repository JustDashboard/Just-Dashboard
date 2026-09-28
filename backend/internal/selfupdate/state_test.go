package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	if run, err := store.Load(); err != nil || run != nil {
		t.Fatalf("a store with no run returned (%v, %v); an install that has never updated is not an error", run, err)
	}
	want := &Run{
		ID: "20260826T101500Z", Status: StatusPending, Phase: PhaseQueued,
		FromVersion: "0.5", ToVersion: "0.6", Dir: "/opt/jd", StartedAt: time.Now().UTC(),
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.ToVersion != "0.6" || got.Dir != "/opt/jd" {
		t.Fatalf("read back %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("Save did not stamp the record")
	}

	if err := store.Update(func(r *Run) { r.Phase = PhaseBuilding }); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.Load(); got.Phase != PhaseBuilding {
		t.Fatalf("phase %s after update", got.Phase)
	}

	if err := store.Finish(errors.New("the build failed")); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Load()
	if got.Status != StatusFailed || got.Error != "the build failed" || got.FinishedAt == nil {
		t.Fatalf("finished run is %+v", got)
	}

	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if run, _ := store.Load(); run != nil {
		t.Fatal("the record survived being cleared")
	}
	// Dismissing twice is a double-click, not a fault.
	if err := store.Clear(); err != nil {
		t.Fatalf("clearing an already-clear store failed: %v", err)
	}
}

// A record that cannot be parsed has to be an error rather than "there is no
// run", because "there is no run" is what lets a second upgrade start.
func TestCorruptRecordIsAnErrorNotAnAbsence(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("a corrupt record read as an absent one")
	}
}

// The transcript is a build log; the part that says what went wrong is the end
// of it, and handing a browser the whole thing is how a status poll becomes a
// megabyte.
func TestTailKeepsTheEnd(t *testing.T) {
	store := NewStore(t.TempDir())
	f, err := store.OpenLog()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(f, "step %d of the build\n", i)
	}
	fmt.Fprintln(f, "THE LAST LINE")
	f.Close()

	tail := store.Tail()
	if len(tail) > maxLogTail+128 {
		t.Fatalf("tail is %d bytes, cap is %d", len(tail), maxLogTail)
	}
	if !strings.Contains(tail, "THE LAST LINE") {
		t.Error("the end of the transcript was trimmed away instead of the start")
	}
	if !strings.HasPrefix(tail, "… earlier output trimmed …\n") {
		t.Error("a trimmed transcript does not say it was trimmed")
	}
	// A seek into the middle lands mid-line; a half-written word rendered as
	// output reads as corruption.
	firstLine := strings.SplitN(strings.TrimPrefix(tail, "… earlier output trimmed …\n"), "\n", 2)[0]
	if !strings.HasPrefix(firstLine, "step ") {
		t.Errorf("the transcript starts mid-line: %q", firstLine)
	}
}

// The console reads a rebuild from its first line, which the tail exists to
// drop: the whole transcript is the other read, and it has to be whole.
func TestTranscriptKeepsWhatTheTailTrims(t *testing.T) {
	store := NewStore(t.TempDir())
	f, err := store.OpenLog()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(f, "step %d of the build\n", i)
	}
	fmt.Fprintln(f, "THE LAST LINE")
	f.Close()

	if strings.Contains(store.Tail(), "step 0 of the build\n") {
		t.Fatal("the fixture is too small to be trimmed by the tail")
	}
	full := store.Transcript()
	if !strings.HasPrefix(full, "step 0 of the build\n") || !strings.HasSuffix(full, "THE LAST LINE\n") {
		t.Errorf("the transcript is not the whole file: starts %q", full[:min(len(full), 40)])
	}
	if strings.Contains(full, "earlier output trimmed") {
		t.Error("a transcript under the cap says it was trimmed")
	}
}

// OpenLog truncates and AppendLog does not: the backend writes the header, the
// updater adds to it, and neither ever reads back the previous run's output as
// if it were this one's.
func TestLogIsTruncatedOncePerRun(t *testing.T) {
	store := NewStore(t.TempDir())
	f, _ := store.OpenLog()
	fmt.Fprintln(f, "first run")
	f.Close()

	f, _ = store.AppendLog()
	fmt.Fprintln(f, "still the first run")
	f.Close()
	if got := store.Tail(); !strings.Contains(got, "first run") || !strings.Contains(got, "still the first run") {
		t.Fatalf("append lost something: %q", got)
	}

	f, _ = store.OpenLog()
	fmt.Fprintln(f, "second run")
	f.Close()
	if got := store.Tail(); strings.Contains(got, "first run") {
		t.Fatalf("the previous run's transcript survived into the next: %q", got)
	}
}

func TestStatusLive(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusRunning} {
		if !s.Live() {
			t.Errorf("%s is not live, so a second upgrade could start alongside it", s)
		}
	}
	for _, s := range []Status{StatusSuccess, StatusFailed} {
		if s.Live() {
			t.Errorf("%s reads as live, so no further upgrade could ever start", s)
		}
	}
}
