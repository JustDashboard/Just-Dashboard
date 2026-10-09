package netx

import (
	"context"
	"strings"
	"testing"
)

func TestNativeRuntimeRejectsForbiddenAutomaticRoutesAndDNS(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	p := &nativeProfile{View: NativeProfileView{Device: "d0", Kind: "dummy", Renderer: "networkd", Contract: f.u.Contract}, Device: ipLink{IfIndex: f.u.IfIndex, Address: f.u.MAC}}
	transcript := nativeExecute
	for _, protocol := range []string{"dhcp", "ra"} {
		nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
			if tool == "ip" && strings.Join(args, " ") == "-j -4 route show table all dev d0" {
				return `[{"dst":"default","gateway":"192.0.2.1","protocol":"` + protocol + `"}]`, nil
			}
			return transcript(ctx, input, tool, args...)
		}
		intent := f.u.CandidateIntent
		intent.IPv4.IgnoreAutoRoutes = true
		if got := readNativeRuntime(context.Background(), p, intent); got.Status != "drift" || !strings.Contains(got.Reason, "DHCP/RA") {
			t.Fatalf("forbidden automatic route passed runtime validation: %+v", got)
		}
	}
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if tool == "networkctl" && strings.Contains(strings.Join(args, " "), "status d0") {
			return `{"Name":"d0","DNS":[{"Family":2,"Address":[192,0,2,53]}],"SearchDomains":[],"RouteDomains":[]}`, nil
		}
		return transcript(ctx, input, tool, args...)
	}
	if got := readNativeRuntime(context.Background(), p, f.u.CandidateIntent); got.Status != "drift" || !strings.Contains(got.Reason, "extra DNS") {
		t.Fatalf("unconfigured DNS was accepted for a manual family: %+v", got)
	}
}
