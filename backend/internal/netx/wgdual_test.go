package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wgDualRuleFixture(n NATSpec) string {
	p := netip.MustParsePrefix(n.Source)
	protocol := "ip"
	if p.Addr().Is6() {
		protocol = "ip6"
	}
	selectors := fmt.Sprintf(`{"match":{"op":"==","left":{"payload":{"protocol":%q,"field":"saddr"}},"right":{"prefix":{"addr":%q,"len":%d}}}},{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":%q}}`, protocol, p.Masked().Addr().String(), p.Bits(), n.Interface)
	return fmt.Sprintf(`{"nftables":[{"chain":{"family":"inet","table":"jd_gateway","name":"nat_post","type":"nat","hook":"postrouting","prio":90,"policy":"accept"}},{"chain":{"family":"inet","table":"jd_gateway","name":"forward","type":"filter","hook":"forward","prio":-10,"policy":"accept"}},{"rule":{"family":"inet","table":"jd_gateway","chain":"nat_post","comment":"nat:%d","expr":[%s,{"counter":{}},{"masquerade":null}]}},{"rule":{"family":"inet","table":"jd_gateway","chain":"forward","comment":"nat-mark:%d","expr":[%s,{"match":{"op":"in","left":{"ct":{"key":"state"}},"right":"new"}},{"mangle":{"key":{"ct":{"key":"mark"}},"value":{"|":[{"&":[{"ct":{"key":"mark"}},1258291199]},1241513984]}}}]}}]}`, n.ID, selectors, n.ID, selectors)
}

func wgDualRulesFixture(ns ...NATSpec) string {
	var all []json.RawMessage
	for _, n := range ns {
		var parsed struct {
			Nftables []json.RawMessage `json:"nftables"`
		}
		_ = json.Unmarshal([]byte(wgDualRuleFixture(n)), &parsed)
		all = append(all, parsed.Nftables...)
	}
	b, _ := json.Marshal(struct {
		Nftables []json.RawMessage `json:"nftables"`
	}{all})
	return string(b)
}

func wgDualReplies(t *testing.T, rec *recorder) {
	t.Helper()
	links := strings.TrimSuffix(strings.TrimSpace(fixture(t, "wg-ip-link.json")), "]") + `,{"ifname":"eth6"}]`
	addrs := strings.TrimSuffix(strings.TrimSpace(fixture(t, "wg-ip-addr.json")), "]") + `,{"ifname":"eth6","addr_info":[{"local":"2001:db8:6::2","prefixlen":64}]}]`
	rec.on("ip -j -d link show", links).
		on("ip -j addr", addrs).
		on("ip -j -6 route get", `[{"dev":"eth6"}]`)
	wgHostReplies(t, rec)
	wgCommitReplies(rec)
}

func wgDualDump(name string, conf *wgConf) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\t(none)\t%s\t51820\toff\n", name, fixPub0)
	for _, peer := range conf.peers() {
		psk, keepalive := peer.get("presharedkey"), peer.get("persistentkeepalive")
		if psk == "" {
			psk = "(none)"
		}
		if keepalive == "" {
			keepalive = "off"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t(none)\t%s\t0\t0\t0\t%s\n", name, peer.get("publickey"), psk, strings.Join(peer.list("allowedips"), ","), keepalive)
	}
	return b.String()
}

