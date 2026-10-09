package netsec

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func metricValue(res *ProbeResult, key string) (float64, bool) {
	for _, m := range res.Metrics {
		if m.Key == key {
			return m.Value, true
		}
	}
	return 0, false
}

func factValue(res *ProbeResult, label string) string {
	for _, f := range res.Facts {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}

func hasFinding(res *ProbeResult, id string) bool {
	for _, f := range res.Findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func hasLink(res *ProbeResult, href string) bool {
	for _, l := range res.Links {
		if l.Href == href {
			return true
		}
	}
	return false
}

func stageStatus(res *ProbeResult, id string) string {
	for _, st := range res.Stages {
		if st.ID == id {
			return st.Status
		}
	}
	return ""
}

func tableByID(res *ProbeResult, id string) *ProbeTable {
	for i := range res.Tables {
		if res.Tables[i].ID == id {
			return &res.Tables[i]
		}
	}
	return nil
}

const pingAllReplies = `PING example.com (93.184.216.34) 56(84) bytes of data.
64 bytes from 93.184.216.34: icmp_seq=1 ttl=57 time=10.0 ms
64 bytes from 93.184.216.34: icmp_seq=2 ttl=57 time=12.0 ms
64 bytes from 93.184.216.34: icmp_seq=3 ttl=57 time=11.0 ms
64 bytes from 93.184.216.34: icmp_seq=4 ttl=57 time=15.0 ms

--- example.com ping statistics ---
4 packets transmitted, 4 received, 0% packet loss, time 3004ms
rtt min/avg/max/mdev = 10.000/12.000/15.000/1.870 ms`

func TestPingParsesLossJitterAndRepliesFromIPv4AndIPv6(t *testing.T) {
	st := parsePing(pingAllReplies)
	if st.Address != "93.184.216.34" || st.Transmitted != 4 || st.Received != 4 || st.Loss != 0 || st.Avg != 12 || st.Mdev != 1.87 || len(st.Replies) != 4 {
		t.Fatalf("ipv4 = %+v", st)
	}
	if jitter, ok := replyJitter(st.Replies); !ok || jitter != 2.333 {
		t.Fatalf("jitter = %v %v", jitter, ok)
	}
	v6 := parsePing("PING 2606:4700:4700::1111 (2606:4700:4700::1111) 56 data bytes\n64 bytes from 2606:4700:4700::1111: icmp_seq=1 ttl=58 time=3.21 ms\n\n--- 2606:4700:4700::1111 ping statistics ---\n4 packets transmitted, 1 received, 75% packet loss, time 3050ms\nrtt min/avg/max/mdev = 3.210/3.210/3.210/0.000 ms")
	if v6.Address != "2606:4700:4700::1111" || v6.Received != 1 || v6.Loss != 75 || len(v6.Replies) != 1 || v6.Replies[0].MS != 3.21 {
		t.Fatalf("ipv6 = %+v", v6)
	}
	busybox := parsePing("PING 10.0.0.1 (10.0.0.1): 56 data bytes\n64 bytes from 10.0.0.1: seq=0 ttl=64 time=0.512 ms\n\n--- 10.0.0.1 ping statistics ---\n1 packets transmitted, 1 packets received, 0% packet loss\nround-trip min/avg/max = 0.512/0.512/0.512 ms")
	if !busybox.Parsed || !busybox.HasRTT || busybox.HasMdev || busybox.Avg != 0.512 || len(busybox.Replies) != 1 {
		t.Fatalf("busybox = %+v", busybox)
	}
}

func TestPingVerdictsNeverCallUnansweredEchoDown(t *testing.T) {
	stubLANDiagnostics(t)
	cases := []struct {
		name, out string
		err       error
		ok        bool
		verdict   string
		finding   string
	}{
		{"all replies", pingAllReplies, nil, true, ProbeOK, ""},
		{"partial loss", strings.Replace(pingAllReplies, "4 received, 0% packet loss", "3 received, 25% packet loss", 1), nil, true, ProbeFindings, ""},
		{"filtered", "PING 192.0.2.9 (192.0.2.9) 56(84) bytes of data.\n\n--- 192.0.2.9 ping statistics ---\n4 packets transmitted, 0 received, 100% packet loss, time 3065ms\n", errors.New("exit status 1"), false, ProbeUnknown, "icmp-filtered"},
		{"unreachable", "PING 192.0.2.9 (192.0.2.9) 56(84) bytes of data.\nFrom 192.0.2.1 icmp_seq=1 Destination Host Unreachable\n\n--- 192.0.2.9 ping statistics ---\n4 packets transmitted, 0 received, +4 errors, 100% packet loss, time 3065ms\n", errors.New("exit status 1"), false, ProbeFailed, "icmp-errors"},
		{"no such host", "ping: nope.invalid: Name or service not known", errors.New("exit status 2"), false, ProbeFailed, ""},
	}
	for _, tc := range cases {
		diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
			if cmd != "ping" || !reflect.DeepEqual(args, []string{"-n", "-c", "4", "-W", "2", "-w", "12", "target.example"}) {
				t.Fatalf("argv = %s %v", cmd, args)
			}
			return tc.out, "3s", tc.err
		}
		res, err := New().Ping(t.Context(), "target.example")
		if err != nil || res.OK != tc.ok || res.Verdict != tc.verdict || (tc.finding != "" && !hasFinding(res, tc.finding)) {
			t.Fatalf("%s: %+v %v", tc.name, res, err)
		}
		if tc.verdict == ProbeUnknown && (res.Error != "" || !strings.Contains(res.Summary, "does not show") || !hasLink(res, "/network/tools?tool=port&target=target.example")) {
			t.Fatalf("filtered ping over-claims: %+v", res)
		}
		if loss, ok := metricValue(res, "loss"); tc.name == "partial loss" && (!ok || loss != 25) {
			t.Fatalf("loss metric = %v %v", loss, ok)
		}
	}
	if _, err := New().Ping(t.Context(), "-c 100 x"); err == nil {
		t.Fatal("accepted an option-shaped target")
	}
}

const tracerouteReached = `traceroute to example.com (93.184.216.34), 20 hops max, 60 byte packets
 1  192.168.1.1  0.512 ms
 2  *
 3  10.0.0.1  5.123 ms !X
 4  93.184.216.34  11.020 ms`

func TestTracerouteBuildsHopTableAndComparableRecords(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticHas = func(name string) bool { return name == "traceroute" }
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "traceroute" || !reflect.DeepEqual(args, []string{"-n", "-w", "2", "-q", "1", "-m", "20", "example.com"}) {
			t.Fatalf("argv = %s %v", cmd, args)
		}
		return tracerouteReached, "4s", nil
	}
	res, err := New().Traceroute(t.Context(), "example.com")
	if err != nil || !res.OK || res.Verdict != ProbeOK {
		t.Fatalf("%+v %v", res, err)
	}
	if !reflect.DeepEqual(res.Records, []string{"hop 1 192.168.1.1", "hop 2 no reply", "hop 3 10.0.0.1", "hop 4 93.184.216.34"}) {
		t.Fatalf("records = %v", res.Records)
	}
	hops := tableByID(res, "hops")
	if hops == nil || len(hops.Rows) != 4 || hops.Rows[2][3] != "administratively prohibited" || hops.Rows[1][1] != "no reply" {
		t.Fatalf("hop table = %+v", hops)
	}
	if v, _ := metricValue(res, "reached"); v != 1 {
		t.Fatalf("reached metric = %v", v)
	}
	if v, _ := metricValue(res, "responding_hops"); v != 3 {
		t.Fatalf("responding = %v", v)
	}

	diagnosticRun = func(context.Context, time.Duration, string, ...string) (string, string, error) {
		return "traceroute to 192.0.2.9 (192.0.2.9), 20 hops max, 60 byte packets\n 1  192.168.1.1  0.5 ms\n 2  *\n 3  *", "40s", nil
	}
	res, _ = New().Traceroute(t.Context(), "192.0.2.9")
	if !res.OK || res.Verdict != ProbeUnknown || !strings.Contains(res.Summary, "does not show") || !hasLink(res, "/network/tools?tool=port&target=192.0.2.9") {
		t.Fatalf("unreached destination over-claims: %+v", res)
	}
}

