package netx

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func qdiscsJSON(t *testing.T, raw string) []tcQdisc {
	t.Helper()
	qs, err := parseQdiscs(raw)
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

const fqCodelOptions = `{"limit":10240,"flows":1024,"quantum":1514,"target":4999,"interval":99999,"memory_limit":33554432,"ecn":true,"drop_batch":64}`

// The first change's verdict on a device's queues, from the same predicate
// the page shows before Apply.
func TestUnmanagedRootVerdicts(t *testing.T) {
	clear := func() (bool, error) { return true, nil }
	filtered := func() (bool, error) { return false, nil }
	unreadable := func() (bool, error) { return false, errors.New("tc: permission denied") }
	cases := []struct {
		name    string
		qdiscs  string
		filters func() (bool, error)
		def     string
		verdict string
		lines   int
		refusal string
	}{
		{"no queue at all", `[{"kind":"noqueue","handle":"0:","root":true}]`, clear, "fq_codel", "kernel", 0, ""},
		{"a multiqueue card's default", `[{"kind":"mq","handle":"0:","root":true},{"kind":"fq_codel","handle":"0:","parent":":1"},{"kind":"fq_codel","handle":"0:","parent":":2"},{"kind":"clsact","handle":"ffff:","parent":"ffff:fff1"}]`, clear, "fq_codel", "kernel", 0, ""},
		{"a multiqueue tree the default no longer matches", `[{"kind":"mq","handle":"0:","root":true},{"kind":"fq_codel","handle":"0:","parent":":1"}]`, clear, "fq", "", 0, "kernel's default is fq"},
		{"a multiqueue tree somebody replaced a queue in", `[{"kind":"mq","handle":"0:","root":true},{"kind":"fq_codel","handle":"8001:","parent":":1"}]`, clear, "fq_codel", "", 0, "multiqueue tree"},
		{"a single-queue default of another kind", `[{"kind":"pfifo_fast","handle":"0:","root":true}]`, clear, "pfifo_fast", "kernel", 0, ""},
		{"an fq somebody set", `[{"kind":"fq","handle":"8002:","root":true}]`, clear, "fq_codel", "", 0, "unmanaged fq queue"},
		{"a classless fq_codel", `[{"kind":"fq_codel","handle":"0:","root":true,"options":` + fqCodelOptions + `}]`, clear, "fq_codel", "preserved", 1, ""},
		{"an fq_codel with filters", `[{"kind":"fq_codel","handle":"8007:","root":true,"options":` + fqCodelOptions + `}]`, filtered, "fq_codel", "", 0, "recovery is unsupported"},
		{"filters that cannot be read", `[{"kind":"fq_codel","handle":"8007:","root":true,"options":` + fqCodelOptions + `}]`, unreadable, "fq_codel", "", 0, "permission denied"},
		{"an HTB hierarchy", `[{"kind":"htb","handle":"1:","root":true},{"kind":"sfq","handle":"10:","parent":"1:10"}]`, clear, "fq_codel", "", 0, "unmanaged queue hierarchy"},
		{"two roots", `[{"kind":"fq","handle":"1:","root":true},{"kind":"fq","handle":"2:","root":true}]`, clear, "fq_codel", "", 0, "multiple root queues"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines, verdict, err := unmanagedRoot("eth0", qdiscsJSON(t, c.qdiscs), c.filters, c.def)
			if c.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), c.refusal) {
					t.Fatalf("err = %v, want %q", err, c.refusal)
				}
				return
			}
			if err != nil || verdict != c.verdict || len(lines) != c.lines {
				t.Fatalf("verdict %q lines %v err %v", verdict, lines, err)
			}
		})
	}
	lines, _, _ := unmanagedRoot("eth0", qdiscsJSON(t, `[{"kind":"fq_codel","handle":"0:","root":true,"options":`+fqCodelOptions+`}]`), clear, "fq_codel")
	if lines[0] != "qdisc replace dev eth0 root handle 0: fq_codel limit 10240 flows 1024 quantum 1514 target 5000us interval 100000us memory_limit 33554432 drop_batch 64 ecn" {
		t.Fatalf("baseline = %q", lines[0])
	}
}

