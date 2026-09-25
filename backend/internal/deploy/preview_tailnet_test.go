package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type tailnetPublish struct{ Port, Upstream, Previous int }

// tailnetPublisherFake stands in for tailscale serve: it remembers every
// publish and withdraw, serves what was published (by the loopback upstream
// the mapping points at; 0 is a mapping of the operator's), and fails on
// request.
type tailnetPublisherFake struct {
	mu           sync.Mutex
	served       map[int]int
	publishes    []tailnetPublish
	withdraws    []int
	publishErr   error
	failUpstream int
	withdrawErr  error
	servedErr    error
	// onServed runs at every status read, for a test that needs something
	// to happen between the sweep's two readings.
	onServed func()
}

func (f *tailnetPublisherFake) PublishTailnet(_ context.Context, port, upstream, previous int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishes = append(f.publishes, tailnetPublish{port, upstream, previous})
	if f.publishErr != nil {
		return "", f.publishErr
	}
	if f.failUpstream != 0 && upstream == f.failUpstream {
		return "", fmt.Errorf("serve refused 127.0.0.1:%d", upstream)
	}
	if f.served == nil {
		f.served = map[int]int{}
	}
	f.served[port] = upstream
	return fmt.Sprintf("https://node.tailnet.ts.net:%d", port), nil
}

func (f *tailnetPublisherFake) WithdrawTailnet(_ context.Context, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withdraws = append(f.withdraws, port)
	if f.withdrawErr != nil {
		return f.withdrawErr
	}
	delete(f.served, port)
	return nil
}

func (f *tailnetPublisherFake) ServedTailnetPorts(_ context.Context) (map[int]int, error) {
	if f.onServed != nil {
		f.onServed()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.servedErr != nil {
		return nil, f.servedErr
	}
	served := make(map[int]int, len(f.served))
	for port, upstream := range f.served {
		served[port] = upstream
	}
	return served, nil
}

func (f *tailnetPublisherFake) published() []tailnetPublish {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tailnetPublish(nil), f.publishes...)
}

func (f *tailnetPublisherFake) withdrawn() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.withdraws...)
}

// previewRuntimeOwner is the ordering owner with the preview cleanup
// contract removePreview needs.
type previewRuntimeOwner struct {
	orderingRuntimeOwner
	removed []int64
}

func (o *previewRuntimeOwner) RemovePreviewResources(_ context.Context, environmentID int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.removed = append(o.removed, environmentID)
	return nil
}

func tailnetPreviewPlan() RuntimePlanConfig {
	return RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
}

// tailnetCandidate records a started candidate for the fixture's next
// release on the given loopback port, as start_candidate would have.
func tailnetCandidate(t *testing.T, fixture *releaseStoreFixture, revision int, identity string, port int) (*EngineRun, QueueLease, *ReleaseWithArtifacts) {
	t.Helper()
	return tailnetCandidateWithPlan(t, fixture, revision, identity, port, tailnetPreviewPlan())
}

