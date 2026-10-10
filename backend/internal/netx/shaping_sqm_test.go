package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSQMShape(t *testing.T) ShapeSpec {
	t.Helper()
	p, err := normSQMProfile(SQMProfile{Overhead: 22, MPU: 64})
	if err != nil {
		t.Fatal(err)
	}
	return ShapeSpec{Device: "eth0", IngressKbit: 10000, SQM: &SQMSpec{SQMProfile: p, IFB: "jds0123456789ab", Token: "0123456789abcdef0123456789abcdef", SourceMAC: "02:00:00:00:00:01", SourceKind: "physical", MTU: 1500, Hook: "clsact"}}
}

func sqmCakeFixture(t *testing.T, sh ShapeSpec) []tcQdisc {
	t.Helper()
	p := sh.SQM
	options := map[string]any{"bandwidth": shapeBytes(sh.IngressKbit), "diffserv": p.Diffserv, "flowmode": p.FlowMode, "nat": p.NAT, "wash": !p.PreserveDSCP, "ingress": true, "ack-filter": "disabled", "split_gso": true, "rtt": p.RTTMillis * 1000, "raw": false, "atm": p.LinkLayer, "overhead": p.Overhead, "mpu": p.MPU, "fwmark": "0"}
	b, _ := json.Marshal([]map[string]any{{"kind": "cake", "handle": "ca11:", "root": true, "options": options}})
	qs, err := parseQdiscs(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

func sqmRedirectFixture(t *testing.T, sh ShapeSpec) []sqmFilter {
	t.Helper()
	b, _ := json.Marshal([]map[string]any{{"protocol": "all", "pref": sqmPreference, "kind": "matchall", "chain": 0}, {"protocol": "all", "pref": sqmPreference, "kind": "matchall", "chain": 0, "options": map[string]any{"handle": sqmHandle, "skip_hw": true, "actions": []map[string]any{{"kind": "mirred", "mirred_action": "redirect", "direction": "egress", "to_dev": sh.SQM.IFB, "cookie": sh.SQM.Token, "control_action": map[string]string{"type": "stolen"}}}}}})
	var filters []sqmFilter
	if err := json.Unmarshal(b, &filters); err != nil {
		t.Fatal(err)
	}
	return filters
}

func TestSQMProfileBoundsAndClosedVocabulary(t *testing.T) {
	for _, profile := range []SQMProfile{{Diffserv: "diffserv8"}, {Diffserv: "besteffort; reboot"}, {FlowMode: "src nat"}, {LinkLayer: "raw"}, {Overhead: -65}, {Overhead: 257}, {MPU: -1}, {MPU: 257}, {RTTMillis: 9}, {RTTMillis: 1001}} {
		if _, err := normSQMProfile(profile); err == nil {
			t.Fatalf("accepted %+v", profile)
		}
	}
	for _, profile := range []SQMProfile{{}, {Diffserv: "diffserv4", FlowMode: "triple-isolate", LinkLayer: "atm", Overhead: -64, MPU: 256, RTTMillis: 10}, {Diffserv: "diffserv3", FlowMode: "flows", LinkLayer: "ptm", Overhead: 256, RTTMillis: 1000}} {
		if _, err := normSQMProfile(profile); err != nil {
			t.Fatalf("refused %+v: %v", profile, err)
		}
	}
	sh := testSQMShape(t)
	sh.IngressKbit = 0
	if _, err := normShape(sh); err == nil {
		t.Fatal("SQM without a rate accepted")
	}
	sh.IngressKbit = 10000
	for _, line := range shapeLines(sh) {
		if strings.Contains(line, "ingress") || strings.Contains(line, "police") {
			t.Fatalf("SQM rendered a legacy policer: %s", line)
		}
	}
}

func TestSQMVerifiesEveryEffectiveCAKEParameter(t *testing.T) {
	sh := testSQMShape(t)
	if err := checkSQMCake(sqmCakeFixture(t, sh), sh, false); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"bandwidth", "diffserv", "flowmode", "nat", "wash", "ingress", "ack-filter", "split_gso", "rtt", "raw", "atm", "overhead", "mpu", "fwmark"} {
		t.Run(key, func(t *testing.T) {
			qs := sqmCakeFixture(t, sh)
			delete(qs[0].Options, key)
			if err := checkSQMCake(qs, sh, false); !errors.Is(err, errShapingDrift) {
				t.Fatalf("missing %s accepted: %v", key, err)
			}
			qs[0].Options[key] = json.RawMessage(`"foreign"`)
			if err := checkSQMCake(qs, sh, false); !errors.Is(err, errShapingDrift) {
				t.Fatalf("changed %s accepted: %v", key, err)
			}
		})
	}
}

func TestSQMRedirectRequiresExactCookieTargetAndSelectors(t *testing.T) {
	sh := testSQMShape(t)
	if found, err := checkSQMFilters(sqmRedirectFixture(t, sh), sh, false); !found || err != nil {
		t.Fatalf("native descriptor+detail: %v %v", found, err)
	}
	for _, mutation := range []func(*sqmFilter){func(f *sqmFilter) { f.Pref++ }, func(f *sqmFilter) { f.Chain++ }, func(f *sqmFilter) { f.Protocol = "ip" }, func(f *sqmFilter) { f.Options.Handle++ }, func(f *sqmFilter) { f.Options.SkipHW = false }, func(f *sqmFilter) { f.Options.Actions[0].Cookie = strings.Repeat("0", 32) }, func(f *sqmFilter) { f.Options.Actions[0].Device = "foreign" }, func(f *sqmFilter) { f.Options.Actions[0].Direction = "ingress" }} {
		filters := sqmRedirectFixture(t, sh)
		mutation(&filters[1])
		if _, err := checkSQMFilters(filters, sh, false); !errors.Is(err, errShapingDrift) {
			t.Fatalf("foreign redirect accepted: %v", err)
		}
	}
	if _, err := checkSQMFilters(nil, sh, false); err == nil {
		t.Fatal("missing redirect accepted")
	}
	if _, err := checkSQMFilters(append(sqmRedirectFixture(t, sh), sqmRedirectFixture(t, sh)[1]), sh, false); err == nil {
		t.Fatal("duplicate redirect accepted")
	}
}

func TestSQMZeroMPUUsesTheDocumentedNativeOmissionOnly(t *testing.T) {
	sh := testSQMShape(t)
	sh.SQM.MPU = 0
	qs := sqmCakeFixture(t, sh)
	delete(qs[0].Options, "mpu")
	if err := checkSQMCake(qs, sh, false); err != nil {
		t.Fatal(err)
	}
	qs[0].Options["mpu"] = json.RawMessage(`64`)
	if err := checkSQMCake(qs, sh, false); err == nil {
		t.Fatal("nonzero native MPU accepted for zero intent")
	}
	delete(qs[0].Options, "mpu")
	delete(qs[0].Options, "overhead")
	if err := checkSQMCake(qs, sh, false); err == nil {
		t.Fatal("an unrelated absent native option was silently defaulted")
	}
}

func TestSQMRecoveryRejectsUntrustedEnvelopeBeforeRestoringFiles(t *testing.T) {
	sh := testSQMShape(t)
	commands, err := sqmRecoveryPlan(emptySpec(), &Spec{Shaping: []ShapeSpec{sh}})
	if err != nil || len(commands) != 1 {
		t.Fatalf("plan %v %v", commands, err)
	}
	good := commands[0].Args[0]
	for _, bad := range []string{good + " {}", strings.Replace(good, `"version":1`, `"version":2`, 1), strings.Replace(good, `"device":"eth0"`, `"device":"eth0;reboot"`, 1), strings.Replace(good, `"version":1`, `"argv":["reboot"],"version":1`, 1), strings.Replace(good, sh.SQM.IFB, "other", 1), strings.Repeat(" ", maxSQMRecoveryBytes+1)} {
		record(t, "systemctl")
		s := testService(t)
		if err := writeFileAtomic(s.specPath(), []byte("candidate"), 0o600); err != nil {
			t.Fatal(err)
		}
		j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "sqm", Phase: "prepared"}, Files: []recoverySnapshot{{Path: s.specPath(), Exists: true, Data: []byte("old"), Mode: 0o600}}, Commands: []recoveryCommand{{Tool: "sqm", Args: []string{bad}}}}
		if err := recoverChange(context.Background(), j); err == nil {
			t.Fatalf("accepted malformed SQM payload: %s", bad)
		}
		data, _ := os.ReadFile(s.specPath())
		if string(data) != "candidate" {
			t.Fatal("restored files before checking all SQM identities")
		}
	}
	if _, err := validateSQMRecoveryArgs([]string{good, good}); err == nil {
		t.Fatal("multiple SQM args accepted")
	}
}

