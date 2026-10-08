package netx

import (
	"context"
	"reflect"
	"testing"
)

func TestTrafficRouteTupleUsesClosedKernelArgv(t *testing.T) {
	var tool string
	var args []string
	s := &Service{}
	r, err := s.LookupTrafficRoute(context.Background(), "2001:db8::9", "2001:db8::2", "0x1", "tcp", 443, func(_ context.Context, t string, a ...string) (string, error) {
		tool, args = t, a
		return `[{"dst":"2001:db8::9","dev":"eth0","gateway":"2001:db8::1","prefsrc":"2001:db8::2","table":100}]`, nil
	})
	want := []string{"-j", "-6", "route", "get", "2001:db8::9", "from", "2001:db8::2", "mark", "0x1", "ipproto", "tcp", "dport", "443"}
	if err != nil || tool != "ip" || !reflect.DeepEqual(args, want) || r.Table != 100 || r.Source != "2001:db8::2" {
		t.Fatalf("wrong tuple: %q %v %#v %v", tool, args, r, err)
	}
}

func TestTrafficRouteRejectsMalformedTupleBeforeExecution(t *testing.T) {
	for _, tuple := range [][5]string{{"--help", "", "", "tcp", "443"}, {"192.0.2.1", "::1", "", "tcp", "443"}, {"192.0.2.1", "", "0x1/0xff", "tcp", "443"}, {"192.0.2.1", "", "", "sh", "443"}} {
		_, err := (&Service{}).LookupTrafficRoute(context.Background(), tuple[0], tuple[1], tuple[2], tuple[3], 443, func(context.Context, string, ...string) (string, error) {
			t.Fatal("invalid tuple executed")
			return "", nil
		})
		if err == nil {
			t.Fatalf("accepted %v", tuple)
		}
	}
}

func TestTrafficRulesRetainUnknownSelectorsAndMarkMask(t *testing.T) {
	rules, err := (&Service{}).TrafficRules(context.Background(), "inet", func(_ context.Context, tool string, args ...string) (string, error) {
		if tool != "ip" || !reflect.DeepEqual(args, []string{"-j", "-4", "rule", "show"}) {
			t.Fatalf("wrong argv %s %v", tool, args)
		}
		return `[{"priority":200,"src":"all","dst":"all","table":100,"fwmark":1,"fwmask":"0xff","uidrange":"1000-1001"},{"priority":0,"src":"all","table":"local"}]`, nil
	})
	if err != nil || len(rules) != 2 || rules[0].Priority != 0 || rules[1].Mark != "1/0xff" || !reflect.DeepEqual(rules[1].UnknownSelectors, []string{"uidrange"}) {
		t.Fatalf("native selectors lost: %#v %v", rules, err)
	}
}

func TestTrafficRouteRejectsAmbiguousKernelDecision(t *testing.T) {
	_, err := (&Service{}).LookupTrafficRoute(context.Background(), "192.0.2.1", "", "", "tcp", 80, func(context.Context, string, ...string) (string, error) { return `[{"dev":"a"},{"dev":"b"}]`, nil })
	if err == nil {
		t.Fatal("ambiguous kernel path accepted")
	}
}