func TestWGIPv6AllocationValidationAndBoundedPeerGaps(t *testing.T) {
	host := wgHostState{prefixes: []wgHostPrefix{{prefix: netip.MustParsePrefix("fd42:1::/64"), what: "a host address"}}}
	other := []wgHostPrefix{{prefix: netip.MustParsePrefix("fd42:2::/65"), what: "a native peer"}}
	for _, raw := range []string{"192.168.8.0/24", "2001:db8:8::/64", "fe80::/64", "fd42:8::/48", "fd42:8::/128", "fd42:1::/64", "fd42:2::/64"} {
		if _, err := wgIPv6Subnet(raw, host, other); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	p, err := wgIPv6Subnet("fd42:8::99/64", host, other)
	if err != nil || p.String() != "fd42:8::/64" {
		t.Fatalf("normalised: %s %v", p, err)
	}
	auto, err := wgIPv6Subnet("", host, other)
	if err != nil || auto.Bits() != 64 || auto.Addr().As16()[0] != 0xfd {
		t.Fatalf("default: %s %v", auto, err)
	}
	taken := []netip.Prefix{netip.MustParsePrefix("fd42:8::/65"), netip.MustParsePrefix("fd42:8:0:0:8000::/127")}
	addr, err := wgNextPeerAddress(p, netip.MustParseAddr("fd42:8::1"), taken)
	if err != nil || addr.String() != "fd42:8::8000:0:0:2" {
		t.Fatalf("gap allocation: %s %v", addr, err)
	}
	for _, all := range []string{"fd42:8::/64", "::/0"} {
		if _, err := wgNextPeerAddress(p, netip.MustParseAddr("fd42:8::1"), []netip.Prefix{netip.MustParsePrefix(all)}); err == nil {
			t.Fatalf("allocated inside %s", all)
		}
	}
}

func TestWGExitEvidenceRejectsPartialWrongAndNarrowedRules(t *testing.T) {
	n := NATSpec{ID: 2, Source: "fd42:8::/64", Interface: "eth6"}
	good := wgDualRuleFixture(n)
	if err := wgCheckExitRules(good, n); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"nftables":[]}`,
		strings.ReplaceAll(good, `"eth6"`, `"eth0"`),
		strings.ReplaceAll(good, `"postrouting"`, `"prerouting"`),
		strings.ReplaceAll(good, `"ip6"`, `"ip"`),
		strings.ReplaceAll(good, `"fd42:8::"`, `"fd42:9::"`),
		strings.ReplaceAll(good, `"masquerade":null`, `"counter":{}`),
		strings.ReplaceAll(good, "1241513984", "42"),
		strings.Replace(good, `{"masquerade":null}`, `{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"eth0"}},{"masquerade":null}`, 1),
		strings.ReplaceAll(good, `{"masquerade":null}`, `{"masquerade":null},{"masquerade":null}`),
		strings.Replace(good, `{"masquerade":null}`, `{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":443}},{"masquerade":null}`, 1),
		"not json",
	} {
		if err := wgCheckExitRules(bad, n); err == nil {
			t.Fatalf("accepted incomplete rule evidence: %s", bad)
		}
	}
}

func TestWGExitSourceLookupRefusesTunnelOrMissingIPv6Uplink(t *testing.T) {
	for _, condition := range []string{"source_route", "native_tunnel", "no_address", "private_address", "unreadable"} {
		t.Run(condition, func(t *testing.T) {
			rec := record(t)
			switch condition {
			case "native_tunnel":
				rec.on("ip -j -d link show", `[{"ifname":"eth0"},{"ifname":"eth6","linkinfo":{"info_kind":"wireguard"}}]`)
			case "no_address":
				rec.on("ip -j addr", fixture(t, "wg-ip-addr.json"))
			case "private_address":
				rec.on("ip -j addr", `[{"ifname":"eth6","addr_info":[{"local":"fd99::2","prefixlen":64}]}]`)
			case "unreadable":
				rec.on("ip -j -6 route get", "not JSON")
			}
			wgDualReplies(t, rec)
			host, err := wgReadHostState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got, err := wgExitUplink(context.Background(), host, "wgd", netip.MustParsePrefix("fd42:8::/64"))
			if condition == "source_route" {
				if err != nil || got != "eth6" || !rec.ran("ip -j -6 route get 2606:4700:4700::1111 from fd42:8::2 iif wgd") {
					t.Fatalf("IPv6 source lookup got %q %v", got, err)
				}
			} else if err == nil {
				t.Fatalf("unusable IPv6 uplink accepted: %q", got)
			}
		})
	}
}