func TestSQMRequiresIndependentHelperAndReconnectionBeforeEffects(t *testing.T) {
	for _, mode := range []string{"unsupported", "no_session", "helper_failed"} {
		t.Run(mode, func(t *testing.T) {
			h := pendingHost(t)
			ctx := WithPendingConfirmation(context.Background(), 7)
			if mode == "unsupported" {
				h.independentRecovery = false
			}
			if mode == "no_session" {
				ctx = context.Background()
			}
			if mode == "helper_failed" {
				h.rec.fail(filepath.Join(h.paths.Dir, recoveryBinary)+" --network-sqm-check", "old helper")
			}
			if err := h.prepareSQMHelper(ctx); err == nil {
				t.Fatal("unsupported SQM enabled")
			}
			for _, command := range h.rec.commands() {
				if strings.HasPrefix(command, "tc ") || strings.HasPrefix(command, "ip link ") {
					t.Fatalf("effect before recovery was ready: %s", command)
				}
			}
		})
	}
}

func TestSQMRecoveryPrecedesOrdinaryRootUndoAndLeavesLegacyIngressAlone(t *testing.T) {
	h := newShapeHost(t)
	sh := testSQMShape(t)
	sh.EgressKbit = 1000
	commands, err := h.recoveryPlan(context.Background(), emptySpec(), &Spec{Shaping: []ShapeSpec{sh}})
	if err != nil || len(commands) != 2 || commands[0].Tool != "sqm" || strings.Join(commands[1].Args, " ") != "qdisc del dev eth0 root" {
		t.Fatalf("unsafe SQM undo order: %+v %v", commands, err)
	}
	legacy := sh
	legacy.SQM = nil
	commands, err = h.recoveryPlan(context.Background(), emptySpec(), &Spec{Shaping: []ShapeSpec{legacy}})
	if err != nil || len(commands) != 2 || commands[0].Tool != "tc" || strings.Join(commands[1].Args, " ") != "qdisc del dev eth0 ingress" {
		t.Fatalf("legacy undo changed: %+v %v", commands, err)
	}
}

