package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

func gwShapeSpec() *Spec {
	sp := emptySpec()
	sp.Shaping = []ShapeSpec{
		{Device: "eth0", EgressKbit: 50000, IngressKbit: 100000},
		{Device: "eth1", Qdisc: "cake", EgressKbit: 20000},
		{Device: "tailscale0", Qdisc: "fq"},
		{Device: "wg0", IngressKbit: 8000},
		{Device: "eth2", Qdisc: "fq", EgressKbit: 5000},
		{Device: "bad name", EgressKbit: 1000}, // a hand-edited entry the renderer must leave out
	}
	return sp
}

func TestRenderShapingGolden(t *testing.T) {
	gwGolden(t, "shaping-full.batch", renderShaping(gwShapeSpec()))
}

func TestRenderShapingOfNothingIsTheHeaderAlone(t *testing.T) {
	if got := renderShaping(emptySpec()); got != generatedHeader {
		t.Fatalf("got %q", got)
	}
}

func TestShapeLinesNeverDeleteTheIngressQueue(t *testing.T) {
	lines := shapeLines(ShapeSpec{Device: "eth0", EgressKbit: 1000, IngressKbit: 1000})
	var dels []string
	for _, l := range lines {
		if strings.HasPrefix(l, "qdisc del") || strings.HasPrefix(l, "filter del") {
			dels = append(dels, l)
		}
		if strings.Contains(l, "del") && strings.Contains(l, "ingress") {
			t.Errorf("a batch that deletes the ingress queue also deletes a clsact queue's BPF filters: %s", l)
		}
	}
	if len(dels) != 2 || dels[0] != "qdisc del dev eth0 root" || dels[1] != "filter del dev eth0 parent ffff: prio 1" {
		t.Fatalf("deletes = %v", dels)
	}
	// Ingress alone must not delete the root queue it has nothing to say about.
	for _, l := range shapeLines(ShapeSpec{Device: "eth0", IngressKbit: 1000}) {
		if strings.Contains(l, " root") {
			t.Fatalf("an ingress-only device touched its root queue: %s", l)
		}
	}
}

func TestPoliceBurstIsATenthOfASecondAndNeverUnder16K(t *testing.T) {
	for kbit, want := range map[int]int{100: 16384, 1000: 16384, 20000: 250000, 1_000_000: 12_500_000} {
		if got := policeBurst(kbit); got != want {
			t.Errorf("policeBurst(%d) = %d, want %d", kbit, got, want)
		}
	}
}