func TestWGFamilyEvidenceDoesNotVerifyAnotherSubnetExit(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	wgDualReplies(t, rec)
	host, err := wgReadHostState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	conf := parseWGConf("# Made by Just Dashboard\n[Interface]\n# jd:ipv6=ula64\nAddress = 10.11.0.1/24, fd42:8::1/64\n")
	host.addrs["wgd"] = []netip.Prefix{netip.MustParsePrefix("10.11.0.1/24"), netip.MustParsePrefix("fd42:8::1/64")}
	sp := emptySpec()
	sp.NAT = []NATSpec{{ID: 1, Owner: wgOwner("wgd"), Source: "fd42:9::/64", Interface: "eth6", Enabled: true}}
	ifc := WGInterface{Name: "wgd", Up: true, IPv6Enabled: true}
	s.wgFamilyEvidence(context.Background(), &ifc, conf, host, nil, sp, Capability{Writable: true}, AdmissionState{})
	if ifc.Families.IPv6.Exit.Runtime != "degraded" || !strings.Contains(ifc.Families.IPv6.Exit.Reason, "source differs") {
		t.Fatalf("wrong subnet claimed verified: %+v", ifc.Families.IPv6)
	}
}

func TestCreateWireGuardDualStackCommitsBothFamiliesOrNeither(t *testing.T) {
	for _, missing6 := range []bool{false, true} {
		t.Run(fmt.Sprint(missing6), func(t *testing.T) {
			s := vpnService(t)
			rec := record(t)
			ns := []NATSpec{{ID: 1, Source: "10.11.0.0/24", Interface: "eth0"}, {ID: 2, Source: "fd42:8::/64", Interface: "eth6"}}
			if missing6 {
				ns = ns[:1]
			}
			rec.on("nft -j list table inet "+gatewayTable, wgDualRulesFixture(ns...))
			wgDualReplies(t, rec)
			wgUnitReplies(rec, "wgd")
			rec.on("systemctl enable --now wg-quick@wgd", "").on("systemctl disable --now wg-quick@wgd", "")
			made, err := s.CreateWireGuard(context.Background(), WGServerRequest{Name: "wgd", Endpoint: "vpn.example.test", ExitNode: true, IPv6: &WGIPv6Request{Subnet: "fd42:8::/64", ExitNode: true}}, "operator")
			if missing6 {
				if err == nil || made != nil {
					t.Fatal("partial IPv6 runtime was accepted")
				}
				if _, err := os.Stat(filepath.Join(s.paths.WireGuard, "wgd.conf")); !os.IsNotExist(err) {
					t.Fatal("failed server file retained")
				}
				if len(wgMustSpec(t, s).NAT) != 0 {
					t.Fatal("one family's intent persisted after failed verification")
				}
				if !rec.ran("systemctl disable --now wg-quick@wgd") {
					t.Fatal("created device was not stopped")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !made.Interface.IPv6Enabled || !made.Interface.Families.IPv6.Configured || !made.Interface.Families.IPv6.Exit.Configured {
				t.Fatalf("view: %+v", made.Interface)
			}
			text, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wgd.conf"))
			if !strings.Contains(string(text), "Address = 10.11.0.1/24, fd42:8::1/64") || !strings.Contains(string(text), "# jd:ipv6=ula64") {
				t.Fatalf("dual config: %s", text)
			}
			sp := wgMustSpec(t, s)
			if len(sp.NAT) != 2 || sp.NAT[0].Interface != "eth0" || sp.NAT[1].Interface != "eth6" {
				t.Fatalf("families: %+v", sp.NAT)
			}
		})
	}
}

func TestWireGuardIPv6RefusesUnverifiedForwardingRoutesAndNativeInventory(t *testing.T) {
	for _, condition := range []string{"forwarding", "route", "native_dump", "native_overlap"} {
		t.Run(condition, func(t *testing.T) {
			s := vpnService(t)
			rec := record(t)
			switch condition {
			case "forwarding":
				wgSetForwarding(t, false)
			case "route":
				rec.fail("ip -j -6 route get", "unreachable")
			case "native_dump":
				rec.on("wg show all dump", "unreadable")
			case "native_overlap":
				rec.on("wg show all dump", fmt.Sprintf("foreign\t%s\t%s\t1\t0\nforeign\t%s\t(none)\t(none)\tfd42:8::/65\t0\t0\t0\t0\n", fixPriv0, fixPub0, fixPeerA))
			}
			wgDualReplies(t, rec)
			wgUnitReplies(rec, "wgd")
			rec.on("systemctl enable --now wg-quick@wgd", "").on("systemctl disable --now wg-quick@wgd", "")
			if _, err := s.CreateWireGuard(context.Background(), WGServerRequest{Name: "wgd", Endpoint: "vpn.example.test", ExitNode: true, IPv6: &WGIPv6Request{Subnet: "fd42:8::/64", ExitNode: true}}, "operator"); err == nil {
				t.Fatal("unsafe dual exit accepted")
			}
			if len(wgMustSpec(t, s).NAT) != 0 {
				t.Fatal("exit intent changed on refusal")
			}
			if condition != "route" && rec.ran("systemctl enable --now wg-quick@wgd") {
				t.Fatal("mutation preceded validation")
			}
		})
	}
}

func TestWireGuardDualPeersUseNativeAllocationAndPreserveLegacyContainment(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			s := vpnService(t)
			conf := parseWGConf(fixture(t, "wg-managed.conf"))
			conf.iface().setBodyMeta("ipv6", "ula64")
			conf.iface().set("Address", "10.8.0.1/24, fd42:8::1/64")
			conf.appendPeer(parseWGConf("[Peer]\nPublicKey = " + fixPeerC + "\nAllowedIPs = fd42:8::/65\n").peers()[0])
			if err := s.writeWGConf("wg0", conf); err != nil {
				t.Fatal(err)
			}
			rec := record(t)
			dump := wgDualDump("wg0", conf)
			rec.on("wg show all dump", dump)
			wgDualReplies(t, rec)
			wgUnitReplies(rec, "wg0")
			rec.on("systemctl reload wg-quick@wg0", "")
			res, err := s.AddWireGuardPeer(context.Background(), "wg0", WGPeerRequest{Name: "dual", Kind: wgKindDevice, FullTunnel: full, ShareNetworks: []string{"fd55::/64"}}, "", "operator")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(res.Config, "Address = 10.8.0.4/32, fd42:8:0:0:8000::/128") {
				t.Fatalf("native gap ignored: %s", res.Config)
			}
			if strings.Contains(res.Config, "This tunnel carries IPv4.") {
				t.Fatal("dual config claims IPv4-only containment")
			}
			if full && !strings.Contains(res.Config, "AllowedIPs = 0.0.0.0/0, ::/0") {
				t.Fatal("one full family escaped the tunnel")
			}
			if !full && !strings.Contains(res.Config, "AllowedIPs = 10.8.0.0/24, fd42:8::/64, fd55::/64") {
				t.Fatal("IPv6 split networks missing")
			}
			stored, err := s.WireGuardPeerConfig(context.Background(), "wg0", res.Peer.ID)
			if err != nil || stored.Config != res.Config {
				t.Fatal("sealed dual profile changed on export")
			}
		})
	}
	s, _ := wgAddPeerHost(t)
	res, err := s.AddWireGuardPeer(context.Background(), "wg0", WGPeerRequest{Name: "legacy", Kind: wgKindDevice, FullTunnel: true}, "", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Config, "Address = 10.8.0.4/32,") || !strings.Contains(res.Config, "IPv6 is routed into it to prevent a native") {
		t.Fatal("legacy profile was silently converted")
	}
}