func TestEffectiveOptionsAndWhatMovedSinceApply(t *testing.T) {
	qs := qdiscsJSON(t, `[{"kind":"htb","handle":"1:","root":true,"options":{"r2q":10,"default":"0x10","direct_packets_stat":42,"direct_qlen":1000}},
		{"kind":"fq_codel","handle":"10:","parent":"1:10","options":`+fqCodelOptions+`}]`)
	sh := ShapeSpec{Device: "eth0", EgressKbit: 50000}
	leaf := parameterQueue(sh, qs)
	if leaf == nil || leaf.Kind != "fq_codel" {
		t.Fatalf("parameter queue = %+v", leaf)
	}
	root := effectiveOptions(qs[0])
	if _, counted := root["direct_packets_stat"]; counted || root["default"] != "0x10" {
		t.Fatalf("root options = %v", root)
	}
	applied := &AppliedQueue{Kind: "fq_codel", Handle: "10:", Options: effectiveOptions(*leaf)}
	if moved := compareApplied(applied, leaf); len(moved) != 0 {
		t.Fatalf("nothing moved, got %v", moved)
	}
	changed := *leaf
	changed.Options = map[string]json.RawMessage{}
	for k, v := range leaf.Options {
		changed.Options[k] = v
	}
	changed.Options["target"] = json.RawMessage("49999")
	delete(changed.Options, "drop_batch")
	changed.Options["ce_threshold"] = json.RawMessage("1000")
	moved := compareApplied(applied, &changed)
	if strings.Join(moved, "; ") != "ce_threshold was added; drop_batch is no longer set; target 4999 → 49999" {
		t.Fatalf("moved = %v", moved)
	}
	replaced := *leaf
	replaced.Handle = "8003:"
	if moved := compareApplied(applied, &replaced); len(moved) != 1 || !strings.Contains(moved[0], "replaced by fq_codel 8003:") {
		t.Fatalf("replaced = %v", moved)
	}
	if moved := compareApplied(applied, nil); len(moved) != 1 || !strings.Contains(moved[0], "gone") {
		t.Fatalf("gone = %v", moved)
	}
	if compareApplied(nil, leaf) != nil {
		t.Fatal("no applied queue compared as moved")
	}
}

// A queue changed in place with `tc qdisc change` passes a kind-only check;
// compared with what was read right after the apply, it is drift.
func TestVerifyShapeEntryComparesTheAppliedParameters(t *testing.T) {
	db := trafficStore(t)
	current := `[{"kind":"fq_codel","handle":"8004:","root":true,"options":` + fqCodelOptions + `}]`
	rec := record(t)
	rec.on("tc -j qdisc show dev eth0", "$q")
	prev := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := prev(ctx, name, args...)
		if out == "$q" {
			return current, nil
		}
		return out, err
	}
	t.Cleanup(func() { run = prev })
	s := New(Options{DB: db})
	ctx := context.Background()
	sh := ShapeSpec{Device: "eth0", Qdisc: "fq_codel"}

	v, applied := s.verifyShapeEntry(ctx, sh, qdiscsJSON(t, current))
	if v.Status != "verified" || applied != nil || !strings.Contains(v.Reason, "only the saved limits") {
		t.Fatalf("before anything was kept: %+v %+v", v, applied)
	}
	s.recordApplied(ctx, sh)
	v, applied = s.verifyShapeEntry(ctx, sh, qdiscsJSON(t, current))
	if v.Status != "verified" || applied == nil || applied.Handle != "8004:" || applied.Options["target"] != "4999" || v.Reason != "" {
		t.Fatalf("kept: %+v %+v", v, applied)
	}
	current = strings.Replace(current, `"target":4999`, `"target":49999`, 1)
	v, _ = s.verifyShapeEntry(ctx, sh, qdiscsJSON(t, current))
	if v.Status != "drift" || !strings.Contains(v.Reason, "target 4999 → 49999") {
		t.Fatalf("changed in place: %+v", v)
	}
	// A different entry (a temporary change its recovery took back) is not
	// compared with a queue kept for another.
	other := ShapeSpec{Device: "eth0", Qdisc: "fq_codel", EgressKbit: 0, Upload: nil, Made: Made{CreatedBy: "someone"}}
	if entryKey(other) != entryKey(sh) {
		t.Fatal("provenance changed the entry key")
	}
	if v, applied := s.verifyShapeEntry(ctx, ShapeSpec{Device: "eth0", Qdisc: "fq"}, qdiscsJSON(t, current)); applied != nil || v.Status == "verified" && strings.Contains(v.Reason, "changed outside") {
		t.Fatalf("another entry compared: %+v %+v", v, applied)
	}
	s.forgetApplied(ctx, "eth0")
	if a, _ := s.appliedQueue(ctx, sh); a != nil {
		t.Fatal("a cleared device kept its applied queue")
	}
}

