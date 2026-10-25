package accountlink

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// fullSnapshot is a machine with something to say in every section, and a name
// for every thing that has one. The names are the point: each is a string the
// observe scope must never carry.
func fullSnapshot() Snapshot {
	return Snapshot{
		Server: ServerInfo{
			Name: "box", Version: "0.8.2", Latest: "0.8.3", UpdateAvailable: true, UpdateBreaking: false,
			UpdateRun:     &UpdateRunInfo{Status: "running", Phase: "building", ToVersion: "0.8.3"},
			UptimeSeconds: 864000, OS: "Ubuntu 24.04", Kernel: "6.14.0-37-generic", Arch: "amd64",
		},
		Access: &AccessInfo{
			Kind: "tailscale", Endpoint: "https://box.tail1234.ts.net:8443", Port: 8443, TLS: "tailscale",
			Tailscale: &TailscaleInfo{Hostname: "box.tail1234.ts.net", IP4: "100.101.102.103", Running: true},
			SSHHost:   "203.0.113.7",
		},
		Metrics: &MetricsInfo{
			CPUPercent: 23.4, Load1: 0.8, MemPercent: 61, MemUsed: 4100000000, MemTotal: 8000000000, SwapPercent: 0,
			DiskPercent: 71.2, DiskMount: "/", NetRx: 1200.5, NetTx: 800.1, CPUSteal: 0.2, PSICPU: 0.1,
		},
		Containers: &ContainersInfo{Running: 12, Stopped: 2, Unhealthy: 1, List: []ContainerInfo{
			{ID: "3f2aaa", Name: "SECRET-container", Image: "registry.example/SECRET-image:1", State: "running", Health: "healthy"},
		}},
		Stacks: []StackInfo{{Name: "SECRET-stack", Services: 3, Running: 3, Managed: true}},
		Alerts: []AlertInfo{
			{Kind: "disk_filling", Subject: "/var", Reading: "91%", Since: 1761340000},
			{Kind: "container_down", Subject: "SECRET-container", Reading: "SECRET-container exited", Since: 1761340001},
			{Kind: "backup_failed", Subject: "job:3", Reading: "SECRET-job failed", Since: 1761340002},
			{Kind: "cpu_high", Subject: "cpu", Reading: "95%", Since: 1761340003},
			{Kind: "something_new", Subject: "SECRET-future", Reading: "SECRET-future", Since: 1761340004},
		},
		Backups: &BackupsInfo{
			Jobs: 2, Failed: 1, LastRun: &RunInfo{Status: "success", At: 1761300000},
			List: []BackupInfo{{ID: 3, Name: "SECRET-job", LastStatus: "success", LastAt: 1761300000}},
		},
		Deployments: &DeploymentsInfo{Projects: 3, LastRun: &DeployRunInfo{Project: "SECRET-project", Status: "success", At: 1761290000}},
	}
}

func jsonPaths(t *testing.T, raw []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				seen[p] = true
				walk(p, child)
			}
		case []any:
			for _, child := range t {
				walk(prefix+"[]", child)
			}
		}
	}
	walk("", v)
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// The field set is pinned. A field added to the snapshot has to be added here,
// where a reviewer sees it, and to the scope table the Settings page shows.
var observeFields = []string{
	"access", "access.endpoint", "access.kind", "access.port", "access.sshHost", "access.tailscale",
	"access.tailscale.hostname", "access.tailscale.ip4", "access.tailscale.running", "access.tls",
	"alerts", "alerts[].kind", "alerts[].reading", "alerts[].since", "alerts[].subject",
	"backups", "backups.failed", "backups.jobs", "backups.lastRun", "backups.lastRun.at", "backups.lastRun.status",
	"containers", "containers.running", "containers.stopped", "containers.unhealthy",
	"deployments", "deployments.lastRun", "deployments.lastRun.at", "deployments.lastRun.status", "deployments.projects",
	"metrics", "metrics.cpuPercent", "metrics.cpuSteal", "metrics.diskMount", "metrics.diskPercent", "metrics.load1",
	"metrics.memPercent", "metrics.memTotal", "metrics.memUsed", "metrics.netRx", "metrics.netTx", "metrics.psiCpu", "metrics.swapPercent",
	"protocol", "sentAt",
	"server", "server.arch", "server.kernel", "server.latest", "server.name", "server.os", "server.scope",
	"server.updateAvailable", "server.updateBreaking", "server.updateRun", "server.updateRun.phase",
	"server.updateRun.status", "server.updateRun.toVersion", "server.uptimeSeconds", "server.version",
}

var operateOnlyFields = []string{
	"backups.list", "backups.list[].id", "backups.list[].lastAt", "backups.list[].lastStatus", "backups.list[].name",
	"containers.list", "containers.list[].health", "containers.list[].id", "containers.list[].image",
	"containers.list[].name", "containers.list[].state",
	"deployments.lastRun.project",
	"stacks", "stacks[].managed", "stacks[].name", "stacks[].running", "stacks[].services",
}

