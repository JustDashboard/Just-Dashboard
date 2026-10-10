package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func dnsEvidenceName(name string) []byte {
	var body []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		body = append(body, byte(len(label)))
		body = append(body, label...)
	}
	return append(body, 0)
}

func dnsAliasExecutor(t *testing.T, aliases map[string]string, flags func(string, string) uint64, questions *[]string) TrafficExecutor {
	t.Helper()
	var queries atomic.Int32
	base := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries)
	return func(ctx context.Context, name string, args ...string) (string, error) {
		if name != "busctl" || len(args) <= 7 || args[7] != "ResolveRecord" {
			return base(ctx, name, args...)
		}
		if args[4] != ":1.42" || args[13] != fmt.Sprint(dnsFreshFlags) || dnsFreshFlags&dnsFlagNoCNAME == 0 {
			t.Fatalf("an alias-capable or unpinned query was sent: %v", args)
		}
		owner := strings.TrimSuffix(args[10], ".")
		kind, _ := strconv.Atoi(args[12])
		label := owner + "/" + args[12]
		*questions = append(*questions, label)
		body := []byte{192, 0, 2, 7}
		if target := aliases[owner]; target != "" {
			if kind != 5 {
				message := "Call failed: CNAME loop detected, or CNAME resolving disabled on '" + owner + "'"
				return message, errors.New(message)
			}
			body = dnsEvidenceName(target)
		}
		f := dnsFlagDNS | dnsFlagNetwork | dnsFlagAuthenticated | dnsFlagConfidential
		if flags != nil {
			f = flags(owner, args[12])
		}
		return dnsEvidenceJSON("a(iqqay)t", []any{[]any{7, 1, kind, dnsEvidenceRR(owner, uint16(kind), body)}}, f), nil
	}
}

func TestDNSAliasSafeChainAndDirectCNAME(t *testing.T) {
	// systemd v257 resolved-def.h defines NO_CNAME at bit 5 and
	// NO_VALIDATE at bit 10. Disabling alias chasing must preserve validation.
	if dnsFreshFlags != uint64(1<<0|1<<5|1<<11|1<<12|1<<13|1<<14|1<<24) || dnsFreshFlags&(1<<10) != 0 {
		t.Fatal("native alias isolation disables validation or permits automatic chasing")
	}
	for _, kind := range []string{"A", "CNAME"} {
		t.Run(kind, func(t *testing.T) {
			questions := []string{}
			r, err := (&Service{}).InvestigateDNS(t.Context(), DNSInvestigationRequest{Name: "alias.corp.example", Type: kind}, dnsAliasExecutor(t, map[string]string{"alias.corp.example": "secret.corp.example"}, nil, &questions))
			wantQuestions := 3
			wantAnswer := "192.0.2.7"
			if kind == "CNAME" {
				wantQuestions, wantAnswer = 1, "secret.corp.example."
			}
			if err != nil || r.Error != "" || len(r.Answers) != 1 || r.Answers[0] != wantAnswer || len(questions) != wantQuestions || r.Transport.State != "encrypted" || r.DNSSEC.State != "validated" || r.Trust.State != "native_policy_validated" {
				t.Fatalf("safe alias failed: %+v %v questions=%v", r, err, questions)
			}
			if len(r.Hops) != wantQuestions || r.Hops[0].SnapshotBefore == "" || r.Hops[0].SnapshotAfter == "" || !r.PolicyStable {
				t.Fatalf("per-question evidence missing: %+v", r.Hops)
			}
		})
	}
}

func TestDNSAliasChainTrustIncludesEveryAcceptedRecord(t *testing.T) {
	for _, mode := range []string{"cleartext alias", "unsigned alias", "cached alias"} {
		t.Run(mode, func(t *testing.T) {
			questions := []string{}
			flags := func(owner, kind string) uint64 {
				f := dnsFlagDNS | dnsFlagNetwork | dnsFlagAuthenticated | dnsFlagConfidential
				if owner == "alias.corp.example" && kind == "5" {
					switch mode {
					case "cleartext alias":
						f &^= dnsFlagConfidential
					case "unsigned alias":
						f &^= dnsFlagAuthenticated
					case "cached alias":
						f = dnsFlagDNS | 1<<20 | dnsFlagAuthenticated | dnsFlagConfidential
					}
				}
				return f
			}
			r, err := (&Service{}).InvestigateDNS(t.Context(), DNSInvestigationRequest{Name: "alias.corp.example", Type: "A"}, dnsAliasExecutor(t, map[string]string{"alias.corp.example": "secret.corp.example"}, flags, &questions))
			if err != nil || r.Error != "" || len(r.Answers) != 1 || r.Trust.State == "native_policy_validated" && mode != "unsigned alias" {
				t.Fatalf("terminal answer hid alias provenance: %+v %v", r, err)
			}
			if mode == "cleartext alias" && r.Transport.State != "unencrypted" || mode == "unsigned alias" && r.DNSSEC.State != "not_authenticated" || mode == "cached alias" && (r.Transport.State != "unknown" || r.DNSSEC.State != "unknown") {
				t.Fatalf("whole-chain claims are too strong: %+v", r)
			}
		})
	}
}