func TestUploadProfileIsValidatedRenderedAndVerified(t *testing.T) {
	p, err := normUploadProfile(UploadProfile{})
	if err != nil || p.Diffserv != "besteffort" || p.FlowMode != "dual-srchost" || p.LinkLayer != "noatm" || p.RTTMillis != 100 {
		t.Fatalf("defaults = %+v %v", p, err)
	}
	for _, bad := range []UploadProfile{{Diffserv: "diffserv8"}, {FlowMode: "dual-dsthost"}, {LinkLayer: "docsis"}, {Overhead: 300}, {MPU: -1}, {RTTMillis: 5}} {
		if _, err := normUploadProfile(bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if _, err := normShape(ShapeSpec{Device: "eth0", Qdisc: "fq_codel", EgressKbit: 5000, Upload: &UploadProfile{}}); err == nil || !strings.Contains(err.Error(), "needs CAKE") {
		t.Fatalf("an upload profile on fq_codel = %v", err)
	}
	if _, err := normShape(ShapeSpec{Device: "eth0", Qdisc: "cake", Upload: &UploadProfile{}}); err == nil {
		t.Fatal("an upload profile without a limit was accepted")
	}
	sh, err := normShape(ShapeSpec{Device: "eth0", Qdisc: "cake", EgressKbit: 20000, Upload: &UploadProfile{Diffserv: "diffserv4", NAT: true, AckFilter: true, Overhead: 34, MPU: 64, LinkLayer: "ptm", RTTMillis: 50}})
	if err != nil {
		t.Fatal(err)
	}
	lines := shapeLines(sh)
	if got := lines[len(lines)-1]; got != "qdisc replace dev eth0 root cake bandwidth 20000kbit diffserv4 dual-srchost nat nowash split-gso ack-filter ptm overhead 34 mpu 64 rtt 50ms" {
		t.Fatalf("rendered %q", got)
	}
	// An older entry keeps its bare bandwidth line.
	if got := shapeLines(ShapeSpec{Device: "eth0", Qdisc: "cake", EgressKbit: 20000}); got[1] != "qdisc replace dev eth0 root cake bandwidth 20000kbit" {
		t.Fatalf("bare = %v", got)
	}

	options := map[string]any{"bandwidth": shapeBytes(20000), "diffserv": "diffserv4", "flowmode": "dual-srchost", "nat": true, "wash": false,
		"ingress": false, "ack-filter": "enabled", "split_gso": true, "rtt": 50000, "raw": false, "atm": "ptm", "overhead": 34, "mpu": 64, "fwmark": "0"}
	raw := func(o map[string]any) map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for k, v := range o {
			b, _ := json.Marshal(v)
			out[k] = b
		}
		return out
	}
	if err := checkUploadCake("eth0", raw(options), 20000, *sh.Upload); err != nil {
		t.Fatalf("matching profile: %v", err)
	}
	for key, wrong := range map[string]any{"diffserv": "besteffort", "flowmode": "triple-isolate", "nat": false, "wash": true, "ack-filter": "disabled",
		"atm": "noatm", "overhead": 0, "mpu": 0, "rtt": 100000, "ingress": true, "bandwidth": shapeBytes(10000)} {
		changed := map[string]any{}
		for k, v := range options {
			changed[k] = v
		}
		changed[key] = wrong
		if err := checkUploadCake("eth0", raw(changed), 20000, *sh.Upload); !errors.Is(err, errShapingDrift) || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%v: %v", key, wrong, err)
		}
	}

	// verifyShaping reads the root and checks the profile beside the bandwidth.
	b, _ := json.Marshal([]map[string]any{{"kind": "cake", "handle": "8005:", "root": true, "options": options}})
	record(t).on("tc -j qdisc show dev eth0", string(b))
	if err := verifyShaping(context.Background(), sh); err != nil {
		t.Fatalf("verify: %v", err)
	}
	options["diffserv"] = "diffserv3"
	b, _ = json.Marshal([]map[string]any{{"kind": "cake", "handle": "8005:", "root": true, "options": options}})
	record(t).on("tc -j qdisc show dev eth0", string(b))
	if err := verifyShaping(context.Background(), sh); !errors.Is(err, errShapingDrift) {
		t.Fatalf("a changed class mode verified: %v", err)
	}
}

