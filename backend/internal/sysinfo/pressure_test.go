package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPressureParsesBothLines(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "cpu"), "some avg10=1.25 avg60=0.80 avg300=0.31 total=91234\n")
	write(t, filepath.Join(dir, "memory"),
		"some avg10=12.50 avg60=4.00 avg300=1.00 total=5\nfull avg10=6.25 avg60=2.00 avg300=0.50 total=3\n")
	write(t, filepath.Join(dir, "io"),
		"some avg10=40.00 avg60=20.00 avg300=8.00 total=9\nfull avg10=33.33 avg60=10.00 avg300=4.00 total=7\n")

	restore := usePressureRoot(t, dir)
	defer restore()

	p := ReadPressure()
	if !p.Supported {
		t.Fatal("expected PSI to be reported as supported")
	}
	if p.CPUSome != 1.25 {
		t.Errorf("cpu some = %v, want 1.25", p.CPUSome)
	}
	if p.MemSome != 12.5 || p.MemFull != 6.25 {
		t.Errorf("memory some/full = %v/%v, want 12.5/6.25", p.MemSome, p.MemFull)
	}
	if p.IOSome != 40 || p.IOFull != 33.33 {
		t.Errorf("io some/full = %v/%v, want 40/33.33", p.IOSome, p.IOFull)
	}
	// Health judges on the longer windows, so each must land in its own field.
	if p.CPUSome60 != 0.8 || p.CPUSome300 != 0.31 {
		t.Errorf("cpu 60/300 = %v/%v, want 0.8/0.31", p.CPUSome60, p.CPUSome300)
	}
	if p.MemSome60 != 4 || p.MemSome300 != 1 || p.MemFull60 != 2 {
		t.Errorf("memory some60/some300/full60 = %v/%v/%v, want 4/1/2", p.MemSome60, p.MemSome300, p.MemFull60)
	}
	if p.IOSome60 != 20 || p.IOSome300 != 8 || p.IOFull60 != 10 {
		t.Errorf("io some60/some300/full60 = %v/%v/%v, want 20/8/10", p.IOSome60, p.IOSome300, p.IOFull60)
	}
}

// A kernel without PSI has no files at all, and that must read as "we cannot
// tell" rather than as three healthy zeroes — the UI says so explicitly, and
// it can only do that if Supported stays false.
func TestReadPressureUnsupportedKernel(t *testing.T) {
	restore := usePressureRoot(t, filepath.Join(t.TempDir(), "absent"))
	defer restore()

	p := ReadPressure()
	if p.Supported {
		t.Fatal("expected Supported=false when /proc/pressure is missing")
	}
	if p.CPUSome != 0 || p.MemSome != 0 || p.IOSome != 0 {
		t.Errorf("expected zero values, got %+v", p)
	}
}

// Each average is taken by name; a parser that read them by position would
// silently swap the windows or report the cumulative total instead.
func TestReadPressureReadsWindowsByName(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "cpu"), "some avg300=99.00 avg60=88.00 avg10=2.00 total=123456789\n")

	restore := usePressureRoot(t, dir)
	defer restore()

	p := ReadPressure()
	if p.CPUSome != 2 || p.CPUSome60 != 88 || p.CPUSome300 != 99 {
		t.Errorf("cpu some 10/60/300 = %v/%v/%v, want 2/88/99", p.CPUSome, p.CPUSome60, p.CPUSome300)
	}
}

func TestReadSocketsSumsIPv4AndIPv6(t *testing.T) {
	dir := t.TempDir()
	v4 := filepath.Join(dir, "sockstat")
	v6 := filepath.Join(dir, "sockstat6")
	write(t, v4, "sockets: used 421\n"+
		"TCP: inuse 30 orphan 2 tw 1500 alloc 44 mem 6\n"+
		"UDP: inuse 7 mem 3\n"+
		"UDPLITE: inuse 0\nRAW: inuse 0\nFRAG: inuse 0 memory 0\n")
	write(t, v6, "TCP6: inuse 12 orphan 1\nUDP6: inuse 4\nRAW6: inuse 0\n")

	old := sockstatPaths
	sockstatPaths = []string{v4, v6}
	defer func() { sockstatPaths = old }()

	s := ReadSockets()
	if s.TCPInUse != 42 {
		t.Errorf("tcp inuse = %d, want 42 (30 + 12)", s.TCPInUse)
	}
	if s.TCPTimeWait != 1500 {
		t.Errorf("time_wait = %d, want 1500", s.TCPTimeWait)
	}
	if s.TCPOrphan != 3 {
		t.Errorf("orphan = %d, want 3", s.TCPOrphan)
	}
	if s.UDPInUse != 11 {
		t.Errorf("udp inuse = %d, want 11 (7 + 4)", s.UDPInUse)
	}
	if s.Used != 421 {
		t.Errorf("used = %d, want 421", s.Used)
	}
}

func TestReadSocketsMissingFile(t *testing.T) {
	old := sockstatPaths
	sockstatPaths = []string{filepath.Join(t.TempDir(), "nope")}
	defer func() { sockstatPaths = old }()

	if got := ReadSockets(); got != (Sockets{}) {
		t.Errorf("expected zero Sockets, got %+v", got)
	}
}

// The counters are cumulative, so a rate is only ever a delta — and a delta
// that goes backwards (a device re-enumerated, a host migrated) must read as
// zero rather than as a negative service time.
func TestLatencyAndBusy(t *testing.T) {
	if got := latency(1000, 900, 250, 200); got != 2 {
		t.Errorf("latency = %v, want 2ms (100ms over 50 ops)", got)
	}
	if got := latency(900, 1000, 250, 200); got != 0 {
		t.Errorf("latency on a counter reset = %v, want 0", got)
	}
	if got := latency(1000, 900, 200, 200); got != 0 {
		t.Errorf("latency with no operations = %v, want 0", got)
	}
	if got := busy(1500, 1000, 1); got != 50 {
		t.Errorf("busy = %v, want 50%% (500ms of a 1s interval)", got)
	}
	if got := busy(5000, 1000, 1); got != 100 {
		t.Errorf("busy = %v, want it capped at 100", got)
	}
	if got := busy(500, 1000, 1); got != 0 {
		t.Errorf("busy on a counter reset = %v, want 0", got)
	}
}

func usePressureRoot(t *testing.T, dir string) func() {
	t.Helper()
	old := pressureRoot
	pressureRoot = dir
	return func() { pressureRoot = old }
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
