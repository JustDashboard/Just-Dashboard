package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func nativePeerTestLinks() []ipLink {
	links := []ipLink{{IfName: "d0", IfIndex: 2, LinkIndex: 3, Address: "02:00:00:00:00:14"}, {IfName: "d1", IfIndex: 3, LinkIndex: 2, Address: "02:00:00:00:00:16"}}
	for i := range links {
		links[i].LinkInfo.InfoKind = "veth"
	}
	return links
}

func TestNativeVethRequiresReciprocalSameNamespacePeer(t *testing.T) {
	links := nativePeerTestLinks()
	peer, err := nativePeer(links[0], links)
	if err != nil || peer == nil || peer.Device != "d1" {
		t.Fatal("reciprocal existing pair refused:", peer, err)
	}
	for _, alter := range []func([]ipLink) []ipLink{
		func(links []ipLink) []ipLink { return links[:1] },
		func(links []ipLink) []ipLink { links[1].LinkIndex = 19; return links },
		func(links []ipLink) []ipLink { links[1].LinkInfo.InfoKind = "dummy"; return links },
		func(links []ipLink) []ipLink { links[1].IfName = "d0"; return links },
		func(links []ipLink) []ipLink { links[1].Address = "invalid"; return links },
	} {
		changed := alter(append([]ipLink(nil), links...))
		if _, err := nativePeer(changed[0], changed); err == nil {
			t.Fatalf("absent/foreign peer was admitted: %+v", changed)
		}
	}
	p := nativeProfile{View: NativeProfileView{Device: "d0", Kind: "veth"}, Device: links[0], Peer: peer}
	before := nativeGeneration(&p)
	changedPeer := *peer
	changedPeer.IfIndex++
	p.Peer = &changedPeer
	if nativeGeneration(&p) == before {
		t.Fatal("a replacement peer retained the reviewed generation")
	}
	p.UUID = "55afc950-b609-4466-955a-2fa4788c0140"
	data := []byte("[connection]\nuuid=" + p.UUID + "\ninterface-name=d0\ntype=ethernet\n[ipv4]\nmethod=disabled\n[ipv6]\nmethod=disabled\n")
	if _, err := parseNativeNM(data, &p); err != nil {
		t.Fatal("existing Ethernet owner profile refused for a verified pair:", err)
	}
	p.Peer = nil
	if _, err := parseNativeNM(data, &p); err == nil || nativeStructureGuard(&p) == nil {
		t.Fatal("unverified pair was admitted as an Ethernet owner")
	}
}

func TestNativeVethResolvesKernelPeerNamesWithoutAdoptingForeignIndices(t *testing.T) {
	var links []ipLink
	if err := json.Unmarshal([]byte(`[{"ifindex":8,"ifname":"d0","link":"d1","address":"02:00:00:00:08:02","linkinfo":{"info_kind":"veth"}},{"ifindex":7,"ifname":"d1","link":"d0","address":"02:00:00:00:08:01","linkinfo":{"info_kind":"veth"}}]`), &links); err != nil {
		t.Fatal(err)
	}
	peer, err := nativePeer(links[0], links)
	if err != nil || peer == nil || *peer != (nativePeerIdentity{Device: "d1", IfIndex: 7, MAC: "02:00:00:00:08:01"}) {
		t.Fatal("actual same-namespace iproute2 schema refused:", peer, err)
	}
	for _, alter := range []func([]ipLink) []ipLink{
		func(links []ipLink) []ipLink { links[0].LinkNS = json.RawMessage(`0`); return links },
		func(links []ipLink) []ipLink { links[1].LinkNS = json.RawMessage(`-1`); return links },
		func(links []ipLink) []ipLink { links[1].LinkNS = json.RawMessage(`null`); return links },
		func(links []ipLink) []ipLink { links[0].LinkIndex = 99; return links },
		func(links []ipLink) []ipLink { links[1].Link = "foreign0"; return links },
		func(links []ipLink) []ipLink { links[1].LinkIndex = 99; return links },
		func(links []ipLink) []ipLink { return append(links, links[1]) },
		func(links []ipLink) []ipLink {
			extra := links[1]
			extra.IfName = "foreign0"
			return append(links, extra)
		},
		func(links []ipLink) []ipLink { extra := links[1]; extra.IfIndex = 99; return append(links, extra) },
		func(links []ipLink) []ipLink { return links[:1] },
	} {
		changed := alter(append([]ipLink(nil), links...))
		if _, err := nativePeer(changed[0], changed); err == nil {
			t.Fatalf("foreign, nonreciprocal or ambiguous inventory admitted: %+v", changed)
		}
	}
	indexed := nativePeerTestLinks()
	indexed[0].LinkNS = json.RawMessage(`0`)
	if _, err := nativePeer(indexed[0], indexed); err == nil {
		t.Fatal("cross-namespace numeric index reuse was admitted as a local pair")
	}
}

func TestNativeVethRecoveryPreservesForeignPeerBeforeEffect(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	links := nativePeerTestLinks()
	f.u.Version, f.u.Kind, f.u.RecoveryStrategy = 3, "veth", nativeExactOriginStrategy
	f.u.Peer, _ = nativePeer(links[0], links)
	if err := saveNativeUndo(f.j, f.u); err != nil {
		t.Fatal(err)
	}
	transcript := nativeExecute
	mutations := 0
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if tool == "ip" && strings.Join(args, " ") == "-j -d link show" {
			data, err := json.Marshal(links)
			return string(data), err
		}
		if tool == "busctl" && (strings.Contains(strings.Join(args, " "), " Reload") || strings.Contains(strings.Join(args, " "), " ReconfigureLink")) {
			mutations++
		}
		return transcript(ctx, input, tool, args...)
	}
	for _, alter := range []func(){
		func() { links[1].Address = "02:00:00:00:00:17" },
		func() { links[1].Address = f.u.Peer.MAC; links[1].IfIndex = 19; links[0].LinkIndex = 19 },
		func() { links = links[:1] },
	} {
		alter()
		if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
			t.Fatal("independent recovery adopted a foreign or missing peer")
		}
		current, err := nativeReadProfile(f.u.Files[0].Before.Path)
		if err != nil || !bytes.Equal(current.Data, f.u.Files[0].Candidate.Data) || mutations != 0 {
			t.Fatalf("refused peer recovery performed an effect: %+v %v %d", current, err, mutations)
		}
		if err := finishPriorChange(context.Background(), f.s.paths.Dir); err == nil {
			t.Fatal("next journal replacement discarded failed peer recovery evidence")
		}
	}
	links = nativePeerTestLinks()
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal("retry with original exact pair failed:", err)
	}
	for _, version := range []int{1, 2} {
		changed := *f.u
		changed.Version = version
		if _, err := nativeCommand(&changed); err == nil {
			t.Fatal("legacy helper vocabulary admitted new peer scope")
		}
	}
}
