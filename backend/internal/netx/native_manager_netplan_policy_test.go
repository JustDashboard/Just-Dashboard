package netx

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These inputs retain the actual private Netplan 1.1.2 generation capture.
const nativeNetplanExplicitAutomaticSource = "network:\n  version: 2\n  renderer: networkd\n  ethernets:\n    d0:\n      dhcp4: true\n      dhcp6: false\n      accept-ra: true\n      dhcp4-overrides:\n        use-domains: true\n        use-mtu: false\n      ra-overrides:\n        use-domains: true\n"

const nativeNetplanExplicitAutomaticGenerated = "[Match]\nName=d0\n\n[Network]\nDHCP=ipv4\nLinkLocalAddressing=ipv6\nIPv6AcceptRA=yes\n\n[DHCP]\nRouteMetric=100\nUseMTU=false\nUseDomains=true\n\n[IPv6AcceptRA]\nUseDomains=true\n"

func nativeNetplanPolicyProfile() *nativeProfile {
	return &nativeProfile{
		View: NativeProfileView{Owner: "netplan", Renderer: "networkd", Device: "d0", Kind: "veth", Version: "1.1.2"},
		File: nativeProfileFile{Data: []byte(nativeNetplanExplicitAutomaticSource)}, Generated: nativeProfileFile{Data: []byte(nativeNetplanExplicitAutomaticGenerated)},
		NetplanID: "d0", NetplanSection: "ethernets",
	}
}

func TestNativeNetplanAutomaticPolicyMatchesActualPrivateGeneration(t *testing.T) {
	p := nativeNetplanPolicyProfile()
	domains, err := nativeNetplanGeneratedAutomaticPolicy(p, p.File.Data, p.Generated.Data)
	if err != nil || domains["DHCPv4"] != "yes" || domains["IPv6AcceptRA"] != "yes" || domains["DHCPv6"] != "" {
		t.Fatal("captured explicit DHCP4/RA domain policy was not preserved", domains, err)
	}
	intent, err := parseNativeNetplan(p.File.Data, p)
	if err != nil {
		t.Fatal(err)
	}
	p.View.Intent = &intent
	if err := nativeNetplanFieldsGuard(p); err != nil {
		t.Fatal("exact authored/generated policy was refused", err)
	}
	if nativeProfileFieldsGuard(nil, p.Generated.Data, "networkd") == nil {
		t.Fatal("shared policy was adopted without its exact authored Netplan origin")
	}
}

func TestNativeNetplanAutomaticPolicyRefusesUnrepresentedOriginsAndOverrides(t *testing.T) {
	for name, alter := range map[string]func(*nativeProfile) []byte{
		"direct owner":   func(p *nativeProfile) []byte { p.View.Owner = "networkd"; return p.File.Data },
		"other renderer": func(p *nativeProfile) []byte { p.View.Renderer = "NetworkManager"; return p.File.Data },
		"controller":     func(p *nativeProfile) []byte { p.View.Contract.Members = []string{"d1"}; return p.File.Data },
		"member":         func(p *nativeProfile) []byte { p.View.Contract.Master = "b0"; return p.File.Data },
		"MTU from DHCP": func(p *nativeProfile) []byte {
			return []byte(strings.Replace(string(p.File.Data), "use-mtu: false", "use-mtu: true", 1))
		},
		"default MTU from DHCP": func(p *nativeProfile) []byte {
			return []byte(strings.Replace(string(p.File.Data), "        use-mtu: false\n", "", 1))
		},
		"string boolean": func(p *nativeProfile) []byte {
			return []byte(strings.Replace(string(p.File.Data), "use-mtu: false", "use-mtu: 'false'", 1))
		},
		"string domain boolean": func(p *nativeProfile) []byte {
			return []byte(strings.Replace(string(p.File.Data), "use-domains: true", "use-domains: 'true'", 1))
		},
		"unknown nested effect": func(p *nativeProfile) []byte {
			return []byte(strings.Replace(string(p.File.Data), "use-mtu: false", "use-mtu: false\n        use-hostname: false", 1))
		},
		"unsupported RA route suppression": func(p *nativeProfile) []byte {
			return append(p.File.Data, []byte("        use-routes: false\n")...)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := nativeNetplanPolicyProfile()
			if _, err := nativeReadNetplanAutomaticPolicy(p, alter(p)); err == nil {
				t.Fatal("unrepresented automatic native effect or origin was admitted")
			}
		})
	}
}