func (e *testEnv) heartbeatAt(scope Scope) []byte {
	e.t.Helper()
	e.host.mu.Lock()
	e.host.snapshot = fullSnapshot()
	e.host.mu.Unlock()
	st := e.link(scope)
	if err := e.svc.sendHeartbeat(e.t.Context(), st, true); err != nil {
		e.t.Fatal(err)
	}
	bodies := e.site.heartbeatBodies()
	return bodies[len(bodies)-1]
}

func TestObserveSendsTheFixedFieldSetAndNoNameOfAnythingOnTheMachine(t *testing.T) {
	t.Parallel()
	body := newEnv(t).heartbeatAt(ScopeObserve)
	if got := jsonPaths(t, body); strings.Join(got, "\n") != strings.Join(observeFields, "\n") {
		t.Fatalf("observe field set changed.\n got: %v\nwant: %v", got, observeFields)
	}
	if strings.Contains(string(body), "SECRET") {
		t.Fatalf("a name leaked at observe: %s", body)
	}
	// What each alert may say below operate.
	var sent Snapshot
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	byKind := map[string]AlertInfo{}
	for _, a := range sent.Alerts {
		byKind[a.Kind] = a
	}
	if a := byKind["disk_filling"]; a.Subject != "/var" || a.Reading != "91%" {
		t.Fatalf("a disk alert lost its reading: %+v", a)
	}
	if a := byKind["cpu_high"]; a.Subject != "cpu" || a.Reading == "" {
		t.Fatalf("a CPU alert lost its reading: %+v", a)
	}
	for _, kind := range []string{"container_down", "backup_failed", "something_new"} {
		if a := byKind[kind]; a.Kind != kind || a.Subject != "" || a.Reading != "" || a.Since == 0 {
			t.Fatalf("%s alert = %+v", kind, a)
		}
	}
	if sent.Containers.Running != 12 || sent.Containers.Stopped != 2 || sent.Backups.Jobs != 2 || sent.Deployments.Projects != 3 {
		t.Fatalf("the counts did not survive redaction: %+v", sent)
	}
}

func TestOperateAndMaintainAddTheNamesAndNothingElse(t *testing.T) {
	t.Parallel()
	want := append(append([]string{}, observeFields...), operateOnlyFields...)
	sort.Strings(want)
	for _, scope := range []Scope{ScopeOperate, ScopeMaintain} {
		t.Run(string(scope), func(t *testing.T) {
			body := newEnv(t).heartbeatAt(scope)
			if got := jsonPaths(t, body); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("field set changed.\n got: %v\nwant: %v", got, want)
			}
			for _, name := range []string{"SECRET-container", "SECRET-stack", "SECRET-job", "SECRET-project"} {
				if !strings.Contains(string(body), name) {
					t.Fatalf("%s missing at %s", name, scope)
				}
			}
		})
	}
}

func TestNothingForbiddenIsInAnySnapshotTheDashboardCanBuild(t *testing.T) {
	t.Parallel()
	// The paths above are the whole vocabulary; this is the other direction, a
	// list of what must never be a key or a value, whoever adds a field.
	body := string(newEnv(t).heartbeatAt(ScopeMaintain))
	for _, forbidden := range []string{
		"JD_ALLOWED_CIDRS", "JD_MASTER_KEY", "password", "token", "apiKey", "secret", "env", "logs", "dsn", "connection",
	} {
		for _, p := range jsonPaths(t, []byte(body)) {
			for _, part := range strings.Split(p, ".") {
				if strings.EqualFold(strings.TrimSuffix(part, "[]"), forbidden) {
					t.Fatalf("snapshot has a %q field: %s", forbidden, p)
				}
			}
		}
	}
}

func TestASectionTheDashboardCannotReadIsLeftOutNotZeroed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.host.mu.Lock()
	e.host.snapshot = Snapshot{Server: ServerInfo{Name: "box", Version: "0.8.2"}}
	e.host.mu.Unlock()
	st := e.link(ScopeOperate)
	if err := e.svc.sendHeartbeat(t.Context(), st, true); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(e.site.heartbeatBodies()[0], &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"access", "metrics", "containers", "stacks", "alerts", "backups", "deployments"} {
		if _, ok := raw[key]; ok {
			t.Errorf("%s is present though it could not be read", key)
		}
	}
	for _, key := range []string{"protocol", "sentAt", "server"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("%s is missing", key)
		}
	}

	// Readable and empty is not the same thing, and is said.
	e.host.mu.Lock()
	e.host.snapshot = Snapshot{Server: ServerInfo{Name: "box", Version: "0.8.2"}, Stacks: []StackInfo{}, Alerts: []AlertInfo{}}
	e.host.mu.Unlock()
	e.advance(time.Minute)
	if err := e.svc.sendHeartbeat(t.Context(), st, true); err != nil {
		t.Fatal(err)
	}
	bodies := e.site.heartbeatBodies()
	if body := string(bodies[len(bodies)-1]); !strings.Contains(body, `"alerts":[]`) || !strings.Contains(body, `"stacks":[]`) {
		t.Fatalf("empty lists were dropped: %s", body)
	}
}