func TestDNSAliasUnavailablePrivateTargetSendsNoTargetQuestion(t *testing.T) {
	for _, mode := range []string{"inactive", "serverless", "unreadable", "owner replaced"} {
		t.Run(mode, func(t *testing.T) {
			questions := []string{}
			base := dnsAliasExecutor(t, map[string]string{"alias.corp.example": "secret.lab.example"}, nil, &questions)
			execute := func(ctx context.Context, name string, args ...string) (string, error) {
				if name == "ip" {
					return `[{"ifindex":7,"ifname":"vpn0"},{"ifindex":8,"ifname":"vpn1"}]`, nil
				}
				if name == "busctl" && len(args) == 5 && args[3] == "tree" {
					return "/org/freedesktop/resolve1\n/org/freedesktop/resolve1/link/_37\n/org/freedesktop/resolve1/link/_38\n", nil
				}
				if name == "busctl" && len(args) > 7 {
					if mode == "owner replaced" && args[7] == "GetNameOwner" && len(questions) >= 2 {
						return dnsEvidenceJSON("s", ":1.43"), nil
					}
					if args[7] == "GetAll" && strings.HasSuffix(args[5], "/_38") {
						if mode == "unreadable" && len(questions) >= 2 {
							return "", errors.New("private link unreadable")
						}
						p := dnsEvidenceProperties(map[string]any{"DNSEx": dnsEvidenceProperty("a(iayqs)", []any{}), "Domains": dnsEvidenceProperty("a(sb)", []any{[]any{"lab.example", true}})})
						if mode == "inactive" {
							p["ScopesMask"] = dnsEvidenceProperty("t", 0)
						}
						return dnsEvidenceJSON("a{sv}", p), nil
					}
				}
				out, err := base(ctx, name, args...)
				if name == "busctl" && len(args) > 7 && args[7] == "GetAll" && args[5] == dnsNativePath {
					out = strings.Replace(out, `[7,"corp.example",true]`, `[7,"corp.example",true],[8,"lab.example",true]`, 1)
				}
				return out, err
			}
			r, err := (&Service{}).InvestigateDNS(t.Context(), DNSInvestigationRequest{Name: "alias.corp.example", Type: "A"}, execute)
			if err != nil || r.Error == "" || len(r.Answers) != 0 || len(questions) != 2 {
				t.Fatalf("private alias leaked: %+v %v questions=%v", r, err, questions)
			}
			for _, question := range questions {
				if strings.HasPrefix(question, "secret.lab.example/") {
					t.Fatalf("unavailable private target was queried: %v", questions)
				}
			}
		})
	}
}

func TestDNSAliasLoopsAndRedirectLimit(t *testing.T) {
	for _, cycle := range []bool{true, false} {
		aliases := map[string]string{}
		for i := 0; i < 10; i++ {
			aliases[fmt.Sprintf("hop%d.corp.example", i)] = fmt.Sprintf("hop%d.corp.example", i+1)
		}
		if cycle {
			aliases["hop1.corp.example"] = "hop0.corp.example"
		}
		questions := []string{}
		r, err := (&Service{}).InvestigateDNS(t.Context(), DNSInvestigationRequest{Name: "hop0.corp.example", Type: "A"}, dnsAliasExecutor(t, aliases, nil, &questions))
		if err != nil || r.Error == "" || len(r.Answers) != 0 || len(questions) > 18 || cycle && len(questions) != 4 {
			t.Fatalf("alias loop/bound failed: %+v %v questions=%v", r, err, questions)
		}
	}
}

func TestDNSEvidenceForeignResolverAndEmptyPolicySendNothing(t *testing.T) {
	for _, mode := range []string{"foreign", "uplink", "other server", "empty chain", "empty policy", "unowned suffix", "bad suffix"} {
		t.Run(mode, func(t *testing.T) {
			var queries atomic.Int32
			base := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries)
			r, err := (&Service{}).InvestigateDNS(t.Context(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
				if name == "cat" {
					if mode == "other server" {
						return "nameserver 192.0.2.53\n", nil
					}
					if mode == "empty chain" {
						return "", nil
					}
				}
				out, err := base(ctx, name, args...)
				if name == "busctl" && len(args) > 7 && args[7] == "GetAll" {
					if args[5] == dnsNativePath {
						switch mode {
						case "foreign", "uplink":
							out = strings.Replace(out, `"data":"stub"`, `"data":"`+mode+`"`, 1)
						case "empty policy":
							out = strings.Replace(out, `[[0,2,[8,8,8,8],0,"dns.google"],[7,2,[10,0,0,53],0,"dns.corp.example"]]`, `[]`, 1)
						case "unowned suffix":
							out = strings.Replace(out, `[7,"corp.example",true]`, `[8,"corp.example",true]`, 1)
						case "bad suffix":
							out = strings.ReplaceAll(out, "corp.example", "corp..example")
						}
					}
					if mode == "empty policy" && strings.HasSuffix(args[5], "/_37") {
						out = strings.Replace(out, `[[2,[10,0,0,53],0,"dns.corp.example"]]`, `[]`, 1)
					}
				}
				return out, err
			})
			if err != nil || r.Error == "" || queries.Load() != 0 {
				t.Fatalf("unsupported resolver queried: %+v %v queries=%d", r, err, queries.Load())
			}
		})
	}
}