func TestNativeNetplanGeneratedPolicyRefusesDriftAndContradictoryAliases(t *testing.T) {
	for name, generated := range map[string]string{
		"changed domain":           strings.Replace(nativeNetplanExplicitAutomaticGenerated, "UseDomains=true", "UseDomains=false", 1),
		"DHCP MTU":                 strings.Replace(nativeNetplanExplicitAutomaticGenerated, "UseMTU=false", "UseMTU=true", 1),
		"different default metric": strings.Replace(nativeNetplanExplicitAutomaticGenerated, "RouteMetric=100", "RouteMetric=101", 1),
		"unknown generated effect": nativeNetplanExplicitAutomaticGenerated + "[DHCP]\nSendHostname=false\n",
		"foreign domain alias":     nativeNetplanExplicitAutomaticGenerated + "[DHCPv4]\nUseDomains=route\n",
		"foreign inactive alias":   nativeNetplanExplicitAutomaticGenerated + "[DHCPv6]\nUseDomains=no\n",
		"duplicate generated key":  nativeNetplanExplicitAutomaticGenerated + "[DHCP]\nUseDomains=true\n",
		"foreign DNS alias":        nativeNetplanExplicitAutomaticGenerated + "[DHCPv4]\nUseDNS=false\n",
		"RA route suppression":     nativeNetplanExplicitAutomaticGenerated + "[IPv6AcceptRA]\nUseGateway=false\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := nativeNetplanPolicyProfile()
			if _, err := nativeNetplanGeneratedAutomaticPolicy(p, p.File.Data, []byte(generated)); err == nil {
				t.Fatal("generated policy drift or ambiguous alias was admitted")
			}
		})
	}
	p := nativeNetplanPolicyProfile()
	if _, err := nativeNetplanGeneratedAutomaticPolicy(p, p.File.Data, []byte(nativeNetplanExplicitAutomaticGenerated+"[DHCPv4]\nUseDomains=yes\n")); err != nil {
		t.Fatal("equivalent closed native boolean alias was refused", err)
	}
}

func TestNativeNetplanDNSPreferenceRetainsIndependentAutomaticDomains(t *testing.T) {
	p := nativeNetplanPolicyProfile()
	authored := strings.Replace(nativeNetplanExplicitAutomaticSource, "use-mtu: false", "use-mtu: false\n        use-dns: false", 1)
	authored += "        use-dns: false\n"
	generated := strings.Replace(nativeNetplanExplicitAutomaticGenerated, "UseMTU=false", "UseMTU=false\nUseDNS=false", 1)
	generated += "UseDNS=false\n"
	if err := nativeNetplanRetainsAutomaticPolicy(p, p.File.Data, []byte(authored)); err != nil {
		t.Fatal("mutable DNS preference changed the retained domain/MTU contract", err)
	}
	domains, err := nativeNetplanGeneratedAutomaticPolicy(p, []byte(authored), []byte(generated))
	if err != nil || domains["DHCPv4"] != "yes" || domains["IPv6AcceptRA"] != "yes" {
		t.Fatal("UseDNS=false removed independently acquired search domains", domains, err)
	}
}

