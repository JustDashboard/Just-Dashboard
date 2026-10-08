package netx

import (
	"context"
	"testing"
)

func TestShapingHealthDistinguishesRateDriftFromUnreadableKernel(t *testing.T) {
	for _, tc := range []struct {
		name, root, classes, want string
		unreadable                bool
	}{
		{"verified", `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"}]`, `[{"class":"htb","handle":"1:10","root":true,"rate":6250000,"ceil":6250000}]`, "verified", false},
		{"external rate change", `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"}]`, `[{"class":"htb","handle":"1:10","root":true,"rate":1250000,"ceil":6250000}]`, "drift", false},
		{"queue removed", `[]`, "", "drift", false},
		{"read permission lost", "", "", "unknown", true},
		{"unreadable class output", `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"}]`, "{", "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := record(t)
			if tc.unreadable {
				r.fail("tc -j qdisc show dev eth0", "permission denied")
			} else {
				r.on("tc -j qdisc show dev eth0", tc.root)
			}
			if tc.classes != "" {
				r.on("tc -j class show dev eth0", tc.classes)
			}
			got := readShapeVerification(context.Background(), ShapeSpec{Device: "eth0", EgressKbit: 50000})
			if got.Status != tc.want || got.CheckedAt.IsZero() || (got.Status != "verified" && got.Reason == "") {
				t.Fatalf("health = %+v, want %s", got, tc.want)
			}
		})
	}
}

func TestIngressVerificationAcceptsNativeDescriptorAndDetailsButRejectsExtraFilters(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(map[bool]string{false: "native descriptor and action", true: "foreign filter"}[extra], func(t *testing.T) {
			r := record(t).on("tc -j qdisc show dev eth0", `[{"kind":"ingress","handle":"ffff:"}]`)
			filters := `[{"protocol":"all","pref":1,"kind":"matchall","chain":0},{"protocol":"all","pref":1,"kind":"matchall","chain":0,"options":{"actions":[{"kind":"police","control_action":{"type":"drop"}}]}}`
			if extra {
				filters += `,{"protocol":"all","pref":2,"kind":"flower","chain":0}`
			}
			r.on("tc -j filter show dev eth0", filters+"]").on("tc -r -d filter show dev eth0", "police 0x1 rate 8Mbit burst 100000b mtu 2Kb action drop")
			got := readShapeVerification(context.Background(), ShapeSpec{Device: "eth0", IngressKbit: 8000})
			want := "verified"
			if extra {
				want = "drift"
			}
			if got.Status != want {
				t.Fatalf("ingress evidence = %+v, want %s", got, want)
			}
		})
	}
}
