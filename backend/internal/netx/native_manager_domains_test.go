package netx

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

const nativeDomainFixture = "[Match]\nName=d0\n[Network]\nDHCP=ipv4\nIPv6AcceptRA=yes\n[DHCPv4]\nUseDomains=yes\n[DHCPv6]\nUseDomains=no\n[IPv6AcceptRA]\nUseDomains=route\n"

func TestNativeNetworkdPreservesAutomaticDomainPolicyIndependentlyOfDNS(t *testing.T) {
	p := &nativeProfile{View: NativeProfileView{Device: "d0"}}
	intent, err := parseNativeNetworkd([]byte(nativeDomainFixture), p)
	if err != nil || intent.IPv4.Method != "auto" || intent.IPv6.Method != "slaac" || intent.IPv4.IgnoreAutoDNS || intent.IPv6.IgnoreAutoDNS {
		t.Fatal("explicit domain acquisition policy changed address/DNS intent", intent, err)
	}
	intent.IPv4.IgnoreAutoDNS, intent.IPv6.IgnoreAutoDNS = true, true
	candidate, err := renderNativeNetworkd([]byte(nativeDomainFixture), intent)
	if err != nil || nativeNetworkdDomainPolicyEqual([]byte(nativeDomainFixture), candidate) != nil || !strings.Contains(string(candidate), "UseDNS=false") || !strings.Contains(string(candidate), "UseDomains=yes") || !strings.Contains(string(candidate), "UseDomains=route") {
		t.Fatal("DNS editing changed retained search/route domain acquisition policy", string(candidate), err)
	}
	if err := nativeProfileFieldsGuard(p, candidate, "networkd"); err != nil {
		t.Fatal("closed preserved domain policy was refused", err)
	}
	for _, unsafe := range []string{
		strings.Replace(nativeDomainFixture, "UseDomains=yes", "UseDomains=yes\nUseDomains=no", 1),
		strings.Replace(nativeDomainFixture, "UseDomains=yes", "UseDomains=foreign", 1),
		strings.Replace(nativeDomainFixture, "DHCP=ipv4", "DHCP=ipv4\nUseDomains=yes", 1),
		nativeDomainFixture + "[DHCP]\nUseDomains=yes\n",
	} {
		if nativeProfileFieldsGuard(p, []byte(unsafe), "networkd") == nil {
			t.Fatal("ambiguous/unrepresented automatic domain policy admitted", unsafe)
		}
	}
	if nativeNetworkdDomainPolicyEqual([]byte(nativeDomainFixture), []byte(strings.Replace(nativeDomainFixture, "UseDomains=yes", "UseDomains=no", 1))) == nil {
		t.Fatal("a staged automatic domain policy change was admitted")
	}
}