func tailnetCandidateWithPlan(t *testing.T, fixture *releaseStoreFixture, revision int, identity string, port int, plan RuntimePlanConfig) (*EngineRun, QueueLease, *ReleaseWithArtifacts) {
	t.Helper()
	fixture.addPlanWithRuntime(t, revision, strings.Repeat(string(rune('a'+revision)), 40), plan)
	run, lease := fixture.claimedRun(t, revision)
	release := createRuntimeCandidate(t, fixture, *run, lease, plan, identity)
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token, ReleaseRuntimeInput{
		ReleaseID: release.Release.ID, Kind: "container", RuntimeID: identity,
		Host: "127.0.0.1", Port: port, Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	return run, lease, release
}

// insertPreviewAddress writes an address row the way AllocatePreviewAddress
// and a publish would have left it, without the preview-kind check the
// allocator applies: the release fixture's environment is production.
func insertPreviewAddress(t *testing.T, fixture *releaseStoreFixture, port, upstream int, published bool) {
	t.Helper()
	url := fmt.Sprintf("https://node.tailnet.ts.net:%d", port)
	flag := 0
	if published {
		flag = 1
	}
	if _, err := fixture.base.DB.Exec(`INSERT INTO deploy_preview_addresses(environment_id,kind,port,upstream_port,url,published,updated_at) VALUES(?,'tailnet',?,?,?,?,?)`,
		fixture.envID, port, upstream, url, flag, fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
}

func setEnvironmentKind(t *testing.T, fixture *releaseStoreFixture, kind EnvironmentKind) {
	t.Helper()
	if _, err := fixture.base.DB.Exec(`UPDATE deploy_environments SET kind=? WHERE id=?`, kind, fixture.envID); err != nil {
		t.Fatal(err)
	}
}

func liveReleaseID(t *testing.T, fixture *releaseStoreFixture) int64 {
	t.Helper()
	var id int64
	if err := fixture.base.DB.QueryRow(`SELECT live_release_id FROM deploy_environments WHERE id=?`, fixture.envID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func decodeActivationEvidence(t *testing.T, result StepResult) activationStepEvidence {
	t.Helper()
	var evidence activationStepEvidence
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatalf("activation evidence %s: %v", result.Evidence, err)
	}
	return evidence
}

func TestAllocatePreviewAddressPicksTheLowestFreePortOnce(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	first := f.addEnvironment(t, "pr-7", EnvironmentPreview)
	second := f.addEnvironment(t, "pr-9", EnvironmentPreview)
	production := f.addEnvironment(t, "production", EnvironmentProduction)

	address, err := f.runs.AllocatePreviewAddress(ctx, first, nil)
	if err != nil || address.Kind != "tailnet" || address.Port != previewTailnetPortMin || address.Published {
		t.Fatalf("first allocation = %#v, %v", address, err)
	}
	again, err := f.runs.AllocatePreviewAddress(ctx, first, map[int]int{previewTailnetPortMin: 32100})
	if err != nil || again.Port != address.Port {
		t.Fatalf("second allocation for the same preview = %#v, %v; want the port it already has", again, err)
	}
	other, err := f.runs.AllocatePreviewAddress(ctx, second, map[int]int{previewTailnetPortMin + 1: 0})
	if err != nil || other.Port != previewTailnetPortMin+2 {
		t.Fatalf("allocation around a foreign port = %#v, %v", other, err)
	}
	if _, err := f.runs.AllocatePreviewAddress(ctx, production, nil); !errors.Is(err, ErrPreviewIsolation) {
		t.Fatalf("production allocation error = %v, want %v", err, ErrPreviewIsolation)
	}
	if _, err := f.runs.AllocatePreviewAddress(ctx, 404, nil); !errors.Is(err, ErrEnvironmentNotFound) {
		t.Fatalf("unknown environment error = %v", err)
	}

	if err := f.runs.MarkPreviewAddressPublished(ctx, first, "https://node.tailnet.ts.net:21000", 32101, true); err != nil {
		t.Fatal(err)
	}
	stored, err := f.runs.PreviewAddressFor(ctx, first)
	if err != nil || stored == nil || !stored.Published || stored.UpstreamPort != 32101 || stored.URL != "https://node.tailnet.ts.net:21000" {
		t.Fatalf("published address = %#v, %v", stored, err)
	}
	if none, err := f.runs.PreviewAddressFor(ctx, production); err != nil || none != nil {
		t.Fatalf("address of an environment without one = %#v, %v", none, err)
	}
	if err := f.runs.MarkPreviewAddressPublished(ctx, production, "", 0, false); !errors.Is(err, ErrPreviewAddressMissing) {
		t.Fatalf("marking a missing address error = %v", err)
	}
	listed, err := f.runs.ListPreviewAddresses(ctx)
	if err != nil || len(listed) != 2 || listed[first].Port != previewTailnetPortMin || listed[second].Port != previewTailnetPortMin+2 {
		t.Fatalf("listed addresses = %#v, %v", listed, err)
	}
	if err := f.runs.DeletePreviewAddress(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := f.runs.DeletePreviewAddress(ctx, first); err != nil {
		t.Fatalf("deleting an absent address = %v", err)
	}
	if gone, err := f.runs.PreviewAddressFor(ctx, first); err != nil || gone != nil {
		t.Fatalf("deleted address = %#v, %v", gone, err)
	}
	// The freed port is the lowest again, so the next preview takes it.
	third := f.addEnvironment(t, "pr-11", EnvironmentPreview)
	if reused, err := f.runs.AllocatePreviewAddress(ctx, third, nil); err != nil || reused.Port != previewTailnetPortMin {
		t.Fatalf("allocation after a delete = %#v, %v", reused, err)
	}
}

func TestAllocatePreviewAddressUnderConcurrencyNeverSharesAPort(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	environments := []int64{f.addEnvironment(t, "pr-1", EnvironmentPreview), f.addEnvironment(t, "pr-2", EnvironmentPreview)}
	ports := make([]int, len(environments))
	errs := make([]error, len(environments))
	var group sync.WaitGroup
	for index, environmentID := range environments {
		group.Add(1)
		go func() {
			defer group.Done()
			address, err := f.runs.AllocatePreviewAddress(context.Background(), environmentID, nil)
			if err == nil {
				ports[index] = address.Port
			}
			errs[index] = err
		}()
	}
	group.Wait()
	if errs[0] != nil || errs[1] != nil || ports[0] == ports[1] {
		t.Fatalf("concurrent allocations = %v, errors %v", ports, errs)
	}
	if got := ports[0] + ports[1]; got != 2*previewTailnetPortMin+1 {
		t.Fatalf("concurrent allocations took %v, want the two lowest ports", ports)
	}
}

func TestAllocatePreviewAddressReportsAnExhaustedRange(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "pr-1", EnvironmentPreview)
	exclude := make(map[int]int, previewTailnetPortMax-previewTailnetPortMin+1)
	for port := previewTailnetPortMin; port <= previewTailnetPortMax; port++ {
		exclude[port] = 0
	}
	if _, err := f.runs.AllocatePreviewAddress(context.Background(), environmentID, exclude); !errors.Is(err, ErrPreviewAddressExhausted) {
		t.Fatalf("exhausted range error = %v", err)
	}
	if address, err := f.runs.PreviewAddressFor(context.Background(), environmentID); err != nil || address != nil {
		t.Fatalf("a failed allocation left a row: %#v, %v", address, err)
	}
}

// Activation publishes the preview's tailnet port before the release
// pointer moves, records what it points at, and treats the reachability
// probe as evidence only: the node's certificate is minted on the first
// request, so a probe that fails does not fail the step.
func TestActivatePublishesThePreviewOnTheTailnetBeforeTheReleasePointerMoves(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		probeErr error
	}{
		{name: "reachable"},
		{name: "certificate pending", probeErr: errors.New("x509: certificate not yet issued")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseStoreFixture(t)
			run, lease, release := tailnetCandidate(t, fixture, 1, "tailnet-v1", 32101)
			insertPreviewAddress(t, fixture, 21000, 0, false)
			owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}
			publisher := &tailnetPublisherFake{}
			var probed []string
			executor := &NormalizedStepExecutor{
				store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher,
				tailnetProbe: func(_ context.Context, url string) error {
					probed = append(probed, url)
					return test.probeErr
				},
			}
			output := &recordingStepOutput{}
			result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: output}, nil)
			if result.State != StepPassed {
				t.Fatalf("activate = %#v", result)
			}
			if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint([]tailnetPublish{{21000, 32101, 0}}) {
				t.Fatalf("publishes = %v", got)
			}
			evidence := decodeActivationEvidence(t, result)
			if evidence.Tailnet == nil || evidence.Tailnet.URL != "https://node.tailnet.ts.net:21000" || evidence.Tailnet.Port != 21000 ||
				evidence.Tailnet.Reachable != (test.probeErr == nil) || (test.probeErr != nil) != (evidence.Tailnet.ProbeError != "") {
				t.Fatalf("tailnet evidence = %#v", evidence.Tailnet)
			}
			if fmt.Sprint(probed) != "[https://node.tailnet.ts.net:21000]" {
				t.Fatalf("probed = %v", probed)
			}
			address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
			if err != nil || address == nil || !address.Published || address.UpstreamPort != 32101 || address.URL != "https://node.tailnet.ts.net:21000" {
				t.Fatalf("recorded address = %#v, %v", address, err)
			}
			if liveReleaseID(t, fixture) != release.Release.ID {
				t.Fatalf("live release = %d, want %d", liveReleaseID(t, fixture), release.Release.ID)
			}
			if log := output.joined(); !strings.Contains(log, "status: Published on the tailnet at https://node.tailnet.ts.net:21000") {
				t.Fatalf("transcript = %s", log)
			}
		})
	}
}

