package netx

import (
	"context"
	"os"
	"strings"
	"testing"
)

// bridgeMutations is what a test changed, without the reads.
func bridgeMutations(rec *recorder) []string {
	var out []string
	for _, c := range rtMutations(rec) {
		if !strings.HasPrefix(c, "bridge -j") {
			out = append(out, c)
		}
	}
	return out
}

func vlanBridgeSpec() *Spec {
	sp := emptySpec()
	sp.Links = []LinkSpec{
		{Name: "jd-lan", Kind: "bridge", VLANFiltering: true, Up: true},
		{Name: "eth0.100", Kind: "vlan", Parent: "eth0", VLANID: 100, Master: "jd-lan", Up: true},
	}
	return sp
}

func TestBridgeCreationCarriesVLANFilteringAndSnooping(t *testing.T) {
	off := false
	l, err := LinkRequest{Name: "jd-sw", Kind: "bridge", STP: true, VLANFiltering: true, MulticastSnooping: &off}.spec()
	if err != nil {
		t.Fatal(err)
	}
	args, err := linkAddArgs(l)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); got != "link add jd-sw type bridge stp_state 1 vlan_filtering 1 mcast_snooping 0" {
		t.Fatalf("args = %q", got)
	}
	plain, _ := LinkRequest{Name: "jd-br", Kind: "bridge"}.spec()
	if args, _ := linkAddArgs(plain); strings.Join(args, " ") != "link add jd-br type bridge" {
		t.Fatalf("an unset option keeps the kernel's default: %v", args)
	}
	if l, _ := (LinkRequest{Name: "jd-d", Kind: "dummy", VLANFiltering: true}).spec(); l.VLANFiltering {
		t.Fatal("only a bridge filters by VLAN")
	}
}

