package netx

import (
	"bytes"
	"strings"
	"testing"
)

func TestNativeINIParseRenderRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"terminal section LF", "[Network]\n", "[Network]\n"},
		{"terminal property LF", "[Match]\nName=d0\n", "[Match]\nName=d0\n"},
		{"missing terminal LF", "[Match]\nName=d0", "[Match]\nName=d0\n"},
		{"intentional terminal blank lines", "[Match]\nName=d0\n\n\n", "[Match]\nName=d0\n\n\n"},
		{"comments and blank lines", "# Owner comment\n\n[Match]\n; Exact device\nName=d0\n \t\n\n", "# Owner comment\n\n[Match]\n; Exact device\nName=d0\n \t\n\n"},
		{"CRLF", "# Owner comment\r\n[Match]\r\nName=d0\r\n\r\n", "# Owner comment\r\n[Match]\nName=d0\r\n\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(test.input)
			for pass := 0; pass < 4; pass++ {
				blocks, err := parseNativeINI(data)
				if err != nil {
					t.Fatal(err)
				}
				data = blocks.render()
				if string(data) != test.want {
					t.Fatalf("round trip %d changed profile lines: got %q, want %q", pass+1, data, test.want)
				}
			}
		})
	}
}

func TestNativeINIRejectsMalformedTerminalLines(t *testing.T) {
	for _, data := range []string{
		"[Network]\nDNS=192.0.2.53\\\n",
		"[Network]\n=unowned\n",
		"[Network\n",
		"[Network]\nDNS=192.0.2.53\x00\n",
	} {
		if _, err := parseNativeINI([]byte(data)); err == nil {
			t.Fatalf("malformed terminal line was accepted: %q", data)
		}
	}
}

func TestNativeNetworkdRenderIdempotence(t *testing.T) {
	const source = "# Owner comment\n\n[Match]\nName=d0\n\n[Network]\n; Retained network comment\n\n[DHCPv4]\n# DHCP search policy\nUseDomains=yes\n\n[DHCPv6]\nUseDomains=no\n\n[IPv6AcceptRA]\n# RA routing policy\nUseDomains=route\n\n"
	p := &nativeProfile{View: NativeProfileView{Device: "d0"}}
	auto := NativeIntent{IPv4: nativeEmptyFamily("auto"), IPv6: nativeEmptyFamily("slaac")}
	suppressed := NativeIntent{IPv4: nativeEmptyFamily("auto"), IPv6: nativeEmptyFamily("slaac")}
	manual := NativeIntent{IPv4: nativeEmptyFamily("manual"), IPv6: nativeEmptyFamily("manual")}
	for _, in := range []*NativeIntent{&suppressed, &manual} {
		in.IPv4.IgnoreAutoDNS, in.IPv6.IgnoreAutoDNS = true, true
		in.IPv4.IgnoreAutoRoutes, in.IPv6.IgnoreAutoRoutes = true, true
		in.IPv4.DNS, in.IPv6.DNS = []string{"192.0.2.53"}, []string{"2001:db8::53"}
		in.IPv4.Domains, in.IPv6.Domains = []string{"fixture.test", "~route.test"}, []string{"fixture.test", "~route.test"}
	}
	manual.IPv4.Addresses, manual.IPv6.Addresses = []string{"192.0.2.10/24"}, []string{"2001:db8::10/64"}
	manual.IPv4.Routes = []NativeRoute{{Destination: "203.0.113.0/24", Gateway: "192.0.2.1", Metric: 20, Table: 254}}
	manual.IPv6.Routes = []NativeRoute{{Destination: "2001:db8:1::/64", Gateway: "2001:db8::1", Metric: 30, Table: 254}}
	for name, input := range map[string]string{
		"LF":         source,
		"missing LF": strings.TrimRight(source, "\n"),
		"CRLF":       strings.ReplaceAll(source, "\n", "\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			data := []byte(input)
			var initial []byte
			for phase, in := range []NativeIntent{auto, manual, suppressed, manual, auto} {
				candidate, err := renderNativeNetworkd(data, in)
				if err != nil {
					t.Fatal(err)
				}
				for pass := 0; pass < 3; pass++ {
					repeated, err := renderNativeNetworkd(candidate, in)
					if err != nil || !bytes.Equal(repeated, candidate) {
						t.Fatalf("phase %d repeat %d changed unchanged policy bytes: before=%q after=%q err=%v", phase, pass+1, candidate, repeated, err)
					}
					candidate = repeated
				}
				parsed, err := parseNativeNetworkd(candidate, p)
				if err != nil || !nativeIntentEqual(parsed, in) {
					t.Fatalf("phase %d changed address/DNS/domain/route intent: %+v err=%v", phase, parsed, err)
				}
				if err := nativeProfileFieldsGuard(p, candidate, "networkd"); err != nil {
					t.Fatal("retained native policy was refused", err)
				}
				for _, retained := range []string{"# Owner comment", "; Retained network comment", "# DHCP search policy", "# RA routing policy"} {
					if !bytes.Contains(candidate, []byte(retained)) {
						t.Fatal("retained profile comment was lost", retained)
					}
				}
				if err := nativeNetworkdDomainPolicyEqual([]byte(input), candidate); err != nil {
					t.Fatal("DHCP/RA domain policy changed", err)
				}
				if phase == 0 {
					initial = candidate
				}
				data = candidate
			}
			if !bytes.Equal(initial, data) {
				t.Fatalf("returning to automatic policy changed its bytes: before=%q after=%q", initial, data)
			}
		})
	}
}