// A port tailscale refuses stops the candidate before anything went live:
// the operator's mapping that refused it is left alone, the address is
// recorded unpublished and the environment still has no live release.
func TestActivateStopsTheCandidateWhenTheTailnetRefusesThePort(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	run, lease, release := tailnetCandidate(t, fixture, 1, "tailnet-v1", 32101)
	insertPreviewAddress(t, fixture, 21000, 0, false)
	owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}
	publisher := &tailnetPublisherFake{served: map[int]int{21000: 0}, publishErr: errors.New("that tailnet port is already served by something the dashboard does not own")}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher}
	result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, nil)
	if result.State != StepFailed || result.ErrorCode != "tailnet_publish_failed" || !strings.Contains(result.ErrorMessage, "already served") || !result.Recovered {
		t.Fatalf("activate = %#v", result)
	}
	evidence := decodeActivationEvidence(t, result)
	if evidence.Tailnet != nil || evidence.Recovery == nil || !evidence.Recovery.CandidateStopped || !evidence.Recovery.TailnetRestored {
		t.Fatalf("evidence = %#v recovery=%#v", evidence, evidence.Recovery)
	}
	owner.mu.Lock()
	running := owner.running["tailnet-v1"]
	owner.mu.Unlock()
	if running || liveReleaseID(t, fixture) != 0 {
		t.Fatalf("refused publish left running=%t live=%d", running, liveReleaseID(t, fixture))
	}
	if got := publisher.withdrawn(); len(got) != 0 {
		t.Fatalf("withdraws = %v, want the operator's mapping left alone", got)
	}
	address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
	if err != nil || address == nil || address.Published || address.UpstreamPort != 0 {
		t.Fatalf("address after refusal = %#v, %v", address, err)
	}
	runtime, err := fixture.runs.RuntimeForRelease(context.Background(), release.Release.ID)
	if err != nil || runtime.State != "failed" {
		t.Fatalf("candidate runtime = %#v, %v", runtime, err)
	}
}

func TestActivateReportsTheTailnetUnavailableWithoutAPublisher(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	run, lease, _ := tailnetCandidate(t, fixture, 1, "tailnet-v1", 32101)
	insertPreviewAddress(t, fixture, 21000, 0, false)
	owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner}
	result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, nil)
	if result.State != StepUnavailable || result.ErrorCode != "tailnet_unavailable" || !result.Recovered {
		t.Fatalf("activate = %#v", result)
	}
	owner.mu.Lock()
	running := owner.running["tailnet-v1"]
	owner.mu.Unlock()
	address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
	if running || liveReleaseID(t, fixture) != 0 || err != nil || address == nil || address.Published {
		t.Fatalf("unavailable tailnet changed state: running=%t live=%d address=%#v, %v", running, liveReleaseID(t, fixture), address, err)
	}
}

