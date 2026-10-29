package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

const siteActor = "account:ion@example.com"

// fakeEngine is a Docker daemon that knows two containers and records what it
// is asked to do to them.
type fakeEngine struct {
	mu       sync.Mutex
	requests []string
}

func (e *fakeEngine) seen() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.requests...)
}

// listed is one container the fake daemon reports.
type listed struct {
	ID, Name, Project, Service, WorkingDir, State string
	Mounts                                        []string
}

func listJSON(items []listed) string {
	var b strings.Builder
	b.WriteString("[")
	for i, it := range items {
		if i > 0 {
			b.WriteString(",")
		}
		state := it.State
		if state == "" {
			state = "running"
		}
		labels := map[string]string{}
		if it.Project != "" {
			labels["com.docker.compose.project"] = it.Project
			labels["com.docker.compose.service"] = it.Service
			labels["com.docker.compose.project.working_dir"] = it.WorkingDir
		}
		rawLabels, _ := json.Marshal(labels)
		mounts := []string{}
		for _, m := range it.Mounts {
			mounts = append(mounts, fmt.Sprintf(`{"Type":"bind","Source":%q,"Destination":%q,"Mode":"","RW":true}`, m, m))
		}
		fmt.Fprintf(&b, `{"Id":%q,"Names":["/%s"],"Image":"img:1","ImageID":"sha256:x","Command":"","Created":1,"Ports":[],"Labels":%s,"State":%q,"Status":"Up","Mounts":[%s]}`,
			it.ID, it.Name, rawLabels, state, strings.Join(mounts, ","))
	}
	b.WriteString("]")
	return b.String()
}

func engineWith(t *testing.T, s *Server) *fakeEngine {
	t.Helper()
	return engineListing(t, s, nil)
}

func engineListing(t *testing.T, s *Server, items []listed) *fakeEngine {
	t.Helper()
	fake := &fakeEngine{}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if i := strings.Index(path, "/containers/"); i >= 0 {
			path = path[i:]
		}
		switch {
		case path == "/_ping":
			w.Header().Set("API-Version", "1.47")
		case r.Method == http.MethodGet && path == "/containers/json":
			_, _ = w.Write([]byte(listJSON(items)))
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/json") && strings.HasPrefix(path, "/containers/"):
			name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
			_, _ = fmt.Fprintf(w, `{"Id":"%s0123456789","Name":"/%s","Image":"sha256:abc","Config":{"Image":"nginx:1"},"State":{"Status":"running","Running":true},"HostConfig":{},"NetworkSettings":{"Networks":{}},"Mounts":[]}`, name, name)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = w.Write([]byte(`{"Id":"sha256:x","RepoDigests":[],"RepoTags":[]}`))
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/containers/"):
			fake.mu.Lock()
			fake.requests = append(fake.requests, strings.TrimPrefix(path, "/containers/"))
			fake.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	return fake
}

func TestContainerCommandsTakeTheContainerButtonsPath(t *testing.T) {
	s := testServer(t)
	engine := engineWith(t, s)
	h := newAccountHost(s)
	for action, want := range map[string]string{"start": "web/start", "stop": "web/stop", "restart": "web/restart"} {
		out, err := h.Container(t.Context(), siteActor, "web", action)
		if err != nil || !strings.HasPrefix(out, "web ") {
			t.Fatalf("%s = %q, %v", action, out, err)
		}
		found := false
		for _, r := range engine.seen() {
			found = found || r == want
		}
		if !found {
			t.Fatalf("%s never reached the daemon: %v", action, engine.seen())
		}
	}
	if _, err := h.Container(t.Context(), siteActor, "web", "kill"); err == nil {
		t.Fatal("kill is not a command a linked account may send")
	}
	if _, err := h.Container(t.Context(), siteActor, "web", "exec"); err == nil {
		t.Fatal("exec is not a command a linked account may send")
	}
}

func TestStopAndRestartFromTheSiteSpendTheDestructiveBudgetStartDoesNot(t *testing.T) {
	s := testServer(t)
	engineWith(t, s)
	h := newAccountHost(s)
	var refused error
	for i := 0; i < 40 && refused == nil; i++ {
		_, refused = h.Container(t.Context(), siteActor, "web", "restart")
	}
	if refused == nil || !strings.Contains(refused.Error(), "too many destructive actions") {
		t.Fatalf("the budget never ran out: %v", refused)
	}
	if _, err := h.Container(t.Context(), siteActor, "web", "start"); err != nil {
		t.Fatalf("start is not in the destructive budget: %v", err)
	}
	// The budget is per actor: a person's clicks and another account's are not
	// the same bucket.
	if _, err := h.Container(t.Context(), "account:other@example.com", "web", "restart"); err != nil {
		t.Fatalf("another actor shares the budget: %v", err)
	}
}

func TestAContainerThatDoesNotExistIsReportedAsDockerReportsIt(t *testing.T) {
	s := testServer(t)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such container: ghost"}`))
	}))
	defer engine.Close()
	s.modules.docker = dockerx.New(engine.URL)
	defer s.modules.docker.Close()
	_, err := newAccountHost(s).Container(t.Context(), siteActor, "ghost", "start")
	if err == nil || !strings.Contains(err.Error(), "Docker does not have that object") {
		t.Fatalf("error = %v", err)
	}
}

