package proxysvc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func observed(id int64, protocol, family, address string, port uint32, process, user string, pid int32) ListenerObservation {
	o := ListenerObservation{ID: id, Protocol: protocol, Family: family, Address: address, Port: port, Process: process, User: user, PID: pid}
	if pid > 0 {
		o.Cmdline = process + " --serve"
	}
	return o
}

func socketOf(protocol, family, address string, port uint32, process, user string, pid int32) Listener {
	scope := bindScope(address)
	return Listener{
		Protocol: protocol, Family: family, Address: address, Port: port, Process: process, User: user, PID: pid,
		Cmdline: process + " --serve", Scope: scope, Exposed: scope != ScopeLoopback,
	}
}

func TestDiffListeners(t *testing.T) {
	sshd4 := observed(1, "tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900)
	sshd6 := observed(2, "tcp", "ipv6", "::", 22, "sshd", "root", 900)
	app := observed(3, "tcp", "ipv4", "127.0.0.1", 3000, "node", "app", 1400)
	unseen := observed(4, "tcp", "ipv4", "0.0.0.0", 8080, "", "", 0)
	for _, tc := range []struct {
		name          string
		open          []ListenerObservation
		current       []Listener
		opened        []string
		closed        []int64
		refreshedPIDs map[int64]int32
	}{
		{
			name:    "nothing changed",
			open:    []ListenerObservation{sshd4, sshd6},
			current: []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900), socketOf("tcp", "ipv6", "::", 22, "sshd", "root", 900)},
		},
		{
			name:    "a socket opened",
			open:    []ListenerObservation{sshd4},
			current: []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900), socketOf("tcp", "ipv4", "0.0.0.0", 6379, "redis-server", "redis", 2000)},
			opened:  []string{"tcp 0.0.0.0:6379 redis-server"},
		},
		{
			name:    "a socket closed",
			open:    []ListenerObservation{sshd4, app},
			current: []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900)},
			closed:  []int64{3},
		},
		{
			// Each family is a socket of its own; the page folds the pair.
			name:    "both families of one service opened",
			current: []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900), socketOf("tcp", "ipv6", "::", 22, "sshd", "root", 900)},
			opened:  []string{"tcp 0.0.0.0:22 sshd", "tcp :::22 sshd"},
		},
		{
			name:    "another program took the port",
			open:    []ListenerObservation{app},
			current: []Listener{socketOf("tcp", "ipv4", "127.0.0.1", 3000, "python3", "app", 1500)},
			opened:  []string{"tcp 127.0.0.1:3000 python3"},
			closed:  []int64{3},
		},
		{
			name:    "another account runs the same program on the port",
			open:    []ListenerObservation{app},
			current: []Listener{socketOf("tcp", "ipv4", "127.0.0.1", 3000, "node", "intruder", 1500)},
			opened:  []string{"tcp 127.0.0.1:3000 node"},
			closed:  []int64{3},
		},
		{
			// A restart between two samples never stopped listening as far
			// as a minute away can tell.
			name:          "the same program restarted",
			open:          []ListenerObservation{app},
			current:       []Listener{socketOf("tcp", "ipv4", "127.0.0.1", 3000, "node", "app", 1600)},
			refreshedPIDs: map[int64]int32{3: 1600},
		},
		{
			name:    "an owner this sample could not read",
			open:    []ListenerObservation{app},
			current: []Listener{socketOf("tcp", "ipv4", "127.0.0.1", 3000, "", "", 0)},
		},
		{
			name:          "an owner read at last",
			open:          []ListenerObservation{unseen},
			current:       []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 8080, "caddy", "caddy", 3100)},
			refreshedPIDs: map[int64]int32{4: 3100},
		},
		{
			name:    "a socket's second open stretch",
			open:    []ListenerObservation{sshd4, observed(9, "tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900)},
			current: []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900)},
			closed:  []int64{9},
		},
		{
			name: "everything went",
			open: []ListenerObservation{sshd6, sshd4, app},
			// Sorted, so a sample writes its closings in a stable order.
			closed: []int64{1, 2, 3},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changes := diffListeners(tc.open, tc.current)
			opened := []string{}
			for _, l := range changes.opened {
				opened = append(opened, l.Protocol+" "+l.Address+":"+strconv.Itoa(int(l.Port))+" "+l.Process)
			}
			if tc.opened == nil {
				tc.opened = []string{}
			}
			if !reflect.DeepEqual(opened, tc.opened) {
				t.Errorf("opened %v, want %v", opened, tc.opened)
			}
			if !reflect.DeepEqual(changes.closed, tc.closed) {
				t.Errorf("closed %v, want %v", changes.closed, tc.closed)
			}
			refreshed := map[int64]int32{}
			for _, o := range changes.refreshed {
				refreshed[o.ID] = o.PID
			}
			if tc.refreshedPIDs == nil {
				tc.refreshedPIDs = map[int64]int32{}
			}
			if !reflect.DeepEqual(refreshed, tc.refreshedPIDs) {
				t.Errorf("refreshed %v, want %v", refreshed, tc.refreshedPIDs)
			}
		})
	}
}