// When a later release fails to publish, the mapping goes back to the
// release that is still live, with the recorded upstream passed along so
// the publisher recognises the dashboard's own earlier mapping.
func TestFailedTailnetPublishPointsTheAddressBackAtThePredecessor(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	firstRun, firstLease, first := tailnetCandidate(t, fixture, 1, "tailnet-v1", 32101)
	insertPreviewAddress(t, fixture, 21000, 0, false)
	owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true, "tailnet-v2": true}, allowConcurrent: true}
	publisher := &tailnetPublisherFake{}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher,
		tailnetProbe: func(context.Context, string) error { return nil },
	}
	if result := executor.activate(context.Background(), StepExecution{Run: *firstRun, ClaimToken: firstLease.Token, Output: discardStepOutput{}}, nil); result.State != StepPassed {
		t.Fatalf("first activate = %#v", result)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)

	fixture.now = fixture.now.Add(time.Minute)
	secondRun, secondLease, second := tailnetCandidate(t, fixture, 2, "tailnet-v2", 32102)
	publisher.failUpstream = 32102
	output := &recordingStepOutput{}
	result := executor.activate(context.Background(), StepExecution{Run: *secondRun, ClaimToken: secondLease.Token, Output: output}, nil)
	if result.State != StepFailed || result.ErrorCode != "tailnet_publish_failed" || !result.Recovered {
		t.Fatalf("second activate = %#v", result)
	}
	want := []tailnetPublish{{21000, 32101, 0}, {21000, 32102, 32101}, {21000, 32101, 32101}}
	if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("publishes = %v, want %v", got, want)
	}
	if got := publisher.withdrawn(); len(got) != 0 {
		t.Fatalf("a live predecessor was withdrawn: %v", got)
	}
	evidence := decodeActivationEvidence(t, result)
	if evidence.Recovery == nil || !evidence.Recovery.TailnetRestored || !evidence.Recovery.CandidateStopped {
		t.Fatalf("recovery = %#v", evidence.Recovery)
	}
	address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
	if err != nil || address == nil || !address.Published || address.UpstreamPort != 32101 {
		t.Fatalf("address after restore = %#v, %v", address, err)
	}
	if liveReleaseID(t, fixture) != first.Release.ID {
		t.Fatalf("live release = %d, want %d", liveReleaseID(t, fixture), first.Release.ID)
	}
	if runtime, err := fixture.runs.RuntimeForRelease(context.Background(), second.Release.ID); err != nil || runtime.State != "failed" {
		t.Fatalf("second runtime = %#v, %v", runtime, err)
	}
	if log := output.joined(); !strings.Contains(log, "Pointed the tailnet address back at the previous release") {
		t.Fatalf("transcript = %s", log)
	}
}