func composeStackOn(t *testing.T, s *Server, script string) (stackDir, marker string) {
	t.Helper()
	root := t.TempDir()
	stackDir = filepath.Join(root, "shop")
	if err := os.Mkdir(stackDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yml"), []byte("services:\n  web:\n    image: nginx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Cfg.ComposeRoots = []string{root}
	_, marker = fakeComposeCLI(t, script)
	return stackDir, marker
}

// fakeComposeCLI puts a `docker` on the path that records how it was run.
func fakeComposeCLI(t *testing.T, script string) (bin, marker string) {
	t.Helper()
	bin = t.TempDir()
	marker = filepath.Join(bin, "calls")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + marker + "\"\n" + script
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return bin, marker
}

func TestStackRestartRunsComposeRestartAndRecordsWhoAskedWithoutATypedPhrase(t *testing.T) {
	s := testServer(t)
	engineWith(t, s)
	stackDir, marker := composeStackOn(t, s, "echo 'SECRET-from-compose-output'\nexit 0\n")
	out, err := newAccountHost(s).RestartStack(t.Context(), siteActor, "shop")
	if err != nil || out != "restarted shop" {
		t.Fatalf("restart = %q, %v", out, err)
	}
	calls, _ := os.ReadFile(marker)
	if !strings.Contains(string(calls), "compose --project-directory "+stackDir) || !strings.Contains(string(calls), "restart") {
		t.Fatalf("docker was run as %q", calls)
	}
	if strings.Contains(out, "SECRET") {
		t.Fatal("compose's output went into the result")
	}
	var actor, action string
	if err := s.Store.DB.QueryRow(`SELECT actor, action FROM docker_stack_deployments WHERE project = 'shop'`).Scan(&actor, &action); err != nil {
		t.Fatalf("the stack's previous state was not written down: %v", err)
	}
	if actor != siteActor || action != "restart" {
		t.Fatalf("history says %q %q", actor, action)
	}
}

func TestAStackThatFailsToRestartIsAFailureWithoutComposesOutput(t *testing.T) {
	s := testServer(t)
	engineWith(t, s)
	composeStackOn(t, s, "echo 'SECRET-from-compose-output' >&2\nexit 3\n")
	_, err := newAccountHost(s).RestartStack(t.Context(), siteActor, "shop")
	if err == nil || !strings.Contains(err.Error(), "exited with code 3") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error = %v", err)
	}
	if _, err := newAccountHost(s).RestartStack(t.Context(), siteActor, "no-such-stack"); err == nil {
		t.Fatal("a stack that does not exist was restarted")
	}
}

