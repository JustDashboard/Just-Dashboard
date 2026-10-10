package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gwHost is a Service on a host that answers the way the sanitized one does:
// ufw over iptables-nft, Docker's chains present, forwarding on, reached by a
// client over tailscale0.
type gwHost struct {
	*Service
	rec *recorder
	// loaded is every ruleset handed to `nft -f`, in order.
	loaded []string
	sys    string
}

const (
	gwClient       = "100.110.34.9"
	gwRouteGet     = `[{"dst":"100.110.34.9","dev":"tailscale0","prefsrc":"100.110.34.31","uid":0,"flags":[],"cache":[]}]`
	gwAddrAll      = `[{"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.20","prefixlen":24,"scope":"global"}]},{"ifname":"docker0","addr_info":[{"family":"inet","local":"172.17.0.1","prefixlen":16,"scope":"global"}]},{"ifname":"tailscale0","addr_info":[{"family":"inet","local":"100.110.34.31","prefixlen":32,"scope":"global"}]}]`
	gwAddrEth0     = `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.20","prefixlen":24,"scope":"global"},{"family":"inet6","local":"2001:db8::20","prefixlen":64,"scope":"global"}]}]`
	gwAddrTS       = `[{"ifname":"tailscale0","addr_info":[{"family":"inet","local":"100.110.34.31","prefixlen":32,"scope":"global"}]}]`
	gwDefaultRoute = `[{"dst":"default","gateway":"203.0.113.1","dev":"eth0","protocol":"dhcp"}]`
)

func newGwHost(t *testing.T, allowlist ...string) *gwHost {
	t.Helper()
	rec := record(t)
	h := &gwHost{Service: testService(t, allowlist...), rec: rec, sys: t.TempDir()}
	prevSys := gatewaySysRoot
	gatewaySysRoot = h.sys
	prevClass := gatewayClassNet
	gatewayClassNet = t.TempDir() // no docker0 unless a test makes one
	t.Cleanup(func() { gatewaySysRoot, gatewayClassNet = prevSys, prevClass })
	for path, v := range map[string]string{
		"net/ipv4/ip_forward":                       "1",
		"net/ipv6/conf/all/forwarding":              "1",
		"net/netfilter/nf_conntrack_count":          "1200",
		"net/netfilter/nf_conntrack_max":            "262144",
		"net/ipv4/tcp_congestion_control":           "cubic",
		"net/ipv4/tcp_available_congestion_control": "reno cubic bbr",
		"net/core/default_qdisc":                    "fq_codel",
	} {
		h.writeSys(path, v)
	}
	for _, d := range protectionDefs {
		if _, ok := gatewayReadSysctl(d.Key); !ok {
			h.writeSys(strings.ReplaceAll(d.Key, ".", "/"), "0")
		}
	}

	rec.on("nft -t -j list ruleset", fixture(t, "gateway-ruleset-ufw.json"))
	rec.fail("firewall-cmd", "not running")
	rec.on("ufw status", "Status: active\n")
	rec.on("iptables -S DOCKER-USER", "-N DOCKER-USER\n")
	// Nothing left to delete: the removal loop ends at the first failure.
	rec.fail("iptables -D", "iptables: Bad rule (does a matching rule exist in that chain?).")
	rec.fail("ip6tables -D", "ip6tables: Bad rule (does a matching rule exist in that chain?).")
	rec.on("iptables ", "")
	rec.on("ip6tables ", "")
	rec.on("nft -c -f", "")
	rec.on("nft -f", "")
	rec.on("nft list set", "")
	rec.on("nft -j list set", `{"nftables":[]}`)
	rec.on("nft delete table", "")
	rec.on("nft -t -j list table inet jd_gateway", fixture(t, "gateway-table.json"))
	rec.fail("ip -j addr show dev nope", "Device \"nope\" does not exist.")
	rec.on("ip -j addr show dev eth0", gwAddrEth0)
	rec.on("ip -j addr show dev tailscale0", gwAddrTS)
	rec.on("ip -j addr show", gwAddrAll)
	rec.on("ip -j route show default", gwDefaultRoute)
	rec.on("ip -j -6 route show default", "[]")
	rec.on("ip -j route get "+gwClient, gwRouteGet)
	rec.on("ss -Hlntup", "")
	rec.on("systemctl daemon-reload", "")
	rec.on("systemctl is-enabled", "enabled")
	rec.on("systemctl enable", "")

	prev := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "nft" && len(args) == 2 && args[0] == "-f" {
			if b, err := os.ReadFile(args[1]); err == nil {
				h.loaded = append(h.loaded, string(b))
			}
		}
		return prev(ctx, name, args...)
	}
	t.Cleanup(func() { run = prev })
	return h
}

