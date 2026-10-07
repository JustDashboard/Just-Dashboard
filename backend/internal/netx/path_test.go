package netx

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestClientPathReadsRouteGet(t *testing.T) {
	record(t).on("ip -j route get 100.110.34.9",
		`[{"dst":"100.110.34.9","dev":"tailscale0","prefsrc":"100.110.34.31","flags":[],"uid":0,"cache":[]}]`).
		on("ip -j -6 route get", `[{"dst":"2001:db8::9","gateway":"fe80::1","dev":"eth0","prefsrc":"2001:db8::20"}]`)
	p, err := clientPath(context.Background(), "100.110.34.9")
	if err != nil || p.Device != "tailscale0" || p.Source != "100.110.34.31" || p.Gateway != "" {
		t.Fatalf("path = %+v, %v", p, err)
	}
	p, err = clientPath(context.Background(), "2001:db8::9")
	if err != nil || p.Device != "eth0" || p.Gateway != "fe80::1" {
		t.Fatalf("v6 path = %+v, %v", p, err)
	}
	if p, _ := clientPath(context.Background(), "127.0.0.1"); !p.Local {
		t.Fatal("loopback is local")
	}
	if p, _ := clientPath(context.Background(), "not-an-address"); p.Address != "" {
		t.Fatal("an unparseable client protects nothing extra")
	}
}

func TestVerifyPathRefusesAMovedReply(t *testing.T) {
	record(t).on("ip -j route get 198.51.100.9", `[{"dst":"198.51.100.9","gateway":"10.9.0.1","dev":"jd-lan"}]`)
	err := verifyPath(Path{Address: "198.51.100.9", Device: "eth0", Gateway: "203.0.113.1"})(context.Background())
	if err == nil || !strings.Contains(err.Error(), "through jd-lan via 10.9.0.1") {
		t.Fatalf("moved path not refused: %v", err)
	}
}

func TestOperatorRangesDropTheOpenInternet(t *testing.T) {
	got := operatorRanges([]string{"0.0.0.0/0", "::/0", "100.64.0.0/10", "10.1.2.3/24", "garbage", "203.0.113.9"})
	want := []string{"100.64.0.0/10", "10.1.2.0/24", "203.0.113.9/32"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestTrustedSetHoldsLoopbackAllowlistAndTheClient(t *testing.T) {
	s := testService(t, "100.64.0.0/10")
	sp := emptySpec()
	if s.trustClient(sp, "100.110.34.9") {
		t.Fatal("an address the allowlist covers is not added again")
	}
	if !s.trustClient(sp, "198.51.100.9") || len(sp.Trusted) != 1 {
		t.Fatalf("a client outside the allowlist is remembered: %v", sp.Trusted)
	}
	if !s.isTrusted(sp, netip.MustParseAddr("127.0.0.1")) || !s.isTrusted(sp, netip.MustParseAddr("198.51.100.9")) {
		t.Fatal("loopback and the remembered client are trusted")
	}
	if s.isTrusted(sp, netip.MustParseAddr("192.0.2.1")) {
		t.Fatal("a stranger is not trusted")
	}
}

func TestMergePrefixesDropsWhatAnotherHolds(t *testing.T) {
	in := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("10.1.0.0/16"),
		netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("192.0.2.7/32"),
		netip.MustParsePrefix("10.0.0.0/8"),
	}
	got := mergePrefixes(in)
	if len(got) != 3 || got[0].String() != "10.0.0.0/8" || got[1].String() != "192.0.2.7/32" || got[2].String() != "2001:db8::/32" {
		t.Fatalf("merged = %v", got)
	}
	if s := prefixList(got[:2]); s != "10.0.0.0/8, 192.0.2.7" {
		t.Fatalf("set elements = %q", s)
	}
}

// readAnchors is the real anchor reader, kept before record() stubs it.
var readAnchors = anchorPaths

func TestVerifyPathRefusesMovingThisServersOwnWayOut(t *testing.T) {
	rec := record(t)
	anchorPaths = readAnchors
	rec.on("ip -j route get 1.1.1.1 mark 0x80000", `[{"dst":"1.1.1.1","gateway":"203.0.113.1","dev":"eth0"}]`).
		on("ip -j route get 1.1.1.1", `[{"dst":"1.1.1.1","gateway":"203.0.113.1","dev":"eth0"}]`).
		fail("ip -j -6 route get", "RTNETLINK answers: Network is unreachable").
		on("ip -j route get 100.110.34.9", `[{"dst":"100.110.34.9","dev":"tailscale0"}]`)
	before, err := clientPath(context.Background(), "100.110.34.9")
	if err != nil || len(before.anchors) != 2 {
		t.Fatalf("anchors = %+v, %v", before.anchors, err)
	}
	// Two half-default blackholes leave the tailnet address on tailscale0 but
	// take Tailscale's own packets nowhere.
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route get 1.1.1.1", err: fmt.Errorf("RTNETLINK answers: No route to host")}}, rec.replies...)
	rec.mu.Unlock()
	err = verifyPath(before)(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no route to the internet") {
		t.Fatalf("a lost way out must be refused: %v", err)
	}
}

func TestLoopbackClientsStillProtectTheWayOut(t *testing.T) {
	rec := record(t)
	anchorPaths = readAnchors
	rec.on("ip -j route get 1.1.1.1", `[{"dst":"1.1.1.1","gateway":"203.0.113.1","dev":"eth0"}]`).
		fail("ip -j -6 route get", "unreachable")
	before, _ := clientPath(context.Background(), "127.0.0.1")
	if !before.Local || len(before.anchors) == 0 {
		t.Fatalf("path = %+v", before)
	}
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route get 1.1.1.1", out: `[{"dst":"1.1.1.1","gateway":"10.9.0.1","dev":"wg0"}]`}}, rec.replies...)
	rec.mu.Unlock()
	if err := verifyPath(before)(context.Background()); err == nil || !strings.Contains(err.Error(), "through wg0") {
		t.Fatalf("a moved way out must be refused for a local client too: %v", err)
	}
}
