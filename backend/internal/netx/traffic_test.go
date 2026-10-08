package netx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestParseSSReadsTheRealShape(t *testing.T) {
	t.Parallel()
	socks, truncated := parseSS(fixture(t, "traffic-ss.txt"))
	if truncated || len(socks) != 9 {
		t.Fatalf("%d sockets, truncated %v", len(socks), truncated)
	}
	first := socks[0]
	if first.local != "203.0.113.10:44134" || first.peer != "198.51.100.20:443" || first.peerAddr != "198.51.100.20" || first.peerPort != 443 ||
		first.name != "agent-cli" || len(first.pids) != 1 || first.pids[0] != 50969 {
		t.Fatalf("first = %+v", first)
	}
	// bytes_sent is the traffic that left; bytes_acked trails it by a byte.
	if first.tx != 1671223 || first.rx == 0 {
		t.Fatalf("counters = tx %d rx %d", first.tx, first.rx)
	}
	// A socket shared by two processes: the first names the program.
	if ssh := socks[2]; ssh.name != "sshd-session" || len(ssh.pids) != 2 || ssh.pids[0] != 395463 || ssh.pids[1] != 395461 {
		t.Fatalf("shared socket = %+v", ssh)
	}
	if v6 := socks[6]; v6.peerAddr != "2001:db8:ffff::443" || v6.peerPort != 443 || v6.tx != 10240 || v6.rx != 204800 {
		t.Fatalf("v6 = %+v", v6)
	}
	// Older kernels print only bytes_acked.
	if nginx := socks[7]; nginx.tx != 5000 || nginx.rx != 900 || len(nginx.pids) != 2 {
		t.Fatalf("acked-only socket = %+v", nginx)
	}
	// A socket whose owner ss could not read has no users column.
	if bare := socks[8]; bare.name != "" || len(bare.pids) != 0 || bare.tx != 800 || bare.rx != 1200 {
		t.Fatalf("socket with no owner = %+v", bare)
	}
}

func TestParseSSNamesWithSpacesAndQuotes(t *testing.T) {
	t.Parallel()
	out := "0 0 127.0.0.1:43181 127.0.0.1:45096 users:((\"next-server (v1\",pid=295481,fd=49))\n" +
		"\t cubic bytes_sent:100 bytes_acked:100 bytes_received:50\n" +
		"0 0 127.0.0.1:1 127.0.0.1:2 users:((\"we\"ird\",pid=7,fd=3))\n" +
		"\t cubic bytes_sent:1 bytes_received:2\n"
	socks, _ := parseSS(out)
	if len(socks) != 2 || socks[0].name != "next-server (v1" || socks[0].pids[0] != 295481 || socks[1].name != `we"ird` {
		t.Fatalf("socks = %+v", socks)
	}
}

func TestParseSSStopsAtTheCap(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < socketCap+10; i++ {
		fmt.Fprintf(&b, "0 0 10.0.%d.%d:%d 10.1.0.1:443 users:((\"p\",pid=1,fd=3))\n\t cubic bytes_sent:1 bytes_received:1\n", i/250, i%250, 1000+i)
	}
	socks, truncated := parseSS(b.String())
	if !truncated || len(socks) != socketCap {
		t.Fatalf("%d sockets, truncated %v", len(socks), truncated)
	}
}

func sock(local, peer, name string, pid int, rx, tx uint64) flowSocket {
	h, p := splitHostPort(peer)
	return flowSocket{local: local, peer: peer, name: name, pids: []int{pid}, rx: rx, tx: tx, peerAddr: h, peerPort: p}
}