func TestDNSEvidenceDNSSECErrorCannotBeSpoofedByEDE(t *testing.T) {
	for _, diagnostic := range []string{
		"Call failed: Could not resolve 'secret.corp.example', server or network returned error: SERVFAIL (DNSSEC Bogus: dnssec validation failed)",
		"Call failed: Could not resolve 'secret.corp.example', server or network returned error: SERVFAIL\nCall failed: DNSSEC validation failed: bogus",
		"Call failed: DNSSEC validation failed: bogus",
	} {
		var queries atomic.Int32
		base := dnsEvidenceExecutor(t, dnsFlagDNS|dnsFlagNetwork, &queries)
		r, err := (&Service{}).InvestigateDNS(t.Context(), DNSInvestigationRequest{Name: "secret.corp.example", Type: "A"}, func(ctx context.Context, name string, args ...string) (string, error) {
			if name == "busctl" && len(args) > 7 && args[7] == "ResolveRecord" {
				return diagnostic, errors.New(diagnostic)
			}
			return base(ctx, name, args...)
		})
		validatedFailure := strings.HasPrefix(diagnostic, "Call failed: DNSSEC validation failed:")
		if err != nil || r.Error == "" || (r.DNSSEC.State == "validation_failed") != validatedFailure || (r.Hops[0].DNSSEC.State == "validation_failed") != validatedFailure {
			t.Fatalf("spoofed DNSSEC classification: %+v %v diagnostic=%s", r, err, diagnostic)
		}
	}
}

func TestEffectiveLookupUsesActualForeignConfiguredChain(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "active\n").on("resolvectl status --no-pager", fixture(t, "dns-resolvectl-status.txt"))
	path := pointResolvConf(t, "static")
	if err := os.WriteFile(path, []byte("nameserver 198.51.100.53\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := startFakeDNS(t, answerA("192.0.2.7"))
	routeDNS(t, map[string]string{"198.51.100.54": server})
	previous, previousFile := dnsNativeExecutor, dnsLookupFileExecutor
	// The container-side file disagrees with the host namespace's chain.
	dnsLookupFileExecutor = func(_ context.Context, command string, args ...string) (string, error) {
		if command != "cat" || len(args) != 1 || args[0] != path {
			t.Fatalf("unexpected host file command: %s %v", command, args)
		}
		return "nameserver 198.51.100.54\n", nil
	}
	dnsNativeExecutor = func(context.Context, string, ...string) (string, error) {
		t.Fatal("foreign chain invoked resolved")
		return "", nil
	}
	t.Cleanup(func() { dnsNativeExecutor, dnsLookupFileExecutor = previous, previousFile })
	r, err := testService(t).Lookup(t.Context(), "secret.corp.example", "A")
	if err != nil || len(r.Answers) != 1 || r.Answers[0].Server != "198.51.100.54" || r.Answers[0].Error != "" || r.Route != "resolv.conf order" {
		t.Fatalf("running resolved replaced the real chain: %+v %v", r, err)
	}
}

func TestEffectiveLookupRefusesUnreadableHostConfiguredChain(t *testing.T) {
	for i, content := range []string{"", "nameserver bad.example\n", "nameserver 198.51.100.53 extra\n", strings.Repeat("#", (64<<10)+1), "unreadable"} {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			rec := record(t)
			rec.on("systemctl is-active systemd-resolved", "active\n").on("resolvectl status --no-pager", fixture(t, "dns-resolvectl-status.txt"))
			previousFile, previousNative, previousDial := dnsLookupFileExecutor, dnsNativeExecutor, dnsDial
			dnsLookupFileExecutor = func(context.Context, string, ...string) (string, error) {
				if content == "unreadable" {
					return "", errors.New("host file is unavailable")
				}
				return content, nil
			}
			dnsNativeExecutor = func(context.Context, string, ...string) (string, error) {
				t.Fatal("unreadable host chain invoked resolved")
				return "", nil
			}
			dnsDial = func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("unreadable host chain sent a wire query")
				return nil, nil
			}
			t.Cleanup(func() { dnsLookupFileExecutor, dnsNativeExecutor, dnsDial = previousFile, previousNative, previousDial })
			if result, err := testService(t).Lookup(t.Context(), "secret.corp.example", "A"); err == nil || result != nil {
				t.Fatalf("unreadable host chain accepted: %+v %v", result, err)
			}
		})
	}
}