func TestSQMBootHookIsExplicitAndItsFailureIsVisible(t *testing.T) {
	s := testService(t)
	legacy := renderUnit(s.paths, false, true)
	if strings.Contains(legacy, "--network-sqm") {
		t.Fatal("legacy boot gained SQM dependency")
	}
	sqm := renderUnit(s.paths, false, true, true)
	command := "ExecStart=" + filepath.Join(s.paths.Dir, recoveryBinary) + " --network-sqm-restore " + s.paths.Dir
	if !strings.Contains(sqm, command) || strings.Index(sqm, command) < strings.Index(sqm, "ExecStart=-sysctl") {
		t.Fatalf("SQM must fail visibly after ordinary resources: %s", sqm)
	}
}

func TestSQMRechecksIFBIdentityAtTheEffectBoundary(t *testing.T) {
	sh := testSQMShape(t)
	foreign := false
	var effects []string
	execute := recoveryExecutor(func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		command := tool + " " + strings.Join(args, " ")
		var value any
		switch command {
		case "ip -j -d link show":
			mac := sqmIFBMAC(sh.SQM.Token)
			if foreign {
				mac = "02:00:00:00:00:99"
			}
			value = []map[string]any{{"ifname": sh.Device, "address": sh.SQM.SourceMAC, "mtu": 1500}, {"ifname": sh.SQM.IFB, "address": mac, "mtu": 1500, "flags": []string{"UP"}, "ifalias": sqmAliasPrefix + sh.SQM.Token, "linkinfo": map[string]string{"info_kind": "ifb"}}}
		case "tc -j qdisc show dev eth0":
			value = []tcQdisc{{Kind: "clsact", Handle: "ffff:"}}
		case "tc -j qdisc show dev " + sh.SQM.IFB:
			value = sqmCakeFixture(t, sh)
		case "tc -j filter show dev eth0 ingress":
			value = sqmRedirectFixture(t, sh)
		case "tc -j filter show dev " + sh.SQM.IFB, "tc -j class show dev " + sh.SQM.IFB:
			return "[]", nil
		default:
			effects = append(effects, command)
			return "", nil
		}
		b, _ := json.Marshal(value)
		return string(b), nil
	})
	ctx := context.WithValue(context.Background(), recoveryExecutorKey{}, execute)
	if err := verifySQM(ctx, sh); err != nil {
		t.Fatal(err)
	}
	// A native owner replaces the saved name after preflight. No CAKE/up/
	// alias command may reach that foreign replacement during apply.
	foreign = true
	candidate := sh
	candidate.IngressKbit = 8000
	if err := applySQM(ctx, candidate, &sh); !errors.Is(err, errShapingDrift) {
		t.Fatalf("accepted replaced IFB: %v", err)
	}
	if len(effects) != 0 {
		t.Fatalf("foreign IFB received effects: %v", effects)
	}
}