func TestBridgeUnitCommandsRenderOwnedVLANsAndFloodEnds(t *testing.T) {
	sp := vlanBridgeSpec()
	sp.Links[0].VLANs = []PortVLAN{{VID: 10, PVID: true, Untagged: true}}
	sp.Links[1].VLANs = []PortVLAN{{VID: 20}, {VID: 10, PVID: true, Untagged: true}}
	sp.Links = append(sp.Links,
		LinkSpec{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7", Remotes: []string{"198.51.100.8", "not-an-address"}},
		LinkSpec{Name: "jd-loose", Kind: "dummy", VLANs: []PortVLAN{{VID: 30}}},
	)
	var lines []string
	for _, cmd := range bridgeUnitCommands(sp) {
		lines = append(lines, strings.Join(cmd, " "))
	}
	want := []string{
		"vlan add dev jd-lan vid 10 pvid untagged self",
		"vlan del dev jd-lan vid 1 self",
		"vlan add dev eth0.100 vid 10 pvid untagged",
		"vlan add dev eth0.100 vid 20",
		"vlan del dev eth0.100 vid 1",
		"fdb append 00:00:00:00:00:00 dev vx42 dst 198.51.100.8",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	paths := Paths{Dir: "/etc/just-dashboard/network", Sysctl: "/etc/sysctl.d/90-just-dashboard.conf"}
	unit := renderUnitWith(paths, false, bridgeUnitCommands(sp))
	links := strings.Index(unit, "ExecStart=-ip -force -batch")
	first := strings.Index(unit, "ExecStart=-bridge vlan add dev jd-lan vid 10 pvid untagged self")
	rules := strings.Index(unit, "ExecStart=-ip -6 -force -batch")
	if links < 0 || first < links || rules < first {
		t.Fatalf("bridge lines follow the devices they configure:\n%s", unit)
	}
	if renderUnitWith(paths, false, bridgeUnitCommands(emptySpec())) != renderUnit(paths, false) {
		t.Fatal("a spec without VLANs or flood ends renders the unit it always did")
	}
}

func TestSetPortVLANsAppliesRecordsAndRendersTheMembership(t *testing.T) {
	rec := rtHost(t).
		on("bridge -j vlan show", `[{"ifname":"jd-lan","vlans":[{"vlan":1,"flags":["PVID","Egress Untagged"]}]},{"ifname":"eth0.100","vlans":[{"vlan":1,"flags":["PVID","Egress Untagged"]}]}]`).
		on("bridge vlan", "")
	s := testService(t)
	rtSaveSpec(t, s, vlanBridgeSpec())
	change, err := s.SetPortVLANs(context.Background(), "eth0.100", []PortVLAN{{VID: 20}, {VID: 10, PVID: true, Untagged: true}}, rtClient, "ion")
	if err != nil || !change.Persisted {
		t.Fatal(change, err)
	}
	want := []string{"bridge vlan add dev eth0.100 vid 10 pvid untagged", "bridge vlan add dev eth0.100 vid 20", "bridge vlan del dev eth0.100 vid 1"}
	if got := bridgeMutations(rec); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %v", got)
	}
	sp := rtLoad(t, s)
	m, _ := sp.link("eth0.100")
	if len(m.VLANs) != 2 || m.VLANs[0].VID != 10 || !m.VLANs[0].PVID {
		t.Fatalf("spec = %+v", m.VLANs)
	}
	unit, err := os.ReadFile(s.paths.Unit)
	if err != nil || !strings.Contains(string(unit), "ExecStart=-bridge vlan add dev eth0.100 vid 20\n") || !strings.Contains(string(unit), "ExecStart=-bridge vlan del dev eth0.100 vid 1\n") {
		t.Fatalf("the boot unit restores the membership: %s %v", unit, err)
	}
}

func TestSetPortVLANsRefusals(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		spec  *Spec
		dev   string
		vlans []PortVLAN
		want  string
	}{
		"two native VLANs":  {vlanBridgeSpec(), "eth0.100", []PortVLAN{{VID: 10, PVID: true}, {VID: 20, PVID: true}}, "one native VLAN"},
		"VLAN out of range": {vlanBridgeSpec(), "eth0.100", []PortVLAN{{VID: 4095}}, "1 to 4094"},
		"duplicate":         {vlanBridgeSpec(), "eth0.100", []PortVLAN{{VID: 7}, {VID: 7}}, "twice"},
		"foreign device":    {vlanBridgeSpec(), "wg0", []PortVLAN{{VID: 7}}, "not created by Just Dashboard"},
		"bridge without filtering": {func() *Spec {
			sp := vlanBridgeSpec()
			sp.Links[0].VLANFiltering = false
			return sp
		}(), "eth0.100", []PortVLAN{{VID: 7}}, "VLAN-filtering"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := rtHost(t)
			s := testService(t)
			rtSaveSpec(t, s, tc.spec)
			_, err := s.SetPortVLANs(ctx, tc.dev, tc.vlans, rtClient, "ion")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(bridgeMutations(rec)) != 0 {
				t.Fatalf("a refusal changes nothing: %v", bridgeMutations(rec))
			}
		})
	}
}

func TestSetPortVLANsIsTakenBackWhenThePathMoves(t *testing.T) {
	rec := rtHost(t).
		on("bridge -j vlan show", `[{"ifname":"eth0.100","vlans":[{"vlan":1,"flags":["PVID","Egress Untagged"]}]}]`).
		on("bridge vlan", "")
	rtAnswerAfter(rec, "bridge vlan add", "ip -j route get", `[{"dst":"100.110.34.9","dev":"eth0","gateway":"203.0.113.1","prefsrc":"203.0.113.20"}]`)
	s := testService(t)
	rtSaveSpec(t, s, vlanBridgeSpec())
	if _, err := s.SetPortVLANs(context.Background(), "eth0.100", []PortVLAN{{VID: 30, PVID: true, Untagged: true}}, rtClient, "ion"); err == nil {
		t.Fatal("a moved path must refuse the change")
	}
	got := strings.Join(bridgeMutations(rec), "|")
	if !strings.Contains(got, "bridge vlan add dev eth0.100 vid 1 pvid untagged") || !strings.Contains(got, "bridge vlan del dev eth0.100 vid 30") {
		t.Fatalf("the previous membership is put back: %s", got)
	}
	if m, _ := rtLoad(t, s).link("eth0.100"); len(m.VLANs) != 0 {
		t.Fatalf("the spec keeps the old membership: %+v", m.VLANs)
	}
}

