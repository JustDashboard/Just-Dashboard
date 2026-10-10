package netx

import "testing"

func TestIPAMWireGuardTupleKeepsNativeFamilyAndNameBounds(t *testing.T) {
	valid := WGServerRequest{Name: "wg-ipam", Subnet: "10.244.0.0/24", IPv6: &WGIPv6Request{Subnet: "fd48:abcd::/64"}}
	if e := ValidateWireGuardReservation(valid); e != nil {
		t.Fatal(e)
	}
	for _, request := range []WGServerRequest{{Name: "", Subnet: valid.Subnet}, {Name: "wg@bad", Subnet: valid.Subnet}, {Name: "wg-ipam", Subnet: "10.244.0.1/24"}, {Name: "wg-ipam", Subnet: "10.244.0.0/30"}, {Name: "wg-ipam", Subnet: "192.0.2.0/24"}, {Name: "wg-ipam", IPv6: &WGIPv6Request{Subnet: "2001:db8::/64"}}, {Name: "wg-ipam", IPv6: &WGIPv6Request{Subnet: "fd48:abcd::/48"}}, {Name: "wg-ipam", IPv6: &WGIPv6Request{Subnet: "fd48:abcd::1/64"}}, {Name: "wg-ipam", IPv6: &WGIPv6Request{Subnet: "fd48:abcd::/64", ExitNode: true}}, {Name: "wg-ipam"}} {
		if e := ValidateWireGuardReservation(request); e == nil {
			t.Fatalf("accepted incompatible native tuple %+v", request)
		}
	}
}