func TestSetWireGuardDualExitFailureRetainsPriorIPv4Intent(t *testing.T) {
	for _, failAdmission := range []bool{false, true} {
		t.Run(fmt.Sprint(failAdmission), func(t *testing.T) {
			s := vpnService(t)
			conf := parseWGConf(fixture(t, "wg-managed.conf"))
			conf.iface().setBodyMeta("ipv6", "ula64")
			conf.iface().set("Address", "10.8.0.1/24, fd42:8::1/64")
			if err := s.writeWGConf("wg0", conf); err != nil {
				t.Fatal(err)
			}
			old := emptySpec()
			upsertOwnedNAT(old, wgOwner("wg0"), "original IPv4 exit", "10.8.0.0/24", "eth0", "original")
			if err := s.writeSpecForTest(old); err != nil {
				t.Fatal(err)
			}
			rec := record(t)
			rec.on("wg show all dump", wgDualDump("wg0", conf))
			if failAdmission {
				rec.fail("ip6tables -I FORWARD", "permission denied")
			}
			rec.on("nft -j list table inet "+gatewayTable, wgDualRulesFixture(old.NAT...))
			wgDualReplies(t, rec)
			wgUnitReplies(rec, "wg0")
			on := true
			if _, err := s.SetWireGuardExitFamilies(context.Background(), "wg0", true, &on, "operator"); err == nil {
				t.Fatal("partial family apply accepted")
			}
			after := wgMustSpec(t, s)
			if wgMustString(t, old) != wgMustString(t, after) {
				t.Fatalf("prior intent changed: %+v", after.NAT)
			}
			text, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
			if string(text) != conf.render() {
				t.Fatal("exit refusal rewrote installed profiles")
			}
		})
	}
}

