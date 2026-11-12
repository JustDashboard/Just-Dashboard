package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// outputFixture is a claimed run with one running step and the engine's own
// output on it, which a test can set the batch delay of.
type outputFixture struct {
	*orchestrationFixture
	run    *EngineRun
	step   *RunStep
	lease  *QueueLease
	output *storeStepOutput
}

func newOutputFixture(t *testing.T, delay time.Duration) *outputFixture {
	t.Helper()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) { req.Steps = []StepKey{StepResolveSource} })
	lease, err := f.runs.ClaimNext(ctx, "worker", QueueBudget{}, time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("claim: %v %v", lease, err)
	}
	step, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	output := newStoreStepOutput(ctx, f.runs, run.ID, step.ID, lease.Token)
	output.batchDelay = delay
	return &outputFixture{orchestrationFixture: f, run: run, step: step, lease: lease, output: output}
}

func (f *outputFixture) written(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_log_chunks WHERE run_id = ? AND step_id = ? AND event_type = 'step.log'`,
		f.run.ID, f.step.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A step's lines reach the transcript a batch at a time, not a transaction
// each: nothing is written until a batch is full, the full batch is written
// at once, and the step's end writes the rest.
func TestStepOutputIsWrittenInBatches(t *testing.T) {
	f := newOutputFixture(t, time.Hour)
	for line := 1; line < stepOutputBatchLines; line++ {
		if err := f.output.Log("stdout", fmt.Sprintf("line-%d\n", line)); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.written(t); got != 0 {
		t.Fatalf("%d lines were written before the batch filled; each line is a transaction of its own again", got)
	}
	if err := f.output.Log("stdout", fmt.Sprintf("line-%d\n", stepOutputBatchLines)); err != nil {
		t.Fatal(err)
	}
	if got := f.written(t); got != stepOutputBatchLines {
		t.Fatalf("a full batch wrote %d lines, want %d", got, stepOutputBatchLines)
	}
	if err := f.output.Log("stderr", "the last line\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.output.Flush(); err != nil {
		t.Fatal(err)
	}
	lines, _, err := f.runs.RunTranscriptTail(context.Background(), f.run.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != stepOutputBatchLines+1 {
		t.Fatalf("transcript holds %d lines, want %d", len(lines), stepOutputBatchLines+1)
	}
	for index, line := range lines {
		want := fmt.Sprintf("line-%d\n", index+1)
		if index == stepOutputBatchLines {
			want = "the last line\n"
		}
		if line.Text != want || (index > 0 && line.Seq != lines[index-1].Seq+1) {
			t.Fatalf("line %d = %q at seq %d, want %q right after seq %d", index, line.Text, line.Seq, want, lines[max(index-1, 0)].Seq)
		}
	}
	step, err := f.runs.Steps(context.Background(), f.run.ID)
	if err != nil || step[0].LastSeq != lines[len(lines)-1].Seq {
		t.Fatalf("step last_seq = %d, want the last line's %d (%v)", step[0].LastSeq, lines[len(lines)-1].Seq, err)
	}
}

// A page following a run still reads as live: a line that fills no batch is
// written after the delay without anybody asking.
func TestStepOutputWritesAWaitingLineAfterTheDelay(t *testing.T) {
	f := newOutputFixture(t, 20*time.Millisecond)
	_, events, unsubscribe, err := f.runs.Subscribe(context.Background(), f.run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if err := f.output.Log("stdout", "one line\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		var data struct{ Stream, Text string }
		if err := json.Unmarshal(event.Data, &data); err != nil || event.Type != EventStepLog || data.Text != "one line\n" || data.Stream != "stdout" {
			t.Fatalf("published event = %+v %s (%v)", event, event.Data, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting line was never written")
	}
	if got := f.written(t); got != 1 {
		t.Fatalf("written = %d, want 1", got)
	}
}

// Nothing reaches the transcript ahead of a line printed before it: an event
// the step records writes the waiting lines first.
func TestStepOutputEventFollowsTheLinesBeforeIt(t *testing.T) {
	f := newOutputFixture(t, time.Hour)
	for _, text := range []string{"a\n", "b\n"} {
		if err := f.output.Log("stdout", text); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.output.Event(EventInput{Type: EventStepEvidence, Data: json.RawMessage(`{"checked":true}`)}); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	rows, err := f.store.DB.Query(`SELECT event_type || ':' || text FROM deploy_log_chunks WHERE run_id = ? AND step_id = ? ORDER BY seq`, f.run.ID, f.step.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	if got := strings.Join(kinds, "|"); !strings.HasSuffix(got, "step.log:a\n|step.log:b\n|"+string(EventStepEvidence)+":") {
		t.Fatalf("order = %q, want both lines before the evidence", got)
	}
}

// A step names the line that proves why it failed. A line has no sequence
// until its batch is written, so the step holds a mark, and the mark still
// finds the line when another event took a sequence between two batches.
func TestStepOutputMarkFindsItsLineInTheTranscript(t *testing.T) {
	f := newOutputFixture(t, time.Hour)
	ctx := context.Background()
	marks := map[string]int64{}
	log := func(text string) {
		t.Helper()
		if err := f.output.Log("stdout", text); err != nil {
			t.Fatal(err)
		}
		marks[text] = lastLogMark(f.output)
	}
	log("first")
	log("second")
	if err := f.output.Flush(); err != nil {
		t.Fatal(err)
	}
	// Somebody else's event, between this step's batches.
	if _, err := f.runs.AppendEvent(ctx, f.run.ID, f.lease.Token, EventInput{Type: EventStepEvidence, Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	log("third")
	log("decisive")
	log("fifth")
	// Asked before anything wrote the second batch.
	seq := logSeq(f.output, marks["decisive"])
	for text, mark := range marks {
		var want int64
		if err := f.store.DB.QueryRow(`SELECT seq FROM deploy_log_chunks WHERE run_id = ? AND text = ?`, f.run.ID, text).Scan(&want); err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if got := logSeq(f.output, mark); got != want {
			t.Fatalf("mark of %q resolves to seq %d, the line is at %d", text, got, want)
		}
	}
	if seq == 0 || logSeq(f.output, 99) != 0 || logSeq(f.output, 0) != 0 {
		t.Fatalf("decisive seq = %d; a mark nothing was logged under must resolve to 0", seq)
	}
}

// A line that cannot be written fails the output, and the lines after it are
// refused rather than queued for a transcript that will never take them.
func TestStepOutputReportsALineItCouldNotWrite(t *testing.T) {
	f := newOutputFixture(t, time.Hour)
	f.output.claimToken = "not-the-lease"
	if err := f.output.Log("stdout", "lost\n"); err != nil {
		t.Fatalf("a waiting line is not an error yet: %v", err)
	}
	if err := f.output.Flush(); err != ErrLeaseLost {
		t.Fatalf("flush error = %v, want the lost lease", err)
	}
	if err := f.output.Log("stdout", "after\n"); err != ErrLeaseLost {
		t.Fatalf("log after a failed write = %v, want the lost lease", err)
	}
	if err := f.output.failure(); err != ErrLeaseLost {
		t.Fatalf("failure = %v", err)
	}
}

// A line's text is stored once. The event a reconnecting page is sent is the
// one a live subscriber received, built from the line's own columns; a line
// stored before, with its text repeated as JSON, still answers with that.
func TestLogTextIsStoredOnceAndReadBackAsItsEvent(t *testing.T) {
	f := newOutputFixture(t, time.Hour)
	ctx := context.Background()
	appended, err := f.runs.AppendLogs(ctx, f.run.ID, f.step.ID, f.lease.Token, []LogLine{
		{Stream: "stdout", Text: "installing \"dependencies\"\n"}, {Stream: "stderr", Text: "warning: é\n"},
	})
	if err != nil || len(appended) != 2 || appended[1].Seq != appended[0].Seq+1 {
		t.Fatalf("appended = %+v %v", appended, err)
	}
	var stored, text string
	if err := f.store.DB.QueryRow(`SELECT data_json, text FROM deploy_log_chunks WHERE run_id = ? AND seq = ?`,
		f.run.ID, appended[0].Seq).Scan(&stored, &text); err != nil {
		t.Fatal(err)
	}
	if stored != "{}" || text != "installing \"dependencies\"\n" {
		t.Fatalf("stored data_json = %s text = %q; the text is kept twice again", stored, text)
	}
	// A line from before the change, and the transcript a 0.6.6 run was
	// migrated with.
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_log_chunks(run_id, step_id, seq, event_type, stream, ts, text, data_json)
		VALUES(?, ?, ?, 'step.log', 'stdout', 1, 'old line', '{"stream":"stdout","text":"old line","truncated":false}'),
		      (?, ?, ?, 'step.log', 'status', 1, 'migrated', '{"migration":"0.6.6"}')`,
		f.run.ID, f.step.ID, appended[1].Seq+1, f.run.ID, f.step.ID, appended[1].Seq+2); err != nil {
		t.Fatal(err)
	}
	events, err := f.runs.EventsAfter(ctx, f.run.ID, appended[0].Seq-1, 10)
	if err != nil || len(events) != 4 {
		t.Fatalf("events = %+v %v", events, err)
	}
	for index, want := range []string{
		string(appended[0].Data), string(appended[1].Data),
		`{"stream":"stdout","text":"old line","truncated":false}`, `{"migration":"0.6.6"}`,
	} {
		if string(events[index].Data) != want {
			t.Fatalf("event %d data = %s, want %s", index, events[index].Data, want)
		}
	}
	var first struct {
		Stream, Text string
		Truncated    bool
	}
	if err := json.Unmarshal(events[0].Data, &first); err != nil || first.Stream != "stdout" || first.Text != "installing \"dependencies\"\n" || first.Truncated {
		t.Fatalf("first event = %+v (%v)", first, err)
	}
}

