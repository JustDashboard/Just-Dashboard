package netx

import "testing"

func TestRemovalsDecideTheDestructiveGate(t *testing.T) {
	sp := vlanBridgeSpec()
	sp.Links[1].VLANs = []PortVLAN{{VID: 10, PVID: true, Untagged: true}, {VID: 20}}
	sp.Links = append(sp.Links, LinkSpec{Name: "vx42", Kind: "vxlan", Remote: "198.51.100.7", Remotes: []string{"198.51.100.8"}})
	if RemovesPortVLAN(sp, "eth0.100", []PortVLAN{{VID: 10, PVID: true}, {VID: 20}, {VID: 30}}) {
		t.Fatal("adding a VLAN removes nothing")
	}
	if !RemovesPortVLAN(sp, "eth0.100", []PortVLAN{{VID: 10, PVID: true}}) {
		t.Fatal("dropping VLAN 20 is a removal")
	}
	if !RemovesPortVLAN(sp, "jd-lan", []PortVLAN{{VID: 10}}) {
		t.Fatal("dropping the default VLAN 1 from a bridge is a removal")
	}
	if RemovesPortVLAN(sp, "unknown0", nil) {
		t.Fatal("an unknown device decides nothing")
	}
	if RemovesVXLANRemote(sp, "vx42", []string{"198.51.100.8", "198.51.100.9"}) || !RemovesVXLANRemote(sp, "vx42", nil) {
		t.Fatal("only dropping a flood end is a removal")
	}
}