func TestNativeNetplanGeneratedPolicyRefusesAdditionalProtocolMTURouteAndMetric(t *testing.T) {
	for _, dhcp6 := range []bool{false, true} {
		p := nativeNetplanPolicyProfile()
		authored := nativeNetplanExplicitAutomaticSource
		if dhcp6 {
			authored = strings.Replace(authored, "dhcp6: false", "dhcp6: true", 1)
			authored += "      dhcp6-overrides:\n        use-domains: true\n        use-mtu: false\n"
		}
		if _, err := nativeNetplanGeneratedAutomaticPolicy(p, []byte(authored), p.Generated.Data); err != nil {
			t.Fatal("matching authored shared DHCP4/6 policy was refused", dhcp6, err)
		}
		for _, section := range []string{"DHCPv4", "DHCPv6"} {
			for _, property := range []string{"UseMTU=true", "UseMTU=false", "UseRoutes=false", "UseRoutes=true", "RouteMetric=101", "RouteMetric=100"} {
				generated := nativeNetplanExplicitAutomaticGenerated + "[" + section + "]\n" + property + "\n"
				if _, err := nativeNetplanGeneratedAutomaticPolicy(p, []byte(authored), []byte(generated)); err == nil {
					t.Fatal("uncaptured active/inactive protocol policy was admitted", dhcp6, section, property)
				}
			}
		}
	}
}

func TestNativeNetplanManualTransitionRetainsDormantAuthoredPolicy(t *testing.T) {
	p := nativeNetplanPolicyProfile()
	intent := NativeIntent{IPv4: nativeEmptyFamily("manual"), IPv6: nativeEmptyFamily("manual")}
	intent.IPv4.Addresses = []string{"198.18.8.20/24"}
	intent.IPv6.Addresses = []string{"2001:db8:8::20/64"}
	candidate, err := renderNativeNetplan(p.File.Data, p, intent)
	if err != nil || nativeNetplanRetainsAutomaticPolicy(p, p.File.Data, candidate) != nil {
		t.Fatal("manual transition lost the dormant authored domain/MTU policy", string(candidate), err)
	}
	manualGenerated := "[Match]\nName=d0\n[Network]\nDHCP=no\nIPv6AcceptRA=no\n[IPv6AcceptRA]\nUseDomains=true\n"
	domains, err := nativeNetplanGeneratedAutomaticPolicy(p, candidate, []byte(manualGenerated))
	if err != nil || len(domains) != 0 {
		t.Fatal("inactive generated DHCP disappearance changed retained authored policy", domains, err)
	}
	if nativeNetplanRetainsAutomaticPolicy(p, p.File.Data, []byte(strings.Replace(string(candidate), "use-domains: true", "use-domains: false", 1))) == nil {
		t.Fatal("a retained dormant domain policy change was admitted")
	}
	if _, err := nativeNetplanGeneratedAutomaticPolicy(p, candidate, []byte(manualGenerated+"[DHCP]\nUseMTU=false\nRouteMetric=100\n")); err == nil {
		t.Fatal("foreign generated DHCP policy survived a manual transition")
	}
}

func TestNativeNetplanSelectedEditPreservesEveryUnselectedSemanticValue(t *testing.T) {
	p := nativeNetplanPolicyProfile()
	before := nativeNetplanExplicitAutomaticSource + "    d1:\n      dhcp4: false\n      addresses: [192.0.2.2/24]\n      optional: true\n"
	intent := NativeIntent{IPv4: nativeEmptyFamily("manual"), IPv6: nativeEmptyFamily("manual")}
	intent.IPv4.Addresses, intent.IPv6.Addresses = []string{"198.18.8.20/24"}, []string{"2001:db8:8::20/64"}
	candidate, err := renderNativeNetplan([]byte(before), p, intent)
	if err != nil || nativeNetplanRetainsAutomaticPolicy(p, []byte(before), candidate) != nil {
		t.Fatal("selected edit altered the unselected source", string(candidate), err)
	}
	for name, altered := range map[string]string{
		"unselected address":         strings.Replace(string(candidate), "192.0.2.2/24", "192.0.2.3/24", 1),
		"unselected boot preference": strings.Replace(string(candidate), "optional: true", "optional: false", 1),
		"native owner":               strings.Replace(string(candidate), "renderer: networkd", "renderer: NetworkManager", 1),
		"selected identity":          strings.Replace(string(candidate), "    d0:", "    d2:", 1),
		"selected native effect":     strings.Replace(string(candidate), "    d0:", "    d0:\n      optional: true", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if nativeNetplanRetainsAutomaticPolicy(p, []byte(before), []byte(altered)) == nil {
				t.Fatal("an unrelated authored semantic change was admitted")
			}
		})
	}
}

