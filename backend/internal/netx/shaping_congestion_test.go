package netx

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Sockets keep the algorithm they opened with, so after a switch the host
// runs two; each is its own group, loopback left out.
func TestCongestionGroupsCompareAlgorithmsOnTheSameTraffic(t *testing.T) {
	mk := func(peer, cc string, rtt float64, retrans, segs, tx uint64, bps float64) flowSocket {
		return flowSocket{peerAddr: peer, congestion: cc, rttMs: rtt, hasRTT: rtt > 0, retransTotal: retrans, segsOut: segs, tx: tx,
			deliveryBps: bps, hasDelivery: bps > 0}
	}
	c := groupCongestion([]flowSocket{
		mk("198.51.100.1", "cubic", 40, 10, 1000, 5000, 20e6),
		mk("198.51.100.2", "cubic", 80, 30, 1000, 7000, 10e6),
		mk("198.51.100.3", "cubic", 60, 0, 2000, 1000, 0),
		mk("198.51.100.4", "bbr", 20, 1, 1000, 9000, 50e6),
		mk("127.0.0.1", "cubic", 0.01, 0, 10, 10, 1e9),
		mk("198.51.100.5", "", 0, 0, 0, 0, 0),
	}, false, time.Unix(100, 0), "bbr")
	if c.Default != "bbr" || c.Loopback != 1 || len(c.Groups) != 3 {
		t.Fatalf("comparison = %+v", c)
	}
	cubic := c.Groups[0]
	if cubic.Algorithm != "cubic" || cubic.Sockets != 3 || cubic.MedianRTT != 60 || cubic.P90RTT != 80 ||
		cubic.RetransmitShare != 0.01 || cubic.MedianDeliveryMbit != 10 || cubic.SegmentsOut != 4000 || cubic.BytesSent != 13000 {
		t.Fatalf("cubic = %+v", cubic)
	}
	if bbr := c.Groups[1]; bbr.Algorithm != "bbr" || bbr.Sockets != 1 || bbr.MedianRTT != 20 || bbr.RetransmitShare != 0.001 || bbr.MedianDeliveryMbit != 50 {
		t.Fatalf("bbr = %+v", bbr)
	}
	if unknown := c.Groups[2]; unknown.Algorithm != "unknown" || unknown.RetransmitShare != 0 {
		t.Fatalf("unknown = %+v", unknown)
	}
}

// Switching keeps the groups as they were just before, and the view lists
// the kept comparisons newest first beside the one now.
func TestBBRSwitchKeepsTheComparisonBeforeIt(t *testing.T) {
	h := newShapeHost(t)
	h.db = trafficStore(t)
	h.rec.on("ss -tinH state established", fixture(t, "traffic-ss.txt"))
	h.rec.on("sysctl -w", "")
	h.rec.on("sysctl -n net.ipv4.tcp_congestion_control", "bbr")
	h.rec.on("sysctl -n net.core.default_qdisc", "fq")
	ctx := context.Background()
	if err := h.SetBBR(ctx, true, "ops"); err != nil {
		t.Fatal(err)
	}
	v, err := h.Congestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Snapshots) != 1 || v.Note == "" {
		t.Fatalf("view = %+v", v)
	}
	snap := v.Snapshots[0]
	if snap.Before != "cubic" || snap.After != "bbr" || snap.Actor != "ops" || snap.Comparison.Default != "cubic" || len(snap.Comparison.Groups) == 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Setting what is already set is not a switch and keeps nothing more.
	h.writeSys("net/ipv4/tcp_congestion_control", "bbr")
	if err := h.SetBBR(ctx, true, "ops"); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.Congestion(ctx); len(v.Snapshots) != 1 {
		t.Fatalf("a non-switch kept a snapshot: %d", len(v.Snapshots))
	}
	// At most the newest twenty are kept.
	for i := 0; i < congestionKept+5; i++ {
		h.keepCongestion(ctx, CongestionComparison{At: time.Unix(int64(1000+i), 0), Groups: []CongestionGroup{}}, "cubic", "bbr", "ops")
	}
	v, _ = h.Congestion(ctx)
	if len(v.Snapshots) != congestionKept {
		t.Fatalf("kept %d", len(v.Snapshots))
	}
}

func TestCongestionWithoutSSSaysSo(t *testing.T) {
	record(t, "ss")
	v, err := testService(t).Congestion(context.Background())
	if err != nil || !strings.Contains(v.Now.Error, "not installed") || v.Snapshots == nil {
		t.Fatalf("view = %+v %v", v, err)
	}
}