func (h *gwHost) writeSys(path, v string) {
	full := filepath.Join(h.sys, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(full, []byte(v+"\n"), 0o644); err != nil {
		panic(err)
	}
}

// first makes a command fail (or answer) ahead of the standard replies.
func (h *gwHost) first(prefix, out string, err error) {
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	h.rec.replies = append([]reply{{prefix: prefix, out: out, err: err}}, h.rec.replies...)
}

func (h *gwHost) fail(prefix string) {
	h.first(prefix, prefix+" failed", fmt.Errorf("%s failed", prefix))
}

// seed writes a spec as if an earlier change had saved it.
func (h *gwHost) seed(t *testing.T, sp *Spec) {
	t.Helper()
	b, err := json.Marshal(sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(h.specPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *gwHost) spec(t *testing.T) *Spec {
	t.Helper()
	sp, err := h.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func (h *gwHost) saved() bool {
	_, err := os.Stat(h.specPath())
	return err == nil
}

// order asserts the commands that start with each prefix were run, in that
// order.
func (h *gwHost) order(t *testing.T, prefixes ...string) {
	t.Helper()
	cmds := h.rec.commands()
	at := 0
	for _, p := range prefixes {
		found := false
		for ; at < len(cmds); at++ {
			if strings.HasPrefix(cmds[at], p) {
				found = true
				at++
				break
			}
		}
		if !found {
			t.Fatalf("%q was not run after the commands before it; ran:\n%s", p, strings.Join(cmds, "\n"))
		}
	}
}

func (h *gwHost) noApplyFile(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(h.paths.Dir, gatewayApplyFile)); err == nil {
		t.Error("the candidate ruleset file was left behind")
	}
}

var gwProtected = []int{22, 80, 443}

func gwWebForward() ForwardRequest {
	return ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", TargetPort: "80"}
}

func TestAddForwardAppliesInOrderThenWritesTheFilesAndSpec(t *testing.T) {
	h := newGwHost(t)
	v, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected)
	if err != nil {
		t.Fatal(err)
	}
	h.order(t, "nft -t -j list ruleset", "ip -j addr show", "nft -c -f", "nft -f", "iptables -D FORWARD", "iptables -I FORWARD 1", "nft list set inet jd_gateway trusted4", "systemctl")
	if len(h.loaded) != 1 || !strings.Contains(h.loaded[0], `dnat ip to 10.0.0.5:80 comment "forward:1"`) {
		t.Fatalf("loaded %v", h.loaded)
	}
	h.noApplyFile(t)
	sp := h.spec(t)
	if len(sp.Forwards) != 1 || sp.Forwards[0].ID != 1 || sp.Forwards[0].CreatedBy != "ops" || sp.Forwards[0].CreatedAt.IsZero() {
		t.Fatalf("spec = %+v", sp.Forwards)
	}
	// 10.0.0.5 is no network this host routes, so auto masquerades.
	if sp.Forwards[0].SourceNAT != "auto:always" || v.SourceNat != "auto" || !v.Masquerade || !v.Enabled {
		t.Fatalf("stored %q, view %+v", sp.Forwards[0].SourceNAT, v)
	}
	boot, err := os.ReadFile(filepath.Join(h.paths.Dir, gatewayFile))
	if err != nil || string(boot) != h.loaded[0] {
		t.Fatalf("the boot file is not what was loaded: %v", err)
	}
	if !h.rec.ran("iptables -I DOCKER-USER 1 -m connmark --mark 0x4a000000/0xff000000") {
		t.Error("the Docker chain was not given the admission rule")
	}
}

func TestAddForwardToABridgeNetworkKeepsTheVisitorAddress(t *testing.T) {
	h := newGwHost(t)
	req := gwWebForward()
	req.Target = "172.17.0.2"
	v, err := h.AddForward(context.Background(), req, gwClient, "ops", gwProtected)
	if err != nil {
		t.Fatal(err)
	}
	if v.Masquerade || v.SourceNat != "auto" || h.spec(t).Forwards[0].SourceNAT != "auto:never" {
		t.Fatalf("a target on a bridge this host routes must not be masqueraded: %+v", v)
	}
	if strings.Contains(h.loaded[0], "forward-nat") {
		t.Error("a masquerade rule was written for it anyway")
	}
}

func TestAutoSourceNATIsDecidedAgainWhenTheGatewayIsChangedAgain(t *testing.T) {
	h := newGwHost(t)
	if _, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	// The network of the target is attached afterwards.
	h.first("ip -j addr show", strings.Replace(gwAddrAll, "172.17.0.1", "10.0.0.1", 1), nil)
	if _, err := h.AddLimit(context.Background(), LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", Rate: 5, Per: "minute"}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	if got := h.spec(t).Forwards[0].SourceNAT; got != "auto:never" {
		t.Fatalf("SourceNAT = %q, want it re-decided", got)
	}
}

func TestAddForwardLeavesNothingBehindWhenNftRefusesTheLoad(t *testing.T) {
	h := newGwHost(t)
	h.first("nft -f", "Error: could not process rule", errors.New("Error: could not process rule"))
	_, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected)
	if err == nil || !strings.Contains(err.Error(), "loading the gateway ruleset") {
		t.Fatalf("err = %v", err)
	}
	if h.saved() {
		t.Error("the spec was saved although nothing applied")
	}
	if h.rec.ran("iptables -I") {
		t.Error("admission was inserted for a ruleset that never loaded")
	}
	h.noApplyFile(t)
}

func TestAddForwardPutsTheOldStateBackWhenAdmissionFails(t *testing.T) {
	h := newGwHost(t)
	h.first("iptables -I INPUT", "iptables: Resource temporarily unavailable", errors.New("xtables lock"))
	_, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected)
	if err == nil || !strings.Contains(err.Error(), "INPUT") {
		t.Fatalf("err = %v", err)
	}
	h.order(t, "nft -f", "iptables -I FORWARD 1", "iptables -I INPUT 1", "nft delete table inet jd_gateway", "iptables -D FORWARD")
	if h.saved() {
		t.Error("the spec was saved")
	}
}