func TestOptedInWireGuardPeerDriftRefusesProvisionAndExitWithoutMutation(t *testing.T) {
	for _, drift := range []string{"live_only", "missing", "allowed_ips", "psk", "keepalive"} {
		t.Run(drift, func(t *testing.T) {
			s := vpnService(t)
			conf := parseWGConf(fixture(t, "wg-managed.conf"))
			conf.iface().setBodyMeta("ipv6", "ula64")
			conf.iface().set("Address", "10.8.0.1/24, fd42:8::1/64")
			if err := s.writeWGConf("wg0", conf); err != nil {
				t.Fatal(err)
			}
			dump := wgDualDump("wg0", conf)
			switch drift {
			case "live_only":
				dump += fmt.Sprintf("wg0\t%s\t(none)\t(none)\tfd42:8::/65\t0\t0\t0\toff\n", fixPeerC)
			case "missing":
				dump = strings.Split(dump, "\n")[0] + "\n"
			case "allowed_ips":
				dump = strings.Replace(dump, "10.8.0.2/32", "10.8.0.9/32", 1)
			case "psk":
				dump = strings.Replace(dump, conf.peers()[0].get("presharedkey"), "(none)", 1)
			case "keepalive":
				dump = strings.Replace(dump, "\toff\n", "\t25\n", 2)
			}
			rec := record(t)
			rec.on("wg show all dump", dump)
			wgDualReplies(t, rec)
			on := true
			for _, operation := range []func() error{
				func() error {
					_, err := s.AddWireGuardPeer(context.Background(), "wg0", WGPeerRequest{Name: "new", Kind: wgKindDevice}, "", "fixture")
					return err
				},
				func() error {
					_, err := s.SetWireGuardExitFamilies(context.Background(), "wg0", true, &on, "fixture")
					return err
				},
				func() error {
					_, err := s.SetWireGuardExitFamilies(context.Background(), "wg0", false, nil, "fixture")
					return err
				},
			} {
				if err := operation(); err == nil || !strings.Contains(err.Error(), "native owner") {
					t.Fatalf("drift not refused actionably: %v", err)
				}
			}
			text, err := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
			if err != nil || string(text) != conf.render() || len(wgMustSpec(t, s).NAT) != 0 {
				t.Fatal("drift refusal changed saved input")
			}
			for _, mutation := range []string{"systemctl reload", "nft -f", "wg set", "ip -6 route add"} {
				if rec.ran(mutation) {
					t.Fatalf("drift refusal ran %s", mutation)
				}
			}
		})
	}
}

func TestWGNativeInventoryRejectsLostOrRepeatedInterfaces(t *testing.T) {
	for _, out := range []string{
		"wg0 (none) public 51820 off\n",
		fmt.Sprintf("wg0\t%s\t(none)\t(none)\tfd42:8::/65\t0\t0\t0\toff\n", fixPeerC),
		"wg0\t(none)\t(none)\t51820\toff\nwg0\t(none)\t(none)\t51820\toff\n",
	} {
		if _, err := wgCheckedDump(out); err == nil {
			t.Fatal("unreadable ownership inventory accepted")
		}
	}
}