// CAKE times its own packets per class: that is the latency evidence a
// shaper can give about its own queue.
func TestCakeTinsCarryMeasuredQueueDelay(t *testing.T) {
	qs := qdiscsJSON(t, `[{"kind":"cake","handle":"8005:","root":true,"options":{"diffserv":"diffserv3"},"bytes":1000,"packets":10,
		"tins":[{"threshold_rate":625000,"sent_bytes":100,"peak_delay_us":9000,"avg_delay_us":1200,"base_delay_us":40,"sent_packets":3,"drops":1,"ecn_mark":2,"sparse_flows":1,"bulk_flows":2},
		{"threshold_rate":2500000,"sent_bytes":800,"peak_delay_us":4100,"avg_delay_us":800,"base_delay_us":20,"sent_packets":6},
		{"threshold_rate":625000,"sent_bytes":100,"peak_delay_us":300,"avg_delay_us":90,"base_delay_us":10,"sent_packets":1}]}]`)
	stat := qdiscStat(qs[0])
	if len(stat.Tins) != 3 || stat.Tins[0].Name != "Bulk" || stat.Tins[1].Name != "Best effort" || stat.Tins[2].Name != "Voice" {
		t.Fatalf("tins = %+v", stat.Tins)
	}
	if b := stat.Tins[0]; b.PeakDelayUs != 9000 || b.AvgDelayUs != 1200 || b.Drops != 1 || b.ECNMarks != 2 || b.BulkFlows != 2 || b.ThresholdBs != 625000 {
		t.Fatalf("bulk tin = %+v", b)
	}
	if fq := qdiscStat(tcQdisc{Kind: "fq_codel"}); fq.Tins != nil {
		t.Fatal("fq_codel claimed tins")
	}
}

func TestParseOffloadReadsTheFeaturesThatChangeADownloadQueue(t *testing.T) {
	o := parseOffload("Features for eth0:\nrx-checksumming: on\ntcp-segmentation-offload: on\n\ttx-tcp-segmentation: on\ngeneric-segmentation-offload: on\ngeneric-receive-offload: on\nlarge-receive-offload: off [fixed]\n")
	if !o.Checked || o.GRO != "on" || o.LRO != "off (fixed)" || o.GSO != "on" || o.TSO != "on" {
		t.Fatalf("offload = %+v", o)
	}
	record(t, "ethtool")
	if o := readOffload(context.Background(), "eth0"); o.Checked || !strings.Contains(o.Error, "not installed") {
		t.Fatalf("without ethtool = %+v", o)
	}
}

// The view says what the next change does with each device's queues.
func TestShapingViewSaysWhatTheNextChangeDoes(t *testing.T) {
	h := newShapeHost(t)
	sp := emptySpec()
	sp.Shaping = []ShapeSpec{{Device: "eth0", EgressKbit: 50000, IngressKbit: 100000}}
	h.seed(t, sp)
	v, err := h.Shaping(context.Background(), gwClient)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ShapeDevice{}
	for _, d := range v.Devices {
		by[d.Name] = d
	}
	if d := by["eth0"]; d.Ownership.Verdict != "managed" || len(d.Tree) != 3 || d.Effective["limit"] != "10240" {
		t.Fatalf("eth0 = %+v", d)
	}
	if d := by["tailscale0"]; d.Ownership.Verdict != "preserved" || !strings.Contains(d.Ownership.Reason, "fq_codel") || d.Effective["target"] == "" {
		t.Fatalf("tailscale0 = %+v", d.Ownership)
	}
	if d := by["docker0"]; d.Ownership.Verdict != "kernel" {
		t.Fatalf("docker0 = %+v", d.Ownership)
	}
	if d := by["lo"]; d.Ownership.Verdict != "refused" || d.Ownership.Reason == "" {
		t.Fatalf("lo = %+v", d.Ownership)
	}
}