func claimPreviewRemoval(t *testing.T, fixture *releaseStoreFixture) StepExecution {
	t.Helper()
	ctx := context.Background()
	run, _, err := fixture.runs.Enqueue(ctx, RunRequest{
		ProjectID: fixture.projectID, EnvironmentID: fixture.envID,
		Operation: OperationPreviewRemove, Trigger: TriggerPreview, Actor: "admin",
		RequestDigest: fmt.Sprintf("preview-remove-%d", fixture.now.UnixNano()), PlanRevision: 1, SlotClass: SlotLight,
		Steps: []StepKey{StepRetirePrevious, StepNotify},
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.runs.ClaimNext(ctx, "preview-remove-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("claim = %#v, %v", lease, err)
	}
	claimed, err := fixture.runs.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: discardStepOutput{}}
}

func archivedAt(t *testing.T, fixture *releaseStoreFixture) int64 {
	t.Helper()
	var archived int64
	if err := fixture.base.DB.QueryRow(`SELECT archived_at FROM deploy_environments WHERE id=?`, fixture.envID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	return archived
}

// Removal takes the port off the tailnet and frees it whether or not the
// preview ever went live, and a withdraw that fails keeps the row so the
// retried run withdraws again instead of leaking the mapping.
func TestRemovePreviewWithdrawsTheTailnetAddressOnBothExits(t *testing.T) {
	t.Parallel()
	t.Run("no live release", func(t *testing.T) {
		t.Parallel()
		fixture := newReleaseStoreFixture(t)
		fixture.addPlan(t, 1, strings.Repeat("a", 40))
		setEnvironmentKind(t, fixture, EnvironmentPreview)
		insertPreviewAddress(t, fixture, 21000, 32101, true)
		execution := claimPreviewRemoval(t, fixture)
		owner := &previewRuntimeOwner{orderingRuntimeOwner: orderingRuntimeOwner{running: map[string]bool{}}}
		publisher := &tailnetPublisherFake{served: map[int]int{21000: 32101}}
		executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher}
		result := executor.removePreview(context.Background(), execution)
		if result.State != StepSkipped || !strings.Contains(string(result.Evidence), `"addressWithdrawn":true`) {
			t.Fatalf("removePreview = %#v", result)
		}
		if got := publisher.withdrawn(); fmt.Sprint(got) != "[21000]" {
			t.Fatalf("withdraws = %v", got)
		}
		if address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID); err != nil || address != nil {
			t.Fatalf("address after removal = %#v, %v", address, err)
		}
		if fmt.Sprint(owner.removed) != fmt.Sprint([]int64{fixture.envID}) || archivedAt(t, fixture) == 0 {
			t.Fatalf("resources removed for %v, archived at %d", owner.removed, archivedAt(t, fixture))
		}
	})
	t.Run("live release", func(t *testing.T) {
		t.Parallel()
		fixture := newReleaseStoreFixture(t)
		run, lease, release := tailnetCandidate(t, fixture, 1, "tailnet-live", 32101)
		if _, err := fixture.runs.ActivateCandidate(context.Background(), run.ID, lease.Token, release.Release.ID); err != nil {
			t.Fatal(err)
		}
		fixture.finishActivatedRun(t, run.ID, release.Release.ID, lease.Token)
		setEnvironmentKind(t, fixture, EnvironmentPreview)
		insertPreviewAddress(t, fixture, 21000, 32101, true)
		execution := claimPreviewRemoval(t, fixture)
		owner := &previewRuntimeOwner{orderingRuntimeOwner: orderingRuntimeOwner{running: map[string]bool{"tailnet-live": true}}}
		publisher := &tailnetPublisherFake{served: map[int]int{21000: 32101}}
		executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher}
		result := executor.removePreview(context.Background(), execution)
		if result.State != StepPassed || !strings.Contains(string(result.Evidence), `"addressWithdrawn":true`) {
			t.Fatalf("removePreview = %#v", result)
		}
		owner.mu.Lock()
		calls := append([]string(nil), owner.calls...)
		owner.mu.Unlock()
		if fmt.Sprint(calls) != "[stop:tailnet-live]" || fmt.Sprint(publisher.withdrawn()) != "[21000]" {
			t.Fatalf("calls = %v, withdraws = %v", calls, publisher.withdrawn())
		}
		if address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID); err != nil || address != nil {
			t.Fatalf("address after removal = %#v, %v", address, err)
		}
		if liveReleaseID(t, fixture) != 0 || archivedAt(t, fixture) == 0 {
			t.Fatalf("live=%d archived=%d after removal", liveReleaseID(t, fixture), archivedAt(t, fixture))
		}
	})
	t.Run("withdraw failure keeps the address for the retry", func(t *testing.T) {
		t.Parallel()
		fixture := newReleaseStoreFixture(t)
		fixture.addPlan(t, 1, strings.Repeat("a", 40))
		setEnvironmentKind(t, fixture, EnvironmentPreview)
		insertPreviewAddress(t, fixture, 21000, 32101, true)
		execution := claimPreviewRemoval(t, fixture)
		owner := &previewRuntimeOwner{orderingRuntimeOwner: orderingRuntimeOwner{running: map[string]bool{}}}
		publisher := &tailnetPublisherFake{served: map[int]int{21000: 32101}, withdrawErr: errors.New("tailscale: connection refused")}
		executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher}
		result := executor.removePreview(context.Background(), execution)
		if result.State != StepFailed || result.ErrorCode != "tailnet_withdraw_failed" || !strings.Contains(result.ErrorMessage, "connection refused") {
			t.Fatalf("removePreview = %#v", result)
		}
		if address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID); err != nil || address == nil || !address.Published {
			t.Fatalf("address after a failed withdraw = %#v, %v", address, err)
		}
		if len(owner.removed) != 0 || archivedAt(t, fixture) != 0 {
			t.Fatalf("a failed withdraw went on to remove resources (%v) or archive (%d)", owner.removed, archivedAt(t, fixture))
		}
	})
}

// The startup sweep withdraws served ports in the preview range that no
// preview owns, leaves ports outside the range alone, and marks a preview
// unpublished when tailscale no longer serves its port.
func TestSweepTailnetWithdrawsOrphanedPortsAndUnpublishesDeadAddresses(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	servedPreview := f.addEnvironment(t, "pr-1", EnvironmentPreview)
	deadPreview := f.addEnvironment(t, "pr-2", EnvironmentPreview)
	for _, environmentID := range []int64{servedPreview, deadPreview} {
		address, err := f.runs.AllocatePreviewAddress(ctx, environmentID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runs.MarkPreviewAddressPublished(ctx, environmentID, fmt.Sprintf("https://node.tailnet.ts.net:%d", address.Port), 32100+address.Port-previewTailnetPortMin, true); err != nil {
			t.Fatal(err)
		}
	}
	// 21007 is served by the operator for something of their own (a
	// directory, a funnel): listed, but not the dashboard's to withdraw.
	publisher := &tailnetPublisherFake{served: map[int]int{21000: 32100, 21005: 32105, 21007: 0, 20999: 31000, 22000: 31001}}
	executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
	sweep, err := executor.SweepTailnet(ctx)
	if err != nil || fmt.Sprint(sweep.Withdrawn) != "[21005]" || fmt.Sprint(sweep.Unpublished) != fmt.Sprint([]int64{deadPreview}) {
		t.Fatalf("sweep = %#v, %v", sweep, err)
	}
	if got := publisher.withdrawn(); fmt.Sprint(got) != "[21005]" {
		t.Fatalf("withdraws = %v", got)
	}
	served, err := f.runs.PreviewAddressFor(ctx, servedPreview)
	if err != nil || served == nil || !served.Published || served.UpstreamPort != 32100 {
		t.Fatalf("served preview address = %#v, %v", served, err)
	}
	dead, err := f.runs.PreviewAddressFor(ctx, deadPreview)
	if err != nil || dead == nil || dead.Published || dead.UpstreamPort != 0 || dead.URL != "https://node.tailnet.ts.net:21001" {
		t.Fatalf("dead preview address = %#v, %v", dead, err)
	}
	if sweep, err := (&NormalizedStepExecutor{store: f.runs}).SweepTailnet(ctx); err != nil || len(sweep.Withdrawn) != 0 || len(sweep.Unpublished) != 0 {
		t.Fatalf("sweep without a publisher = %#v, %v", sweep, err)
	}
}

func TestAllocatePreviewAddressStepsAroundEveryServedPortWhateverItsShape(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "pr-9", EnvironmentPreview)
	// The operator serves a directory on 21000 (listed with no upstream)
	// and the dashboard's own earlier mapping sits on 21001; a new preview
	// must take neither.
	address, err := f.runs.AllocatePreviewAddress(context.Background(), environmentID, map[int]int{21000: 0, 21001: 32101})
	if err != nil || address.Port != 21002 {
		t.Fatalf("address = %#v, %v", address, err)
	}
}

