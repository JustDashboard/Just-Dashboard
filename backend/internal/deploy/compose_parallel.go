package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"
)

// A Compose project's services are built and pulled at once, a few at a
// time, instead of one after another. What the run records stays as it was:
//
//   - The transcript holds each service's output whole, in the file's
//     order (orderedBuildOutput): the first unfinished service streams as
//     it runs, and the others' lines wait until every service before them
//     has finished, then follow at once. Each buildx run numbers its steps
//     from #1 again, so interleaved lines could not be told apart, by a
//     person or by the transcript's step grouping.
//   - The release's artifacts, services and per-service evidence are
//     assembled in the file's order, whatever order the builds end in.
//   - The first failure stops the rest. The services it stopped say so in
//     the transcript, and the error names the service that failed.
//   - Cancelling the run cancels every build.

// composeBuildParallelism is how many Compose services build at once.
// BuildKit already runs one build's stages and layers in parallel, so a
// second build mostly overlaps one service's export, unpack and pulls with
// another's compile; beyond that, builds contend for the same cores and
// memory. A quarter of the cores and a build per 2 GiB of memory, between 1
// and 3: a 2-core server builds one at a time as before, a 12-core one
// three. The dashboard shares the server with what it runs.
func composeBuildParallelism(cpus int, memory int64) int {
	limit := cpus / 4
	if memory > 0 {
		if byMemory := int(memory / (2 << 30)); byMemory < limit {
			limit = byMemory
		}
	}
	return min(max(limit, 1), 3)
}

// hostBuildParallelism reads this server's cores and memory for
// composeBuildParallelism.
func hostBuildParallelism() int {
	_, total := hostMemoryTotals()
	return composeBuildParallelism(runtime.NumCPU(), total)
}

// composeJob is one service's work: run builds or pulls it and writes what
// it printed through emit.
type composeJob struct {
	service string
	run     func(ctx context.Context, emit func(BuildLog) error) error
}