func TestAddForwardTakesTheLoadBackWhenVerifyFails(t *testing.T) {
	h := newGwHost(t)
	h.fail("nft list set")
	if _, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected); err == nil {
		t.Fatal("verify failed and the change was reported done")
	}
	h.order(t, "nft -f", "nft list set", "nft delete table inet jd_gateway", "iptables -D FORWARD")
	if h.saved() {
		t.Error("the spec was saved")
	}
}

func TestAnAbsentDockerChainAndAnUnusedIPv6FamilyDoNotBlockIPv4(t *testing.T) {
	h := newGwHost(t)
	h.first("iptables -S DOCKER-USER", "No chain/target/match by that name.", errors.New("no chain"))
	h.fail("ip6tables -I")
	if _, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatalf("a host without that chain was refused: %v", err)
	}
}

func TestEditingAForwardRestoresItsPreviousRulesetOnFailure(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	before := h.spec(t)
	h.loaded = nil
	h.first("nft list set", "gone", errors.New("gone"))
	req := gwWebForward()
	req.TargetPort = "8081"
	if _, err := h.UpdateForward(ctx, 1, req, gwClient, "ops", gwProtected); err == nil {
		t.Fatal("expected the verify failure")
	}
	if len(h.loaded) != 2 || !strings.Contains(h.loaded[1], "dnat ip to 10.0.0.5:80 ") {
		t.Fatalf("the old ruleset was not loaded back; loaded %d", len(h.loaded))
	}
	if got := h.spec(t); got.Forwards[0].TargetPort != before.Forwards[0].TargetPort {
		t.Fatal("the spec changed")
	}
}