// A publish whose record fails comes down again: a mapping the address
// table does not know about would refuse every later publish of the
// preview as somebody else's.
func TestPublishPreviewAddressWithdrawsAMappingItCouldNotRecord(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "pr-3", EnvironmentPreview)
	publisher := &tailnetPublisherFake{}
	executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
	// The environment has no address row, so the record cannot land.
	evidence, failure := executor.publishPreviewAddress(context.Background(), StepExecution{Output: discardStepOutput{}}, environmentID,
		PreviewAddress{Kind: previewAddressTailnet, Port: 21000}, ReleaseRuntime{Port: 32101})
	if evidence != nil || failure == nil || failure.ErrorCode != "tailnet_publish_failed" || !strings.Contains(failure.ErrorMessage, "could not be recorded") {
		t.Fatalf("publish = %#v, %#v", evidence, failure)
	}
	if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint([]tailnetPublish{{21000, 32101, 0}}) {
		t.Fatalf("publishes = %v", got)
	}
	if got := publisher.withdrawn(); fmt.Sprint(got) != "[21000]" {
		t.Fatalf("withdraws = %v, want the unrecorded mapping withdrawn", got)
	}
	if served, _ := publisher.ServedTailnetPorts(context.Background()); len(served) != 0 {
		t.Fatalf("served after the failure = %v", served)
	}
}

// A removal frees the row whatever serves the port, but withdraws only the
// mapping the row vouches for: after the dashboard has withdrawn its own,
// or once the operator re-pointed the port, what is served there is theirs.
func TestWithdrawPreviewAddressWithdrawsOnlyTheMappingTheRowVouchesFor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		upstream  int
		published bool
		served    map[int]int
		withdrawn string
	}{
		{"own mapping", 32101, true, map[int]int{21000: 32101}, "[21000]"},
		{"foreign mapping on a published row", 32101, true, map[int]int{21000: 0}, "[]"},
		{"operator's loopback mapping after our withdrawal", 0, false, map[int]int{21000: 8080}, "[]"},
		{"nothing served", 0, false, map[int]int{}, "[]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newOrchestrationFixture(t)
			ctx := context.Background()
			environmentID := f.addEnvironment(t, "pr-4", EnvironmentPreview)
			if _, err := f.runs.AllocatePreviewAddress(ctx, environmentID, nil); err != nil {
				t.Fatal(err)
			}
			if err := f.runs.MarkPreviewAddressPublished(ctx, environmentID, "https://node.tailnet.ts.net:21000", test.upstream, test.published); err != nil {
				t.Fatal(err)
			}
			publisher := &tailnetPublisherFake{served: test.served}
			executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
			had, err := executor.withdrawPreviewAddress(ctx, environmentID)
			if err != nil || !had {
				t.Fatalf("withdraw = %t, %v", had, err)
			}
			if got := publisher.withdrawn(); fmt.Sprint(got) != test.withdrawn {
				t.Fatalf("withdraws = %v, want %s", got, test.withdrawn)
			}
			if address, err := f.runs.PreviewAddressFor(ctx, environmentID); err != nil || address != nil {
				t.Fatalf("address after the withdrawal = %#v, %v; want the row freed", address, err)
			}
		})
	}
	t.Run("unreadable serve config keeps the row for the retry", func(t *testing.T) {
		t.Parallel()
		f := newOrchestrationFixture(t)
		ctx := context.Background()
		environmentID := f.addEnvironment(t, "pr-4", EnvironmentPreview)
		if _, err := f.runs.AllocatePreviewAddress(ctx, environmentID, nil); err != nil {
			t.Fatal(err)
		}
		publisher := &tailnetPublisherFake{servedErr: errors.New("tailscale: connection refused")}
		executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
		if had, err := executor.withdrawPreviewAddress(ctx, environmentID); !had || err == nil {
			t.Fatalf("withdraw = %t, %v; want the read failure reported", had, err)
		}
		if address, err := f.runs.PreviewAddressFor(ctx, environmentID); err != nil || address == nil {
			t.Fatalf("address after the failed read = %#v, %v; want the row kept", address, err)
		}
	})
}