func program(t *testing.T, res *ProcessTraffic, name string) ProgramTraffic {
	t.Helper()
	for _, p := range res.Programs {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no program %q in %+v", name, res.Programs)
	return ProgramTraffic{}
}

func TestFlowRatesAcrossReads(t *testing.T) {
	f := newFlowSampler()
	t0 := time.Unix(1_000_000, 0)

	first := f.observe(t0, []flowSocket{
		sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 4000, 1000),
		sock("10.0.0.1:2", "198.51.100.2:443", "curl", 10, 1000, 500),
		sock("10.0.0.1:3", "198.51.100.3:80", "nginx", 20, 100, 100),
	})
	if !first.Warming || first.IntervalSeconds != 0 || first.Note != trafficNote {
		t.Fatalf("first read = %+v", first)
	}
	if c := program(t, first, "curl"); c.RxRate != 0 || c.TxRate != 0 || c.RxTotal != 5000 || c.TxTotal != 1500 || c.Connections != 2 {
		t.Fatalf("a first read has totals and no rates: %+v", c)
	}

	// Two seconds on: curl's first socket moved 2000 down and 600 up, its
	// second is gone, nginx's did not move, and a new curl socket appeared
	// with 400 bytes down: young, so its bytes are this interval's.
	second := f.observe(t0.Add(2*time.Second), []flowSocket{
		sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 6000, 1600),
		sock("10.0.0.1:3", "198.51.100.3:80", "nginx", 20, 100, 100),
		sock("10.0.0.1:4", "198.51.100.4:443", "curl", 11, 400, 40),
	})
	if second.Warming || second.IntervalSeconds != 2 {
		t.Fatalf("second read = %+v", second)
	}
	c := program(t, second, "curl")
	if c.RxRate != (2000+400)/2.0 || c.TxRate != (600+40)/2.0 {
		t.Fatalf("curl rate = %v rx, %v tx", c.RxRate, c.TxRate)
	}
	if c.RxTotal != 6400 || c.TxTotal != 1640 || c.Connections != 2 || len(c.PIDs) != 2 || c.PIDs[0] != 10 || c.PIDs[1] != 11 {
		t.Fatalf("curl totals = %+v", c)
	}
	if n := program(t, second, "nginx"); n.RxRate != 0 || n.TxRate != 0 {
		t.Fatalf("an idle socket has a rate: %+v", n)
	}
	// Busiest first.
	if second.Programs[0].Name != "curl" {
		t.Fatalf("order = %v", second.Programs)
	}
	// Peers: by bytes, with their port.
	if c.Peers[0].Address != "198.51.100.1" || c.Peers[0].Port != 443 || c.Peers[0].RxBytes != 6000 {
		t.Fatalf("peers = %+v", c.Peers)
	}

	// The same four-tuple reopened: its counters went backwards, which is a
	// new connection and not negative traffic.
	third := f.observe(t0.Add(4*time.Second), []flowSocket{
		sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 200, 20),
	})
	if c := program(t, third, "curl"); c.RxRate != 100 || c.TxRate != 10 {
		t.Fatalf("a reopened tuple read as %+v", c)
	}
}

// A socket first seen after a long gap may be hours old; its lifetime bytes
// are not one interval's traffic.
func TestFlowSkipsTheFirstIntervalOfAnUnknownOldSocket(t *testing.T) {
	f := newFlowSampler()
	t0 := time.Unix(1_000_000, 0)
	f.observe(t0, []flowSocket{sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 100, 100)})

	later := f.observe(t0.Add(30*time.Second), []flowSocket{
		sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 3100, 100),
		sock("10.0.0.1:9", "198.51.100.9:443", "curl", 10, 9_000_000, 9_000_000),
	})
	if c := program(t, later, "curl"); c.RxRate != 100 || c.TxRate != 0 {
		t.Fatalf("rate = %v rx %v tx: the new socket's lifetime bytes were drawn as a burst", c.RxRate, c.TxRate)
	}
	// The next read has a baseline for it.
	next := f.observe(t0.Add(32*time.Second), []flowSocket{
		sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 3100, 100),
		sock("10.0.0.1:9", "198.51.100.9:443", "curl", 10, 9_000_400, 9_000_000),
	})
	if c := program(t, next, "curl"); c.RxRate != 200 {
		t.Fatalf("rate = %v", c.RxRate)
	}
}

func TestFlowWarmsAgainAfterALongGap(t *testing.T) {
	f := newFlowSampler()
	t0 := time.Unix(1_000_000, 0)
	f.observe(t0, []flowSocket{sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 0, 0)})
	res := f.observe(t0.Add(10*time.Minute), []flowSocket{sock("10.0.0.1:1", "198.51.100.1:443", "curl", 10, 6000, 0)})
	if !res.Warming || program(t, res, "curl").RxRate != 0 {
		t.Fatalf("a rate averaged over ten minutes: %+v", res)
	}
}