func TestTracepathFallbackMergesDuplicateHopsAndReadsPMTU(t *testing.T) {
	out := ` 1?: [LOCALHOST]                      pmtu 1500
 1:  192.168.1.1                                           0.512ms
 1:  192.168.1.1                                           0.400ms pmtu 1492
 2:  no reply
 3:  10.0.0.1                                              5.123ms asymm  4
 4:  2001:db8::9                                           9.800ms reached
     Resume: pmtu 1492 hops 4 back 4 `
	r := parseTracepath(out)
	if r.LocalMTU != 1500 || r.PMTU != 1492 || r.PMTUHop != 1 || !r.Reached || !r.Resume || r.ResumeHops != 4 || len(r.Hops) != 4 {
		t.Fatalf("report = %+v", r)
	}
	if len(r.Hops[0].RTTs) != 2 || r.Hops[1].Address != "" || r.Hops[2].Notes[0] != "asymmetric return path (4 hops back)" {
		t.Fatalf("hops = %+v", r.Hops)
	}
	stubLANDiagnostics(t)
	diagnosticHas = func(name string) bool { return name == "tracepath" }
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "tracepath" {
			t.Fatalf("fallback ran %s", cmd)
		}
		return out, "3s", nil
	}
	res, err := New().Traceroute(t.Context(), "2001:db8::9")
	if err != nil || res.Verdict != ProbeOK || !strings.Contains(factValue(res, "Tool"), "tracepath") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestPathMTUSeparatesDiscoveredFromUnknownAndFiltered(t *testing.T) {
	discovered := parseTracepath(" 1?: [LOCALHOST] pmtu 1500\n 1:  10.0.0.1  1.0ms pmtu 1420\n 2:  192.0.2.9  9.0ms reached\n     Resume: pmtu 1420 hops 2 back 2")
	res := &ProbeResult{}
	interpretPathMTU(res, discovered, "192.0.2.9", nil)
	if res.Verdict != ProbeOK || factValue(res, "Path MTU") != "1420 bytes" || !hasFinding(res, "smaller-mtu") {
		t.Fatalf("discovered = %+v", res)
	}
	if v, ok := metricValue(res, "pmtu"); !ok || v != 1420 {
		t.Fatalf("pmtu metric = %v %v", v, ok)
	}

	partial := parseTracepath(" 1?: [LOCALHOST] pmtu 1500\n 1:  10.0.0.1  1.0ms\n 2:  no reply\n 3:  no reply\n     Too many hops: pmtu 1500\n     Resume: pmtu 1500")
	res = &ProbeResult{}
	interpretPathMTU(res, partial, "192.0.2.9", nil)
	if res.Verdict != ProbeUnknown || !strings.Contains(factValue(res, "Path MTU"), "unknown") {
		t.Fatalf("partial = %+v", res)
	}
	if _, ok := metricValue(res, "pmtu"); ok {
		t.Fatal("an unknown path MTU was recorded as a measurement")
	}

	filtered := parseTracepath(" 1?: [LOCALHOST] pmtu 1500\n 1:  no reply\n 2:  no reply")
	res = &ProbeResult{}
	interpretPathMTU(res, filtered, "192.0.2.9", nil)
	if res.Verdict != ProbeUnknown || !strings.Contains(res.Summary, "filtered") {
		t.Fatalf("filtered = %+v", res)
	}
}

