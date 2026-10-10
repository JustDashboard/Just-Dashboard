package netx

import (
	"context"
	"reflect"
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
		// iproute2 6.1 (Ubuntu 24.04) ignores -j for classes and prints text.
		{"iproute2 6.1 text classes", `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"}]`, "class htb 1:10 root leaf 10: prio 0 rate 50Mbit ceil 50Mbit burst 1600b cburst 1600b \n", "verified", false},
		{"iproute2 6.1 text rate change", `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"}]`, "class htb 1:10 root leaf 10: prio 0 rate 10Mbit ceil 50Mbit burst 1600b cburst 1600b \n", "drift", false},
		{"unreadable text class", `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"0x10"}},{"kind":"fq_codel","handle":"10:","parent":"1:10"}]`, "class htb 1:10 root rate fast\n", "unknown", false},
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

func TestTCClassesReadJSONAndTheOlderTextForm(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		want      []tcClass
	}{
		{"json", `[{"class":"htb","handle":"1:10","root":true,"rate":2500000,"ceil":2500000}]`, []tcClass{{Kind: "htb", Handle: "1:10", Root: true, Rate: 2500000, Ceil: 2500000}}},
		{"no classes in text form", "", nil},
		{"htb text", "class htb 1:10 root leaf 10: prio 0 rate 20Mbit ceil 20Mbit burst 1600b cburst 1600b \n", []tcClass{{Kind: "htb", Handle: "1:10", Root: true, Rate: 2500000, Ceil: 2500000}}},
		{"kbit text", "class htb 1:10 root prio 0 rate 12345Kbit ceil 12345Kbit burst 1600b cburst 1600b \n", []tcClass{{Kind: "htb", Handle: "1:10", Root: true, Rate: 1543125, Ceil: 1543125}}},
		{"cake virtual classes", "class cake ca11:1 parent ca11: \nclass cake ca11:2 parent ca11: \n", []tcClass{{Kind: "cake", Handle: "ca11:1", Parent: "ca11:"}, {Kind: "cake", Handle: "ca11:2", Parent: "ca11:"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTCClasses(tc.out)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("classes = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	for _, bad := range []string{"qdisc htb 1: root", "class htb", "class htb 1:10 root rate", "class htb 1:10 root rate 1.5Gbit"} {
		if _, err := parseTCClasses(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