// runComposeJobs runs jobs at most limit at a time, starting them in their
// order so the one whose output streams is the one that runs first. It
// writes their output through emit in that order and returns each job's
// error, with the index of the one whose failure stopped the others (-1 if
// none failed).
func runComposeJobs(ctx context.Context, jobs []composeJob, limit int, emit func(BuildLog) error) ([]error, int) {
	errs := make([]error, len(jobs))
	if len(jobs) == 0 {
		return errs, -1
	}
	group, cancel := context.WithCancel(ctx)
	defer cancel()
	output := newOrderedBuildOutput(len(jobs), emit)
	defer output.close()
	slots := make(chan struct{}, max(limit, 1))
	var mu sync.Mutex
	failed := -1
	// record keeps a job's first error; the first failure cancels the rest.
	// It returns the job whose failure that was.
	record := func(index int, err error) int {
		mu.Lock()
		defer mu.Unlock()
		if errs[index] == nil {
			errs[index] = err
		}
		if err != nil && failed < 0 && ctx.Err() == nil {
			failed = index
			cancel()
		}
		return failed
	}
	// end records how a job ended; a job stopped by another's failure says
	// so in the transcript, after its own lines.
	end := func(index int, err error) {
		if err == nil {
			err = output.failure()
		}
		stoppedBy := record(index, err)
		if err != nil && stoppedBy >= 0 && stoppedBy != index {
			_ = output.writer(index)(BuildLog{Stream: "status", Text: fmt.Sprintf(
				"Stopped Compose service %s: service %s failed", jobs[index].service, jobs[stoppedBy].service)})
		}
		// The jobs this one held up are written out now, after they recorded
		// their own success: a line the transcript refuses there is theirs.
		if refused, err := output.finish(index); err != nil {
			record(refused, err)
		}
	}
	var wg sync.WaitGroup
	for index, job := range jobs {
		select {
		case slots <- struct{}{}:
		case <-group.Done():
		}
		if err := group.Err(); err != nil {
			end(index, err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := job.run(group, output.writer(index))
			// The slot is freed after the end is recorded, so a failure has
			// cancelled the rest before another job could start.
			end(index, err)
			<-slots
		}()
	}
	wg.Wait()
	return errs, failed
}

// orderedBuildOutput writes several jobs' lines in the jobs' order: the
// head, the first job that has not finished, writes straight through; every
// other job's lines are held until it becomes the head. A job holds up to
// heldMemoryBytes in memory and the rest in a private temporary file, so a
// chatty build waiting behind a slow one never grows the process without
// bound and never blocks on its own output.
type orderedBuildOutput struct {
	mu   sync.Mutex
	emit func(BuildLog) error
	head int
	jobs []*heldOutput
	// err is the first line the transcript refused; every job then stops.
	err error
}

const heldMemoryBytes = 4 << 20

type heldOutput struct {
	lines []BuildLog
	bytes int
	spill *os.File
	spilt *bufio.Writer
	done  bool
}

func newOrderedBuildOutput(count int, emit func(BuildLog) error) *orderedBuildOutput {
	if emit == nil {
		emit = func(BuildLog) error { return nil }
	}
	output := &orderedBuildOutput{emit: emit, jobs: make([]*heldOutput, count)}
	for index := range output.jobs {
		output.jobs[index] = &heldOutput{}
	}
	return output
}

func (o *orderedBuildOutput) writer(index int) func(BuildLog) error {
	return func(line BuildLog) error {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.err != nil {
			return o.err
		}
		if index == o.head {
			if err := o.emit(line); err != nil {
				o.err = err
			}
			return o.err
		}
		o.jobs[index].hold(line)
		return nil
	}
}

// failure is the transcript's refusal, if any: a job that finished while
// its output could not be written did not succeed.
func (o *orderedBuildOutput) failure() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

// finish marks a job done; when it was the head, the jobs after it that
// are done too are written out, and the next unfinished one takes over.
// When the transcript refuses a line of a job that had already finished,
// it returns that job and the error: nothing else would tell the job it
// failed. A job still running reads the refusal from its next write or
// from failure.
func (o *orderedBuildOutput) finish(index int) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.jobs[index].done = true
	refused := -1
	for o.head < len(o.jobs) && o.jobs[o.head].done {
		o.head++
		if o.head < len(o.jobs) {
			before := o.err
			o.release(o.head)
			if before == nil && o.err != nil && o.jobs[o.head].done {
				refused = o.head
			}
		}
	}
	if refused < 0 {
		return -1, nil
	}
	return refused, o.err
}

// close writes whatever is still held, in order, and removes the files.
func (o *orderedBuildOutput) close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for index := o.head + 1; index < len(o.jobs); index++ {
		o.release(index)
	}
	o.head = len(o.jobs)
}

// release writes one job's held lines and forgets them. The caller holds mu.
func (o *orderedBuildOutput) release(index int) {
	job := o.jobs[index]
	write := func(line BuildLog) {
		if o.err == nil {
			o.err = o.emit(line)
		}
	}
	for _, line := range job.lines {
		write(line)
	}
	if job.spill != nil {
		_ = job.spilt.Flush()
		if _, err := job.spill.Seek(0, 0); err == nil {
			scanner := bufio.NewScanner(job.spill)
			scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
			for scanner.Scan() {
				var line BuildLog
				if json.Unmarshal(scanner.Bytes(), &line) == nil {
					write(line)
				}
			}
		}
		name := job.spill.Name()
		_ = job.spill.Close()
		_ = os.Remove(name)
	}
	*job = heldOutput{done: job.done}
}

// hold keeps a line until its job is the head: in memory up to
// heldMemoryBytes, then in a temporary file only this process can read.
func (h *heldOutput) hold(line BuildLog) {
	if h.spill == nil && h.bytes+len(line.Text) > heldMemoryBytes {
		if file, err := os.CreateTemp("", "just-dashboard-compose-output-"); err == nil {
			h.spill, h.spilt = file, bufio.NewWriter(file)
		}
	}
	if h.spill != nil {
		if raw, err := json.Marshal(line); err == nil {
			if _, err := h.spilt.Write(append(raw, '\n')); err == nil {
				return
			}
		}
	}
	// No file could be made or written: keep it in memory instead.
	h.lines = append(h.lines, line)
	h.bytes += len(line.Text)
}
