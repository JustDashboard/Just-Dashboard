package netx

import (
	"context"
	"testing"
)

func TestLookupRouteAsksOnlyTheKernelInTheSelectedFamily(t *testing.T) {
	r := record(t).on("ip -j -6 route get 2001:db8::9 from 2001:db8::2 mark 0x20", `[{"dst":"2001:db8::9","dev":"wg0","gateway":"fe80::1","prefsrc":"2001:db8::2","table":120}]`)
	v, err := testService(t).LookupRoute(context.Background(), "2001:db8::9", "2001:db8::2", "32")
	if err != nil || v.Family != "inet6" || v.Table != 120 || v.Device != "wg0" || v.Source != "2001:db8::2" {
		t.Fatalf("decision = %+v, %v", v, err)
	}
	if len(r.commands()) != 1 {
		t.Fatalf("lookup emitted unrelated queries: %v", r.commands())
	}
}

func TestLookupRouteRejectsNamesMixedFamiliesAndMarkMasksBeforeExecution(t *testing.T) {
	r := record(t)
	s := testService(t)
	for _, input := range [][3]string{{"private.example", "", ""}, {"192.0.2.1", "2001:db8::2", ""}, {"192.0.2.1", "", "0x20/0xff"}, {"192.0.2.1", "", "1;id"}} {
		if _, err := s.LookupRoute(context.Background(), input[0], input[1], input[2]); err == nil {
			t.Fatalf("invalid lookup accepted: %v", input)
		}
	}
	if len(r.commands()) != 0 {
		t.Fatalf("invalid lookup executed a command: %v", r.commands())
	}
}

func TestLookupRouteDoesNotInventAUsablePathFromMalformedOutput(t *testing.T) {
	for _, reply := range []string{"[]", "garbage", `[{"dev":"a"},{"dev":"b"}]`} {
		t.Run(reply, func(t *testing.T) {
			record(t).on("ip -j route get", reply)
			if _, err := testService(t).LookupRoute(context.Background(), "192.0.2.1", "", ""); err == nil {
				t.Fatal("ambiguous output was accepted")
			}
		})
	}
}
