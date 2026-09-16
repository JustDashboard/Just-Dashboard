// Command engine-host runs a real deployment engine in its own process so a
// test can kill it the way an operator's backend actually dies: with no
// shutdown, no final transition, and a lease that simply stops beating.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func main() {
	dir := flag.String("dir", "", "data directory holding the deployment database")
	worker := flag.String("worker", "engine-host", "worker identity for the claim")
	stall := flag.String("stall", "", "step key to stop inside and never complete")
	marker := flag.String("marker", "", "file written once the stalled step is running")
	lease := flag.Duration("lease", 2*time.Second, "lease TTL")
	flag.Parse()
	if *dir == "" || *stall == "" || *marker == "" {
		fmt.Fprintln(os.Stderr, "dir, stall and marker are required")
		os.Exit(2)
	}
	st, err := store.Open(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open store:", err)
		os.Exit(1)
	}
	engine := deploy.NewEngine(
		deploy.NewOrchestrationStore(st),
		stallExecutor{stall: deploy.StepKey(*stall), marker: *marker},
		nil,
		deploy.EngineConfig{WorkerID: *worker, LeaseTTL: *lease, PollEvery: 20 * time.Millisecond},
		nil,
	)
	if err := engine.Start(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "start engine:", err)
		os.Exit(1)
	}
	select {}
}

// stallExecutor completes every step until the chosen one, where it writes
// durable log output and then stops responding forever. The log line matters:
// it is the diagnostic the test expects to survive the kill.
type stallExecutor struct {
	stall  deploy.StepKey
	marker string
}

func (e stallExecutor) Execute(ctx context.Context, execution deploy.StepExecution) deploy.StepResult {
	_ = execution.Output.Log("status", "engine-host entered "+string(execution.Step.Key))
	if execution.Step.Key != e.stall {
		return deploy.StepResult{State: deploy.StepPassed}
	}
	if err := os.WriteFile(e.marker, []byte(string(execution.Step.Key)), 0o644); err != nil {
		return deploy.StepResult{State: deploy.StepFailed, ErrorCode: "marker", ErrorMessage: err.Error()}
	}
	<-ctx.Done()
	return deploy.StepResult{State: deploy.StepFailed, ErrorCode: "cancelled"}
}