func TestTheSharedStackPathStillRefusesDownWithoutAConfirmation(t *testing.T) {
	s := testServer(t)
	engineWith(t, s)
	_, marker := composeStackOn(t, s, "exit 0\n")
	// A caller with no way to ask for the phrase cannot take a stack down.
	if _, _, err := s.runStackAction(t.Context(), siteActor, "shop", dockerx.ComposeDown, "", nil); err == nil || !strings.Contains(err.Error(), "typed") {
		t.Fatalf("down without a confirmation = %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("docker ran")
	}
	asked := false
	if _, _, err := s.runStackAction(t.Context(), siteActor, "shop", dockerx.ComposeDown, "", func() error { asked = true; return errors.New("not typed") }); err == nil || !asked {
		t.Fatalf("down with a refused confirmation = %v (asked %v)", err, asked)
	}
	if composeNeedsPhrase(dockerx.ComposeRestart) {
		t.Fatal("restart started to need a phrase, which changes what the stack page asks too")
	}
}

func TestBackupRunFromTheSiteStartsTheJobAndSaysItCameFromTheAccount(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "backup-admin", auth.RoleAdmin)}
	job, _ := fileBackupJob(t, s, admin, "nightly", nil)
	out, err := newAccountHost(s).RunBackup(t.Context(), siteActor, job.ID)
	if err != nil || out != "started backup job nightly" {
		t.Fatalf("run = %q, %v", out, err)
	}
	runs := settledBackupRuns(t, s, job.ID, 1)
	if runs[0].Status != backups.StatusSuccess || runs[0].Trigger != "account_link" {
		t.Fatalf("runs = %+v", runs)
	}
	if _, err := newAccountHost(s).RunBackup(t.Context(), siteActor, 9999); err == nil {
		t.Fatal("a job that does not exist was run")
	}

	// The Run now button is unchanged: the same path, recorded as manual.
	w := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/run", job.ID), "", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run now = %d %s", w.Code, w.Body)
	}
	if runs = settledBackupRuns(t, s, job.ID, 2); runs[0].Trigger != "manual" {
		t.Fatalf("runs after the button = %+v", runs)
	}
}