// After a failed cutover with nothing left to serve, the restore leaves a
// mapping the row does not vouch for alone — the operator's, or one the
// dashboard already took down — and still records the address unpublished;
// its own mapping comes down, and a serve config it cannot read means the
// state could not be proven.
func TestRestoreTailnetLeavesAForeignMappingAloneAndUnpublishesTheRow(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "pr-5", EnvironmentPreview)
	if _, err := f.runs.AllocatePreviewAddress(ctx, environmentID, nil); err != nil {
		t.Fatal(err)
	}
	execution := StepExecution{Output: discardStepOutput{}}
	release := Release{EnvironmentID: environmentID}
	for _, test := range []struct {
		name      string
		served    map[int]int
		servedErr error
		restored  bool
		withdrawn string
	}{
		{name: "foreign mapping", served: map[int]int{21000: 0}, restored: true, withdrawn: "[]"},
		{name: "own mapping", served: map[int]int{21000: 32101}, restored: true, withdrawn: "[21000]"},
		{name: "unreadable serve config", servedErr: errors.New("tailscale: connection refused"), restored: false, withdrawn: "[]"},
	} {
		if err := f.runs.MarkPreviewAddressPublished(ctx, environmentID, "https://node.tailnet.ts.net:21000", 32101, true); err != nil {
			t.Fatal(err)
		}
		publisher := &tailnetPublisherFake{served: test.served, servedErr: test.servedErr}
		executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
		if restored := executor.restoreTailnet(ctx, execution, release, false); restored != test.restored {
			t.Fatalf("%s: restored = %t, want %t", test.name, restored, test.restored)
		}
		if got := publisher.withdrawn(); fmt.Sprint(got) != test.withdrawn {
			t.Fatalf("%s: withdraws = %v, want %s", test.name, got, test.withdrawn)
		}
		address, err := f.runs.PreviewAddressFor(ctx, environmentID)
		if err != nil || address == nil || address.Published == test.restored || (address.UpstreamPort == 0) != test.restored {
			t.Fatalf("%s: address after restore = %#v, %v", test.name, address, err)
		}
	}
}

// stoppedPredecessorOwner is the ordering owner whose predecessor cannot be
// started again, as when its container is gone.
type stoppedPredecessorOwner struct{ orderingRuntimeOwner }

func (o *stoppedPredecessorOwner) StartExisting(_ context.Context, runtime ReleaseRuntime, _ map[string]string, _ func(BuildLog) error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "restore:"+runtime.RuntimeID)
	return fmt.Errorf("no such container: %s", runtime.RuntimeID)
}

// A stop-first cutover that fails after the predecessor was stopped, and
// cannot start it again, has nothing to point the tailnet address at: the
// mapping is withdrawn and the address recorded unreachable rather than
// advertised at a port that answers nothing.
func TestFailedTailnetPublishWithdrawsTheAddressWhenThePredecessorStaysDown(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := tailnetPreviewPlan()
	plan.Strategy = StrategyStopFirst
	firstRun, firstLease, first := tailnetCandidateWithPlan(t, fixture, 1, "tailnet-v1", 32101, plan)
	insertPreviewAddress(t, fixture, 21000, 0, false)
	owner := &stoppedPredecessorOwner{orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true, "tailnet-v2": true}, allowConcurrent: true}}
	publisher := &tailnetPublisherFake{}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher,
		tailnetProbe: func(context.Context, string) error { return nil },
	}
	if result := executor.activate(context.Background(), StepExecution{Run: *firstRun, ClaimToken: firstLease.Token, Output: discardStepOutput{}}, nil); result.State != StepPassed {
		t.Fatalf("first activate = %#v", result)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)
	// Starting the second candidate stopped the first release, as stop-first
	// does; its container is then gone by the time the cutover fails.
	owner.mu.Lock()
	owner.running["tailnet-v1"] = false
	owner.mu.Unlock()

	fixture.now = fixture.now.Add(time.Minute)
	secondRun, secondLease, second := tailnetCandidateWithPlan(t, fixture, 2, "tailnet-v2", 32102, plan)
	publisher.failUpstream = 32102
	output := &recordingStepOutput{}
	result := executor.activate(context.Background(), StepExecution{Run: *secondRun, ClaimToken: secondLease.Token, Output: output}, nil)
	if result.State != StepFailed || result.ErrorCode != "tailnet_publish_failed" || result.Recovered {
		t.Fatalf("second activate = %#v", result)
	}
	evidence := decodeActivationEvidence(t, result)
	if evidence.Recovery == nil || evidence.Recovery.RuntimeRestored || !evidence.Recovery.TailnetRestored || !evidence.Recovery.CandidateStopped {
		t.Fatalf("recovery = %#v", evidence.Recovery)
	}
	want := []tailnetPublish{{21000, 32101, 0}, {21000, 32102, 32101}}
	if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("publishes = %v, want %v and no republish at a stopped predecessor", got, want)
	}
	if got := publisher.withdrawn(); fmt.Sprint(got) != "[21000]" {
		t.Fatalf("withdraws = %v", got)
	}
	address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
	if err != nil || address == nil || address.Published || address.UpstreamPort != 0 {
		t.Fatalf("address after the failed cutover = %#v, %v; want it recorded unreachable", address, err)
	}
	if liveReleaseID(t, fixture) != first.Release.ID {
		t.Fatalf("live release = %d, want %d", liveReleaseID(t, fixture), first.Release.ID)
	}
	if runtime, err := fixture.runs.RuntimeForRelease(context.Background(), second.Release.ID); err != nil || runtime.State != "failed" {
		t.Fatalf("second runtime = %#v, %v", runtime, err)
	}
	if log := output.joined(); !strings.Contains(log, "Withdrew the tailnet address") || strings.Contains(log, "Pointed the tailnet address back") {
		t.Fatalf("transcript = %s", log)
	}
}