func TestNativeNetworkdVerifiesDomainProtocolProviderAndPlacement(t *testing.T) {
	intent := NativeIntent{IPv4: NativeFamilyIntent{Method: "auto"}, IPv6: NativeFamilyIntent{Method: "slaac"}}
	v4 := nativeNetworkdDomain{Domain: "auto.test", Source: "DHCPv4", Provider: []byte{198, 18, 8, 1}}
	v6 := nativeNetworkdDomain{Domain: "ra.test", Source: "NDisc", Provider: []byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	domains, err := nativeNetworkdVerifyDomains([]byte(nativeDomainFixture), intent, []nativeNetworkdDomain{v4}, []nativeNetworkdDomain{v6})
	if err != nil || len(domains) != 2 || domains[0] != "auto.test" || domains[1] != "~ra.test" {
		t.Fatal("real DHCP/RA domain projection refused", domains, err)
	}
	for name, alter := range map[string]func(*NativeIntent, *nativeNetworkdDomain, *nativeNetworkdDomain) []byte{
		"unset inherited policy": func(_ *NativeIntent, _, _ *nativeNetworkdDomain) []byte {
			return []byte(strings.Replace(nativeDomainFixture, "UseDomains=yes", "", 1))
		},
		"disabled policy": func(_ *NativeIntent, _, _ *nativeNetworkdDomain) []byte {
			return []byte(strings.Replace(nativeDomainFixture, "UseDomains=yes", "UseDomains=no", 1))
		},
		"wrong placement": func(_ *NativeIntent, _, _ *nativeNetworkdDomain) []byte {
			return []byte(strings.Replace(nativeDomainFixture, "UseDomains=yes", "UseDomains=route", 1))
		},
		"disabled source": func(in *NativeIntent, _, _ *nativeNetworkdDomain) []byte {
			in.IPv4.Method = "manual"
			return []byte(nativeDomainFixture)
		},
		"foreign source": func(_ *NativeIntent, d, _ *nativeNetworkdDomain) []byte {
			d.Source = "runtime"
			return []byte(nativeDomainFixture)
		},
		"missing provider": func(_ *NativeIntent, d, _ *nativeNetworkdDomain) []byte {
			d.Provider = nil
			return []byte(nativeDomainFixture)
		},
		"wrong family": func(_ *NativeIntent, d, _ *nativeNetworkdDomain) []byte {
			d.Provider = v6.Provider
			return []byte(nativeDomainFixture)
		},
		"zero provider": func(_ *NativeIntent, d, _ *nativeNetworkdDomain) []byte {
			d.Provider = []byte{0, 0, 0, 0}
			return []byte(nativeDomainFixture)
		},
		"wrong v6 protocol": func(_ *NativeIntent, _, d *nativeNetworkdDomain) []byte {
			d.Source = "DHCPv6"
			return []byte(nativeDomainFixture)
		},
		"foreign domain": func(_ *NativeIntent, d, _ *nativeNetworkdDomain) []byte {
			d.Domain = "foreign\n.test"
			return []byte(nativeDomainFixture)
		},
	} {
		t.Run(name, func(t *testing.T) {
			in, a, b := intent, v4, v6
			data := alter(&in, &a, &b)
			if _, err := nativeNetworkdVerifyDomains(data, in, []nativeNetworkdDomain{a}, []nativeNetworkdDomain{b}); err == nil {
				t.Fatal("foreign/mismatched automatic domain evidence accepted")
			}
		})
	}
	intent.IPv4.Domains = []string{"corp.test", "~."}
	if _, err := nativeNetworkdVerifyDomains(nil, intent, []nativeNetworkdDomain{{Domain: "corp.test", Source: "static"}}, []nativeNetworkdDomain{{Domain: ".", Source: "static"}}); err != nil {
		t.Fatal("captured static search/root routing domain refused", err)
	}
	if _, err := nativeNetworkdVerifyDomains(nil, intent, []nativeNetworkdDomain{{Domain: "foreign.test", Source: "static"}}, nil); err == nil {
		t.Fatal("foreign static domain accepted")
	}
}

func TestNativeNetworkdDomainPolicyFencesRecoveryAndActiveOverrides(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	f.u.Files[0].Before.Data = append(f.u.Files[0].Before.Data, []byte("[DHCPv4]\nUseDomains=yes\n")...)
	f.u.Files[0].Candidate.Data = append(f.u.Files[0].Candidate.Data, []byte("[DHCPv4]\nUseDomains=no\n")...)
	if _, err := nativeCommand(f.u); err == nil {
		t.Fatal("recovery accepted a candidate domain policy outside its prior captured scope")
	}
	p := &nativeProfile{View: NativeProfileView{Device: "d0", Kind: "dummy", Renderer: "networkd", Contract: f.u.Contract}, Device: ipLink{IfIndex: f.u.IfIndex, Address: f.u.MAC}}
	transcript := nativeExecute
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if tool == "networkctl" && strings.Contains(strings.Join(args, " "), "status d0") {
			return `{"Name":"d0","DNS":[],"SearchDomains":[{"Domain":"foreign.test","ConfigSource":"runtime"}],"RouteDomains":[]}`, nil
		}
		return transcript(ctx, input, tool, args...)
	}
	if got := readNativeRuntime(context.Background(), p, f.u.CandidateIntent); got.Status == "matching" {
		t.Fatal("active runtime-only domain override passed native admission", got)
	}
}

func TestNativeDomainRecoveryRefusesOldHelperBeforeJournalOrWatchdog(t *testing.T) {
	for _, token := range []string{"jd-native-manager-v3", "jd-native-manager-v4", "jd-native-manager-v5"} {
		t.Run(token, func(t *testing.T) {
			h := pendingHost(t)
			prior := nativeExecute
			t.Cleanup(func() { nativeExecute = prior })
			nativeExecute = func(_ context.Context, _ []byte, _ string, args ...string) (string, error) {
				if len(args) != 1 || args[0] != "--network-native-check" {
					t.Fatal("unexpected effect before native helper capability check", args)
				}
				return token + "\n", nil
			}
			_, err := h.prepareNativeChange(WithPendingConfirmation(context.Background(), 7), &nativeUndo{Transaction: strings.Repeat("1", 32)})
			var refusal *ReadOnlyError
			if !errors.As(err, &refusal) || h.rec.ran("systemd-run") {
				t.Fatal("old native helper admitted a journal or watchdog", err, h.rec.commands())
			}
			if _, err := readChange(h.paths.Dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("old native helper left a new transaction journal", err)
			}
		})
	}
}