func TestNormShapeRules(t *testing.T) {
	cases := []struct {
		name string
		sh   ShapeSpec
		want string
	}{
		{"egress", ShapeSpec{Device: "eth0", EgressKbit: 1}, ""},
		{"qdisc only", ShapeSpec{Device: "eth0", Qdisc: "cake"}, ""},
		{"nothing", ShapeSpec{Device: "eth0"}, "set a queue discipline"},
		{"an unknown qdisc", ShapeSpec{Device: "eth0", Qdisc: "pfifo_fast"}, "queue discipline"},
		{"a qdisc with options smuggled in", ShapeSpec{Device: "eth0", Qdisc: "fq_codel limit 1"}, "queue discipline"},
		{"negative", ShapeSpec{Device: "eth0", EgressKbit: -5}, "speed limit"},
		{"absurd", ShapeSpec{Device: "eth0", IngressKbit: maxShapeKbit + 1}, "speed limit"},
		{"a device with a space", ShapeSpec{Device: "eth 0", EgressKbit: 5}, "interface name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := normShape(c.sh)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

// ---- host

const (
	gwLinkEth0  = `[{"ifindex":2,"ifname":"eth0","flags":["BROADCAST","UP"],"mtu":1500,"link_type":"ether"}]`
	gwLinkTS    = `[{"ifindex":4,"ifname":"tailscale0","flags":["POINTOPOINT","UP"],"mtu":1280,"link_type":"none","linkinfo":{"info_kind":"tun"}}]`
	gwLinkVeth  = `[{"ifindex":6,"ifname":"veth6e4f828","flags":["UP"],"mtu":1500,"link_type":"ether","linkinfo":{"info_kind":"veth"}}]`
	gwLinkLo    = `[{"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP"],"mtu":65536,"link_type":"loopback"}]`
	gwLinkWG    = `[{"ifindex":7,"ifname":"wg0","flags":["UP"],"mtu":1420,"link_type":"none","linkinfo":{"info_kind":"wireguard"}}]`
	gwTcHTB     = `[{"kind":"htb","handle":"1:","dev":"eth0","root":true,"options":{"default":"0x10"},"bytes":10,"packets":1,"drops":0,"overlimits":0,"requeues":0,"backlog":0,"qlen":0},{"kind":"fq_codel","handle":"10:","dev":"eth0","parent":"1:10","bytes":10,"packets":1,"drops":0,"overlimits":0,"requeues":0,"backlog":0,"qlen":0},{"kind":"ingress","handle":"ffff:","dev":"eth0","parent":"ffff:fff1","bytes":0,"packets":0,"drops":0,"overlimits":0,"requeues":0,"backlog":0,"qlen":0}]`
	gwTcFQCodel = `[{"kind":"fq_codel","handle":"0:","dev":"eth0","root":true,"bytes":10,"packets":1,"drops":0,"overlimits":0,"requeues":0,"backlog":0,"qlen":0}]`
)

// newShapeHost is a gwHost whose devices answer ip link and tc.
func newShapeHost(t *testing.T) *gwHost {
	t.Helper()
	h := newGwHost(t)
	h.rec.fail("ip -j -d link show dev nope", "Device \"nope\" does not exist.")
	h.rec.on("ip -j -d link show dev eth0", gwLinkEth0)
	h.rec.on("ip -j -d link show dev tailscale0", gwLinkTS)
	h.rec.on("ip -j -d link show dev veth6e4f828", gwLinkVeth)
	h.rec.on("ip -j -d link show dev lo", gwLinkLo)
	h.rec.on("ip -j -d link show dev wg0", gwLinkWG)
	h.rec.on("ip -j -d link show", fixture(t, "ip-link.json"))
	h.rec.on("tc -j -s qdisc show", fixture(t, "shaping-qdisc.json"))

	h.rec.on("tc -j qdisc show dev", "$shape")
	h.rec.on("tc -j class show dev", "$shape")
	h.rec.on("tc -j filter show dev", "$shape")
	h.rec.on("tc -r -d filter show dev", "$shape")
	h.rec.on("tc ", "")
	states := map[string]ShapeSpec{}
	hooks := map[string]bool{}
	prevRun := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := prevRun(ctx, name, args...)
		if name != "tc" || err != nil {
			return out, err
		}
		line := strings.Join(args, " ")
		device := ""
		for i, a := range args {
			if a == "dev" && i+1 < len(args) {
				device = args[i+1]
				break
			}
		}
		sh := states[device]
		sh.Device = device
		if out == "$shape" {
			switch {
			case strings.Contains(line, "qdisc show"):
				qs := []map[string]any{{"kind": "noqueue", "handle": "0:", "root": true}}
				if sh.hasRoot() {
					kind := sh.Qdisc
					options := map[string]any{}
					if sh.EgressKbit > 0 && sh.Qdisc != "cake" {
						kind = "htb"
						options["default"] = "0x10"
					}
					if kind == "cake" {
						options["bandwidth"] = shapeBytes(sh.EgressKbit)
					}
					qs[0] = map[string]any{"kind": kind, "handle": "1:", "root": true, "options": options}
					if kind == "htb" {
						leaf := sh.Qdisc
						if leaf == "" {
							leaf = "fq_codel"
						}
						qs = append(qs, map[string]any{"kind": leaf, "handle": "10:", "parent": "1:10"})
					}
				}
				if hooks[device] {
					qs = append(qs, map[string]any{"kind": "ingress", "handle": "ffff:", "parent": "ffff:fff1"})
				}
				b, _ := json.Marshal(qs)
				return string(b), nil
			case strings.Contains(line, "class show"):
				if sh.EgressKbit == 0 {
					return "[]", nil
				}
				return fmt.Sprintf(`[{"class":"htb","handle":"1:10","root":true,"rate":%d,"ceil":%d}]`, shapeBytes(sh.EgressKbit), shapeBytes(sh.EgressKbit)), nil
			case strings.Contains(line, "root"):
				return "[]", nil
			case strings.HasPrefix(line, "-j filter show"):
				if sh.IngressKbit == 0 {
					return "[]", nil
				}
				return `[{"protocol":"all","pref":1,"kind":"matchall","chain":0,"options":{"actions":[{"kind":"police","control_action":{"type":"drop"}}]}}]`, nil
			default:
				return fmt.Sprintf("police 0x1 rate %dKbit burst %db mtu 2Kb action drop", sh.IngressKbit, policeBurst(sh.IngressKbit)), nil
			}
		}
		switch {
		case strings.HasPrefix(line, "qdisc del") && strings.HasSuffix(line, " root"):
			sh.Qdisc = ""
			sh.EgressKbit = 0
		case strings.HasPrefix(line, "qdisc replace") && strings.Contains(line, "root cake"):
			sh.Qdisc = "cake"
			for _, a := range args {
				if strings.HasSuffix(a, "kbit") {
					sh.EgressKbit, _ = strconv.Atoi(strings.TrimSuffix(a, "kbit"))
				}
			}
		case strings.HasPrefix(line, "qdisc replace") && strings.Contains(line, "parent 1:10"):
			sh.Qdisc = args[len(args)-1]
		case strings.HasPrefix(line, "qdisc replace") && strings.Contains(line, " root") && !strings.Contains(line, "htb"):
			sh.Qdisc = args[len(args)-1]
		case strings.HasPrefix(line, "class replace"):
			for _, a := range args {
				if strings.HasSuffix(a, "kbit") {
					sh.EgressKbit, _ = strconv.Atoi(strings.TrimSuffix(a, "kbit"))
				}
			}
		case strings.HasPrefix(line, "qdisc replace") && strings.HasSuffix(line, " ingress"):
			hooks[device] = true
		case strings.HasPrefix(line, "qdisc del") && strings.HasSuffix(line, " ingress"):
			hooks[device] = false
			sh.IngressKbit = 0
		case strings.HasPrefix(line, "filter replace"):
			for _, a := range args {
				if strings.HasSuffix(a, "kbit") {
					sh.IngressKbit, _ = strconv.Atoi(strings.TrimSuffix(a, "kbit"))
				}
			}
		}
		states[device] = sh
		return out, err
	}
	t.Cleanup(func() { run = prevRun })
	return h
}

func (h *gwHost) tcCommands() []string {
	var out []string
	for _, c := range h.rec.commands() {
		if strings.HasPrefix(c, "tc ") {
			out = append(out, c)
		}
	}
	return out
}

func TestSetShapingClearsThenSetsThenVerifiesThenSaves(t *testing.T) {
	h := newShapeHost(t)
	err := h.SetShaping(context.Background(), "eth0", ShapeRequest{EgressKbit: 50000, IngressKbit: 100000}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tc -j qdisc show dev eth0", // is the ingress hook a clsact queue?
		"tc qdisc del dev eth0 root",
		"tc qdisc replace dev eth0 root handle 1: htb default 10",
		"tc class replace dev eth0 parent 1: classid 1:10 htb rate 50000kbit ceil 50000kbit",
		"tc qdisc replace dev eth0 parent 1:10 handle 10: fq_codel",
		"tc qdisc replace dev eth0 handle ffff: ingress",
		"tc filter del dev eth0 parent ffff: prio 1",
		"tc filter replace dev eth0 parent ffff: protocol all prio 1 matchall action police rate 100000kbit burst 1250000 drop",
		"tc -j qdisc show dev eth0",
		"tc -j class show dev eth0",
		"tc -j filter show dev eth0 parent ffff:",
		"tc -r -d filter show dev eth0 parent ffff: pref 1",
	}
	if got := h.tcCommands(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tc commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	h.order(t, "tc qdisc del dev eth0 root", "tc -j qdisc show dev eth0", "systemctl")
	sp := h.spec(t)
	if len(sp.Shaping) != 1 || sp.Shaping[0].EgressKbit != 50000 || sp.Shaping[0].CreatedBy != "ops" {
		t.Fatalf("spec = %+v", sp.Shaping)
	}
	b, _ := os.ReadFile(h.paths.Dir + "/" + shapingFile)
	if !strings.Contains(string(b), "htb rate 50000kbit ceil 50000kbit") || !strings.Contains(string(b), "police rate 100000kbit") {
		t.Fatalf("batch file:\n%s", b)
	}
}

func TestSetShapingRestoresThePreviousQueueWhenItFails(t *testing.T) {
	h := newShapeHost(t)
	ctx := context.Background()
	if err := h.SetShaping(ctx, "eth0", ShapeRequest{EgressKbit: 50000}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	h.rec.mu.Lock()
	n := len(h.rec.calls)
	h.rec.mu.Unlock()
	h.first("tc class replace dev eth0 parent 1: classid 1:10 htb rate 2000kbit", "RTNETLINK answers: Invalid argument", errors.New("RTNETLINK answers: Invalid argument"))
	err := h.SetShaping(ctx, "eth0", ShapeRequest{EgressKbit: 2000}, gwClient, "ops")
	if err == nil || !strings.Contains(err.Error(), "Invalid argument") {
		t.Fatalf("err = %v", err)
	}
	after := h.rec.commands()[n:]
	var restored bool
	for _, c := range after {
		if c == "tc class replace dev eth0 parent 1: classid 1:10 htb rate 50000kbit ceil 50000kbit" {
			restored = true
		}
	}
	if !restored {
		t.Fatalf("the earlier 50 Mbit queue was not put back:\n%s", strings.Join(after, "\n"))
	}
	if got := h.spec(t).Shaping[0].EgressKbit; got != 50000 {
		t.Fatalf("spec says %d", got)
	}
}

func TestSetShapingFirstTimeFailureLeavesTheDeviceUnshaped(t *testing.T) {
	h := newShapeHost(t)
	h.first("tc qdisc replace dev eth0 root cake", "Error: Specified qdisc kind is unknown.", errors.New("Error: Specified qdisc kind is unknown."))
	err := h.SetShaping(context.Background(), "eth0", ShapeRequest{Qdisc: "cake", EgressKbit: 20000}, gwClient, "ops")
	if err == nil || !strings.Contains(err.Error(), "Specified qdisc kind is unknown") {
		t.Fatalf("cake's own error was not reported: %v", err)
	}
	cmds := h.tcCommands()
	if last := cmds[len(cmds)-1]; last != "tc qdisc del dev eth0 root" {
		t.Fatalf("the device was left with whatever the failed change started: last command %q", last)
	}
	if h.saved() {
		t.Error("saved")
	}
}

func TestSetShapingVerifyMismatchTakesTheChangeBack(t *testing.T) {
	h := newShapeHost(t)
	original := run
	reads := 0
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := original(ctx, name, args...)
		if name == "tc" && strings.Join(args, " ") == "-j qdisc show dev eth0" {
			reads++
			if reads == 2 {
				return gwTcFQCodel, nil
			}
		}
		return out, err
	}
	t.Cleanup(func() { run = original })
	err := h.SetShaping(context.Background(), "eth0", ShapeRequest{EgressKbit: 50000}, gwClient, "ops")
	if err == nil || !strings.Contains(err.Error(), `"fq_codel" as its queue after setting "htb"`) {
		t.Fatalf("err=%v", err)
	}
	if h.saved() {
		t.Fatal("saved")
	}
}

func TestSetShapingGuards(t *testing.T) {
	cases := []struct {
		name   string
		device string
		req    ShapeRequest
		want   string
		guard  bool
	}{
		{"under a megabit on the uplink", "eth0", ShapeRequest{EgressKbit: 999}, "default route", true},
		{"download under a megabit on the uplink", "eth0", ShapeRequest{IngressKbit: 500}, "default route", true},
		{"exactly a megabit on the uplink", "eth0", ShapeRequest{EgressKbit: 1000}, "", false},
		{"under a megabit on the client's path", "tailscale0", ShapeRequest{IngressKbit: 100}, "Your browser is reached through tailscale0", true},
		{"under a megabit on another device", "wg0", ShapeRequest{IngressKbit: 100}, "", false},
		{"a veth", "veth6e4f828", ShapeRequest{EgressKbit: 5000}, "veth", false},
		{"loopback", "lo", ShapeRequest{EgressKbit: 5000}, "loopback", false},
		{"a device that does not exist", "nope", ShapeRequest{EgressKbit: 5000}, "no interface called nope", false},
		{"nothing asked", "eth0", ShapeRequest{}, "set a queue discipline", false},
		{"an unknown discipline", "eth0", ShapeRequest{Qdisc: "htb"}, "queue discipline", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newShapeHost(t)
			err := h.SetShaping(context.Background(), c.device, c.req, gwClient, "ops")
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if c.guard {
				var g *GuardError
				if !errors.As(err, &g) {
					t.Fatalf("a refusal that protects the reader must be a guard (409), got %T", err)
				}
			}
			if c.want != "" && (len(h.tcCommands()) != 0 || h.saved()) {
				t.Errorf("a refused change reached tc: %v", h.tcCommands())
			}
		})
	}
}

func TestClearShaping(t *testing.T) {
	h := newShapeHost(t)
	ctx := context.Background()
	if err := h.ClearShaping(ctx, "eth0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := h.SetShaping(ctx, "eth0", ShapeRequest{EgressKbit: 50000, IngressKbit: 9000}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	if err := h.ClearShaping(ctx, "eth0"); err != nil {
		t.Fatal(err)
	}
	h.order(t, "tc qdisc del dev eth0 root", "tc qdisc del dev eth0 ingress")

	if len(h.spec(t).Shaping) != 0 {
		t.Fatal("still in the spec")
	}
	b, _ := os.ReadFile(h.paths.Dir + "/" + shapingFile)
	if strings.Contains(string(b), "eth0") {
		t.Fatalf("the boot file still shapes it:\n%s", b)
	}
}

func TestShapingViewJoinsTheKernelsQueuesWithWhatWasSet(t *testing.T) {
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
	eth0 := by["eth0"]
	if eth0.Root == nil || eth0.Root.Kind != "htb" || eth0.Leaf == nil || eth0.Leaf.Kind != "fq_codel" || !eth0.Ingress ||
		!eth0.Managed || eth0.EgressKbit != 50000 || eth0.IngressKbit != 100000 || !eth0.Uplink || !eth0.Shapeable {
		t.Fatalf("eth0 = %+v", eth0)
	}
	if eth0.Root.Bytes != 123456789 || eth0.Leaf.Drops != 17 {
		t.Fatalf("counters: root %+v leaf %+v", eth0.Root, eth0.Leaf)
	}
	if d := by["tailscale0"]; d.Managed || d.Root == nil || d.Root.Kind != "fq_codel" || !d.ClientPath || d.Ingress {
		t.Fatalf("tailscale0 = %+v", d)
	}
	for _, name := range []string{"lo", "veth6e4f828"} {
		if d := by[name]; d.Shapeable || d.Guard == "" {
			t.Fatalf("%s = %+v", name, d)
		}
	}
	if v.Devices[0].Name != "eth0" {
		t.Fatalf("the uplink should come first, got %s", v.Devices[0].Name)
	}
	if v.BBR.Active || !v.BBR.Available || v.BBR.Congestion != "cubic" || v.BBR.DefaultQdisc != "fq_codel" || v.BBR.Managed {
		t.Fatalf("bbr = %+v", v.BBR)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"qdiscs":["fq_codel","cake","fq"]`) || !strings.Contains(string(b), `"algorithms":["reno","cubic","bbr"]`) {
		t.Fatalf("json = %s", b)
	}
}

func TestBBR(t *testing.T) {
	ctx := context.Background()
	t.Run("on", func(t *testing.T) {
		h := newShapeHost(t)
		h.rec.on("ss -tinH state established", fixture(t, "traffic-ss.txt"))
		h.rec.on("sysctl -w", "")
		h.rec.on("sysctl -n net.ipv4.tcp_congestion_control", "bbr")
		h.rec.on("sysctl -n net.core.default_qdisc", "fq")
		if err := h.SetBBR(ctx, true, "ops"); err != nil {
			t.Fatal(err)
		}
		h.order(t, "sysctl -w net.ipv4.tcp_congestion_control=bbr", "sysctl -w net.core.default_qdisc=fq", "sysctl -n net.ipv4.tcp_congestion_control", "sysctl -n net.core.default_qdisc", "systemctl")
		sp := h.spec(t)
		if sp.Sysctls["net.ipv4.tcp_congestion_control"] != "bbr" || sp.Sysctls["net.core.default_qdisc"] != "fq" {
			t.Fatalf("spec = %v", sp.Sysctls)
		}
		b, _ := os.ReadFile(h.paths.Sysctl)
		if !strings.Contains(string(b), "net.core.default_qdisc = fq") {
			t.Fatalf("drop-in:\n%s", b)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		h := newShapeHost(t)
		h.writeSys("net/ipv4/tcp_available_congestion_control", "reno cubic")
		err := h.SetBBR(ctx, true, "ops")
		if err == nil || !strings.Contains(err.Error(), "modprobe tcp_bbr") {
			t.Fatalf("err = %v", err)
		}
		if h.rec.ran("sysctl") || h.saved() {
			t.Error("a kernel without bbr was written to")
		}
	})
	t.Run("off restores the defaults and forgets the keys", func(t *testing.T) {
		h := newShapeHost(t)
		sp := emptySpec()
		sp.Sysctls = map[string]string{"net.ipv4.tcp_congestion_control": "bbr", "net.core.default_qdisc": "fq", "net.ipv4.tcp_syncookies": "1"}
		h.seed(t, sp)
		h.rec.on("ss -tinH state established", fixture(t, "traffic-ss.txt"))
		h.rec.on("sysctl -w", "")
		h.rec.on("sysctl -n net.ipv4.tcp_congestion_control", "cubic")
		h.rec.on("sysctl -n net.core.default_qdisc", "fq_codel")
		if err := h.SetBBR(ctx, false, "ops"); err != nil {
			t.Fatal(err)
		}
		if !h.rec.ran("sysctl -w net.ipv4.tcp_congestion_control=cubic") || !h.rec.ran("sysctl -w net.core.default_qdisc=fq_codel") {
			t.Fatalf("commands: %v", h.rec.commands())
		}
		got := h.spec(t).Sysctls
		if len(got) != 1 || got["net.ipv4.tcp_syncookies"] != "1" {
			t.Fatalf("spec = %v: only BBR's own keys may go", got)
		}
	})
	t.Run("a refused write puts the first one back", func(t *testing.T) {
		h := newShapeHost(t)
		h.rec.on("ss -tinH state established", fixture(t, "traffic-ss.txt"))
		h.first("sysctl -w net.core.default_qdisc", "sysctl: permission denied", errors.New("permission denied"))
		h.rec.on("sysctl -w", "")
		if err := h.SetBBR(ctx, true, "ops"); err == nil {
			t.Fatal("expected failure")
		}
		if !h.rec.ran("sysctl -w net.ipv4.tcp_congestion_control=cubic") {
			t.Fatalf("congestion control was left on bbr: %v", h.rec.commands())
		}
		if h.saved() {
			t.Error("saved")
		}
	})
}

const gwTcClsact = `[{"kind":"fq_codel","handle":"0:","dev":"eth0","root":true},{"kind":"clsact","handle":"ffff:","dev":"eth0","parent":"ffff:fff1"}]`

func TestAnIngressLimitIsRefusedOverAClsactQueue(t *testing.T) {
	h := newShapeHost(t)
	h.first("tc -j qdisc show dev eth0", gwTcClsact, nil)
	err := h.SetShaping(context.Background(), "eth0", ShapeRequest{IngressKbit: 9000}, gwClient, "ops")
	if err == nil || !strings.Contains(err.Error(), "clsact") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range h.tcCommands() {
		if !strings.HasPrefix(c, "tc -j qdisc show") {
			t.Fatalf("a refused change ran %s", c)
		}
	}
	if h.saved() {
		t.Error("saved")
	}
	// An upload limit on the same device is no business of the clsact queue.
	sp := emptySpec()
	sp.Shaping = []ShapeSpec{{Device: "eth0", EgressKbit: 9000}}
	h.seed(t, sp)
	h.first("tc -j qdisc show dev eth0", `[{"kind":"htb","handle":"1:","dev":"eth0","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"},{"kind":"clsact","handle":"ffff:","dev":"eth0","parent":"ffff:fff1"}]`, nil)
	if err := h.SetShaping(context.Background(), "eth0", ShapeRequest{EgressKbit: 9000}, gwClient, "ops"); err != nil {
		t.Fatalf("an upload limit beside a clsact queue was refused: %v", err)
	}
	for _, c := range h.tcCommands() {
		if strings.Contains(c, "ingress") {
			t.Fatalf("touched the ingress hook of a clsact device: %s", c)
		}
	}
}

func TestDroppingTheDownloadLimitDeletesOnlyAPlainIngressQueue(t *testing.T) {
	for name, listing := range map[string]string{"plain ingress": gwTcHTB, "clsact": gwTcClsact} {
		t.Run(name, func(t *testing.T) {
			h := newShapeHost(t)
			ctx := context.Background()
			if err := h.SetShaping(ctx, "eth0", ShapeRequest{EgressKbit: 50000, IngressKbit: 9000}, gwClient, "ops"); err != nil {
				t.Fatal(err)
			}
			// Somebody attaches a BPF program's clsact queue afterwards.
			h.first("tc -j qdisc show dev eth0", listing, nil)
			h.rec.mu.Lock()
			n := len(h.rec.calls)
			h.rec.mu.Unlock()
			// Verify wants a htb root; add one to the clsact listing.
			if name == "clsact" {
				h.first("tc -j qdisc show dev eth0", `[{"kind":"htb","handle":"1:","dev":"eth0","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"},{"kind":"clsact","handle":"ffff:","dev":"eth0","parent":"ffff:fff1"}]`, nil)
			}
			if err := h.SetShaping(ctx, "eth0", ShapeRequest{EgressKbit: 50000}, gwClient, "ops"); err != nil {
				t.Fatal(err)
			}
			deleted := false
			for _, c := range h.rec.commands()[n:] {
				deleted = deleted || c == "tc qdisc del dev eth0 ingress"
			}
			if deleted != (name == "plain ingress") {
				t.Fatalf("ingress deleted = %v with %s", deleted, name)
			}
		})
	}
}

func TestAnIngressOnlyLimitLeavesTheRootQueueAlone(t *testing.T) {
	h := newShapeHost(t)
	if err := h.SetShaping(context.Background(), "wg0", ShapeRequest{IngressKbit: 8000}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.tcCommands() {
		if strings.Contains(c, " root") {
			t.Fatalf("an ingress-only limit touched the root queue: %s", c)
		}
	}
}

func TestClearShapingLeavesAClsactQueueAlone(t *testing.T) {
	h := newShapeHost(t)
	ctx := context.Background()
	if err := h.SetShaping(ctx, "eth0", ShapeRequest{EgressKbit: 50000, IngressKbit: 9000}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	h.first("tc -j qdisc show dev eth0", strings.Replace(gwTcHTB, `"kind":"ingress"`, `"kind":"clsact"`, 1), nil)
	h.rec.mu.Lock()
	n := len(h.rec.calls)
	h.rec.mu.Unlock()
	if err := h.ClearShaping(ctx, "eth0"); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.rec.commands()[n:] {
		if strings.Contains(c, "del dev eth0 ingress") {
			t.Fatalf("deleted a clsact queue: %s", c)
		}
	}
}

func TestVerifyShapingRejectsWrongRatesClassesAndPolicers(t *testing.T) {
	for _, tc := range []struct {
		name, command, output string
		spec                  ShapeSpec
	}{
		{"htb rate", "tc -j class show", `[{"class":"htb","handle":"1:10","root":true,"rate":1,"ceil":6250000}]`, ShapeSpec{Device: "eth0", EgressKbit: 50000}},
		{"htb ceil", "tc -j class show", `[{"class":"htb","handle":"1:10","root":true,"rate":6250000,"ceil":9000000}]`, ShapeSpec{Device: "eth0", EgressKbit: 50000}},
		{"htb class", "tc -j class show", `[{"class":"htb","handle":"1:20","root":true,"rate":6250000,"ceil":6250000}]`, ShapeSpec{Device: "eth0", EgressKbit: 50000}},
		{"leaf", "tc -j qdisc show", `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"0x10"}},{"kind":"fq","parent":"1:10","handle":"10:"}]`, ShapeSpec{Device: "eth0", EgressKbit: 50000}},
		{"cake bandwidth", "tc -j qdisc show", `[{"kind":"cake","root":true,"options":{"bandwidth":1}}]`, ShapeSpec{Device: "eth0", Qdisc: "cake", EgressKbit: 50000}},
		{"filter pref", "tc -j filter show", `[{"protocol":"all","pref":2,"kind":"matchall","options":{"actions":[{"kind":"police","control_action":{"type":"drop"}}]}}]`, ShapeSpec{Device: "eth0", IngressKbit: 8000}},
		{"policer rate", "tc -r -d filter show", "police 0x1 rate 7Mbit burst 100000b mtu 2Kb action drop", ShapeSpec{Device: "eth0", IngressKbit: 8000}},
		{"policer burst", "tc -r -d filter show", "police 0x1 rate 8Mbit burst 50000b mtu 2Kb action drop", ShapeSpec{Device: "eth0", IngressKbit: 8000}},
		{"policer action", "tc -r -d filter show", "police 0x1 rate 8Mbit burst 100000b mtu 2Kb action pass", ShapeSpec{Device: "eth0", IngressKbit: 8000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newShapeHost(t)
			if err := h.SetShaping(context.Background(), tc.spec.Device, ShapeRequest{Qdisc: tc.spec.Qdisc, EgressKbit: tc.spec.EgressKbit, IngressKbit: tc.spec.IngressKbit}, gwClient, "ops"); err != nil {
				t.Fatal(err)
			}
			h.first(tc.command, tc.output, nil)
			if err := verifyShaping(context.Background(), tc.spec); err == nil {
				t.Fatal("wrong runtime parameters accepted")
			}
		})
	}
}

func TestFirstShapingApplyRefusesUnrecoverableForeignQueues(t *testing.T) {
	for _, listing := range []string{
		`[{"kind":"htb","handle":"5:","root":true}]`,
		`[{"kind":"cake","handle":"5:","root":true}]`,
		`[{"kind":"fq","handle":"0:","root":true}]`,
		`[{"kind":"fq_codel","handle":"0:","root":true}]`,
		`[{"kind":"fq_codel","handle":"0:","root":true},{"kind":"fq","parent":"1:10"}]`,
	} {
		t.Run(listing, func(t *testing.T) {
			h := newShapeHost(t)
			h.first("tc -j qdisc show", listing, nil)
			if err := h.SetShaping(context.Background(), "eth0", ShapeRequest{EgressKbit: 50000}, gwClient, "ops"); err == nil {
				t.Fatal("foreign queue overwritten")
			}
			for _, command := range h.tcCommands() {
				if !strings.HasPrefix(command, "tc -j ") {
					t.Fatalf("refused change ran %s", command)
				}
			}
			if h.saved() {
				t.Fatal("refused change saved")
			}
		})
	}
}

const savedFQCoDel = `[{"kind":"fq_codel","handle":"5:","root":true,"options":{"limit":1000,"flows":1024,"quantum":1514,"target":4999,"interval":99999,"memory_limit":33554432,"ecn":false,"drop_batch":64}}]`

func TestFirstShapingFailureRestoresSupportedForeignQueueParameters(t *testing.T) {
	h := newShapeHost(t)
	h.first("tc -j qdisc show", savedFQCoDel, nil)
	h.first("tc qdisc replace dev eth0 root cake", "unsupported cake", errors.New("unsupported cake"))
	if err := h.SetShaping(context.Background(), "eth0", ShapeRequest{Qdisc: "cake", EgressKbit: 50000}, gwClient, "ops"); err == nil {
		t.Fatal("expected failure")
	}
	want := "tc qdisc replace dev eth0 root handle 5: fq_codel limit 1000 flows 1024 quantum 1514 target 5000us interval 100000us memory_limit 33554432 drop_batch 64 noecn"
	if !h.rec.ran(want) {
		t.Fatalf("original queue parameters not restored: %v", h.tcCommands())
	}
	if h.saved() {
		t.Fatal("failed change saved")
	}
}

func TestShapingRefusesForeignFiltersAddedToManagedQueues(t *testing.T) {
	for _, half := range []string{"root", "ingress"} {
		t.Run(half, func(t *testing.T) {
			h := newShapeHost(t)
			ctx := context.Background()
			if err := h.SetShaping(ctx, "eth0", ShapeRequest{EgressKbit: 50000, IngressKbit: 8000}, gwClient, "ops"); err != nil {
				t.Fatal(err)
			}
			command := "tc -j filter show dev eth0 root"
			if half == "ingress" {
				command = "tc -j filter show dev eth0 parent ffff:"
			}
			h.first(command, `[{"kind":"bpf","pref":9,"protocol":"all"}]`, nil)
			before := len(h.tcCommands())
			if err := h.ClearShaping(ctx, "eth0"); err == nil {
				t.Fatal("foreign filters removed during clear")
			}
			for _, command := range h.tcCommands()[before:] {
				if !strings.HasPrefix(command, "tc -j ") {
					t.Fatalf("foreign filters mutated: %s", command)
				}
			}
			if len(h.spec(t).Shaping) != 1 {
				t.Fatal("refused clear changed spec")
			}
		})
	}
}