func TestRouteGetParsesKernelDecisionInAnyKeyOrder(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want routeGet
	}{
		{"1.1.1.1 via 192.168.1.1 dev eth0 src 192.168.1.10 uid 1000 \n    cache ", routeGet{Type: "unicast", Destination: "1.1.1.1", Gateway: "192.168.1.1", Device: "eth0", Source: "192.168.1.10", Table: "main"}},
		{"2606:4700:4700::1111 from :: via fe80::1 dev eth0 proto ra src 2001:db8::2 metric 1024 pref medium", routeGet{Type: "unicast", Destination: "2606:4700:4700::1111", Gateway: "fe80::1", Device: "eth0", Source: "2001:db8::2", Table: "main", Protocol: "ra", Metric: "1024"}},
		{"local 127.0.0.1 dev lo table local src 127.0.0.1 uid 0 \n    cache <local> ", routeGet{Type: "local", Destination: "127.0.0.1", Device: "lo", Source: "127.0.0.1", Table: "local"}},
		{"10.9.0.1 dev wg0 table 51820 src 10.9.0.2 uid 0", routeGet{Type: "unicast", Destination: "10.9.0.1", Device: "wg0", Source: "10.9.0.2", Table: "51820"}},
	} {
		got, ok := parseRouteGet(tc.out)
		if !ok || got != tc.want {
			t.Errorf("%q => %+v, want %+v", tc.out, got, tc.want)
		}
	}
	res := &ProbeResult{}
	interpretRoute(res, "RTNETLINK answers: Network is unreachable", errors.New("exit status 2"))
	if res.OK || res.Verdict != ProbeFailed || !strings.Contains(res.Summary, "no usable route") {
		t.Fatalf("unreachable = %+v", res)
	}
	res = &ProbeResult{}
	interpretRoute(res, "blackhole 203.0.113.9 dev lo table main", nil)
	if res.OK || res.Verdict != ProbeFailed || factValue(res, "Route type") != "blackhole" {
		t.Fatalf("blackhole = %+v", res)
	}
	res = &ProbeResult{}
	interpretRoute(res, "1.1.1.1 via 192.168.1.1 dev eth0 src 192.168.1.10 uid 1000", nil)
	if !res.OK || factValue(res, "Selected source") != "192.168.1.10" || factValue(res, "Next hop") != "192.168.1.1" ||
		!reflect.DeepEqual(res.Records, []string{"dev eth0", "src 192.168.1.10", "via 192.168.1.1", "table main", "type unicast"}) {
		t.Fatalf("route = %+v", res)
	}
}