func TestForwardsAreReadOnlyWhereTheFirewallCannotAdmitThem(t *testing.T) {
	foreign := `{"nftables":[{"table":{"family":"inet","name":"filter"}},{"chain":{"family":"inet","table":"filter","name":"forward","type":"filter","hook":"forward","prio":0,"policy":"drop"}}]}`
	firewalld := `{"nftables":[{"table":{"family":"inet","name":"firewalld"}},{"chain":{"family":"inet","table":"firewalld","name":"filter_FORWARD","type":"filter","hook":"forward","prio":10,"policy":"accept"}}]}`
	cases := []struct {
		name, listing, firewall string
		firewalldState          string
		reason                  string
	}{
		{"firewalld running", `{"nftables":[]}`, "firewalld", "running\n", "firewalld"},
		{"firewalld's table", firewalld, "firewalld", "", "firewalld"},
		{"a foreign drop-forward table", foreign, "nftables", "", `table "filter" can drop translated traffic in its chain "forward"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newGwHost(t)
			h.first("nft -t -j list ruleset", c.listing, nil)
			if c.firewalldState != "" {
				h.first("firewall-cmd --state", c.firewalldState, nil)
			}
			cap := h.GatewayCapability(context.Background())
			if cap.Writable || cap.Firewall != c.firewall || !strings.Contains(cap.Reason, c.reason) {
				t.Fatalf("capability = %+v", cap)
			}
			if c.firewall == "nftables" && (cap.Blocker == nil || cap.Blocker.Rule != "ct mark and 0xff000000 == 0x4a000000 accept" ||
				!strings.Contains(cap.Reason, "ct mark and 0xff000000 == 0x4a000000 accept")) {
				t.Fatalf("the accept to add was not named: %+v", cap)
			}
			var ro *ReadOnlyError
			_, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected)
			if !errors.As(err, &ro) || !errors.Is(err, ErrReadOnly) {
				t.Fatalf("AddForward err = %v", err)
			}
			_, err = h.AddNAT(context.Background(), NATRequest{Name: "lab", Source: "10.9.0.0/24", Interface: "eth0"}, gwClient, "ops")
			if !errors.As(err, &ro) {
				t.Fatalf("AddNAT err = %v", err)
			}
			if h.rec.ran("nft -f") || h.saved() {
				t.Error("a read-only host was written to")
			}
		})
	}
}

func TestTheRealRulesetOfAUfwDockerTailscaleHostIsWritable(t *testing.T) {
	h := newGwHost(t)
	cap := h.GatewayCapability(context.Background())
	if !cap.Writable || cap.Firewall != "ufw" || !cap.Docker || cap.Blocker != nil {
		t.Fatalf("capability = %+v", cap)
	}
}

func TestNftMissingIsAnInstallHandOffNotAReadOnlyHost(t *testing.T) {
	h := newGwHost(t)
	h.first("nft -t -j list ruleset", "", &UnavailableError{Tool: "nft"})
	_, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected)
	var missing *UnavailableError
	if !errors.As(err, &missing) || missing.Package != "nftables" {
		t.Fatalf("err = %v", err)
	}
}

func TestDockerIsFoundByItsInterfaceWhenIptablesCannotBeListed(t *testing.T) {
	h := newGwHost(t)
	h.first("iptables -S DOCKER-USER", "", errors.New("no chain"))
	if h.GatewayCapability(context.Background()).Docker {
		t.Fatal("no chain and no docker0, yet Docker was reported")
	}
	class := t.TempDir()
	if err := os.Mkdir(filepath.Join(class, "docker0"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := gatewayClassNet
	gatewayClassNet = class
	t.Cleanup(func() { gatewayClassNet = prev })
	if !h.GatewayCapability(context.Background()).Docker {
		t.Fatal("docker0 was not noticed")
	}
}

func TestEntriesCanAlwaysBeRemovedAndDisabledOnAReadOnlyHost(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	h.first("firewall-cmd --state", "running\n", nil)
	off := false
	req := gwWebForward()
	req.Enabled = &off
	if _, err := h.UpdateForward(ctx, 1, req, gwClient, "ops", gwProtected); err != nil {
		t.Fatalf("disabling was refused on a read-only host: %v", err)
	}
	if h.spec(t).Forwards[0].Enabled {
		t.Fatal("still enabled")
	}
	on := true
	req.Enabled = &on
	var ro *ReadOnlyError
	if _, err := h.UpdateForward(ctx, 1, req, gwClient, "ops", gwProtected); !errors.As(err, &ro) {
		t.Fatalf("enabling must be refused, err = %v", err)
	}
	if err := h.DeleteForward(ctx, 1); err != nil {
		t.Fatalf("removing was refused on a read-only host: %v", err)
	}
	if len(h.spec(t).Forwards) != 0 {
		t.Fatal("not removed")
	}
	// With nothing left to admit the admission rules are taken out again.
	h.order(t, "iptables -D FORWARD")
}

func TestForwardingMustBeOnForTheEntryToCarryTraffic(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	h.writeSys("net/ipv4/ip_forward", "0")
	_, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected)
	var off *ForwardingRequiredError
	if !errors.As(err, &off) || off.Family != "4" || !strings.Contains(err.Error(), "Routing page") {
		t.Fatalf("err = %v", err)
	}
	if h.rec.ran("nft -f") || h.saved() {
		t.Error("applied with forwarding off")
	}
	// A v6 target needs the v6 switch, not the v4 one.
	h.writeSys("net/ipv4/ip_forward", "1")
	h.writeSys("net/ipv6/conf/all/forwarding", "0")
	req := gwWebForward()
	req.Target, req.TargetPort = "2001:db8::5", ""
	if _, err := h.AddForward(ctx, req, gwClient, "ops", gwProtected); !errors.As(err, &off) || off.Family != "6" {
		t.Fatalf("err = %v", err)
	}
	// NAT entries are checked the same way.
	h.writeSys("net/ipv4/ip_forward", "0")
	if _, err := h.AddNAT(ctx, NATRequest{Name: "lab", Source: "10.9.0.0/24", Interface: "eth0"}, gwClient, "ops"); !errors.As(err, &off) || off.Family != "4" {
		t.Fatalf("NAT err = %v", err)
	}
	// An entry already working is not refused for an edit that does not enable it.
	h.writeSys("net/ipv4/ip_forward", "1")
	if _, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	h.writeSys("net/ipv4/ip_forward", "0")
	req = gwWebForward()
	req.Name = "renamed"
	if _, err := h.UpdateForward(ctx, 1, req, gwClient, "ops", gwProtected); err != nil {
		t.Fatalf("renaming a working forward was refused: %v", err)
	}
}

func TestForwardGuards(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ForwardRequest)
		want   string // empty is accepted
		err    error
	}{
		{"ssh on every interface", func(r *ForwardRequest) { r.Ports, r.TargetPort = "22", "" }, "port 22 is how this server answers you", nil},
		{"https on every interface", func(r *ForwardRequest) { r.Ports, r.TargetPort = "443", "" }, "port 443", nil},
		{"a range containing both web ports", func(r *ForwardRequest) { r.Ports, r.TargetPort = "75-90", "" }, "port 80", nil},
		{"ssh on the interface the client arrives on", func(r *ForwardRequest) { r.Ports, r.TargetPort, r.Interface = "22", "", "tailscale0" }, "port 22", nil},
		{"ssh on another interface", func(r *ForwardRequest) { r.Ports, r.TargetPort, r.Interface = "22", "", "eth0" }, "", nil},
		{"ssh for networks the client is not in", func(r *ForwardRequest) { r.Ports, r.TargetPort, r.Sources = "22", "", []string{"198.51.100.0/24"} }, "", nil},
		{"ssh for a network the client is in", func(r *ForwardRequest) { r.Ports, r.TargetPort, r.Sources = "22", "", []string{"100.64.0.0/10"} }, "port 22", nil},
		{"udp on the web port", func(r *ForwardRequest) { r.Protocol, r.Ports, r.TargetPort = "udp", "443", "" }, "", nil},
		{"loopback target", func(r *ForwardRequest) { r.Target = "127.0.0.1" }, "route_localnet", nil},
		{"range size mismatch", func(r *ForwardRequest) { r.Ports, r.TargetPort = "8000-8010", "9000-9004" }, "same size", nil},
		{"an interface that does not exist", func(r *ForwardRequest) { r.Interface = "nope" }, "no interface called nope", nil},
		{"bad source translation", func(r *ForwardRequest) { r.SourceNAT = "auto:never" }, "auto, always or never", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newGwHost(t)
			req := gwWebForward()
			c.mutate(&req)
			_, err := h.AddForward(context.Background(), req, gwClient, "ops", gwProtected)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if c.want != "" && strings.Contains(c.want, "answers you") {
				var g *GuardError
				if !errors.As(err, &g) {
					t.Errorf("a takeover of the reader's port must be a guard (409), not %T", err)
				}
			}
			if c.want != "" && (h.rec.ran("nft -f") || h.saved()) {
				t.Error("a refused forward was applied")
			}
		})
	}
}

func TestDuplicateForwardIsRefused(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	req := gwWebForward()
	req.Name, req.Target = "second", "10.0.0.6"
	_, err := h.AddForward(ctx, req, gwClient, "ops", gwProtected)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	req.Ports, req.TargetPort = "8081", "80"
	if _, err := h.AddForward(ctx, req, gwClient, "ops", gwProtected); err != nil {
		t.Fatalf("a different port was refused: %v", err)
	}
}

func TestUpdateAndDeleteOfAMissingEntryAreNotFound(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.UpdateForward(ctx, 9, gwWebForward(), gwClient, "ops", gwProtected); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateForward err = %v", err)
	}
	if err := h.DeleteForward(ctx, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteForward err = %v", err)
	}
	if err := h.DeleteNAT(ctx, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteNAT err = %v", err)
	}
	if err := h.DeleteLimit(ctx, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteLimit err = %v", err)
	}
	if err := h.DeleteBlocklist(ctx, 9, gwClient); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteBlocklist err = %v", err)
	}
}

func TestUpdateForwardKeepsItsIdentityAndHonoursEnabled(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	first := h.spec(t).Forwards[0]
	req := gwWebForward()
	req.Name = "renamed"
	if _, err := h.UpdateForward(ctx, 1, req, gwClient, "someone-else", gwProtected); err != nil {
		t.Fatal(err)
	}
	got := h.spec(t).Forwards[0]
	if got.ID != 1 || got.Name != "renamed" || got.CreatedBy != "ops" || !got.CreatedAt.Equal(first.CreatedAt) || !got.Enabled {
		t.Fatalf("after an edit: %+v", got)
	}
	off := false
	req.Enabled = &off
	if _, err := h.UpdateForward(ctx, 1, req, gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	if h.spec(t).Forwards[0].Enabled {
		t.Fatal("not disabled")
	}
	if strings.Contains(h.loaded[len(h.loaded)-1], "forward:1") {
		t.Fatal("a disabled forward is still in the loaded ruleset")
	}
}

// ---- NAT

func TestAddNATAppliesAndAdmits(t *testing.T) {
	h := newGwHost(t)
	v, err := h.AddNAT(context.Background(), NATRequest{Name: "lab", Source: "10.9.0.77/24", Interface: "eth0", ToAddress: "203.0.113.20"}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	h.order(t, "ip -j addr show dev eth0", "nft -c -f", "nft -f", "iptables -I FORWARD 1", "nft list set")
	if v.Source != "10.9.0.0/24" || !v.Enabled {
		t.Fatalf("view = %+v", v)
	}
	if !strings.Contains(h.loaded[0], `snat ip to 203.0.113.20 comment "nat:1"`) || !strings.Contains(h.loaded[0], `ct state new ct mark set`) {
		t.Fatalf("ruleset:\n%s", h.loaded[0])
	}
}

func TestNATRefusals(t *testing.T) {
	cases := []struct {
		name string
		req  NATRequest
		want string
	}{
		{"every source", NATRequest{Name: "x", Source: "0.0.0.0/0", Interface: "eth0"}, "everything this server sends"},
		{"an interface that does not exist", NATRequest{Name: "x", Source: "10.9.0.0/24", Interface: "nope"}, "no interface called nope"},
		{"an address the interface does not hold", NATRequest{Name: "x", Source: "10.9.0.0/24", Interface: "eth0", ToAddress: "203.0.113.99"}, "not an address of eth0"},
		{"an address of the other family", NATRequest{Name: "x", Source: "10.9.0.0/24", Interface: "eth0", ToAddress: "2001:db8::20"}, "not the same kind"},
		{"no name", NATRequest{Source: "10.9.0.0/24", Interface: "eth0"}, "name"},
		{"not a network", NATRequest{Name: "x", Source: "lab", Interface: "eth0"}, "IP address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newGwHost(t)
			_, err := h.AddNAT(context.Background(), c.req, gwClient, "ops")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if h.saved() {
				t.Error("saved")
			}
		})
	}
}

func TestOwnedNATEntriesAreChangedByTheirOwnerOnly(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	sp := emptySpec()
	upsertOwnedNAT(sp, "wireguard:wg0", "wg0 clients", "10.8.0.0/24", "eth0", "ops")
	h.seed(t, sp)
	id := sp.NAT[0].ID
	if _, err := h.UpdateNAT(ctx, id, NATRequest{Name: "mine", Source: "10.8.0.0/24", Interface: "eth0"}, gwClient, "ops"); !errors.Is(err, ErrNotManaged) {
		t.Errorf("UpdateNAT err = %v", err)
	}
	if err := h.DeleteNAT(ctx, id); !errors.Is(err, ErrNotManaged) || !strings.Contains(err.Error(), "wireguard:wg0") {
		t.Errorf("DeleteNAT err = %v", err)
	}
}

func TestUpsertAndRemoveOwnedNAT(t *testing.T) {
	sp := emptySpec()
	upsertOwnedNAT(sp, "wireguard:wg0", "wg0", "10.8.0.0/24", "eth0", "ops")
	upsertOwnedNAT(sp, "wireguard:wg0", "wg0 (exit)", "10.8.0.0/24", "eth1", "ops") // the same source: updated in place
	upsertOwnedNAT(sp, "wireguard:wg0", "wg0 v6", "fd00:8::/64", "eth1", "ops")
	upsertOwnedNAT(sp, "wireguard:wg1", "wg1", "10.9.0.0/24", "eth0", "ops")
	sp.NAT = append(sp.NAT, NATSpec{ID: sp.takeID(), Name: "mine", Source: "10.5.0.0/24", Interface: "eth0", Enabled: true})
	if len(sp.NAT) != 4 {
		t.Fatalf("upsert made %d entries: %+v", len(sp.NAT), sp.NAT)
	}
	if n := sp.NAT[0]; n.Name != "wg0 (exit)" || n.Interface != "eth1" || n.Owner != "wireguard:wg0" || !n.Enabled || n.CreatedBy != "ops" || n.CreatedAt.IsZero() {
		t.Fatalf("entry = %+v", n)
	}
	removeOwnedNAT(sp, "wireguard:wg0")
	if len(sp.NAT) != 2 || sp.NAT[0].Owner != "wireguard:wg1" || sp.NAT[1].Name != "mine" {
		t.Fatalf("after removal: %+v", sp.NAT)
	}
	ids := map[int]bool{}
	for _, n := range sp.NAT {
		if ids[n.ID] {
			t.Fatal("ids repeat")
		}
		ids[n.ID] = true
	}
}

// ---- Limits

func TestAddLimitKeepsTheReaderOutOfItsReach(t *testing.T) {
	h := newGwHost(t)
	v, err := h.AddLimit(context.Background(), LimitRequest{Name: "ssh", Protocol: "tcp", Ports: "22", Rate: 10, Per: "minute", Burst: 5, Action: "reject"}, "198.51.100.44", "ops")
	if err != nil {
		t.Fatal(err)
	}
	sp := h.spec(t)
	if len(sp.Trusted) != 1 || sp.Trusted[0] != "198.51.100.44/32" {
		t.Fatalf("the reader was not kept out of the drops: %v", sp.Trusted)
	}
	if !strings.Contains(h.loaded[0], "elements = { 127.0.0.0/8, 198.51.100.44 }") {
		t.Fatalf("trusted set in the ruleset:\n%s", h.loaded[0])
	}
	if v.ID != 1 || v.Packets != 0 {
		t.Fatalf("view = %+v", v)
	}
	// A limit only drops: it needs no admission, and nothing is inserted.
	if h.rec.ran("iptables -I") || h.rec.ran("iptables -D") {
		t.Error("a limit touched the admission rules")
	}
}

func TestAddLimitDoesNotTrustAClientTheAllowlistAlreadyCovers(t *testing.T) {
	h := newGwHost(t, "100.64.0.0/10")
	if _, err := h.AddLimit(context.Background(), LimitRequest{Name: "ssh", Protocol: "tcp", Ports: "22", Rate: 10, Per: "minute"}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	if got := h.spec(t).Trusted; len(got) != 0 {
		t.Fatalf("trusted = %v", got)
	}
}

func TestLimitRefusals(t *testing.T) {
	cases := []struct {
		name string
		req  LimitRequest
		want string
	}{
		{"neither a rate nor connections", LimitRequest{Name: "x", Protocol: "tcp", Ports: "22"}, "needs a rate"},
		{"rate above the range", LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", Rate: 1_000_001, Per: "second"}, "rate"},
		{"a rate with no unit", LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", Rate: 5}, "per second"},
		{"burst below zero", LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", Rate: 5, Per: "second", Burst: -1}, "burst"},
		{"connections above the range", LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", MaxConnections: 2_000_000}, "connection limit"},
		{"an action that admits", LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", Rate: 5, Per: "second", Action: "accept"}, "drop or reject"},
		{"no port", LimitRequest{Name: "x", Protocol: "tcp", Rate: 5, Per: "second"}, "port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newGwHost(t)
			_, err := h.AddLimit(context.Background(), c.req, gwClient, "ops")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if h.saved() {
				t.Error("saved")
			}
		})
	}
}

func TestLimitsAreNotHeldBackByAFirewallThatCannotAdmitForwards(t *testing.T) {
	h := newGwHost(t)
	h.first("firewall-cmd --state", "running\n", nil)
	if _, err := h.AddLimit(context.Background(), LimitRequest{Name: "ssh", Protocol: "tcp", Ports: "22", Rate: 10, Per: "minute"}, gwClient, "ops"); err != nil {
		t.Fatalf("a drop works whatever else filters: %v", err)
	}
}

func TestLimitRoundTrip(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	v, err := h.AddLimit(ctx, LimitRequest{Name: "app", Protocol: "both", Ports: "8000-8010", Rate: 10, Per: "second", Burst: 20, PerSource: true, MaxConnections: 50}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != "drop" || !v.Enabled {
		t.Fatalf("defaults: %+v", v)
	}
	// Dropping the rate clears what only a rate uses.
	if _, err := h.UpdateLimit(ctx, v.ID, LimitRequest{Name: "app", Protocol: "tcp", Ports: "8000", MaxConnections: 5, Per: "minute", PerSource: true, Burst: 9}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	l := h.spec(t).Limits[0]
	if l.Rate != 0 || l.Per != "" || l.Burst != 0 || l.PerSource || l.MaxConnections != 5 {
		t.Fatalf("after an edit: %+v", l)
	}
	if err := h.DeleteLimit(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if len(h.spec(t).Limits) != 0 || strings.Contains(h.loaded[len(h.loaded)-1], "limit:") {
		t.Fatal("limit remained")
	}
}

// ---- Reading

func TestGatewayViewJoinsCountersByComment(t *testing.T) {
	h := newGwHost(t)
	sp, _, _ := gwFullSpec()
	h.seed(t, sp)
	v, err := h.Gateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Loaded || !v.Forwarding.IPv4 || !v.Forwarding.IPv6 || !v.Admission.Needed || !v.Admission.Present || !v.Capability.Writable {
		t.Fatalf("view = %+v", v)
	}
	if len(v.Forwards) != 4 || len(v.NAT) != 3 {
		t.Fatalf("%d forwards, %d NAT", len(v.Forwards), len(v.NAT))
	}
	by := map[int]ForwardView{}
	for _, f := range v.Forwards {
		by[f.ID] = f
	}
	if f := by[2]; f.Packets != 3 || f.Bytes != 180 || f.SourceNat != "never" || f.Masquerade {
		t.Fatalf("forward 2 = %+v", f)
	}
	if f := by[1]; f.SourceNat != "auto" || !f.Masquerade || len(f.Sources) != 2 {
		t.Fatalf("forward 1 = %+v", f)
	}
	if n := v.NAT[0]; n.Packets != 7 || n.Owner != "wireguard:wg0" {
		t.Fatalf("nat 5 = %+v", n)
	}
	b, _ := json.Marshal(v)
	for _, want := range []string{`"sourceNat":"auto"`, `"createdBy":"admin"`, `"sources":["198.51.100.0/24","203.0.113.7/32"]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("json lacks %s: %s", want, b)
		}
	}
}

