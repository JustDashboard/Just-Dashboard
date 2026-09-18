package deploy

import (
	"context"
	"sort"
	"testing"
	"time"
)

// Reference scale from 08-test-and-acceptance.md, scaled to what a unit test can
// build in a reasonable time. The point of these is the shape of the cost, not
// the absolute number on this particular machine: a read whose cost is fixed in
// the number of deployments stays fixed here whatever the host is.
const (
	referenceDeployments = 100
	referenceRunsEach    = 3
)

func percentile(samples []time.Duration, fraction float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(float64(len(ordered)-1) * fraction)
	return ordered[index]
}

// The fleet read is what the deployment list polls. Its budget is p95 under
// 500 ms at reference scale with no per-deployment container inspect and no
// subprocess call.
func TestFleetReadStaysWithinItsBudgetAtReferenceScale(t *testing.T) {
	store, driver := countingFleet(t, referenceDeployments)
	budget := QueueBudget{Heavy: 2, Light: 4}

	// One warm read first: the budget describes a running dashboard, not the
	// first statement prepared after opening the database.
	if _, err := store.Fleet(context.Background(), budget); err != nil {
		t.Fatal(err)
	}
	driver.reset()

	samples := make([]time.Duration, 0, 20)
	for attempt := 0; attempt < 20; attempt++ {
		started := time.Now()
		fleet, err := store.Fleet(context.Background(), budget)
		samples = append(samples, time.Since(started))
		if err != nil {
			t.Fatal(err)
		}
		if len(fleet.Deployments) != referenceDeployments {
			t.Fatalf("fleet returned %d deployments", len(fleet.Deployments))
		}
	}
	if got := percentile(samples, 0.95); got > 500*time.Millisecond {
		t.Fatalf("fleet p95 = %s, budget is 500ms", got)
	}
	// Twenty reads of a hundred deployments must not be two thousand statements.
	if perRead := driver.count.Load() / 20; perRead > 10 {
		t.Fatalf("fleet issued %d statements per read at reference scale", perRead)
	}
	t.Logf("fleet p95 %s over %d deployments, %d statements per read",
		percentile(samples, 0.95), referenceDeployments, driver.count.Load()/20)
}

// Opening one workspace is the five-second poll every operator leaves running.
func TestWorkspaceReadStaysWithinItsBudgetAtReferenceScale(t *testing.T) {
	store, driver := countingFleet(t, referenceDeployments)
	budget := QueueBudget{Heavy: 2, Light: 4}
	if _, err := store.DeploymentSummary(context.Background(), 50, budget); err != nil {
		t.Fatal(err)
	}
	driver.reset()

	samples := make([]time.Duration, 0, 40)
	for attempt := 0; attempt < 40; attempt++ {
		started := time.Now()
		summary, err := store.DeploymentSummary(context.Background(), 50, budget)
		samples = append(samples, time.Since(started))
		if err != nil {
			t.Fatal(err)
		}
		if summary.ID != 50 {
			t.Fatalf("summary = %#v", summary)
		}
	}
	if got := percentile(samples, 0.95); got > 250*time.Millisecond {
		t.Fatalf("workspace p95 = %s, budget is 250ms", got)
	}
	if perRead := driver.count.Load() / 40; perRead > 10 {
		t.Fatalf("workspace read issued %d statements at reference scale", perRead)
	}
	t.Logf("workspace p95 %s, %d statements per read",
		percentile(samples, 0.95), driver.count.Load()/40)
}

// A read whose cost grows with the fleet passes a budget on a small host and
// fails on a real one. This is the shape assertion the numbers above rest on.
func TestFleetCostDoesNotGrowWithTheFleet(t *testing.T) {
	measure := func(deployments int) (time.Duration, int64) {
		store, driver := countingFleet(t, deployments)
		budget := QueueBudget{Heavy: 2, Light: 4}
		if _, err := store.Fleet(context.Background(), budget); err != nil {
			t.Fatal(err)
		}
		driver.reset()
		samples := make([]time.Duration, 0, 10)
		for attempt := 0; attempt < 10; attempt++ {
			started := time.Now()
			if _, err := store.Fleet(context.Background(), budget); err != nil {
				t.Fatal(err)
			}
			samples = append(samples, time.Since(started))
		}
		return percentile(samples, 0.5), driver.count.Load() / 10
	}
	_, smallStatements := measure(5)
	_, largeStatements := measure(referenceDeployments)
	if smallStatements != largeStatements {
		t.Fatalf("statements grew from %d at 5 deployments to %d at %d",
			smallStatements, largeStatements, referenceDeployments)
	}
}
