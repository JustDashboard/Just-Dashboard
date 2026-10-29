package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var dataPointMoment = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// A server whose archive reaches back before the release is recorded at the
// moment itself, even when a dump exists too: the archive stops anywhere.
func TestDataPointIsTheMomentWhenTheArchiveReachesIt(t *testing.T) {
	point := DecideDataPoint(DataPointFacts{
		ConnectionID: 5, Name: "orders-db", Engine: "postgres",
		ArchiveID: 3, ArchiveFrom: dataPointMoment.Add(-6 * time.Hour),
		DumpRunID: 41, DumpJobID: 2, DumpAt: dataPointMoment.Add(-time.Hour),
	}, dataPointMoment)
	if point.Kind != DataPointPITR || point.At == nil || !point.At.Equal(dataPointMoment) || point.ArchiveID != 3 || point.BackupRunID != 0 {
		t.Fatalf("point with an archive = %+v", point)
	}
}

// Only dumps: the newest from before the release is the point.
func TestDataPointIsTheNewestDumpWithoutAnArchive(t *testing.T) {
	taken := dataPointMoment.Add(-90 * time.Minute)
	point := DecideDataPoint(DataPointFacts{ConnectionID: 5, Name: "orders-db", Engine: "mysql", DumpRunID: 41, DumpJobID: 2, DumpAt: taken}, dataPointMoment)
	if point.Kind != DataPointDump || point.At == nil || !point.At.Equal(taken) || point.BackupRunID != 41 || point.BackupJobID != 2 {
		t.Fatalf("point from a dump = %+v", point)
	}
	if line := DataPointLine(point); !strings.Contains(line, "backup run 41") {
		t.Fatalf("transcript line = %q", line)
	}
	// An archive that has no base backup from before the release cannot
	// recover to it; the dump still can.
	point = DecideDataPoint(DataPointFacts{ConnectionID: 5, Name: "orders-db", ArchiveID: 3, ArchiveFrom: dataPointMoment.Add(time.Minute),
		DumpRunID: 41, DumpAt: taken}, dataPointMoment)
	if point.Kind != DataPointDump {
		t.Fatalf("an archive newer than the release outranked the dump: %+v", point)
	}
}

// Nothing to restore from is a point of its own, with the reason.
func TestDataPointSaysWhyThereIsNone(t *testing.T) {
	for name, facts := range map[string]DataPointFacts{
		"no backup":       {ConnectionID: 5, Name: "orders-db"},
		"young archive":   {ConnectionID: 5, Name: "orders-db", ArchiveID: 3},
		"engine":          {ConnectionID: 6, Name: "cache", Engine: "redis", Unsupported: "a copy is restored for PostgreSQL, MySQL and MariaDB databases, and cache is redis", DumpRunID: 9, DumpAt: dataPointMoment},
		"dump from after": {ConnectionID: 5, Name: "orders-db", DumpRunID: 9, DumpAt: dataPointMoment.Add(time.Minute)},
	} {
		point := DecideDataPoint(facts, dataPointMoment)
		if point.Kind != DataPointNone || point.Reason == "" || point.At != nil {
			t.Fatalf("%s: point = %+v", name, point)
		}
	}
}

func TestDataCopyEligibility(t *testing.T) {
	at := dataPointMoment
	restorable := []ReleaseDataPoint{{ConnectionID: 5, Name: "orders-db", Kind: DataPointDump, At: &at, BackupRunID: 41}}
	none := []ReleaseDataPoint{{ConnectionID: 5, Name: "orders-db", Kind: DataPointNone, Reason: "no backup holds a dump of orders-db"}}
	for _, test := range []struct {
		name     string
		kind     EnvironmentKind
		state    string
		points   []ReleaseDataPoint
		eligible bool
		reason   string
	}{
		{"retained with a point", EnvironmentProduction, "retained", restorable, true, ""},
		{"live with a point", EnvironmentStaging, "live", restorable, true, ""},
		{"preview", EnvironmentPreview, "retained", restorable, false, "preview"},
		{"failed", EnvironmentProduction, "failed", restorable, false, "went live"},
		{"candidate", EnvironmentProduction, "candidate", restorable, false, "went live"},
		{"no point recorded", EnvironmentProduction, "retained", nil, false, "no data point was recorded"},
		{"only none", EnvironmentProduction, "retained", none, false, "no backup holds a dump"},
		{"one of two restorable", EnvironmentProduction, "retained", append(append([]ReleaseDataPoint{}, none...), restorable...), true, ""},
	} {
		got := DataCopyEligibilityFor(test.kind, Release{State: test.state}, test.points)
		if got.Eligible != test.eligible || !strings.Contains(got.Reason, test.reason) || (test.eligible && got.Reason != "") {
			t.Errorf("%s: eligibility = %+v", test.name, got)
		}
	}
}