func TestGatewayViewWithNothingMadeOrNotLoadedIsEmptyListsAndZeroCounters(t *testing.T) {
	h := newGwHost(t)
	h.first("nft -t -j list table inet jd_gateway", "Error: No such file or directory", errors.New("no such table"))
	h.first("iptables -C FORWARD", "Bad rule", errors.New("Bad rule"))
	v, err := h.Gateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Loaded || v.Admission.Present || v.Admission.Needed {
		t.Fatalf("view = %+v", v)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"forwards":[]`) || !strings.Contains(string(b), `"nat":[]`) {
		t.Fatalf("empty lists must serialise as []: %s", b)
	}
	sp, _, _ := gwFullSpec()
	h.seed(t, sp)
	v, _ = h.Gateway(context.Background())
	for _, f := range v.Forwards {
		if f.Packets != 0 {
			t.Fatalf("a table that is not loaded has counters: %+v", f)
		}
	}
}

func TestAnUnreadableSpecIsAnErrorNotAnEmptyGateway(t *testing.T) {
	h := newGwHost(t)
	if err := os.MkdirAll(h.paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.specPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Gateway(context.Background()); err == nil {
		t.Fatal("an unreadable spec read as empty")
	}
	if _, err := h.AddLimit(context.Background(), LimitRequest{Name: "x", Protocol: "tcp", Ports: "22", Rate: 1, Per: "second"}, gwClient, "ops"); err == nil {
		t.Fatal("a change was made over an unreadable spec")
	}
}

func TestAdmissionRemovalRepeatsUntilNoCopyIsLeft(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	// Three copies of the rule sit in FORWARD (and one on the others): each -D
	// succeeds while there is one and fails when there is none.
	copies := map[string]int{"iptables -D FORWARD": 3, "iptables -D INPUT": 1}
	var mu sync.Mutex
	prev := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		for prefix := range copies {
			if strings.HasPrefix(line, prefix) {
				mu.Lock()
				defer mu.Unlock()
				if copies[prefix] > 0 {
					copies[prefix]--
					return "", nil
				}
				return "", errors.New("Bad rule")
			}
		}
		return prev(ctx, name, args...)
	}
	t.Cleanup(func() { run = prev })
	h.rec.mu.Lock()
	n := len(h.rec.calls)
	h.rec.mu.Unlock()
	if err := h.DeleteForward(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if copies["iptables -D FORWARD"] != 0 || copies["iptables -D INPUT"] != 0 {
		t.Fatalf("copies left: %v", copies)
	}
	// Chains with nothing to remove still get exactly one failing attempt.
	got := 0
	for _, c := range h.rec.commands()[n:] {
		if strings.HasPrefix(c, "iptables -D DOCKER-USER") {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("DOCKER-USER was tried %d times", got)
	}
}

func TestAdmissionRemovalStopsAtTheCapWhateverTheKernelSays(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddForward(ctx, gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	h.first("iptables -D FORWARD", "", nil) // always succeeds
	h.rec.mu.Lock()
	n := len(h.rec.calls)
	h.rec.mu.Unlock()
	if err := h.DeleteForward(ctx, 1); err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, c := range h.rec.commands()[n:] {
		if strings.HasPrefix(c, "iptables -D FORWARD") {
			got++
		}
	}
	if got != admissionDeleteCap {
		t.Fatalf("-D FORWARD ran %d times, want the cap of %d", got, admissionDeleteCap)
	}
}

func TestTheRulesetAndTableAreReadTerse(t *testing.T) {
	h := newGwHost(t)
	if _, err := h.Gateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.rec.commands() {
		if strings.HasPrefix(c, "nft ") && strings.Contains(c, " list ") && !strings.Contains(c, "list set") && !strings.HasPrefix(c, "nft -t -j list ") {
			t.Errorf("a listing that carries every set element: %s", c)
		}
	}
	if !h.rec.ran("nft -t -j list ruleset") || !h.rec.ran("nft -t -j list table inet jd_gateway") {
		t.Fatalf("commands: %v", h.rec.commands())
	}
}
