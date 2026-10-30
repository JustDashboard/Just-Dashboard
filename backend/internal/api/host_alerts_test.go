package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

var hostAlertClock = time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)

func TestHostAlertRulesTakeTheirKindsDefaultsAndRefuseTheRest(t *testing.T) {
	for kind, want := range map[string]hostAlertParams{
		hostAlertCPUHigh:       {Threshold: 90, Minutes: 10},
		hostAlertMemoryHigh:    {Threshold: 90, Minutes: 5},
		hostAlertDiskFilling:   {Threshold: 90},
		hostAlertContainerDown: {Minutes: 2},
		hostAlertBackupFailed:  {},
	} {
		// A figure a kind does not read is cleared rather than kept.
		in, err := validateHostAlert(hostAlertWrite{Kind: " " + strings.ToUpper(kind), Params: hostAlertParams{}, Channels: []int64{3, 3, -1, 2}})
		if err != nil || in.Kind != kind || in.Params != want || len(in.Channels) != 2 || in.Channels[0] != 2 {
			t.Fatalf("%s defaults: %+v, %v", kind, in, err)
		}
	}
	if in, err := validateHostAlert(hostAlertWrite{Kind: hostAlertBackupFailed, Params: hostAlertParams{Threshold: 50, Minutes: 9}}); err != nil || in.Params != (hostAlertParams{}) {
		t.Fatalf("backup rule kept figures it does not read: %+v, %v", in, err)
	}
	for _, bad := range []hostAlertWrite{
		{Kind: "load_high"},
		{Kind: hostAlertCPUHigh, Params: hostAlertParams{Threshold: 101}},
		{Kind: hostAlertCPUHigh, Params: hostAlertParams{Threshold: 5}},
		{Kind: hostAlertDiskFilling, Params: hostAlertParams{Threshold: 100}},
		{Kind: hostAlertMemoryHigh, Params: hostAlertParams{Minutes: 1441}},
		{Kind: hostAlertContainerDown, Params: hostAlertParams{Minutes: -1}},
	} {
		if _, err := validateHostAlert(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

// The whole of the behaviour: told once on the way over, once on the way
// back, never in between; a container must hold for its passes and minutes;
// a reading that cannot be taken keeps the subject where it was; one that
// comes back before it was told ends without a word.
func TestStepHostAlertTellsEachTransitionOnce(t *testing.T) {
	container := hostAlertRule{Kind: hostAlertContainerDown, Params: hostAlertParams{Minutes: 2}}
	now := hostAlertClock
	st := hostAlertState{Subject: "shop-api-1"}
	var tr hostAlertTransition
	expect := func(verdict hostAlertVerdict, want hostAlertTransition, step time.Duration) {
		t.Helper()
		now = now.Add(step)
		st, tr = stepHostAlert(container, st, verdict, "shop-api-1", "detail", now)
		if tr != want {
			t.Fatalf("at %s: transition %d, want %d (state %+v)", now.Format(time.Kitchen), tr, want, st)
		}
	}
	expect(hostAlertOver, hostAlertQuiet, 0)           // first pass: holding
	expect(hostAlertOver, hostAlertQuiet, time.Minute) // two passes, one minute: still holding
	expect(hostAlertUnknown, hostAlertQuiet, time.Minute)
	if st.FiringSince != hostAlertClock.Unix() || st.NotifiedAt != 0 {
		t.Fatalf("an unknown reading moved a holding subject: %+v", st)
	}
	expect(hostAlertOver, hostAlertFired, 0) // three passes, two minutes
	expect(hostAlertOver, hostAlertQuiet, time.Minute)
	expect(hostAlertUnknown, hostAlertQuiet, time.Minute)
	if st.NotifiedAt == 0 {
		t.Fatalf("an unknown reading recovered a firing subject: %+v", st)
	}
	expect(hostAlertClear, hostAlertRecovered, time.Minute)
	expect(hostAlertClear, hostAlertQuiet, time.Minute)
	if st.FiringSince != 0 || st.Seen != 0 {
		t.Fatalf("recovered state not cleared: %+v", st)
	}
	// Over once and back: never told, so nothing to end.
	expect(hostAlertOver, hostAlertQuiet, time.Minute)
	expect(hostAlertClear, hostAlertQuiet, time.Minute)

	// CPU's minutes are its window, which the reading already averaged, so
	// one pass over the line is the alert.
	cpu := hostAlertRule{Kind: hostAlertCPUHigh, Params: hostAlertParams{Threshold: 90, Minutes: 10}}
	if _, tr := stepHostAlert(cpu, hostAlertState{Subject: "cpu"}, hostAlertOver, "web-01", "97%", now); tr != hostAlertFired {
		t.Fatalf("a CPU window over the line was not told at once: %d", tr)
	}
	disk := hostAlertRule{Kind: hostAlertDiskFilling, Params: hostAlertParams{Threshold: 90}}
	first, tr := stepHostAlert(disk, hostAlertState{Subject: "/"}, hostAlertOver, "/", "93%", now)
	if tr != hostAlertQuiet {
		t.Fatal("a disk was told on one reading")
	}
	if _, tr := stepHostAlert(disk, first, hostAlertOver, "/", "93%", now.Add(time.Minute)); tr != hostAlertFired {
		t.Fatal("a disk over the line on two passes was not told")
	}
}

func TestUsageReadingAveragesTheRecordedWindow(t *testing.T) {
	rule := hostAlertRule{Kind: hostAlertCPUHigh, Params: hostAlertParams{Threshold: 90, Minutes: 10}}
	now := hostAlertClock
	window := func(mean, peak float64) hostWindow {
		return hostWindow{Samples: 40, Mean: mean, Peak: peak, First: now.Add(-10*time.Minute + 15*time.Second), Last: now.Add(-5 * time.Second)}
	}
	r := usageAlertReading(rule, window(94, 100), 15*time.Second, "web-01", now)
	if !r.Judged || len(r.Firing) != 1 || r.Firing[0].Subject != "cpu" || r.Firing[0].Label != "web-01" ||
		r.Firing[0].Detail != "CPU averaged 94% over the last 10 min (peak 100%); the line is 90%." {
		t.Fatalf("held high: %+v", r)
	}
	// A spike inside a quiet window is a high peak, not a high mean.
	if r := usageAlertReading(rule, window(41, 100), 15*time.Second, "web-01", now); len(r.Firing) != 0 ||
		r.Now["cpu"] != "CPU averaged 41% over the last 10 min (peak 100%)." || r.Labels["cpu"] != "web-01" {
		t.Fatalf("a spike: %+v", r)
	}
	// Between the line and five points under it, it stays where it was.
	if r := usageAlertReading(rule, window(87, 95), 15*time.Second, "web-01", now); !r.Unknown["cpu"] || len(r.Now) != 0 {
		t.Fatalf("hysteresis band: %+v", r)
	}
	memory := hostAlertRule{Kind: hostAlertMemoryHigh, Params: hostAlertParams{Threshold: 80, Minutes: 10}}
	if r := usageAlertReading(memory, window(85, 90), 15*time.Second, "", now); len(r.Firing) != 1 ||
		r.Firing[0].Subject != "memory" || r.Firing[0].Label != "this server" || !strings.HasPrefix(r.Firing[0].Detail, "Memory averaged 85%") {
		t.Fatalf("memory: %+v", r)
	}
	// Three minutes recorded is not ten minutes held, and a recorder that
	// stopped is not a quiet machine.
	short := window(99, 99)
	short.First = now.Add(-3 * time.Minute)
	if r := usageAlertReading(rule, short, 15*time.Second, "web-01", now); r.Judged || !strings.Contains(r.Why, "Only 3 of the 10 minutes") {
		t.Fatalf("short window: %+v", r)
	}
	stale := window(99, 99)
	stale.Last = now.Add(-5 * time.Minute)
	if r := usageAlertReading(rule, stale, 15*time.Second, "web-01", now); r.Judged {
		t.Fatalf("stale record judged: %+v", r)
	}
}

func TestDiskReadingJudgesSpaceAndInodesPerMount(t *testing.T) {
	rule := hostAlertRule{Kind: hostAlertDiskFilling, Params: hostAlertParams{Threshold: 90}}
	now := hostAlertClock
	gb := int64(1 << 30)
	mounts := []hostMount{
		{Mountpoint: "/", UsedPercent: 93, Used: 186 * gb, Total: 200 * gb, InodesPercent: 12},
		{Mountpoint: "/srv", UsedPercent: 40, Used: 40 * gb, Total: 100 * gb, InodesPercent: 97},
		{Mountpoint: "/data", UsedPercent: 87, Used: 87 * gb, Total: 100 * gb},
		{Mountpoint: "/boot", UsedPercent: 20, Used: gb, Total: 5 * gb},
	}
	r := diskAlertReading(rule, mounts, now.Add(-10*time.Second), 15*time.Second, now)
	if len(r.Firing) != 2 || r.Firing[0].Detail != "93% of / is used (186.0 GB of 200.0 GB); the line is 90%." ||
		r.Firing[1].Subject != "/srv" || r.Firing[1].Detail != "97% of its inodes are used; the line is 90%." {
		t.Fatalf("firing mounts: %+v", r.Firing)
	}
	if !r.Unknown["/data"] || r.Now["/boot"] != "20% of /boot is used (1.0 GB of 5.0 GB)." {
		t.Fatalf("band and quiet mounts: %+v", r)
	}
	if r := diskAlertReading(rule, mounts, now.Add(-10*time.Minute), 15*time.Second, now); r.Judged {
		t.Fatal("a filesystem sample ten minutes old was judged")
	}
}

func TestContainerReadingTellsACrashFromAStop(t *testing.T) {
	now := hostAlertClock
	rule := hostAlertRule{Kind: hostAlertContainerDown, Params: hostAlertParams{Minutes: 2}, watchFrom: now.Add(-time.Hour)}
	stopped := func(code int, mutate func(*hostContainerExit)) *hostContainerExit {
		exit := &hostContainerExit{ContainerExit: dockerx.ContainerExit{ExitCode: code, StartedAt: now.Add(-50 * time.Minute), FinishedAt: now.Add(-time.Minute)}, Witnessed: true}
		if mutate != nil {
			mutate(exit)
		}
		return exit
	}
	list := []hostContainer{
		{Name: "web", State: "running", Health: "healthy"},
		{Name: "api", State: "running", Health: "unhealthy"},
		{Name: "worker", State: "running", RecentRestarts: 4},
		{Name: "cron", State: "running", RecentRestarts: 1},
		{Name: "queue", State: "restarting"},
		{Name: "backup-frozen", State: "paused"},
		{Name: "crashed", State: "exited", Exit: stopped(1, nil)},
		{Name: "oom", State: "exited", Exit: stopped(137, func(e *hostContainerExit) { e.OOMKilled = true })},
		{Name: "killed", State: "exited", Exit: stopped(137, nil)},
		{Name: "killed-unseen", State: "exited", Exit: stopped(137, func(e *hostContainerExit) { e.Witnessed = false })},
		{Name: "stopped", State: "exited", Exit: stopped(1, func(e *hostContainerExit) { e.Requested = true })},
		{Name: "sigterm", State: "exited", Exit: stopped(143, nil)},
		{Name: "finished", State: "exited", Exit: stopped(0, nil)},
		{Name: "long-gone", State: "exited", Exit: stopped(1, func(e *hostContainerExit) { e.FinishedAt = now.Add(-2 * time.Hour) })},
		{Name: "zombie", State: "dead", Exit: stopped(0, func(e *hostContainerExit) { e.Error = "device busy" })},
		{Name: "unread", State: "exited"},
		{Name: "migrate", State: "exited", Exit: stopped(1, nil), Labels: map[string]string{"io.just-dashboard.release-task": "migrate"}},
		{Name: "shop-run-1", State: "exited", Exit: stopped(1, nil), Labels: map[string]string{"com.docker.compose.oneoff": "True"}},
		{Name: "jd-e4-run9-rehearsal-db", State: "exited", Exit: stopped(137, nil), Labels: map[string]string{"io.just-dashboard.rehearsal": "9"}},
		{Name: "fresh", State: "created"},
	}
	r := containerAlertReading(rule, list, true)
	firing := map[string]string{}
	for _, f := range r.Firing {
		firing[f.Subject] = f.Detail
	}
	for _, name := range []string{"api", "worker", "queue", "crashed", "oom", "killed", "zombie"} {
		if firing[name] == "" {
			t.Errorf("%s is down and was not found firing", name)
		}
	}
	if len(firing) != 7 {
		t.Errorf("firing %v", firing)
	}
	for _, name := range []string{"web", "backup-frozen", "stopped", "sigterm", "finished", "killed-unseen"} {
		if r.Now[name] == "" {
			t.Errorf("%s is fine and was not found quiet: %+v", name, r.Now)
		}
	}
	for _, name := range []string{"cron", "unread"} {
		if !r.Unknown[name] {
			t.Errorf("%s cannot be judged yet and was judged", name)
		}
	}
	for _, name := range []string{"long-gone", "migrate", "shop-run-1", "fresh"} {
		if _, ok := r.Now[name]; ok || r.Unknown[name] || firing[name] != "" {
			t.Errorf("%s is not a subject of this rule", name)
		}
	}
	if !strings.HasPrefix(firing["crashed"], "It exited with code 1 at 2026-10-25 11:59 UTC.") ||
		!strings.Contains(firing["oom"], "out of memory") || !strings.Contains(firing["zombie"], "device busy") ||
		!strings.Contains(r.Now["stopped"], "stopped on purpose") {
		t.Errorf("details: %v / %v", firing, r.Now)
	}
	if r := containerAlertReading(rule, nil, false); r.Judged || r.Why == "" {
		t.Fatalf("Docker unreachable was judged: %+v", r)
	}
}

func TestRestartMarksCountOnlyTheWindow(t *testing.T) {
	a := &hostAlerts{restarts: map[string][]hostRestartMark{}}
	now := hostAlertClock
	if n := a.noteRestarts("c1", 5, now); n != 0 {
		t.Fatalf("a first sight counted %d restarts", n)
	}
	now = now.Add(time.Minute)
	if n := a.noteRestarts("c1", 7, now); n != 2 {
		t.Fatalf("two restarts read as %d", n)
	}
	now = now.Add(time.Minute)
	if n := a.noteRestarts("c1", 9, now); n != 4 {
		t.Fatalf("four restarts read as %d", n)
	}
	// Quiet for longer than the window: the old marks age out.
	now = now.Add(hostRestartWindow + time.Minute)
	if n := a.noteRestarts("c1", 9, now); n != 0 {
		t.Fatalf("a quiet window still counts %d restarts", n)
	}
}

func TestBackupReadingCoversEveryJobNotLeftToTheDatabaseRules(t *testing.T) {
	now := hostAlertClock
	failed := &dbAlertRun{ID: 1, Status: "failed", StartedAt: now.Add(-time.Hour), Reason: "upload refused"}
	ok := &dbAlertRun{ID: 2, Status: "success", StartedAt: now.Add(-time.Hour)}
	shop := int64(4)
	jobs := []dbAlertJob{
		{ID: 1, Name: "files", Last: failed},
		{ID: 2, Name: "volumes", Last: ok},
		{ID: 3, Name: "shop dump", Databases: []int64{shop}, Last: failed},
		{ID: 4, Name: "new"},
	}
	r := backupAlertReading(jobs, nil, now)
	if len(r.Firing) != 2 || r.Firing[0].Subject != "job:1" || r.Firing[0].Label != "files" ||
		!strings.HasSuffix(r.Firing[0].Detail, "failed: upload refused") || r.Now["job:2"] == "" || r.Now["job:4"] == "" {
		t.Fatalf("every job, no database rule: %+v", r)
	}
	// A database rule for the shop follows its dump job; the host rule
	// leaves that job to it and keeps the files.
	leftToDB := func(job dbAlertJob) bool { return job.dumps(&shop) }
	r = backupAlertReading(jobs, leftToDB, now)
	if len(r.Firing) != 1 || r.Firing[0].Subject != "job:1" || !strings.Contains(r.Now["job:3"], "database alert rule") {
		t.Fatalf("dump job left to the database rule: %+v", r)
	}
}

// The readings that are queries: the recorder's samples and filesystems,
// every backup job with its runs, and which jobs a database rule follows.
func TestHostAlertSourcesReadTheStore(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	db := s.Store.DB
	now := hostAlertClock
	for i := 0; i < 40; i++ {
		at := now.Add(-time.Duration(i) * 15 * time.Second).Unix()
		if _, err := db.Exec(`INSERT INTO metric_samples(ts, cpu_percent, mem_percent) VALUES(?, ?, 50)`, at, 90+float64(i%3)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO metric_container_samples(ts, name, cpu_percent, mem_bytes) VALUES(?, 'api', 180, 1073741824)`, at); err != nil {
			t.Fatal(err)
		}
	}
	w, err := readHostWindow(ctx, db, "cpu_percent", now.Add(-10*time.Minute), now)
	if err != nil || w.Samples != 40 || w.Peak != 92 || w.Mean < 90.9 || w.Mean > 91.1 || !w.Last.Equal(now) {
		t.Fatalf("window: %+v, %v", w, err)
	}
	if _, err := readHostWindow(ctx, db, "uptime_seconds; DROP TABLE users", now, now); err == nil {
		t.Fatal("a column outside the two was read")
	}
	if got := busiestContainer(ctx, db, hostAlertCPUHigh, now.Add(-10*time.Minute), now); got != " The busiest container was api, 1.8 cores on average." {
		t.Fatalf("busiest: %q", got)
	}
	if got := busiestContainer(ctx, db, hostAlertMemoryHigh, now.Add(-10*time.Minute), now); got != " The largest container was api, 1.0 GB on average." {
		t.Fatalf("largest: %q", got)
	}
	for _, row := range []struct {
		ts    int64
		mount string
		used  float64
	}{{now.Unix() - 15, "/", 50}, {now.Unix(), "/", 93}, {now.Unix(), "/srv", 10}} {
		if _, err := db.Exec(`INSERT INTO metric_mount_samples(ts, mountpoint, used_percent, used_bytes, total_bytes, inodes_percent) VALUES(?,?,?,1,2,3)`, row.ts, row.mount, row.used); err != nil {
			t.Fatal(err)
		}
	}
	mounts, at, err := readHostMounts(ctx, db)
	if err != nil || !at.Equal(now) || len(mounts) != 2 || mounts[0].Mountpoint != "/" || mounts[0].UsedPercent != 93 || mounts[0].InodesPercent != 3 {
		t.Fatalf("mounts: %+v at %s, %v", mounts, at, err)
	}

	for _, job := range []struct{ name, dumps string }{{"files", "[]"}, {"shop", "[7]"}} {
		if _, err := db.Exec(`INSERT INTO backup_jobs(name, sources, target_kind, database_dumps, created_at) VALUES(?, '["/srv"]', 'local', ?, 1)`, job.name, job.dumps); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO backup_runs(job_id, started_at, status, log) VALUES(1, 10, 'failed', 'FAILED: disk full')`); err != nil {
		t.Fatal(err)
	}
	jobs, err := readAlertBackupJobs(ctx, db)
	if err != nil || len(jobs) != 2 || jobs[0].Last == nil || jobs[0].Last.Reason != "disk full" || len(jobs[1].Databases) != 1 {
		t.Fatalf("every job, files included: %+v, %v", jobs, err)
	}
	if dbJobs, err := s.modules.dbAlerts.backupJobs(ctx); err != nil || len(dbJobs) != 1 || dbJobs[0].Name != "shop" {
		t.Fatalf("the database alerts still read only dump jobs: %+v, %v", dbJobs, err)
	}
	left, err := databaseBackupRules(ctx, db)
	if err != nil || left(jobs[0]) || left(jobs[1]) {
		t.Fatalf("no database rule follows a job yet: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO db_alert_rules(kind, created_at, updated_at) VALUES(?, 1, 1)`, dbx.AlertBackupFailed); err != nil {
		t.Fatal(err)
	}
	if left, err = databaseBackupRules(ctx, db); err != nil || left(jobs[0]) || !left(jobs[1]) {
		t.Fatalf("a rule over every database follows the dump job only: %v", err)
	}
}