func TestTheHeartbeatCarriesTheScopeAndThePolicyTheDashboardHolds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeMaintain)
	if _, err := e.svc.setPolicy(t.Context(), UpdatePolicy{Auto: AutoPatch, Window: Window{"03:00", "05:00"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.sendHeartbeat(t.Context(), st, true); err != nil {
		t.Fatal(err)
	}
	var sent Snapshot
	if err := json.Unmarshal(e.site.heartbeatBodies()[0], &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Server.Scope != ScopeMaintain || sent.Protocol != 1 || sent.SentAt != vectorNow ||
		sent.Server.UpdatePolicy == nil || sent.Server.UpdatePolicy.Auto != AutoPatch {
		t.Fatalf("sent = %+v", sent.Server)
	}
	// "off" is said by saying nothing.
	e.svc.setPolicy(t.Context(), UpdatePolicy{Auto: AutoOff, Window: Window{"03:00", "05:00"}})
	e.advance(time.Minute)
	if err := e.svc.sendHeartbeat(t.Context(), st, true); err != nil {
		t.Fatal(err)
	}
	bodies := e.site.heartbeatBodies()
	if strings.Contains(string(bodies[len(bodies)-1]), "updatePolicy") {
		t.Fatal("an off policy was sent")
	}
}

func TestListsAreCutToTheSitesLimitsAndTheBodyToTheProtocols(t *testing.T) {
	t.Parallel()
	s := fullSnapshot()
	for i := 0; i < 500; i++ {
		s.Containers.List = append(s.Containers.List, ContainerInfo{ID: fmt.Sprint(i), Name: strings.Repeat("n", 100), Image: strings.Repeat("i", 100), State: "running"})
		s.Stacks = append(s.Stacks, StackInfo{Name: fmt.Sprint(i)})
		s.Alerts = append(s.Alerts, AlertInfo{Kind: "disk_filling", Subject: "/", Since: 1})
		s.Backups.List = append(s.Backups.List, BackupInfo{ID: int64(i), Name: "job"})
	}
	body, err := s.encode()
	if err != nil {
		t.Fatal(err)
	}
	var got Snapshot
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(body) >= maxRequestBody {
		t.Fatalf("body of %d bytes is not under the %d limit", len(body), maxRequestBody)
	}
	if len(got.Containers.List) > maxContainers || len(got.Stacks) > maxStacks || len(got.Alerts) > maxAlerts || len(got.Backups.List) > maxBackups {
		t.Fatalf("lists above the site's caps: %d %d %d %d", len(got.Containers.List), len(got.Stacks), len(got.Alerts), len(got.Backups.List))
	}
	if got.Containers.Running != 12 || got.Backups.Jobs != 2 {
		t.Fatalf("the counts changed: %+v", got)
	}
}

func TestAnAlertsReadingIsKeptOnlyIfItIsANumberAtEveryScope(t *testing.T) {
	t.Parallel()
	s := Snapshot{Alerts: []AlertInfo{
		{Kind: "cpu_high", Subject: "cpu", Reading: "95%", Since: 1},
		{Kind: "disk_filling", Subject: "/var", Reading: "91.25%", Since: 2},
		{Kind: "container_down", Subject: "web", Reading: "exit 137", Since: 3},
		{Kind: "cpu_high", Subject: "cpu", Reading: "CPU averaged 95%. The busiest container was shop-db", Since: 4},
		{Kind: "disk_filling", Subject: "/var", Reading: "91% of /var is used (/srv/secret)", Since: 5},
		{Kind: "backup_failed", Subject: "job:3", Reading: "open /srv/secret/dump.sql: permission denied", Since: 6},
		{Kind: "memory_high", Subject: "memory", Reading: "9%\nmore", Since: 7},
		{Kind: "memory_high", Subject: "memory", Reading: "1234%", Since: 8},
	}}
	for _, scope := range []Scope{ScopeObserve, ScopeOperate, ScopeMaintain} {
		red := s.Redact(scope)
		kept := map[int64]string{}
		for _, a := range red.Alerts {
			kept[a.Since] = a.Reading
			for _, leak := range []string{"shop-db", "/srv/secret", "permission denied", "\n"} {
				if strings.Contains(a.Reading, leak) {
					t.Errorf("%s: reading %q leaks %q", scope, a.Reading, leak)
				}
			}
		}
		want := map[int64]string{1: "95%", 2: "91.25%"}
		if scope != ScopeObserve {
			want[3] = "exit 137"
		}
		for since, reading := range want {
			if kept[since] != reading {
				t.Errorf("%s: alert %d reading = %q, want %q", scope, since, kept[since], reading)
			}
		}
		for _, since := range []int64{4, 5, 6, 7, 8} {
			if kept[since] != "" {
				t.Errorf("%s: alert %d kept the reading %q", scope, since, kept[since])
			}
		}
	}
	// Redacting does not edit the snapshot it was given.
	if s.Alerts[3].Reading == "" {
		t.Fatal("Redact rewrote the original alerts")
	}
}
