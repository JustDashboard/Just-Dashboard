package netsec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures named suricata-eve-stats.json, suricata-default-debian.txt and
// suricata-yaml-afpacket.txt come from Suricata 7.0.10 as Debian 12 packages
// it, run in a throwaway container: its own stats event, its defaults file
// and the af-packet section of its suricata.yaml. The queue rules were made
// with iptables and nft inside a throwaway network namespace.

type recordingLines struct{ status, lines []string }

func (r *recordingLines) Status(format string, args ...any) {
	r.status = append(r.status, fmt.Sprintf(format, args...))
}
func (r *recordingLines) Line(_, text string) { r.lines = append(r.lines, text) }

func TestLatestCaptureReadsTheNewestStatsEvent(t *testing.T) {
	stats := testdata(t, "suricata-eve-stats.json")
	eve := `{"timestamp":"2026-10-09T21:10:00.000000+0000","event_type":"stats","stats":{"uptime":10,"capture":{"kernel_packets":1},"decoder":{"pkts":1}}}` + "\n" +
		`{"timestamp":"2026-10-09T21:11:00.000000+0000","event_type":"alert","alert":{"signature":"x"}}` + "\n" + stats
	c := latestCapture([]byte(eve))
	if c == nil || c.Uptime != 400 || c.KernelPackets != 67 || c.KernelDrops != 0 || c.DecoderPackets != 67 || c.At != "2026-10-09T21:14:43.534959Z" {
		t.Fatalf("capture=%+v", c)
	}
	if latestCapture([]byte(`{"event_type":"alert"}`)) != nil {
		t.Fatal("a log with no stats event invented a capture")
	}
}

func TestAfPacketInterfacesAreReadAndRewrittenLineForLine(t *testing.T) {
	yaml := testdata(t, "suricata-yaml-afpacket.txt")
	if got := afPacketInterfaces(yaml); strings.Join(got, ",") != "eth0" {
		t.Fatalf("interfaces=%v", got)
	}
	next, previous, err := setAfPacketInterface(yaml, "ens3")
	if err != nil || previous != "eth0" {
		t.Fatalf("rewrite=%q %v", previous, err)
	}
	if strings.Join(afPacketInterfaces(next), ",") != "ens3" || !strings.Contains(next, "  - interface: default") {
		t.Fatalf("rewritten interfaces=%v", afPacketInterfaces(next))
	}
	before, after := strings.Split(yaml, "\n"), strings.Split(next, "\n")
	changed := 0
	for i := range before {
		if before[i] != after[i] {
			changed++
		}
	}
	if len(before) != len(after) || changed != 1 {
		t.Fatalf("%d lines changed of %d/%d", changed, len(before), len(after))
	}
	if _, _, err := setAfPacketInterface("pcap:\n  - interface: eth0\n", "ens3"); err == nil {
		t.Fatal("a pcap interface was taken for af-packet's")
	}
}

func TestQueueRulesAreReadWithTheirFailOpenFlag(t *testing.T) {
	ipt := parseIptablesQueues(testdata(t, "suricata-iptables-save.txt"), "iptables-save")
	if len(ipt) != 2 || ipt[0].Chain != "INPUT" || ipt[0].Queue != "1:3" || ipt[0].Bypass || ipt[1].Chain != "FORWARD" || ipt[1].Queue != "0" || !ipt[1].Bypass {
		t.Fatalf("iptables=%+v", ipt)
	}
	nft := parseNFTQueues(testdata(t, "suricata-nft-queue.json"))
	if len(nft) != 2 || nft[0].Queue != "0" || !nft[0].Bypass || nft[1].Queue != "1:3" || nft[1].Bypass || nft[0].Chain != "inet suricata forward" {
		t.Fatalf("nft=%+v", nft)
	}
}

func TestInlineEvidenceSaysWhetherAStoppedSuricataCutsTraffic(t *testing.T) {
	h := newSuricataHost(t)
	h.iptables = testdata(t, "suricata-iptables-save.txt")
	in := readInline(t.Context())
	if in.FailOpen || len(in.Queues) != 2 || !strings.Contains(in.Words, "without bypass drops") {
		t.Fatalf("mixed bypass=%+v", in)
	}
	h.iptables = "-A FORWARD -j NFQUEUE --queue-num 0 --queue-bypass\n"
	if in := readInline(t.Context()); !in.FailOpen || !strings.Contains(in.Words, "passes uninspected") {
		t.Fatalf("fail-open=%+v", in)
	}
	h.iptables = ""
	if in := readInline(t.Context()); in.FailOpen || len(in.Queues) != 0 || !strings.Contains(in.Words, "No rule sends packets") {
		t.Fatalf("no queue=%+v", in)
	}
}