func TestFlowUnknownOwnerAndPeerCap(t *testing.T) {
	f := newFlowSampler()
	var socks []flowSocket
	for i := 0; i < 8; i++ {
		socks = append(socks, sock(fmt.Sprintf("10.0.0.1:%d", i), fmt.Sprintf("198.51.100.%d:443", i), "curl", 1, uint64(100*(i+1)), 0))
	}
	socks = append(socks, sock("10.0.0.1:99", "198.51.100.99:22", "", 0, 5, 5))
	res := f.observe(time.Unix(1, 0), socks)
	c := program(t, res, "curl")
	if len(c.Peers) != peersShown || c.Peers[0].Address != "198.51.100.7" {
		t.Fatalf("peers = %+v", c.Peers)
	}
	if u := program(t, res, "unknown"); u.Connections != 1 {
		t.Fatalf("unknown = %+v", u)
	}
}

func TestProcessesRunsSSAndAnswersABurstOfPollsOnce(t *testing.T) {
	rec := record(t)
	rec.on("ss -tinpH state established", fixture(t, "traffic-ss.txt"))
	s := testService(t)

	a, err := s.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !a.Warming || len(a.Programs) == 0 || a.Programs[0].Name == "" {
		t.Fatalf("first read = %+v", a)
	}
	b, err := s.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !b.At.Equal(a.At) {
		t.Fatal("a second poll inside a second moved the baseline")
	}
	if n := len(rec.commands()); n != 1 {
		t.Fatalf("ran ss %d times", n)
	}
}

func TestProcessesWithoutSS(t *testing.T) {
	record(t, "ss")
	s := testService(t)
	_, err := s.Processes(context.Background())
	var missing *UnavailableError
	if !errors.As(err, &missing) || missing.Tool != "ss" || missing.Package != "iproute2" {
		t.Fatalf("error = %v", err)
	}
}

// containerDB is an in-memory store with only the table the query reads.
func containerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// One connection: each connection to ":memory:" is its own database.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE metric_container_samples (
		ts INTEGER NOT NULL, name TEXT NOT NULL,
		net_rx INTEGER NOT NULL DEFAULT 0, net_tx INTEGER NOT NULL DEFAULT 0,
		network_available INTEGER DEFAULT NULL,
		PRIMARY KEY (name, ts))`); err != nil {
		t.Fatal(err)
	}
	return db
}

func addSample(t *testing.T, db *sql.DB, name string, ts int64, rx, tx int64, avail any) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO metric_container_samples(ts,name,net_rx,net_tx,network_available) VALUES(?,?,?,?,?)`,
		ts, name, rx, tx, avail); err != nil {
		t.Fatal(err)
	}
}