// A name read later fills in one that was not; one that could not be read
// never replaces one that was.
func TestRefreshedOwnerKeepsWhatWasRead(t *testing.T) {
	app := observed(3, "tcp", "ipv4", "127.0.0.1", 3000, "node", "app", 1400)
	if _, changed := refreshedOwner(app, socketOf("tcp", "ipv4", "127.0.0.1", 3000, "", "", 0)); changed {
		t.Error("an unread owner replaced a read one")
	}
	next, changed := refreshedOwner(observed(4, "tcp", "ipv4", "0.0.0.0", 8080, "", "", 0),
		socketOf("tcp", "ipv4", "0.0.0.0", 8080, "caddy", "caddy", 3100))
	if !changed || next.Process != "caddy" || next.User != "caddy" || next.PID != 3100 || next.Cmdline != "caddy --serve" {
		t.Errorf("refreshed to %+v", next)
	}
}

func TestRecordableLeavesOutLoopbackEphemeralPorts(t *testing.T) {
	span := &PortRange{Low: 32768, High: 60999}
	for _, tc := range []struct {
		socket Listener
		span   *PortRange
		want   bool
	}{
		{socketOf("tcp", "ipv4", "127.0.0.1", 41051, "node", "ubuntu", 1), span, false},
		{socketOf("tcp", "ipv6", "::1", 32768, "node", "ubuntu", 1), span, false},
		{socketOf("tcp", "ipv4", "127.0.0.1", 60999, "node", "ubuntu", 1), span, false},
		// Chosen ports on loopback are somebody's service.
		{socketOf("tcp", "ipv4", "127.0.0.1", 5432, "postgres", "postgres", 1), span, true},
		{socketOf("tcp", "ipv4", "127.0.0.1", 61200, "node", "ubuntu", 1), span, true},
		// Off the machine, an ephemeral port answers strangers like any other.
		{socketOf("tcp", "ipv4", "0.0.0.0", 41051, "node", "ubuntu", 1), span, true},
		{socketOf("udp", "ipv4", "100.110.34.31", 41641, "tailscaled", "root", 1), span, true},
		// Without the kernel's range there is nothing to judge by.
		{socketOf("tcp", "ipv4", "127.0.0.1", 41051, "node", "ubuntu", 1), nil, true},
	} {
		if got := recordable(tc.socket, tc.span); got != tc.want {
			t.Errorf("recordable(%s:%d, %v) = %v, want %v", tc.socket.Address, tc.socket.Port, tc.span, got, tc.want)
		}
	}
}

// fakeHost is a lister and a clock the sampler test moves by hand.
type fakeHost struct {
	sockets []Listener
	fail    error
	at      time.Time
}

func (h *fakeHost) list(context.Context) ([]Listener, error) { return h.sockets, h.fail }
func (h *fakeHost) now() time.Time                           { return h.at }

func testRecorder(t *testing.T) (*PortRecorder, *fakeHost) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	host := &fakeHost{at: time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)}
	r := NewPortRecorder(st.DB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.list = host.list
	r.now = host.now
	r.ephemeral = func() (PortRange, error) { return PortRange{Low: 32768, High: 60999}, nil }
	return r, host
}

func sample(t *testing.T, r *PortRecorder) SampleResult {
	t.Helper()
	got, err := r.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// event is how the sampler test reads one event: its kind, endpoint and
// owner, and its two samples as minutes after 03:00.
func eventLine(e PortEvent, start time.Time) string {
	return string(e.Kind) + " " + e.Protocol + " " + net.JoinHostPort(e.Address, strconv.Itoa(int(e.Port))) + " " + e.Process +
		" " + strconv.Itoa(int(e.After.Sub(start)/time.Minute)) + "-" + strconv.Itoa(int(e.At.Sub(start)/time.Minute))
}

func history(t *testing.T, r *PortRecorder, since time.Time, limit int) (PortHistory, []string) {
	t.Helper()
	got, err := r.History(context.Background(), since, limit)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)
	lines := []string{}
	for _, e := range got.Events {
		lines = append(lines, eventLine(e, start))
	}
	return got, lines
}