// A mapping the row does not vouch for — a publish whose record never
// landed — is withdrawn at start-up whether the row says published at some
// other upstream or not published at all, and the published one is marked
// unreachable. Left standing, the mapping would refuse every later publish
// of the preview as somebody else's.
func TestSweepTailnetWithdrawsAMappingTheRowDoesNotVouchFor(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	stale := f.addEnvironment(t, "pr-1", EnvironmentPreview)
	unrecorded := f.addEnvironment(t, "pr-2", EnvironmentPreview)
	for _, environmentID := range []int64{stale, unrecorded} {
		if _, err := f.runs.AllocatePreviewAddress(ctx, environmentID, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.runs.MarkPreviewAddressPublished(ctx, stale, "https://node.tailnet.ts.net:21000", 32101, true); err != nil {
		t.Fatal(err)
	}
	publisher := &tailnetPublisherFake{served: map[int]int{21000: 32102, 21001: 32103}}
	executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
	sweep, err := executor.SweepTailnet(ctx)
	if err != nil || fmt.Sprint(sweep.Withdrawn) != "[21000 21001]" || fmt.Sprint(sweep.Unpublished) != fmt.Sprint([]int64{stale}) {
		t.Fatalf("sweep = %#v, %v", sweep, err)
	}
	if got := publisher.withdrawn(); fmt.Sprint(got) != "[21000 21001]" {
		t.Fatalf("withdraws = %v", got)
	}
	address, err := f.runs.PreviewAddressFor(ctx, stale)
	if err != nil || address == nil || address.Published || address.UpstreamPort != 0 || address.URL != "https://node.tailnet.ts.net:21000" {
		t.Fatalf("stale preview address = %#v, %v", address, err)
	}
	if address, err = f.runs.PreviewAddressFor(ctx, unrecorded); err != nil || address == nil || address.Published || address.UpstreamPort != 0 {
		t.Fatalf("unrecorded preview address = %#v, %v", address, err)
	}
	if served, _ := publisher.ServedTailnetPorts(ctx); len(served) != 0 {
		t.Fatalf("served after the sweep = %v", served)
	}
	if sweep, err = executor.SweepTailnet(ctx); err != nil || len(sweep.Withdrawn) != 0 || len(sweep.Unpublished) != 0 {
		t.Fatalf("second sweep = %#v, %v; want nothing left to do", sweep, err)
	}
}

// A serve config that cannot be read — Tailscale absent or stopped — is
// only a problem for a preview recorded as live on it; otherwise the sweep
// has nothing to reconcile and stays quiet.
func TestSweepTailnetStaysQuietAboutAnUnreadableServeConfigWithoutLivePreviews(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "pr-1", EnvironmentPreview)
	publisher := &tailnetPublisherFake{servedErr: errors.New("tailscale is not installed on this host")}
	executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
	if sweep, err := executor.SweepTailnet(ctx); err != nil || len(sweep.Withdrawn) != 0 || len(sweep.Unpublished) != 0 {
		t.Fatalf("sweep without addresses = %#v, %v", sweep, err)
	}
	if _, err := f.runs.AllocatePreviewAddress(ctx, environmentID, nil); err != nil {
		t.Fatal(err)
	}
	if sweep, err := executor.SweepTailnet(ctx); err != nil || len(sweep.Withdrawn) != 0 || len(sweep.Unpublished) != 0 {
		t.Fatalf("sweep with an unpublished address = %#v, %v", sweep, err)
	}
	if err := f.runs.MarkPreviewAddressPublished(ctx, environmentID, "https://node.tailnet.ts.net:21000", 32101, true); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.SweepTailnet(ctx); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("sweep with a live address = %v, want the read failure", err)
	}
	if address, err := f.runs.PreviewAddressFor(ctx, environmentID); err != nil || address == nil || !address.Published || address.UpstreamPort != 32101 {
		t.Fatalf("a sweep that could not read changed the record: %#v, %v", address, err)
	}
}

// The sweep reads the address table before the serve config, so an address
// recorded after the table was read is never judged against a serve config
// older than itself: it is simply not part of this sweep.
func TestSweepTailnetReadsTheAddressTableBeforeTheServeConfig(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "pr-1", EnvironmentPreview)
	publisher := &tailnetPublisherFake{}
	publisher.onServed = func() {
		if _, err := f.runs.AllocatePreviewAddress(ctx, environmentID, nil); err != nil {
			t.Error(err)
		}
		if err := f.runs.MarkPreviewAddressPublished(ctx, environmentID, "https://node.tailnet.ts.net:21000", 32101, true); err != nil {
			t.Error(err)
		}
	}
	executor := &NormalizedStepExecutor{store: f.runs, tailnet: publisher}
	sweep, err := executor.SweepTailnet(ctx)
	if err != nil || len(sweep.Withdrawn) != 0 || len(sweep.Unpublished) != 0 {
		t.Fatalf("sweep = %#v, %v; want the address recorded after the table was read left alone", sweep, err)
	}
	if address, err := f.runs.PreviewAddressFor(ctx, environmentID); err != nil || address == nil || !address.Published {
		t.Fatalf("address after the sweep = %#v, %v", address, err)
	}
}