// settledBackupRuns waits for a job's runs to number want with the newest
// finished, and for the runner to have let the job go. The run's row says
// success before the runner has pruned and released the job, so a Run now
// pressed on the row alone could be refused as already running, and a test
// that ended there would close the store and remove the job's directories
// under a run still working in them. Each wait has its own deadline: under
// load a run's archive and prune take what they take, and the second wait is
// not owed what the first one spent.
func settledBackupRuns(t *testing.T, s *Server, jobID int64, want int) []*backups.Run {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		runs, err := s.modules.backupStore.Runs(t.Context(), jobID, 5)
		if err == nil && len(runs) == want && runs[0].Status != backups.StatusRunning &&
			!s.modules.backupRunner.IsRunning(jobID) {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %d finished runs: %+v (error %v, runner holds the job: %v)",
				want, runs, err, s.modules.backupRunner.IsRunning(jobID))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestUpdateInstallFromTheSiteIsRefusedWhenItCannotOrShouldNotRun(t *testing.T) {
	s := testServer(t)
	h := newAccountHost(s)

	// A test host has no compose project to update from.
	if _, err := h.InstallUpdate(t.Context(), siteActor, "0.8.3"); err == nil || !strings.Contains(err.Error(), "cannot update itself") {
		t.Fatalf("install on an install that cannot update = %v", err)
	}

	// A checkout the dashboard could update from, and a site that says 0.9.1 is newest.
	checkout := t.TempDir()
	for name, body := range map[string]string{"docker-compose.yml": "services: {}\n", ".git": "gitdir: x\n"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"releases":[
			{"version":"0.9.1","date":"2026-10-01","title":"Next","summary":"s","changes":[{"kind":"added","text":"x"}]},
			{"version":"0.8.2","date":"2026-09-01","title":"Now","summary":"s","changes":[{"kind":"added","text":"y"}]}]}`))
	}))
	defer manifest.Close()
	s.modules.selfUpdate = selfupdate.New(selfupdate.Options{
		Current: "0.8.2", DataDir: t.TempDir(), UpdateDir: checkout, Repo: "o/r", Ref: "main", CheckOnline: true,
		BaseURL: manifest.URL, List: func(context.Context) ([]selfupdate.Sibling, error) { return nil, nil },
	})
	if rep := s.modules.selfUpdate.Report(t.Context(), selfupdate.Forced); !rep.Available || rep.Latest != "0.9.1" || !rep.Install.Supported {
		t.Fatalf("fixture does not offer an update: %+v", rep)
	}

	_, err := h.InstallUpdate(t.Context(), siteActor, "0.8.3")
	if !errors.Is(err, accountlink.ErrVersionMoved) || !strings.Contains(err.Error(), "newest version is now 0.9.1, not 0.8.3") {
		t.Fatalf("an update for a version that is not the newest = %v", err)
	}
	st, err := h.UpdateStatus(t.Context())
	if err != nil || st.Version != "0.8.2" || st.Latest != "0.9.1" || !st.Available || !st.Supported || st.Busy {
		t.Fatalf("update status = %+v, %v", st, err)
	}
}

func TestRestartFromTheSiteIsRefusedWhereTheDashboardCannotRestartItself(t *testing.T) {
	s := testServer(t)
	_, err := newAccountHost(s).RestartDashboard(t.Context(), siteActor, false)
	if err == nil {
		t.Fatal("a restart was started on an install with no compose project")
	}
}

// ---- what a heartbeat is made of -------------------------------------------

func TestCollectReadsWhatTheMachineHasAndLeavesOutWhatItCannotRead(t *testing.T) {
	s := testServer(t)
	// A daemon that does not answer: containers and stacks are left out.
	s.modules.docker = dockerx.New("tcp://127.0.0.1:1")
	defer s.modules.docker.Close()

	snap, err := newAccountHost(s).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Server.Name == "" || snap.Server.Version == "" || snap.Server.Arch == "" {
		t.Fatalf("server = %+v", snap.Server)
	}
	if snap.Metrics == nil || snap.Metrics.MemTotal == 0 || snap.Metrics.DiskMount == "" {
		t.Fatalf("metrics = %+v", snap.Metrics)
	}
	if snap.Access == nil || snap.Access.Kind != "tunnel" {
		t.Fatalf("access = %+v", snap.Access)
	}
	if snap.Containers != nil || snap.Stacks != nil {
		t.Fatalf("a daemon that did not answer produced containers %+v stacks %+v", snap.Containers, snap.Stacks)
	}
	if snap.Alerts == nil || len(snap.Alerts) != 0 || snap.Backups == nil || snap.Backups.Jobs != 0 || snap.Deployments == nil || snap.Deployments.Projects != 0 {
		t.Fatalf("the sections kept in the store: alerts %v backups %+v deployments %+v", snap.Alerts, snap.Backups, snap.Deployments)
	}
	raw, _ := json.Marshal(snap)
	for _, forbidden := range []string{"JD_MASTER_KEY", "ALLOWED_CIDRS", s.Cfg.DataDir, "password"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("snapshot contains %q: %s", forbidden, raw)
		}
	}
}

func TestAccessIsAnSSHTunnelOnALoopbackSiteAndATailnetOtherwise(t *testing.T) {
	s := testServer(t)
	h := newAccountHost(s)
	for site, want := range map[string]string{"": "tunnel", "localhost": "tunnel", "127.0.0.1": "tunnel", "box.tail1234.ts.net": "tailscale", "100.101.102.103": "tailscale"} {
		s.Cfg.Site = site
		got := h.access(t.Context())
		if got.Kind != want {
			t.Errorf("site %q = %s, want %s", site, got.Kind, want)
		}
		if want == "tunnel" && got.Tailscale != nil {
			t.Errorf("a tunnel install told the site about its tailnet: %+v", got.Tailscale)
		}
	}
}

func TestContainerCountsListAndHealth(t *testing.T) {
	info := containersInfo([]dockerx.Container{
		{ID: "aaaaaaaaaaaaaaaa", Name: "web", Image: "nginx", State: "running", Health: "healthy"},
		{ID: "bbbbbbbbbbbbbbbb", Name: "api", Image: "app", State: "running", Health: "unhealthy"},
		{ID: "cccccccccccccccc", Name: "old", Image: "x", State: "exited"},
		{ID: "dddddddddddddddd", Name: "new", Image: "y", State: "created"},
	}, "")
	if info.Running != 2 || info.Stopped != 2 || info.Unhealthy != 1 || len(info.List) != 4 || info.List[0].ID != "aaaaaaaaaaaa" || info.List[1].Health != "unhealthy" {
		t.Fatalf("containers = %+v", info)
	}
	if empty := containersInfo(nil, ""); empty.List == nil || len(empty.List) != 0 {
		t.Fatalf("no containers should be an empty list: %+v", empty)
	}
}

func TestOnlyAlertsFiringAndNotMutedAreReported(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-time.Hour)
	got := alertsInfo([]hostAlertSubject{
		{Kind: "disk_filling", Subject: "/var", Detail: "91% of /var is used (1.0 GiB of 2.0 GiB); the line is 90%.", State: "firing", Since: &earlier},
		{Kind: "cpu_high", Subject: "cpu", State: "pending", Since: &earlier},
		{Kind: "container_down", Subject: "web", State: "firing", Since: &earlier, Muted: true},
		{Kind: "backup_failed", Subject: "job:1", State: "quiet", Muted: true},
		{Kind: "memory_high", Subject: "memory", Detail: "Memory averaged 93% over the last 5 min (peak 97%); the line is 90%.", State: "firing", Since: &now},
	})
	if len(got) != 2 || got[0].Kind != "disk_filling" || got[0].Since != earlier.Unix() || got[0].Reading != "91%" ||
		got[1].Kind != "memory_high" || got[1].Reading != "93%" {
		t.Fatalf("alerts = %+v", got)
	}
}

func TestBackupAndDeploymentSummaries(t *testing.T) {
	t0 := time.Unix(1761000000, 0)
	t1 := t0.Add(time.Hour)
	t2 := t0.Add(2 * time.Hour)
	b := backupsInfo([]*backups.Job{
		{ID: 1, Name: "db", LastRun: &backups.Run{Status: backups.StatusSuccess, StartedAt: t0, EndedAt: &t1}},
		{ID: 2, Name: "files", LastRun: &backups.Run{Status: backups.StatusFailed, StartedAt: t2}},
		{ID: 3, Name: "new"},
	})
	if b.Jobs != 3 || b.Failed != 1 || b.LastRun == nil || b.LastRun.Status != "failed" || b.LastRun.At != t2.Unix() ||
		len(b.List) != 3 || b.List[0].LastAt != t1.Unix() || b.List[2].LastStatus != "" {
		t.Fatalf("backups = %+v", b)
	}
	archived := t2
	d := deploymentsInfo([]*deploy.Project{
		{Name: "shop", LastRun: &deploy.Run{Status: deploy.StatusSuccess, StartedAt: t0, EndedAt: &t1}},
		{Name: "blog", LastRun: &deploy.Run{Status: deploy.StatusFailed, StartedAt: t1}},
		{Name: "gone", ArchivedAt: &archived, LastRun: &deploy.Run{Status: deploy.StatusSuccess, StartedAt: t2}},
		{Name: "idle"},
	})
	if d.Projects != 3 || d.LastRun == nil || d.LastRun.Project != "shop" || d.LastRun.At != t1.Unix() {
		t.Fatalf("deployments = %+v", d)
	}
}

func TestMetricsComeFromTheFullestMountAndTheTotals(t *testing.T) {
	info := metricsInfo(&sysinfo.Snapshot{
		CPU:    sysinfo.CPUStats{TotalPercent: 23.44, LoadAvg1: 0.8, Modes: sysinfo.CPUModes{Steal: 0.23}},
		Memory: sysinfo.MemoryStats{UsedPercent: 61.04, Used: 4, Total: 8},
		Mounts: []sysinfo.MountStats{{Mountpoint: "/boot", UsedPercent: 12}, {Mountpoint: "/var", UsedPercent: 91.2}, {Mountpoint: "/", UsedPercent: 71.2}},
		Net:    []sysinfo.NetStats{{RecvRate: 100, SendRate: 50}, {RecvRate: 1100.55, SendRate: 750.1}},
	})
	if info.CPUPercent != 23.4 || info.MemPercent != 61 || info.DiskPercent != 91.2 || info.DiskMount != "/var" ||
		info.NetRx != 1200.6 || info.NetTx != 800.1 || info.CPUSteal != 0.2 || info.MemTotal != 8 {
		t.Fatalf("metrics = %+v", info)
	}
}

func TestAFinishedUpdateRunIsReportedForTenMinutesOnly(t *testing.T) {
	now := time.Now()
	recent, old := now.Add(-time.Minute), now.Add(-time.Hour)
	if got := updateRunInfo(&selfupdate.Run{Status: selfupdate.StatusRunning, Phase: selfupdate.PhaseBuilding, ToVersion: "0.8.3"}, now); got == nil || got.Status != "running" || got.Phase != "building" || got.ToVersion != "0.8.3" {
		t.Fatalf("running = %+v", got)
	}
	if got := updateRunInfo(&selfupdate.Run{Status: selfupdate.StatusSuccess, FinishedAt: &recent}, now); got == nil || got.Status != "success" {
		t.Fatalf("just finished = %+v", got)
	}
	if got := updateRunInfo(&selfupdate.Run{Status: selfupdate.StatusSuccess, FinishedAt: &old}, now); got != nil {
		t.Fatalf("finished an hour ago = %+v", got)
	}
	if updateRunInfo(nil, now) != nil {
		t.Fatal("no run should report nothing")
	}
}

// ---- what leaves, and what a site may not touch ------------------------------

// realAlerts runs each kind of host alert's own producer, as the evaluator
// does, and stores what it found as the evaluator stores it. The messages are
// the real ones, with the things a message really carries: the busiest
// container's name, a path in an error, a line of a backup's log.
func realAlerts(t *testing.T, s *Server) (secrets []string) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	from := now.Add(-10 * time.Minute)
	secrets = []string{"shop-secret-db", "/srv/shop/secret.env", "/srv/secret/dump.sql", "permission denied", "Docker said", "busiest", "largest", "OCI runtime"}
	if _, err := s.Store.DB.Exec(`INSERT INTO metric_container_samples(ts, name, cpu_percent, mem_bytes) VALUES(?, 'shop-secret-db', 180, ?)`,
		now.Add(-time.Minute).Unix(), int64(3)<<30); err != nil {
		t.Fatal(err)
	}
	interval := 15 * time.Second
	window := func(mean float64) hostWindow {
		return hostWindow{Samples: 40, Mean: mean, Peak: mean + 4, First: from, Last: now}
	}
	cpu := usageAlertReading(hostAlertRule{Kind: hostAlertCPUHigh, Params: hostAlertParams{Threshold: 90, Minutes: 10}}, window(95), interval, "box", now)
	cpu.Firing[0].Detail += busiestContainer(ctx, s.Store.DB, hostAlertCPUHigh, from, now)
	memory := usageAlertReading(hostAlertRule{Kind: hostAlertMemoryHigh, Params: hostAlertParams{Threshold: 90, Minutes: 10}}, window(92), interval, "box", now)
	memory.Firing[0].Detail += busiestContainer(ctx, s.Store.DB, hostAlertMemoryHigh, from, now)
	disk := diskAlertReading(hostAlertRule{Kind: hostAlertDiskFilling, Params: hostAlertParams{Threshold: 90}},
		[]hostMount{{Mountpoint: "/srv/data", UsedPercent: 93, Used: 93 << 30, Total: 100 << 30}}, now, interval, now)
	down := containerAlertReading(hostAlertRule{Kind: hostAlertContainerDown}, []hostContainer{
		{ID: "aaaaaaaaaaaa", Name: "shop-web", State: "exited", Exit: &hostContainerExit{ContainerExit: dockerx.ContainerExit{
			ExitCode: 3, Error: "OCI runtime exec failed: open /srv/shop/secret.env: permission denied", FinishedAt: now}}},
		{ID: "bbbbbbbbbbbb", Name: "shop-cache", State: "dead", Exit: &hostContainerExit{ContainerExit: dockerx.ContainerExit{
			ExitCode: 1, Error: "removal in progress; the process exited with code 99", FinishedAt: now}}},
	}, true)
	backup := backupAlertReading([]dbAlertJob{{ID: 3, Name: "nightly", Last: &dbAlertRun{
		Status: "failed", StartedAt: now, Reason: "open /srv/secret/dump.sql: permission denied"}}}, nil, now)

	for kind, reading := range map[string]hostAlertReading{
		hostAlertCPUHigh: cpu, hostAlertMemoryHigh: memory, hostAlertDiskFilling: disk,
		hostAlertContainerDown: down, hostAlertBackupFailed: backup,
	} {
		if len(reading.Firing) == 0 {
			t.Fatalf("%s: the fixture does not fire", kind)
		}
		rule, err := s.modules.hostAlerts.CreateRule(ctx, hostAlertWrite{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range reading.Firing {
			st := hostAlertState{Subject: f.Subject, Label: f.Label, Detail: f.Detail, FiringSince: now.Unix(), NotifiedAt: now.Unix()}
			if ok, err := s.modules.hostAlerts.writeState(ctx, *rule, st); err != nil || !ok {
				t.Fatalf("%s: state not stored: %v %v", kind, ok, err)
			}
		}
	}
	// The messages really do carry what they are said to, or this proves nothing.
	var details string
	_ = s.Store.DB.QueryRow(`SELECT group_concat(detail, ' | ') FROM host_alert_state`).Scan(&details)
	for _, secret := range secrets {
		if !strings.Contains(details, secret) {
			t.Fatalf("the fixture's messages lack %q, so the test cannot show it is removed: %s", secret, details)
		}
	}
	return secrets
}

func TestTheRealTextOfEveryAlertIsReducedToANumberAtEveryScope(t *testing.T) {
	s := testServer(t)
	s.modules.docker = dockerx.New("tcp://127.0.0.1:1")
	defer s.modules.docker.Close()
	secrets := realAlerts(t, s)

	snap, err := newAccountHost(s).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Alerts) != 6 {
		t.Fatalf("alerts = %+v", snap.Alerts)
	}
	byKey := func(a accountlink.Snapshot) map[string]accountlink.AlertInfo {
		out := map[string]accountlink.AlertInfo{}
		for _, al := range a.Alerts {
			out[al.Kind+"/"+al.Subject] = al
		}
		return out
	}

	for _, scope := range []accountlink.Scope{accountlink.ScopeObserve, accountlink.ScopeOperate, accountlink.ScopeMaintain} {
		body, err := json.Marshal(snap.Redact(scope))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if strings.Contains(string(body), secret) {
				t.Errorf("%s: %q leaves the machine in an alert: %s", scope, secret, body)
			}
		}
		var got accountlink.Snapshot
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		alerts := byKey(got)
		for key, want := range map[string]string{
			"cpu_high/cpu": "95%", "memory_high/memory": "92%", "disk_filling//srv/data": "93%",
		} {
			if alerts[key].Reading != want {
				t.Errorf("%s: %s reading = %q, want %q", scope, key, alerts[key].Reading, want)
			}
		}
		if scope == accountlink.ScopeObserve {
			// Below operate, what names a container or a job is gone altogether.
			for _, kind := range []string{"container_down", "backup_failed"} {
				for key, a := range alerts {
					if a.Kind == kind && (a.Subject != "" || a.Reading != "" || a.Since == 0) {
						t.Errorf("observe: %s keeps %+v", key, a)
					}
				}
			}
			continue
		}
		if a := alerts["container_down/shop-web"]; a.Reading != "exit 3" {
			t.Errorf("%s: container reading = %+v", scope, a)
		}
		// What Docker said is not read, even when it contains something that
		// looks like a code.
		if a := alerts["container_down/shop-cache"]; a.Reading != "" {
			t.Errorf("%s: a dead container's reading came from Docker's text: %+v", scope, a)
		}
		if a := alerts["backup_failed/job:3"]; a.Subject != "job:3" || a.Reading != "" {
			t.Errorf("%s: backup alert = %+v", scope, a)
		}
	}
}

func TestAnAlertReadingIsTakenFromTheNumberNotTheSentence(t *testing.T) {
	for _, tc := range []struct{ kind, detail, want string }{
		{hostAlertCPUHigh, "CPU averaged 95% over the last 10 min (peak 99%); the line is 90%. The busiest container was shop-db, 1.8 cores on average.", "95%"},
		{hostAlertCPUHigh, "CPU averaged 9.5% over the last 10 min", "9.5%"},
		{hostAlertMemoryHigh, "Memory averaged 91% over the last 5 min", "91%"},
		{hostAlertDiskFilling, "92% of its inodes are used; the line is 90%.", "92%"},
		{hostAlertContainerDown, "It was killed for running out of memory at 2026-10-25 01:00 UTC (exit 137).", "exit 137"},
		{hostAlertContainerDown, "Docker marked it dead at 2026-10-25 01:00 UTC: it could not be stopped or removed cleanly. Docker said: exited with code 99", ""},
		{hostAlertContainerDown, "Docker restarted it 4 times in the last 10 min; it keeps crashing.", ""},
		{hostAlertBackupFailed, "The run of nightly that started 2026-10-25 01:00 UTC failed: exit status 2", ""},
		{"something_new", "CPU averaged 95% over the last 10 min", ""},
		{hostAlertCPUHigh, "an unexpected sentence about shop-db", ""},
		{hostAlertCPUHigh, "", ""},
	} {
		if got := alertReading(tc.kind, tc.detail); got != tc.want {
			t.Errorf("alertReading(%s, %q) = %q, want %q", tc.kind, tc.detail, got, tc.want)
		}
	}
}

// dashboardStack is a daemon with the dashboard's own three containers, and
// one stack that belongs to somebody else.
func dashboardStack(t *testing.T, s *Server) (*fakeEngine, string) {
	t.Helper()
	root := t.TempDir()
	s.Cfg.ComposeRoots = []string{root}
	// A checkout whose directory is not named like its compose project lists
	// twice: as the running project, and as a compose file on disk.
	checkout := filepath.Join(root, "Just-Dashboard-checkout")
	if err := os.Mkdir(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "compose.yml"), []byte("services:\n  backend:\n    image: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shop := filepath.Join(root, "shop")
	if err := os.Mkdir(shop, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shop, "compose.yml"), []byte("services:\n  web:\n    image: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := engineListing(t, s, []listed{
		{ID: "b4c3k3ndb4c3k3nd0000", Name: "just-dashboard-backend-1", Project: "just-dashboard", Service: "backend", WorkingDir: checkout, Mounts: []string{s.Cfg.DataDir}},
		{ID: "fr0nt3ndfr0nt3nd0000", Name: "just-dashboard-frontend-1", Project: "just-dashboard", Service: "frontend", WorkingDir: checkout},
		{ID: "pr0xypr0xypr0xypr0xy", Name: "just-dashboard-proxy-1", Project: "just-dashboard", Service: "proxy", WorkingDir: checkout},
		{ID: "5h0p5h0p5h0p5h0p5h0p", Name: "shop-web-1", Project: "shop", Service: "web", WorkingDir: shop},
	})
	return engine, checkout
}

func TestASiteCannotStopOrRestartTheDashboardItself(t *testing.T) {
	s := testServer(t)
	engine, _ := dashboardStack(t, s)
	h := newAccountHost(s)

	for _, target := range []string{"just-dashboard-backend-1", "just-dashboard-frontend-1", "just-dashboard-proxy-1", "b4c3k3ndb4c3"} {
		for _, action := range []string{"stop", "restart", "start"} {
			_, err := h.Container(t.Context(), siteActor, target, action)
			if err == nil || !strings.Contains(err.Error(), "dashboard's own containers") || !strings.Contains(err.Error(), "dashboard.restart") {
				t.Errorf("%s %s = %v", action, target, err)
			}
		}
	}
	for _, request := range engine.seen() {
		if strings.Contains(request, "just-dashboard") || strings.Contains(request, "b4c3k3nd") {
			t.Fatalf("a command on the dashboard's own container reached the daemon: %v", engine.seen())
		}
	}
	// Somebody else's container is still the site's to restart.
	if _, err := h.Container(t.Context(), siteActor, "shop-web-1", "restart"); err != nil {
		t.Fatalf("a container of another stack was refused: %v", err)
	}
}

func TestASiteCannotRestartTheDashboardsOwnStackUnderEitherName(t *testing.T) {
	s := testServer(t)
	dashboardStack(t, s)
	_, marker := fakeComposeCLI(t, "exit 0\n")
	h := newAccountHost(s)

	stacks, err := s.modules.docker.ListStacks(t.Context(), s.Cfg.ComposeRoots)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]dockerx.ComposeStack{}
	for _, st := range stacks {
		names[st.Name] = st
	}
	// The dashboard's own stack is a managed one: nothing but this refusal
	// stood between an operate-scope site and `docker compose restart` of it.
	if own, ok := names["just-dashboard"]; !ok || !own.Managed {
		t.Fatalf("the dashboard's own stack does not list as managed: %+v", names)
	}
	if _, ok := names["Just-Dashboard-checkout"]; !ok {
		t.Fatalf("the checkout does not list a second time under its directory's name: %v", names)
	}
	for _, name := range []string{"just-dashboard", "Just-Dashboard-checkout"} {
		_, err := h.RestartStack(t.Context(), siteActor, name)
		if err == nil || !strings.Contains(err.Error(), "dashboard's own stack") {
			t.Errorf("restart %s = %v", name, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("docker compose ran for the dashboard's own stack")
	}
	if out, err := h.RestartStack(t.Context(), siteActor, "shop"); err != nil || out != "restarted shop" {
		t.Fatalf("another stack = %q, %v", out, err)
	}
}

func TestTheSnapshotListsLeaveOutTheDashboardsOwnStackButCountEverything(t *testing.T) {
	s := testServer(t)
	dashboardStack(t, s)
	snap, err := newAccountHost(s).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := snap.Containers
	if c == nil || c.Running != 4 || len(c.List) != 1 || c.List[0].Name != "shop-web-1" {
		t.Fatalf("containers = %+v", c)
	}
	if len(snap.Stacks) != 1 || snap.Stacks[0].Name != "shop" {
		t.Fatalf("stacks = %+v", snap.Stacks)
	}
}

func TestWithNoOwnContainerFoundNothingIsHeldBack(t *testing.T) {
	// A dashboard that is not in a container (a binary under systemd) has no
	// project to protect, and everything on the host is listed.
	s := testServer(t)
	engineListing(t, s, []listed{{ID: "5h0p5h0p5h0p5h0p5h0p", Name: "shop-web-1", Project: "shop", Service: "web", WorkingDir: t.TempDir()}})
	snap, err := newAccountHost(s).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Containers == nil || len(snap.Containers.List) != 1 {
		t.Fatalf("containers = %+v", snap.Containers)
	}
	if _, err := newAccountHost(s).Container(t.Context(), siteActor, "shop-web-1", "stop"); err != nil {
		t.Fatalf("stop = %v", err)
	}
}