func TestDataCopyLifetime(t *testing.T) {
	now := dataPointMoment
	inAnHour := now.Add(time.Hour)
	if next, err := ExtendDataCopyExpiry(&inAnHour, now, 1); err != nil || !next.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("one more hour from an hour left = %s, %v", next, err)
	}
	past := now.Add(-time.Hour)
	if next, err := ExtendDataCopyExpiry(&past, now, 2); err != nil || !next.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("an extension of a copy past its end counts from now: %s, %v", next, err)
	}
	far := now.Add(23 * time.Hour)
	if next, err := ExtendDataCopyExpiry(&far, now, 5); err != nil || !next.Equal(now.Add(DataCopyMaxAhead)) {
		t.Fatalf("an extension is bounded: %s, %v", next, err)
	}
	for _, hours := range []int{0, -1, 25} {
		if _, err := ExtendDataCopyExpiry(&inAnHour, now, hours); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("%d hours = %v", hours, err)
		}
	}
	ready := DataCopy{State: DataCopyReady, ExpiresAt: &inAnHour}
	if DataCopyExpired(ready, now) || !DataCopyExpired(ready, inAnHour) || DataCopyExpired(DataCopy{State: DataCopyRemoved, ExpiresAt: &past}, now) {
		t.Fatal("a copy expires when it is ready and its end has come")
	}
	waiting := DataCopy{AfterRunID: 9}
	if start, skip := WaitingDataCopyDecision(waiting, &EngineRun{State: RunRunning}); start || skip != "" {
		t.Fatal("a copy waiting for a running rollback starts or ends early")
	}
	if start, _ := WaitingDataCopyDecision(waiting, &EngineRun{State: RunSucceeded}); !start {
		t.Fatal("a copy whose rollback succeeded does not start")
	}
	if start, skip := WaitingDataCopyDecision(waiting, &EngineRun{State: RunFailed}); start || !strings.Contains(skip, "failed") {
		t.Fatalf("a copy whose rollback failed = %v %q", start, skip)
	}
	if start, skip := WaitingDataCopyDecision(waiting, nil); start || skip == "" {
		t.Fatal("a copy whose run is gone waits for ever")
	}
	if start, _ := WaitingDataCopyDecision(DataCopy{}, nil); !start {
		t.Fatal("a copy that waits for nobody does not start")
	}
}

