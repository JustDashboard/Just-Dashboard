package procs

import (
	"testing"
	"time"
)

// Two units as `systemctl show -p … -- a b` prints them on systemd 257: one
// record each, in the order asked, a blank line between, and "[not set]"
// where a stopped unit has no cgroup to count.
const showTwo = `Type=notify
MainPID=812
Result=success
NRestarts=2
ExecMainCode=0
ExecMainStatus=0
MemoryCurrent=48234496
CPUUsageNSec=9000000000
TasksCurrent=5
Id=nginx.service
ActiveState=active
InvocationID=aaa
ActiveEnterTimestampMonotonic=3600000000
StateChangeTimestampMonotonic=3600000000
FragmentPath=/usr/lib/systemd/system/nginx.service

Type=oneshot
MainPID=0
Result=exit-code
NRestarts=0
ExecMainCode=1
ExecMainStatus=1
MemoryCurrent=[not set]
CPUUsageNSec=5777545000
TasksCurrent=18446744073709551615
Id=certbot.service
ActiveState=failed
InvocationID=
ActiveEnterTimestampMonotonic=0
StateChangeTimestampMonotonic=7200000000
FragmentPath=/usr/lib/systemd/system/certbot.service
`

func TestParseShowRecordsSplitsOneRecordPerUnit(t *testing.T) {
	records := parseShowRecords(showTwo)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0]["Id"] != "nginx.service" || records[1]["Id"] != "certbot.service" {
		t.Fatalf("ids = %q, %q", records[0]["Id"], records[1]["Id"])
	}
	if records[1]["MemoryCurrent"] != "[not set]" {
		t.Fatalf("memory = %q", records[1]["MemoryCurrent"])
	}
}

func TestApplyReadingFillsTheListedUnit(t *testing.T) {
	s := NewSystemd()
	records := parseShowRecords(showTwo)
	// Booted three hours ago: nginx started an hour after boot and certbot
	// failed two hours after it.
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	uptime := uint64((3 * time.Hour) / time.Second)

	nginx := Unit{Name: "nginx.service", ActiveState: "active"}
	s.applyReading(&nginx, records[0], uptime, now)
	if nginx.MainPID != 812 || nginx.Tasks != 5 || nginx.Memory != 48234496 || nginx.Restarts != 2 {
		t.Fatalf("nginx = %+v", nginx)
	}
	if nginx.SinceUnix != now.Add(-2*time.Hour).Unix() || nginx.ChangedAt != nginx.SinceUnix {
		t.Fatalf("nginx since = %d, changed = %d", nginx.SinceUnix, nginx.ChangedAt)
	}
	if nginx.Fragment != "/usr/lib/systemd/system/nginx.service" || nginx.Type != "notify" {
		t.Fatalf("nginx file = %q, type = %q", nginx.Fragment, nginx.Type)
	}
	// The first reading of a counter is the start of a window, not a rate.
	if nginx.CPUReady {
		t.Fatal("a first reading reported a rate")
	}

	certbot := Unit{Name: "certbot.service", ActiveState: "failed", SinceUnix: 99}
	s.applyReading(&certbot, records[1], uptime, now)
	if certbot.Memory != 0 || certbot.Tasks != 0 {
		t.Fatalf("an unset counter read as memory %d, tasks %d", certbot.Memory, certbot.Tasks)
	}
	if certbot.SinceUnix != 0 {
		t.Fatal("a failed unit kept an active-since time")
	}
	if certbot.ChangedAt != now.Add(-time.Hour).Unix() {
		t.Fatalf("certbot changed = %d", certbot.ChangedAt)
	}
	if certbot.ExitCode != "exited" || certbot.ExitStatus != 1 || certbot.Result != "exit-code" {
		t.Fatalf("certbot exit = %q %d %q", certbot.ExitCode, certbot.ExitStatus, certbot.Result)
	}
	if certbot.CPUReady {
		t.Fatal("a failed unit was measured")
	}
}