func TestSuricataViewCarriesItsSetup(t *testing.T) {
	h := newSuricataHost(t)
	h.file("suricata.yaml", testdata(t, "suricata-yaml-afpacket.txt"))
	h.file("default-suricata", testdata(t, "suricata-default-debian.txt"))
	h.file("suricata.rules", testdata(t, "suricata-rules.txt"))
	h.file("eve.json", testdata(t, "suricata-eve-stats.json"))
	if err := os.MkdirAll(filepath.Join(h.dir, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.file("sources/et-open.yaml", "source: et/open\n")
	v, err := New().Suricata(t.Context(), func(p string) (string, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != "ids" || v.Capture == nil || v.Capture.KernelPackets != 67 {
		t.Fatalf("mode=%s capture=%+v", v.Mode, v.Capture)
	}
	if strings.Join(v.Interfaces.Configured, ",") != "eth0" || !v.Interfaces.Editable {
		t.Fatalf("interfaces=%+v", v.Interfaces)
	}
	if strings.Join(v.Rules.Sources, ",") != "et/open" || v.Rules.UpdatedAt == "" || !v.Rules.Updater {
		t.Fatalf("rules=%+v", v.Rules)
	}
	h.execs = testdata(t, "suricata-execstart-ips.txt")
	if v, _ := New().Suricata(t.Context(), nil); v.Interfaces.Editable || !strings.Contains(v.Interfaces.Reason, "queue") {
		t.Fatalf("inline mode offered an interface: %+v", v.Interfaces)
	}
}

// longHost stands a transcript behind runLong as well as run.
func longHost(t *testing.T, h *suricataHost, answers map[string]error, after func(string)) *[]string {
	t.Helper()
	var calls []string
	prev := runLong
	runLong = func(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
		call := strings.TrimSpace(name + " " + strings.Join(args, " "))
		calls = append(calls, call)
		if after != nil {
			after(call)
		}
		for prefix, err := range answers {
			if strings.HasPrefix(call, prefix) {
				return call + " said no", err
			}
		}
		return "", nil
	}
	t.Cleanup(func() { runLong = prev })
	return &calls
}

func planOnHost(t *testing.T, h *suricataHost, iface string) (*SuricataInterfacePlan, error) {
	t.Helper()
	h.file("suricata.yaml", testdata(t, "suricata-yaml-afpacket.txt"))
	plan, err := New().PlanSuricataInterface(t.Context(), iface)
	return plan, err
}

func TestPlanSuricataInterfaceRefusesWhatCannotCapture(t *testing.T) {
	h := newSuricataHost(t)
	candidates := hostCandidates()
	if len(candidates) == 0 {
		t.Skip("this host has no interface that is up")
	}
	for _, bad := range []string{"eth0; reboot", "lo", "jd-no-such-iface0"} {
		if _, err := planOnHost(t, h, bad); err == nil {
			t.Fatalf("planned %q", bad)
		}
	}
	h.execs = testdata(t, "suricata-execstart-ips.txt")
	if _, err := planOnHost(t, h, candidates[0]); err == nil || !strings.Contains(err.Error(), "queue") {
		t.Fatalf("inline mode planned an interface: %v", err)
	}
	h.execs = "ExecStart={ argv[]=/usr/bin/suricata --af-packet=eth9 -c /etc/suricata/suricata.yaml ; }"
	if _, err := planOnHost(t, h, candidates[0]); err == nil || !strings.Contains(err.Error(), "command line") {
		t.Fatalf("a command-line interface was overridden: %v", err)
	}
}

func TestApplySuricataInterfaceTestsRestartsAndRestores(t *testing.T) {
	h := newSuricataHost(t)
	candidates := hostCandidates()
	if len(candidates) == 0 {
		t.Skip("this host has no interface that is up")
	}
	iface := candidates[0]
	if iface == "eth0" {
		if len(candidates) < 2 {
			t.Skip("the only interface that is up is the one the fixture already names")
		}
		iface = candidates[1]
	}
	yamlPath := filepath.Join(h.dir, "suricata.yaml")
	original := testdata(t, "suricata-yaml-afpacket.txt")

	t.Run("applied", func(t *testing.T) {
		plan, err := planOnHost(t, h, iface)
		if err != nil {
			t.Fatal(err)
		}
		calls := longHost(t, h, nil, nil)
		if err := New().ApplySuricataInterface(t.Context(), plan, &recordingLines{}); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(yamlPath)
		if strings.Join(afPacketInterfaces(string(got)), ",") != iface {
			t.Fatalf("file=%v", afPacketInterfaces(string(got)))
		}
		if strings.Join(*calls, "\n") != "suricata -T -c "+yamlPath+"\nsystemctl restart suricata" {
			t.Fatalf("calls=%q", *calls)
		}
	})
	t.Run("rejected by suricata -T", func(t *testing.T) {
		plan, err := planOnHost(t, h, iface)
		if err != nil {
			t.Fatal(err)
		}
		calls := longHost(t, h, map[string]error{"suricata -T": errors.New("exit status 1")}, nil)
		if err := New().ApplySuricataInterface(t.Context(), plan, &recordingLines{}); err == nil {
			t.Fatal("a rejected configuration was reported applied")
		}
		if got, _ := os.ReadFile(yamlPath); string(got) != original {
			t.Fatal("the previous file was not restored")
		}
		if len(*calls) != 1 {
			t.Fatalf("restarted after a failed test: %q", *calls)
		}
	})
	t.Run("did not come back", func(t *testing.T) {
		plan, err := planOnHost(t, h, iface)
		if err != nil {
			t.Fatal(err)
		}
		restarts := 0
		longHost(t, h, nil, func(call string) {
			if call == "systemctl restart suricata" {
				restarts++
				if restarts == 1 {
					h.active = "failed\n"
				} else {
					h.active = "active\n"
				}
			}
		})
		err = New().ApplySuricataInterface(t.Context(), plan, &recordingLines{})
		if err == nil || !strings.Contains(err.Error(), "it is back on eth0") || restarts != 2 {
			t.Fatalf("err=%v restarts=%d", err, restarts)
		}
		if got, _ := os.ReadFile(yamlPath); string(got) != original {
			t.Fatal("the previous interface was not restored after a failed restart")
		}
	})
}

func TestUpdateRulesAndStartReportWhatTheHostDid(t *testing.T) {
	h := newSuricataHost(t)
	h.file("suricata.rules", "alert icmp any any -> any any (sid:1;)\n")
	calls := longHost(t, h, nil, func(call string) {
		if call == "suricata-update" {
			h.file("suricata.rules", "alert icmp any any -> any any (sid:1;)\nalert tcp any any -> any any (sid:2;)\n")
		}
	})
	out := &recordingLines{}
	if err := New().UpdateSuricataRules(t.Context(), out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*calls, "\n") != "suricata-update\nsystemctl reload suricata" || !strings.Contains(strings.Join(out.status, "\n"), "2 rules enabled (+1)") {
		t.Fatalf("calls=%q status=%q", *calls, out.status)
	}

	failing := longHost(t, h, map[string]error{"suricata-update": errors.New("exit status 1")}, nil)
	if err := New().UpdateSuricataRules(t.Context(), &recordingLines{}); err == nil || len(*failing) != 1 {
		t.Fatalf("a failed download reloaded: %v %q", err, *failing)
	}

	h.active = "inactive\n"
	started := longHost(t, h, nil, func(call string) {
		if strings.HasPrefix(call, "systemctl enable --now") {
			h.active = "active\n"
		}
	})
	if err := New().StartSuricata(t.Context(), &recordingLines{}); err != nil || strings.Join(*started, ",") != "systemctl enable --now suricata" {
		t.Fatalf("start=%v %q", err, *started)
	}
	h.active = "failed\n"
	longHost(t, h, nil, nil)
	if err := New().StartSuricata(t.Context(), &recordingLines{}); err == nil {
		t.Fatal("a Suricata that stopped again was reported running")
	}
}