// The points recorded for a release are listed with it, with whether a copy
// is offered, and two releases recorded at different points differ in the
// comparison.
func TestReleaseListCarriesDataPointsAndEligibility(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	ctx := context.Background()
	release := func(number int, state string) int64 {
		result, err := fixture.base.DB.Exec(
			"INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, config_digest, variables_digest, strategy, created_at) VALUES(?, ?, ?, ?, 1, ?, ?, 'blue_green', ?)",
			fixture.projectID, fixture.envID, number, state, fakeContentDigest("config"), fakeContentDigest("variables"), fixture.now.Unix())
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	first, second, unrecorded := release(1, "retained"), release(2, "live"), release(3, "retained")
	firstAt, secondAt := fixture.now.Add(-time.Hour), fixture.now
	if err := fixture.runs.RecordReleaseDataPoints(ctx, first, []ReleaseDataPoint{DecideDataPoint(DataPointFacts{ConnectionID: 5, Name: "orders-db", Engine: "postgres", DumpRunID: 41, DumpJobID: 2, DumpAt: firstAt}, firstAt)}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runs.RecordReleaseDataPoints(ctx, second, []ReleaseDataPoint{DecideDataPoint(DataPointFacts{ConnectionID: 5, Name: "orders-db", Engine: "postgres", DumpRunID: 42, DumpJobID: 2, DumpAt: secondAt}, secondAt)}); err != nil {
		t.Fatal(err)
	}
	// A second recording of the same release keeps the first.
	if err := fixture.runs.RecordReleaseDataPoints(ctx, first, []ReleaseDataPoint{{ConnectionID: 5, Kind: DataPointNone, Reason: "later", RecordedAt: secondAt}}); err != nil {
		t.Fatal(err)
	}
	releases, err := fixture.runs.EnvironmentReleases(ctx, fixture.projectID, fixture.envID, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]Release{}
	for _, release := range releases {
		byID[release.ID] = release
	}
	if points := byID[first].DataPoints; len(points) != 1 || points[0].Kind != DataPointDump || points[0].BackupRunID != 41 || byID[first].DataCopy == nil || !byID[first].DataCopy.Eligible {
		t.Fatalf("release 1 = %+v %+v", byID[first].DataPoints, byID[first].DataCopy)
	}
	if byID[unrecorded].DataPoints != nil || byID[unrecorded].DataCopy == nil || byID[unrecorded].DataCopy.Eligible {
		t.Fatalf("a release with no point = %+v %+v", byID[unrecorded].DataPoints, byID[unrecorded].DataCopy)
	}
	raw, _ := json.Marshal(byID[unrecorded])
	if strings.Contains(string(raw), `"dataPoints"`) {
		t.Fatalf("a release with no point lists them: %s", raw)
	}
	comparison, err := fixture.runs.CompareReleases(ctx, first, second)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Changes["dataPoint"] || len(comparison.DataPoints.From) != 1 || comparison.DataPoints.To[0].BackupRunID != 42 {
		t.Fatalf("comparison = %+v %+v", comparison.Changes, comparison.DataPoints)
	}
	same, err := fixture.runs.CompareReleases(ctx, first, first)
	if err != nil || same.Changes["dataPoint"] {
		t.Fatalf("a release compared with itself = %+v, %v", same, err)
	}
}

// A copy is asked for once per release and database while it is kept; it
// waits, is claimed once, becomes ready with a two-hour lifetime, can be kept
// longer, and ends once.
func TestDataCopyLifecycle(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	ctx := context.Background()
	result, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, config_digest, variables_digest, strategy, created_at) VALUES(?, ?, 4, 'retained', 1, ?, ?, 'blue_green', ?)",
		fixture.projectID, fixture.envID, fakeContentDigest("config"), fakeContentDigest("variables"), fixture.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	release, err := fixture.runs.Release(ctx, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.CreateDataCopies(ctx, release.Release, 0, "admin"); !errors.Is(err, ErrDataCopyUnavailable) {
		t.Fatalf("a copy of a release with no point = %v", err)
	}
	at := fixture.now.Add(-time.Hour)
	if err := fixture.runs.RecordReleaseDataPoints(ctx, releaseID, []ReleaseDataPoint{
		DecideDataPoint(DataPointFacts{ConnectionID: 5, Name: "orders-db", Engine: "postgres", ArchiveID: 3, ArchiveFrom: at.Add(-time.Hour)}, at),
		DecideDataPoint(DataPointFacts{ConnectionID: 6, Name: "cache", Engine: "redis", Unsupported: "not restorable"}, at),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.CreateDataCopies(ctx, release.Release, 999, "admin"); !errors.Is(err, ErrDataCopyUnavailable) {
		t.Fatalf("a copy waiting for another environment's run = %v", err)
	}
	copies, err := fixture.runs.CreateDataCopies(ctx, release.Release, 0, "admin")
	if err != nil || len(copies) != 1 {
		t.Fatalf("copies = %+v, %v", copies, err)
	}
	copy := copies[0]
	if copy.State != DataCopyWaiting || copy.Source.Kind != DataPointPITR || copy.Source.At == nil || !copy.Source.At.Equal(at) || copy.ReleaseNumber != 4 || copy.CreatedBy != "admin" {
		t.Fatalf("the copy asked for = %+v", copy)
	}
	if _, err := fixture.runs.CreateDataCopies(ctx, release.Release, 0, "admin"); !errors.Is(err, ErrDataCopyExists) {
		t.Fatalf("a second copy while the first waits = %v", err)
	}
	var wg sync.WaitGroup
	claims := make([]bool, 4)
	for index := range claims {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claims[index], _ = fixture.runs.ClaimDataCopy(ctx, copy.ID)
		}()
	}
	wg.Wait()
	claimed := 0
	for _, won := range claims {
		if won {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("%d claims of one copy won", claimed)
	}
	recovered := at.Add(-time.Second)
	ready, err := fixture.runs.DataCopyReady(ctx, copy.ID, DataCopyReadiness{
		Container: "jd-e1-copy1-db", Network: "jd-e1-db-x", Host: "db-5-copy-1.jd.internal", Port: 5432, Database: "app",
		SecretSealed: "sealed", RecoveredTo: &recovered, RestoreMS: 1200,
	})
	if err != nil || ready.State != DataCopyReady || ready.ExpiresAt == nil || !ready.ExpiresAt.Equal(fixture.now.Add(DataCopyLifetime)) || ready.Host != "db-5-copy-1.jd.internal" {
		t.Fatalf("ready copy = %+v, %v", ready, err)
	}
	if sealed, err := fixture.runs.DataCopySecret(ctx, copy.ID); err != nil || sealed != "sealed" {
		t.Fatalf("secret = %q, %v", sealed, err)
	}
	raw, _ := json.Marshal(ready)
	if strings.Contains(string(raw), "sealed") {
		t.Fatalf("the copy's JSON carries its sealed sign-in: %s", raw)
	}
	extended, err := fixture.runs.ExtendDataCopy(ctx, copy.ID, 1)
	if err != nil || !extended.ExpiresAt.Equal(fixture.now.Add(DataCopyLifetime+time.Hour)) {
		t.Fatalf("extended = %+v, %v", extended, err)
	}
	listed, err := fixture.runs.DataCopies(ctx, fixture.envID, DataCopyFilter{ReleaseID: releaseID})
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	removed, err := fixture.runs.EndDataCopy(ctx, copy.ID, DataCopyRemoved, "expired")
	if err != nil || removed.State != DataCopyRemoved || removed.Reason != "expired" || removed.EndedAt == nil || removed.ExpiresAt != nil {
		t.Fatalf("removed = %+v, %v", removed, err)
	}
	if sealed, _ := fixture.runs.DataCopySecret(ctx, copy.ID); sealed != "" {
		t.Fatal("an ended copy keeps its sign-in")
	}
	if _, err := fixture.runs.EndDataCopy(ctx, copy.ID, DataCopyFailed, "again"); !errors.Is(err, ErrDataCopyEnded) {
		t.Fatalf("ending an ended copy = %v", err)
	}
	if _, err := fixture.runs.ExtendDataCopy(ctx, copy.ID, 1); !errors.Is(err, ErrDataCopyNotReady) {
		t.Fatalf("extending an ended copy = %v", err)
	}
	// Ended within the day, it is still listed; a day on, it is not.
	fixture.now = fixture.now.Add(25 * time.Hour)
	if listed, _ := fixture.runs.DataCopies(ctx, fixture.envID, DataCopyFilter{}); len(listed) != 0 {
		t.Fatalf("a copy ended a day ago is still listed: %+v", listed)
	}
	// And a new copy can be asked for once the old one ended.
	if again, err := fixture.runs.CreateDataCopies(ctx, release.Release, 0, "admin"); err != nil || len(again) != 1 {
		t.Fatalf("a copy after the first ended = %+v, %v", again, err)
	}
}

type dataPointStub struct {
	facts []DataPointFacts
	err   error
	asked []int64
}

func (d *dataPointStub) ReleaseDataFacts(_ context.Context, environmentID int64, _ time.Time) ([]DataPointFacts, error) {
	d.asked = append(d.asked, environmentID)
	return d.facts, d.err
}

// activateWithDataPoints activates one candidate with the facts given and
// returns the release's points and the transcript.
func activateWithDataPoints(t *testing.T, observer DataPointObserver) ([]ReleaseDataPoint, []string) {
	t.Helper()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, HostPort: 31996, BindAddress: "127.0.0.1",
		Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	release := createRuntimeCandidate(t, fixture, *run, lease, plan, "data-point")
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token, ReleaseRuntimeInput{
		ReleaseID: release.Release.ID, Kind: "container", RuntimeID: "data-point",
		Host: "127.0.0.1", Port: plan.HostPort, Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	owner := &orderingRuntimeOwner{running: map[string]bool{"data-point": true}, allowConcurrent: true}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner}
	if observer != nil {
		executor = executor.WithDataPoints(observer)
	}
	output := &recordingStepOutput{}
	result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: output}, mustExecutionPlan(t, fixture, *run))
	if result.State != StepPassed {
		t.Fatalf("activation = %#v", result)
	}
	points, err := fixture.runs.ReleaseDataPoints(context.Background(), release.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	return points[release.Release.ID], output.lines
}

// Recording at activation: a linked PostgreSQL with an archive is recorded
// at the activation itself, one with only dumps at its newest dump, and an
// environment with no linked database records nothing. None of it can stop
// the release going live.
func TestActivationRecordsTheDataPointOfEachLinkedDatabase(t *testing.T) {
	stub := &dataPointStub{facts: []DataPointFacts{
		{ConnectionID: 5, Name: "orders-db", Engine: "postgres", ArchiveID: 3, ArchiveFrom: dataPointMoment.Add(-24 * time.Hour)},
		{ConnectionID: 7, Name: "billing", Engine: "mysql", DumpRunID: 41, DumpJobID: 2, DumpAt: dataPointMoment.Add(-time.Hour)},
	}}
	points, lines := activateWithDataPoints(t, stub)
	if len(points) != 2 || len(stub.asked) != 1 {
		t.Fatalf("points = %+v, asked %v", points, stub.asked)
	}
	byConnection := map[int64]ReleaseDataPoint{}
	for _, point := range points {
		byConnection[point.ConnectionID] = point
	}
	pitr, dump := byConnection[5], byConnection[7]
	// The fixture's clock is the moment the release goes live.
	if pitr.Kind != DataPointPITR || pitr.At == nil || !pitr.At.Equal(dataPointMoment) || pitr.ArchiveID != 3 {
		t.Fatalf("the archived database's point = %+v", pitr)
	}
	if dump.Kind != DataPointDump || dump.BackupRunID != 41 || dump.BackupJobID != 2 {
		t.Fatalf("the dumped database's point = %+v", dump)
	}
	transcript := strings.Join(lines, "\n")
	if !strings.Contains(transcript, "Data point: orders-db") || !strings.Contains(transcript, "backup run 41") {
		t.Fatalf("transcript = %s", transcript)
	}

	none, _ := activateWithDataPoints(t, &dataPointStub{})
	if len(none) != 0 {
		t.Fatalf("an environment with no linked database recorded %+v", none)
	}
	unattached, _ := activateWithDataPoints(t, nil)
	if len(unattached) != 0 {
		t.Fatalf("an executor without the Databases owner recorded %+v", unattached)
	}
	failing, lines := activateWithDataPoints(t, &dataPointStub{err: errors.New("store unavailable")})
	if len(failing) != 0 || !strings.Contains(strings.Join(lines, "\n"), "could not be recorded") {
		t.Fatalf("a failed reading = %+v %v", failing, lines)
	}
}