func TestPortRecorderKeepsWhatOpenedAndClosed(t *testing.T) {
	r, host := testRecorder(t)
	start := host.at
	sshd := socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900)
	app := socketOf("tcp", "ipv4", "127.0.0.1", 3000, "node", "app", 1400)
	host.sockets = []Listener{sshd, app, socketOf("tcp", "ipv4", "127.0.0.1", 41051, "node", "app", 1401)}

	// Before the first sample there is no history, and no socket is dated.
	got, _ := history(t, r, start.Add(-time.Hour), 100)
	if got.RecordingSince != nil || got.LastSample != nil || len(got.Events) != 0 {
		t.Fatalf("history before any sample: %+v", got)
	}
	if got.IntervalSeconds != 60 || got.RetentionDays != 30 {
		t.Errorf("interval %ds and retention %dd, want 60s and 30d", got.IntervalSeconds, got.RetentionDays)
	}

	// The first sample is where the history starts: nothing in it opened.
	if res := sample(t, r); !res.Baseline || res.Opened != 2 || res.Closed != 0 {
		t.Fatalf("first sample: %+v", res)
	}
	got, lines := history(t, r, start.Add(-time.Hour), 100)
	if len(lines) != 0 || !got.RecordingSince.Equal(start) || !got.LastSample.Equal(start) {
		t.Fatalf("after the first sample: %v, recording since %v, last %v", lines, got.RecordingSince, got.LastSample)
	}
	if seen, _ := r.FirstSeen(context.Background(), host.sockets); seen[0] != nil || seen[1] != nil {
		t.Errorf("a socket listening when recording began was dated: %v", seen)
	}

	// Redis opens; the next sample dates it after the one before.
	redis := socketOf("tcp", "ipv4", "0.0.0.0", 6379, "redis-server", "redis", 2000)
	host.at = start.Add(time.Minute)
	host.sockets = []Listener{sshd, app, redis}
	if res := sample(t, r); res.Baseline || res.Opened != 1 || res.Closed != 0 {
		t.Fatalf("second sample: %+v", res)
	}
	seen, err := r.FirstSeen(context.Background(), []Listener{sshd, redis, socketOf("tcp", "ipv4", "0.0.0.0", 6379, "nc", "mallory", 9)})
	if err != nil {
		t.Fatal(err)
	}
	if seen[0] != nil || seen[1] == nil || !seen[1].Equal(start.Add(time.Minute)) || seen[2] != nil {
		t.Errorf("first seen %v, want only redis at 03:01", seen)
	}

	// Another program takes 3000 between samples: one closing, one opening.
	host.at = start.Add(2 * time.Minute)
	host.sockets = []Listener{sshd, socketOf("tcp", "ipv4", "127.0.0.1", 3000, "python3", "app", 1500), redis}
	if res := sample(t, r); res.Opened != 1 || res.Closed != 1 {
		t.Fatalf("third sample: %+v", res)
	}

	// A walk that fails records nothing: not every socket closing.
	host.at = start.Add(3 * time.Minute)
	host.fail = errors.New("walk timed out")
	if _, err := r.Sample(context.Background()); err == nil {
		t.Fatal("a failed walk was not reported")
	}
	host.fail = nil

	// The dashboard is down for two hours; Redis went meanwhile, and the
	// closing is dated across the gap.
	host.at = start.Add(2*time.Hour + 2*time.Minute)
	host.sockets = []Listener{sshd, socketOf("tcp", "ipv4", "127.0.0.1", 3000, "python3", "app", 1500)}
	if res := sample(t, r); res.Opened != 0 || res.Closed != 1 {
		t.Fatalf("sample after the gap: %+v", res)
	}

	got, lines = history(t, r, start.Add(-time.Hour), 100)
	want := []string{
		"closed tcp 0.0.0.0:6379 redis-server 2-122",
		// Within one sample, a socket's closing comes before its opening.
		"closed tcp 127.0.0.1:3000 node 1-2",
		"opened tcp 127.0.0.1:3000 python3 1-2",
		"opened tcp 0.0.0.0:6379 redis-server 0-1",
	}
	if !reflect.DeepEqual(lines, want) || got.Truncated {
		t.Fatalf("history\n got %q\nwant %q (truncated %v)", lines, want, got.Truncated)
	}
	if !got.LastSample.Equal(start.Add(2*time.Hour+2*time.Minute)) || got.Stalled {
		t.Errorf("last sample %v, stalled %v", got.LastSample, got.Stalled)
	}
	// A closing says how long the socket had listened, and whether it was
	// there before recording began.
	if e := got.Events[1]; !e.Since.Equal(start) || !e.Baseline || e.Scope != ScopeLoopback || e.Exposed {
		t.Errorf("node's closing: %+v", e)
	}
	if e := got.Events[0]; !e.Since.Equal(start.Add(time.Minute)) || e.Baseline || e.Scope != ScopeAll || !e.Exposed || e.PID != 2000 || e.Cmdline != "redis-server --serve" {
		t.Errorf("redis's closing: %+v", e)
	}

	// The window and the limit.
	_, lines = history(t, r, start.Add(2*time.Hour), 100)
	if !reflect.DeepEqual(lines, want[:1]) {
		t.Errorf("the last hour: %q", lines)
	}
	got, lines = history(t, r, start.Add(-time.Hour), 2)
	if !reflect.DeepEqual(lines, want[:2]) || !got.Truncated {
		t.Errorf("two events: %q, truncated %v", lines, got.Truncated)
	}

	// Three intervals with no sample is a sampler whose walks are failing.
	host.at = start.Add(2*time.Hour + 5*time.Minute + time.Second)
	if got, _ = history(t, r, start, 100); !got.Stalled {
		t.Error("no sample for over three minutes did not read as stalled")
	}

	// Stretches that ended more than thirty days ago go; open ones stay.
	host.at = start.Add(31 * 24 * time.Hour)
	r.prune(context.Background())
	var closed, open int
	if err := r.db.QueryRow(`SELECT COUNT(*) FILTER (WHERE gone_at IS NOT NULL), COUNT(*) FILTER (WHERE gone_at IS NULL) FROM listener_observations`).Scan(&closed, &open); err != nil {
		t.Fatal(err)
	}
	if closed != 0 || open != 2 {
		t.Errorf("after pruning: %d closed and %d open stretches, want 0 and 2", closed, open)
	}
}