// iproute2 6.1 (Ubuntu 24.04) prints classes as text even with -j, and
// nothing for an IFB whose CAKE has no flows yet. A guest acceptance found every
// SQM apply refused and its recovery left degraded on that release.
func TestSQMReadsTheOlderTextClassForm(t *testing.T) {
	sh := testSQMShape(t)
	for _, tc := range []struct {
		name, classes string
		owned         bool
	}{
		{"no classes", "", true},
		{"cake virtual classes", "class cake ca11:1 parent ca11: \nclass cake ca11:3 parent ca11: \n", true},
		{"foreign hierarchy", "class htb 1:10 root prio 0 rate 8Mbit ceil 8Mbit burst 1600b cburst 1600b \n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execute := recoveryExecutor(func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
				var value any
				switch command := tool + " " + strings.Join(args, " "); command {
				case "ip -j -d link show":
					value = []map[string]any{{"ifname": sh.Device, "address": sh.SQM.SourceMAC, "mtu": 1500}, {"ifname": sh.SQM.IFB, "address": sqmIFBMAC(sh.SQM.Token), "mtu": 1500, "flags": []string{"UP"}, "ifalias": sqmAliasPrefix + sh.SQM.Token, "linkinfo": map[string]string{"info_kind": "ifb"}}}
				case "tc -j qdisc show dev eth0":
					value = []tcQdisc{{Kind: "clsact", Handle: "ffff:"}}
				case "tc -j qdisc show dev " + sh.SQM.IFB:
					value = sqmCakeFixture(t, sh)
				case "tc -j filter show dev eth0 ingress":
					value = sqmRedirectFixture(t, sh)
				case "tc -j filter show dev " + sh.SQM.IFB:
					return "[]", nil
				case "tc -j class show dev " + sh.SQM.IFB:
					return tc.classes, nil
				default:
					return "", fmt.Errorf("unexpected %s", command)
				}
				b, _ := json.Marshal(value)
				return string(b), nil
			})
			err := verifySQM(context.WithValue(context.Background(), recoveryExecutorKey{}, execute), sh)
			if tc.owned && err != nil || !tc.owned && !errors.Is(err, errShapingDrift) {
				t.Fatalf("verify = %v, want owned=%t", err, tc.owned)
			}
		})
	}
}