// The run row's legacy tail, which the 0.6.6 run shape still answers with,
// holds what was printed, bounded, and is rewritten once for a batch.
func TestLegacyLogTailFollowsABatchAndStaysBounded(t *testing.T) {
	f := newOutputFixture(t, time.Hour)
	ctx := context.Background()
	legacy := func() string {
		t.Helper()
		var log string
		if err := f.store.DB.QueryRow(`SELECT log FROM deploy_runs WHERE id = ?`, f.run.ID).Scan(&log); err != nil {
			t.Fatal(err)
		}
		return log
	}
	if _, err := f.runs.AppendLogs(ctx, f.run.ID, f.step.ID, f.lease.Token, []LogLine{
		{Stream: "stdout", Text: "a\n"}, {Stream: "stdout", Text: "b\n"}, {Stream: "stderr", Text: "c\n"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := legacy(); got != "a\nb\nc\n" {
		t.Fatalf("legacy tail = %q", got)
	}
	long := make([]LogLine, 0, 40)
	for index := range 40 {
		long = append(long, LogLine{Stream: "stdout", Text: strings.Repeat("é", 4999) + fmt.Sprintf("%02d\n", index)})
	}
	if _, err := f.runs.AppendLogs(ctx, f.run.ID, f.step.ID, f.lease.Token, long); err != nil {
		t.Fatal(err)
	}
	got := []rune(legacy())
	if len(got) != 64001 || got[0] != '…' || !strings.HasSuffix(string(got), "é39\n") {
		t.Fatalf("legacy tail is %d characters, starts %q and ends %q; want the newest 64,000 behind an ellipsis",
			len(got), string(got[:1]), string(got[len(got)-4:]))
	}
}

// Build output is written through the store's bulk writer: a pile-up on the
// pool every request shares does not stop a build's log, and the log's
// commits do not each wait for the disk.
func TestALogBatchIsWrittenWhileThePoolIsExhausted(t *testing.T) {
	f := newOutputFixture(t, time.Hour)
	storetest.OwnHandles(t, f.store)
	hold, cancelHold := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHold()
	for range f.store.DB.Stats().MaxOpenConnections {
		conn, err := f.store.DB.Conn(hold)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := f.runs.AppendLogs(ctx, f.run.ID, f.step.ID, f.lease.Token, []LogLine{{Stream: "stdout", Text: "still writing\n"}}); err != nil {
		t.Fatalf("a log batch behind an exhausted pool: %v", err)
	}
}
