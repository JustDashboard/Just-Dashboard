package netx

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

const procNetDevSample = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:    1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0: 5000000    4000    2    5    0     0          0         0  3000000    3500    0    0    0     0       0          0
`

func TestParseProcNetDev(t *testing.T) {
	got := parseProcNetDev(strings.NewReader(procNetDevSample))
	if c := got["eth0"]; c.rxBytes != 5000000 || c.txBytes != 3000000 || c.rxErrs != 2 || c.rxDrop != 5 || c.txPackets != 3500 {
		t.Fatalf("eth0 = %+v", c)
	}
	if len(got) != 2 {
		t.Fatalf("the two header lines are not devices: %v", got)
	}
}

func TestSamplerRatesAndRingAndResets(t *testing.T) {
	s := newSampler(nil, nil, 0, 0)
	t0 := time.Unix(1_000_000, 0)
	s.observe(t0, map[string]devCounters{"eth0": {rxBytes: 1000, txBytes: 500}, "veth1": {}})
	if len(s.Rates()) != 0 {
		t.Fatal("one reading is not a rate")
	}
	s.observe(t0.Add(2*time.Second), map[string]devCounters{"eth0": {rxBytes: 5000, txBytes: 1500}, "veth1": {}})
	if r := s.Rates()["eth0"]; r.Rx != 2000 || r.Tx != 500 {
		t.Fatalf("rate = %+v", r)
	}
	// A recreated device restarts its counters; that is not a burst.
	s.observe(t0.Add(4*time.Second), map[string]devCounters{"eth0": {rxBytes: 10, txBytes: 10}})
	if r := s.Rates()["eth0"]; r.Rx != 0 || r.Tx != 0 {
		t.Fatalf("a reset read as %+v", r)
	}
	if _, ok := s.Rates()["veth1"]; ok {
		t.Fatal("a device that went away keeps no ring")
	}
	if pts := s.Live(t0.Add(2 * time.Second).Unix()); len(pts["eth0"]) != 1 {
		t.Fatalf("since filters the ring: %v", pts["eth0"])
	}
}

func TestSamplerDoesNotAccumulateDisabledHistory(t *testing.T) {
	s := newSampler(nil, nil, 0, 0)
	now := time.Unix(1_000_000, 0)
	for i := 0; i < liveKeep*2; i++ {
		s.observe(now.Add(time.Duration(i)*liveStep), map[string]devCounters{"eth0": {rxBytes: uint64(i * 100)}})
	}
	if len(s.pending) != 0 || len(s.Live(0)["eth0"]) != liveKeep {
		t.Fatalf("disabled history retained %d pending devices and %d live points", len(s.pending), len(s.Live(0)["eth0"]))
	}
}

func TestSamplerStopIsConcurrentAndIdempotent(t *testing.T) {
	s := newSampler(nil, nil, 0, 0)
	s.Start(context.Background())
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(s.Stop)
	}
	wg.Wait()
	s.Stop()
}

func TestInterfaceHistoryHonorsItsPointLimitAtInclusiveWindowEnds(t *testing.T) {
	db := containerDB(t)
	_, err := db.Exec(`CREATE TABLE metric_interface_samples (
		ts INTEGER, iface TEXT, rx_rate REAL, tx_rate REAL,
		rx_peak REAL, tx_peak REAL, rx_errors INTEGER, tx_errors INTEGER,
		rx_dropped INTEGER, tx_dropped INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<3600)
		INSERT INTO metric_interface_samples
		SELECT ?+i, 'eth0', 100, 10, 100, 10, 0, 0, 0, 0 FROM n`, time.Now().Unix()-3600)
	if err != nil {
		t.Fatal(err)
	}
	s := newSampler(db, nil, liveStep, time.Hour)
	history, err := s.History(context.Background(), time.Hour, 60, "eth0")
	if err != nil || len(history.Interfaces["eth0"]) > 60 || len(history.Interfaces["eth0"]) == 0 {
		t.Fatalf("history exceeds requested point bound: %+v, %v", history, err)
	}
}