func TestSetVXLANRemotesAddsAndRemovesFloodEnds(t *testing.T) {
	rec := rtHost(t).on("bridge fdb", "")
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7", Port: 4789, Remotes: []string{"198.51.100.9"}, Up: true}}
	rtSaveSpec(t, s, sp)
	if _, err := s.SetVXLANRemotes(context.Background(), "vx42", []string{"198.51.100.8", "198.51.100.7", "198.51.100.8"}, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"bridge fdb append 00:00:00:00:00:00 dev vx42 dst 198.51.100.8",
		"bridge fdb del 00:00:00:00:00:00 dev vx42 dst 198.51.100.9",
	}
	if got := rtMutations(rec); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %v", got)
	}
	if m, _ := rtLoad(t, s).link("vx42"); strings.Join(m.Remotes, ",") != "198.51.100.8" {
		t.Fatalf("the device's own remote is not repeated: %v", m.Remotes)
	}
	for _, bad := range [][]string{{"2001:db8::7"}, {"239.1.1.1"}} {
		if _, err := s.SetVXLANRemotes(context.Background(), "vx42", bad, rtClient, "ion"); err == nil {
			t.Fatalf("%v must be refused", bad)
		}
	}
}

func TestBridgeRecoveryRestoresMembershipsAndFloodEnds(t *testing.T) {
	old := vlanBridgeSpec()
	old.Links[1].VLANs = []PortVLAN{{VID: 10, PVID: true, Untagged: true}}
	old.Links = append(old.Links, LinkSpec{Name: "vx42", Kind: "vxlan", Remote: "198.51.100.7", Remotes: []string{"198.51.100.9"}})
	next := old.clone()
	next.Links[1].VLANs = []PortVLAN{{VID: 20, PVID: true}}
	next.Links[2].Remotes = []string{"198.51.100.8"}
	names := map[string]bool{"jd-lan": true, "eth0.100": true, "vx42": true}
	var got []string
	for _, c := range bridgeRecovery(old, next, names) {
		if c.Tool != "bridge" {
			t.Fatalf("tool %s", c.Tool)
		}
		got = append(got, strings.Join(c.Args, " "))
	}
	want := []string{
		"vlan add dev eth0.100 vid 10 pvid untagged",
		"vlan del dev eth0.100 vid 20",
		"fdb del 00:00:00:00:00:00 dev vx42 dst 198.51.100.8",
		"fdb append 00:00:00:00:00:00 dev vx42 dst 198.51.100.9",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("recovery =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(bridgeRecovery(old, old.clone(), names)) != 0 {
		t.Fatal("an unchanged membership needs no recovery")
	}
}

func TestBridgeViewReadsSettingsPortsAndLearnedEntries(t *testing.T) {
	link := `[{"ifindex":8,"ifname":"jd-lan","flags":["UP"],"mtu":1500,"operstate":"UP","linkinfo":{"info_kind":"bridge","info_data":{"stp_state":1,"vlan_filtering":1,"mcast_snooping":0,"vlan_default_pvid":1,"ageing_time":30000,"vlan_protocol":"802.1Q"}}},{"ifindex":9,"ifname":"eth0.100","master":"jd-lan","flags":["UP"],"mtu":1500,"operstate":"UP","linkinfo":{"info_kind":"vlan"}}]`
	record(t).
		on("ip -j -d link show", link).
		on("bridge -j vlan show", `[{"ifname":"jd-lan","vlans":[{"vlan":1,"flags":["PVID","Egress Untagged"]}]},{"ifname":"eth0.100","vlans":[{"vlan":10,"flags":["PVID","Egress Untagged"]},{"vlan":20,"vlanEnd":22}]}]`).
		on("bridge -j fdb show br jd-lan", `[{"mac":"33:33:00:00:00:01","ifname":"jd-lan","flags":["self"],"state":"permanent"},{"mac":"02:42:ac:11:00:02","ifname":"eth0.100","vlan":10,"flags":[],"master":"jd-lan","state":""},{"mac":"02:00:00:00:00:08","ifname":"jd-lan","vlan":1,"flags":[],"master":"jd-lan","state":"permanent"}]`)
	s := testService(t)
	sp := vlanBridgeSpec()
	sp.Links[1].VLANs = []PortVLAN{{VID: 10, PVID: true, Untagged: true}, {VID: 20}}
	rtSaveSpec(t, s, sp)
	v, err := s.Bridge(context.Background(), "jd-lan")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Managed || !v.STP || !v.VLANFiltering || v.MulticastSnooping || v.AgeingSeconds != 300 || v.SettingsRead.State != "ok" {
		t.Fatalf("settings: %+v", v)
	}
	if len(v.Ports) != 2 || !v.Ports[0].Self || len(v.Ports[1].VLANs) != 4 || len(v.Ports[1].Desired) != 2 {
		t.Fatalf("ports with a VLAN range expanded and the desired list beside it: %+v", v.Ports)
	}
	if v.FDBTotal != 2 || v.FDB[0].MAC != "02:42:ac:11:00:02" || v.FDB[0].Static || !v.FDB[1].Static {
		t.Fatalf("the bridge's own multicast entries are left out: %+v", v.FDB)
	}
}

func TestPreviewMasterNamesWhatAMigrationLeavesBehind(t *testing.T) {
	rtHost(t).
		on("ip -j route show dev jd-d0", `[{"dst":"10.77.0.0/24","dev":"jd-d0","protocol":"kernel"},{"dst":"10.88.0.0/16","gateway":"10.77.0.9","dev":"jd-d0","protocol":"static"}]`).
		on("ip -j -6 route show dev jd-d0", `[]`)
	prevRun := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "ip" && strings.Join(args, " ") == "-j -d link show dev jd-lan" {
			return `[{"ifname":"jd-lan","linkinfo":{"info_kind":"bridge","info_data":{"stp_state":1,"vlan_filtering":1,"vlan_default_pvid":1}}}]`, nil
		}
		if name == "ip" && strings.Join(args, " ") == "-j addr show" {
			return `[{"ifname":"jd-d0","addr_info":[{"family":"inet","local":"10.77.0.1","prefixlen":24,"scope":"global"}]}]`, nil
		}
		if name == "ip" && strings.Join(args, " ") == "-j -d link show" {
			out, err := prevRun(ctx, name, args...)
			return strings.TrimSuffix(strings.TrimSpace(out), "]") + `,{"ifindex":40,"ifname":"jd-d0","flags":["UP","LOWER_UP"],"mtu":1500,"operstate":"UNKNOWN","linkinfo":{"info_kind":"dummy"}}]`, err
		}
		return prevRun(ctx, name, args...)
	}
	t.Cleanup(func() { run = prevRun })
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge", STP: true, VLANFiltering: true}, {Name: "jd-d0", Kind: "dummy", Up: true}}
	sp.Routes = []RouteSpec{{ID: 1, Family: "inet", Destination: "10.88.0.0/16", Gateway: "10.77.0.9", Device: "jd-d0", Table: 254, Type: "unicast"}}
	rtSaveSpec(t, s, sp)
	p, err := s.PreviewMaster(context.Background(), "jd-d0", "jd-lan", rtClient)
	if err != nil {
		t.Fatal(err)
	}
	if p.Allowed || !strings.Contains(p.Refusal, "holds an address") || !p.Persisted {
		t.Fatalf("an addressed device is refused, with the reason: %+v", p)
	}
	kinds := map[string]int{}
	for _, e := range p.Effects {
		kinds[e.Kind]++
	}
	if kinds["address"] != 1 || kinds["route"] != 1 || kinds["vlan"] != 1 || kinds["stp"] != 1 || kinds["dependent"] != 1 {
		t.Fatalf("every consequence is named: %+v", p.Effects)
	}
	if len(p.Steps) != 1 || p.Steps[0] != "ip link set jd-d0 master jd-lan" {
		t.Fatalf("steps: %v", p.Steps)
	}
}