func TestContainersWindowArithmetic(t *testing.T) {
	db := containerDB(t)
	s := testService(t)
	s.db = db
	now := time.Unix(1_800_000_000, 0)
	at := func(ago int64) int64 { return now.Unix() - ago }

	// web: counters only grow.
	addSample(t, db, "web", at(900), 1000, 100, nil)
	addSample(t, db, "web", at(600), 4000, 400, nil)
	addSample(t, db, "web", at(60), 7000, 700, nil)
	addSample(t, db, "web", at(30), 10000, 1000, nil)
	// db restarts between its third and fourth samples: its counters begin
	// again, and what it carried since is what they show.
	addSample(t, db, "db", at(900), 50000, 5000, nil)
	addSample(t, db, "db", at(600), 60000, 6000, nil)
	addSample(t, db, "db", at(300), 500, 50, nil)
	addSample(t, db, "db", at(120), 2500, 250, nil)
	// A container whose counters were not available is not an idle one.
	addSample(t, db, "noisy", at(120), 0, 0, 0)
	addSample(t, db, "noisy", at(60), 0, 0, 0)
	// Outside the window.
	addSample(t, db, "old", at(5000), 100, 100, nil)

	res, err := s.containers(context.Background(), time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recording || res.WindowSeconds != 3600 || len(res.Containers) != 2 {
		t.Fatalf("result = %+v", res)
	}
	by := map[string]ContainerFlow{}
	for _, c := range res.Containers {
		by[c.Name] = c
	}
	web := by["web"]
	if web.RxBytes != 9000 || web.TxBytes != 900 {
		t.Fatalf("web totals = %d rx %d tx", web.RxBytes, web.TxBytes)
	}
	// The last two samples are 30 s apart and 3000 bytes down, 300 up.
	if web.RxRate != 100 || web.TxRate != 10 || web.LastSeen != at(30) {
		t.Fatalf("web current = %v rx %v tx, last seen %d", web.RxRate, web.TxRate, web.LastSeen)
	}
	d := by["db"]
	if d.RxBytes != 10000+500+2000 || d.TxBytes != 1000+50+200 {
		t.Fatalf("db across a restart = %d rx %d tx", d.RxBytes, d.TxBytes)
	}
	if d.RxRate != 2000.0/180 {
		t.Fatalf("db rate = %v", d.RxRate)
	}
	if len(web.Series) == 0 || len(web.Series) > seriesPoints {
		t.Fatalf("series = %d points", len(web.Series))
	}
	for _, p := range web.Series {
		if p.Rx < 0 || p.Tx < 0 {
			t.Fatalf("negative rate in %+v", web.Series)
		}
	}
	// Biggest first.
	if res.Containers[0].Name != "db" {
		t.Fatalf("order = %v", res.Containers)
	}
}

func TestContainersSeriesIsBounded(t *testing.T) {
	db := containerDB(t)
	s := testService(t)
	s.db = db
	now := time.Unix(1_800_000_000, 0)
	tx, _ := db.Begin()
	for i := 0; i < 3000; i++ {
		if _, err := tx.Exec(`INSERT INTO metric_container_samples(ts,name,net_rx,net_tx) VALUES(?,?,?,?)`,
			now.Unix()-int64(3000-i)*10, "web", (i+1)*1000, (i+1)*10); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res, err := s.containers(context.Background(), 24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	web := res.Containers[0]
	if len(web.Series) == 0 || len(web.Series) > seriesPoints {
		t.Fatalf("%d points", len(web.Series))
	}
	if web.RxBytes != 2999*1000 {
		t.Fatalf("total = %d", web.RxBytes)
	}
	if web.RxRate != 100 {
		t.Fatalf("rate = %v", web.RxRate)
	}
}

func TestContainersExcludeFutureSamplesAndDoNotReportOldRatesAsLive(t *testing.T) {
	db := containerDB(t)
	s := testService(t)
	s.db = db
	now := time.Unix(1_800_000_000, 0)
	addSample(t, db, "web", now.Unix()-60, 1000, 100, true)
	addSample(t, db, "web", now.Unix()-30, 4000, 400, true)
	addSample(t, db, "web", now.Unix()+30, 400000, 40000, true)
	addSample(t, db, "stopped", now.Unix()-600, 1000, 100, true)
	addSample(t, db, "stopped", now.Unix()-300, 4000, 400, true)
	res, err := s.containers(context.Background(), time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Containers {
		switch c.Name {
		case "web":
			if c.RxRate != 100 || c.TxRate != 10 || c.LastSeen != now.Unix()-30 {
				t.Errorf("future samples changed current traffic: %+v", c)
			}
		case "stopped":
			if c.RxRate != 0 || c.TxRate != 0 || c.RxBytes != 3000 {
				t.Errorf("old historical traffic reported as live: %+v", c)
			}
		}
	}
}

func TestContainerSeriesIncludesAtMostSixtyPointsAcrossPartialBuckets(t *testing.T) {
	db := containerDB(t)
	s := testService(t)
	s.db = db
	now := time.Unix(1_800_000_031, 0)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i <= 3600; i++ {
		if _, err := tx.Exec(`INSERT INTO metric_container_samples(ts,name,net_rx,net_tx) VALUES(?,?,?,?)`, now.Unix()-3600+int64(i), "web", i*100, i*10); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res, err := s.containers(context.Background(), time.Hour, now)
	if err != nil || len(res.Containers) != 1 || len(res.Containers[0].Series) > seriesPoints {
		t.Fatalf("partial buckets exceed point bound: %+v, %v", res, err)
	}
}

func TestContainersWithNothingRecorded(t *testing.T) {
	s := testService(t)
	s.db = containerDB(t)
	res, err := s.Containers(context.Background(), time.Hour)
	if err != nil || res.Recording || res.Containers == nil || len(res.Containers) != 0 {
		t.Fatalf("empty table = %+v, %v", res, err)
	}
	s.db = nil
	if res, err := s.Containers(context.Background(), time.Hour); err != nil || res.Recording {
		t.Fatalf("no store = %+v, %v", res, err)
	}
}

func TestCounterDelta(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ prev, cur, want int64 }{{10, 25, 15}, {10, 10, 0}, {100, 7, 7}, {0, 0, 0}} {
		if got := counterDelta(tc.prev, tc.cur); got != tc.want {
			t.Errorf("counterDelta(%d, %d) = %d, want %d", tc.prev, tc.cur, got, tc.want)
		}
	}
}