func TestNativeNetplanSuppressionUsesAuthoredRADNSAndRetainedDomainProvenance(t *testing.T) {
	p := nativeNetplanPolicyProfile()
	intent, err := parseNativeNetplan(p.File.Data, p)
	if err != nil {
		t.Fatal(err)
	}
	intent.IPv4.IgnoreAutoDNS, intent.IPv6.IgnoreAutoDNS, intent.IPv4.IgnoreAutoRoutes = true, true, true
	candidate, err := renderNativeNetplan(p.File.Data, p, intent)
	if err != nil {
		t.Fatal(err)
	}
	staged := *p
	staged.File.Data = candidate
	generated := strings.Replace(nativeNetplanExplicitAutomaticGenerated, "UseMTU=false", "UseMTU=false\nUseDNS=false\nUseRoutes=false", 1) + "UseDNS=false\n"
	staged.Generated.Data = []byte(generated)
	staged.View.Intent = &intent
	if nativeNetplanFieldsGuard(&staged) != nil || nativeNetplanRetainsAutomaticPolicy(p, p.File.Data, candidate) != nil {
		t.Fatal("represented DNS/IPv4 route suppression lost authored domain policy")
	}
	parsed, err := parseNativeNetplan(candidate, p)
	if err != nil || !nativeIntentEqual(parsed, intent) {
		t.Fatal("authored RA DNS preference did not preserve requested supported intent", parsed, err)
	}
	policy, err := nativeNetworkdProfileDomainPolicy(staged.Generated.Data, &staged)
	if err != nil {
		t.Fatal(err)
	}
	domains, err := nativeNetworkdVerifyDomainPolicy(policy, intent, []nativeNetworkdDomain{{Domain: "auto.test", Source: "DHCPv4", Provider: []byte{198, 18, 8, 1}}, {Domain: "ra.test", Source: "NDisc", Provider: []byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}}, nil)
	if err != nil || len(domains) != 2 {
		t.Fatal("DNS suppression discarded independently acquired native search domains", domains, err)
	}
	intent.IPv6.IgnoreAutoRoutes = true
	if nativeL3Contract(p, intent) == nil {
		t.Fatal("unrepresentable persistent IPv6 RA route suppression was admitted")
	}
}