func TestMeasureNeedsAWholeWindowAndStartsOverOnANewRun(t *testing.T) {
	s := NewSystemd()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if _, ok := s.measure("api.service", "run-1", 1_000_000_000, 10, at); ok {
		t.Fatal("first reading is not a window")
	}
	// Half a core over two seconds.
	rate, ok := s.measure("api.service", "run-1", 2_000_000_000, 10, at.Add(2*time.Second))
	if !ok || rate != 50 {
		t.Fatalf("rate = %v, %v; want 50", rate, ok)
	}
	// A read 100ms later reports the last whole window rather than measuring
	// the scheduler's rounding.
	rate, ok = s.measure("api.service", "run-1", 2_900_000_000, 10, at.Add(2100*time.Millisecond))
	if !ok || rate != 50 {
		t.Fatalf("too-soon rate = %v, %v; want the last window's 50", rate, ok)
	}
	// A restart is a new cgroup whose counter starts again.
	if _, ok := s.measure("api.service", "run-2", 100, 10, at.Add(5*time.Second)); ok {
		t.Fatal("a new invocation inherited the old one's window")
	}
	if got := len(s.History("api.service")); got != 1 {
		t.Fatalf("history = %d points, want 1", got)
	}
}

func TestHistoryIsCappedAndSpaced(t *testing.T) {
	s := NewSystemd()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	nsec := uint64(0)
	for i := 0; i <= historySize+20; i++ {
		nsec += 1_000_000_000
		s.measure("busy.service", "run", nsec, uint64(i), at.Add(time.Duration(i)*3*time.Second))
	}
	points := s.History("busy.service")
	if len(points) != historySize {
		t.Fatalf("history = %d points, want %d", len(points), historySize)
	}
	if points[len(points)-1].Memory != uint64(historySize+20) {
		t.Fatalf("newest point = %+v", points[len(points)-1])
	}
	// One more reading a second later is a window but not a new point.
	s.measure("busy.service", "run", nsec+1_000_000_000, 0, at.Add(time.Duration(historySize+20)*3*time.Second+time.Second))
	if got := s.History("busy.service"); got[len(got)-1].Memory != uint64(historySize+20) {
		t.Fatal("a point was kept less than two seconds after the last")
	}
}

func TestPruneForgetsUnitsNoLongerListed(t *testing.T) {
	s := NewSystemd()
	at := time.Now()
	s.measure("gone.service", "run", 1, 0, at)
	s.measure("gone.service", "run", 2_000_000_000, 0, at.Add(2*time.Second))
	s.state.idle = map[string]idleRead{"old.service": {}}
	s.prune([]Unit{{Name: "kept.service"}})
	if len(s.History("gone.service")) != 0 || len(s.state.cpu) != 0 || len(s.state.idle) != 0 {
		t.Fatal("an unlisted unit's readings were kept")
	}
}

func TestCounterRejectsUnsetValues(t *testing.T) {
	for _, value := range []string{"[not set]", "", "18446744073709551615"} {
		if _, ok := counter(value); ok {
			t.Fatalf("counter(%q) read as set", value)
		}
	}
	if n, ok := counter("4096"); !ok || n != 4096 {
		t.Fatalf("counter(4096) = %d, %v", n, ok)
	}
}

func TestExitOfNamesTheKernelsCode(t *testing.T) {
	cases := []struct {
		code, status, want string
		number             int
	}{
		{"1", "1", "exited", 1},
		{"2", "9", "killed", 9},
		{"3", "11", "dumped", 11},
		{"0", "0", "", 0},
	}
	for _, c := range cases {
		got, number := exitOf(map[string]string{"ExecMainCode": c.code, "ExecMainStatus": c.status})
		if got != c.want || number != c.number {
			t.Fatalf("exitOf(%s, %s) = %q %d", c.code, c.status, got, number)
		}
	}
}