// Loopback sockets on ports the kernel handed out come and go by the dozen
// and are never written down.
func TestPortRecorderLeavesOutLoopbackEphemeralPorts(t *testing.T) {
	r, host := testRecorder(t)
	host.sockets = []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900)}
	sample(t, r)
	host.at = host.at.Add(time.Minute)
	host.sockets = append(host.sockets,
		socketOf("tcp", "ipv4", "127.0.0.1", 41051, "node", "app", 1401),
		socketOf("tcp", "ipv4", "0.0.0.0", 41052, "node", "app", 1402))
	if res := sample(t, r); res.Opened != 1 {
		t.Fatalf("opened %d, want only the ephemeral port on every interface", res.Opened)
	}
	_, lines := history(t, r, host.at.Add(-time.Hour), 100)
	if !reflect.DeepEqual(lines, []string{"opened tcp 0.0.0.0:41052 node 0-1"}) {
		t.Errorf("history %q", lines)
	}
}

// Start samples straight away, so a restart leaves no minute-long hole, and
// Stop waits for the loop; stopping a recorder never started does nothing.
func TestPortRecorderStartsAndStops(t *testing.T) {
	r, host := testRecorder(t)
	r.Stop()
	host.sockets = []Listener{socketOf("tcp", "ipv4", "0.0.0.0", 22, "sshd", "root", 900)}
	r.Start(context.Background())
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := r.History(context.Background(), host.at.Add(-time.Hour), 10)
		if err != nil {
			t.Fatal(err)
		}
		if got.LastSample != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no sample ten seconds after Start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.Stop()
	r.Stop()
}

// Against the real kernel tables: a socket this test opens is recorded
// opening with its owner, and closing once it closes.
func TestPortRecorderRecordsARealSocket(t *testing.T) {
	r, host := testRecorder(t)
	r.list = ListListeners
	// A port from :0 is inside the kernel's range, which the recorder would
	// set aside on loopback.
	r.ephemeral = func() (PortRange, error) { return PortRange{}, errors.New("no range") }
	ctx := context.Background()
	if _, err := r.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint32(ln.Addr().(*net.TCPAddr).Port)
	host.at = host.at.Add(time.Minute)
	if _, err := r.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	ln.Close()
	host.at = host.at.Add(time.Minute)
	if _, err := r.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := r.History(ctx, host.at.Add(-time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []PortEventKind{}
	for _, e := range got.Events {
		if e.Protocol != "tcp" || e.Address != "127.0.0.1" || e.Port != port {
			continue
		}
		kinds = append(kinds, e.Kind)
		if e.PID != int32(os.Getpid()) || e.Process == "" {
			t.Errorf("%s event names pid %d %q, want this test's pid %d", e.Kind, e.PID, e.Process, os.Getpid())
		}
	}
	if !reflect.DeepEqual(kinds, []PortEventKind{PortClosed, PortOpened}) {
		t.Errorf("events for 127.0.0.1:%d: %v, want it closing after opening", port, kinds)
	}
}