func nativeNetplanPolicyUndo(t *testing.T) *nativeUndo {
	t.Helper()
	p := nativeNetplanPolicyProfile()
	before, err := parseNativeNetplan(p.File.Data, p)
	if err != nil {
		t.Fatal(err)
	}
	candidateIntent := NativeIntent{IPv4: nativeEmptyFamily("manual"), IPv6: nativeEmptyFamily("manual")}
	candidateIntent.IPv4.Addresses, candidateIntent.IPv6.Addresses = []string{"198.18.8.20/24"}, []string{"2001:db8:8::20/64"}
	candidate, err := renderNativeNetplan(p.File.Data, p, candidateIntent)
	if err != nil {
		t.Fatal(err)
	}
	manualGenerated := []byte("[Match]\nName=d0\n[Network]\nDHCP=no\nIPv6AcceptRA=no\nAddress=198.18.8.20/24\nAddress=2001:db8:8::20/64\n[IPv6AcceptRA]\nUseDomains=true\n")
	u := &nativeUndo{Version: 3, Transaction: strings.Repeat("a", 32), Owner: "netplan", Renderer: "networkd", OwnerVersion: "1.1.2", OwnerBus: ":1.17", BusID: strings.Repeat("b", 32), TransportGUID: strings.Repeat("c", 32), BootID: "cd8a65d9-42f1-4fe9-966c-c9a3268f89a2", RecoveryStrategy: nativeExactOriginStrategy, Device: "d0", IfIndex: 2, Kind: "physical", MAC: "02:00:00:00:08:02", NetplanID: "d0", NetplanSection: "ethernets", BeforeIntent: before, CandidateIntent: candidateIntent, CheckpointState: "none"}
	for i, pair := range [][3][]byte{{[]byte("/etc/netplan/20-auto.yaml"), p.File.Data, candidate}, {[]byte("/run/systemd/network/10-netplan-d0.network"), p.Generated.Data, manualGenerated}} {
		path := string(pair[0])
		prefix := filepath.Join(filepath.Dir(path), ".jd-native-"+u.Transaction+"-")
		u.Files = append(u.Files, nativeUndoFile{Before: nativeProfileFile{Path: path, Data: pair[1], Identity: nativeFileIdentity{Inode: uint64(1 + 3*i), Mode: 0o600}}, Candidate: nativeProfileFile{Path: path, Data: pair[2], Identity: nativeFileIdentity{Inode: uint64(2 + 3*i), Mode: 0o600}}, CandidatePath: prefix + "candidate-" + strconv.Itoa(i), RollbackPath: prefix + "rollback-" + strconv.Itoa(i), RollbackID: nativeFileIdentity{Inode: uint64(3 + 3*i), Mode: 0o600}, Cleanup: "pending"})
	}
	return u
}

func TestNativeNetplanRecoveryDecodesExactAuthoredPolicyAcrossGeneratedDHCPDisappearance(t *testing.T) {
	u := nativeNetplanPolicyUndo(t)
	if _, err := nativeCommand(u); err != nil {
		t.Fatal("exact authored before/manual candidate recovery scope was refused", err)
	}
	for name, mutate := range map[string]func(*nativeUndo){
		"recorded intent drift": func(u *nativeUndo) { u.CandidateIntent.IPv4.Addresses = []string{"198.18.8.21/24"} },
		"retained domain change": func(u *nativeUndo) {
			u.Files[0].Candidate.Data = []byte(strings.Replace(string(u.Files[0].Candidate.Data), "use-domains: true", "use-domains: false", 1))
		},
		"foreign generated policy": func(u *nativeUndo) {
			u.Files[1].Candidate.Data = append(u.Files[1].Candidate.Data, []byte("[DHCP]\nUseMTU=false\nRouteMetric=100\n")...)
		},
		"generated address drift": func(u *nativeUndo) {
			u.Files[1].Candidate.Data = []byte(strings.Replace(string(u.Files[1].Candidate.Data), "198.18.8.20/24", "198.18.8.21/24", 1))
		},
		"selected native effect": func(u *nativeUndo) {
			u.Files[0].Candidate.Data = []byte(strings.Replace(string(u.Files[0].Candidate.Data), "    d0:", "    d0:\n      optional: true", 1))
		},
		"unselected source definition": func(u *nativeUndo) {
			u.Files[0].Candidate.Data = append(u.Files[0].Candidate.Data, []byte("    d1:\n      dhcp4: true\n")...)
		},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(u)
			if err != nil {
				t.Fatal(err)
			}
			var changed nativeUndo
			if json.Unmarshal(encoded, &changed) != nil {
				t.Fatal("private fixture clone failed")
			}
			mutate(&changed)
			if _, err := nativeCommand(&changed); err == nil {
				t.Fatal("foreign or inconsistent retained recovery policy was admitted")
			}
		})
	}
}
