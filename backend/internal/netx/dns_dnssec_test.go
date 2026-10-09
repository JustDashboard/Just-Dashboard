package netx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

type nativeSetReply struct {
	rdatas [][]byte
	flags  uint64
	fail   string
}

func testDNSKEY(t *testing.T) []byte {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte{1, 1, 3, 15}, public...)
}

func testDS(zone string, key []byte) []byte {
	digest, _ := dnsDSDigest(zone, key, 2)
	out := binary.BigEndian.AppendUint16(nil, dnsKeyTag(key))
	return append(append(out, 15, 2), digest...)
}

// chainExecutor is the native adapter over a fixed policy (a global ~. scope
// and vpn0 holding ~corp.example) whose ResolveRecord answers come from sets.
func chainExecutor(t *testing.T, sets map[string]nativeSetReply, asked *[]string) TrafficExecutor {
	t.Helper()
	base := dnsEvidenceExecutor(t, 0, nil)
	return func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "busctl" && len(args) > 12 && args[7] == "ResolveRecord" {
			key := args[10] + " " + args[12]
			*asked = append(*asked, key)
			reply, ok := sets[key]
			if !ok {
				return fmt.Sprintf("Call failed: '%s' does not have any RR of the requested type\n", args[10]), fmt.Errorf("native DNS command failed")
			}
			if reply.fail != "" {
				return "Call failed: " + reply.fail + "\n", fmt.Errorf("native DNS command failed")
			}
			owner := strings.TrimSuffix(args[10], ".")
			qtype := uint16(dnsTypeDNSKEY)
			if args[12] == "43" {
				qtype = dnsTypeDS
			}
			rows := []any{}
			for _, rdata := range reply.rdatas {
				rr := dnsEvidenceRR(owner, qtype, rdata)
				if owner == "" {
					rr = append([]byte{0}, dnsEvidenceRR("x", qtype, rdata)[3:]...)
				}
				rows = append(rows, []any{0, 1, qtype, rr})
			}
			return dnsEvidenceJSON("a(iqqay)t", rows, reply.flags), nil
		}
		return base(ctx, name, args...)
	}
}

func TestDNSSECChainRecomputesEveryLinkToTheRoot(t *testing.T) {
	root, com, example := testDNSKEY(t), testDNSKEY(t), testDNSKEY(t)
	previous := rootAnchors
	digest, _ := dnsDSDigest(".", root, 2)
	rootAnchors = []DNSSECDelegation{{KeyTag: dnsKeyTag(root), Algorithm: 15, DigestType: 2, Digest: hex.EncodeToString(digest)}}
	t.Cleanup(func() { rootAnchors = previous })
	fresh := dnsFlagDNS | dnsFlagNetwork | dnsFlagAuthenticated
	sets := map[string]nativeSetReply{
		". 48":            {rdatas: [][]byte{root}, flags: fresh},
		"com. 48":         {rdatas: [][]byte{com}, flags: fresh},
		"com. 43":         {rdatas: [][]byte{testDS("com", com)}, flags: fresh},
		"example.com. 48": {rdatas: [][]byte{example}, flags: fresh},
		"example.com. 43": {rdatas: [][]byte{testDS("example.com", example)}, flags: fresh},
	}
	var asked []string
	c, err := (&Service{}).InvestigateDNSSEC(context.Background(), "www.example.com", chainExecutor(t, sets, &asked))
	if err != nil || c.Error != "" {
		t.Fatalf("chain = %+v, %v", c, err)
	}
	if c.Verdict != "secure" || c.Questions != 6 || len(c.Levels) != 4 || !c.PolicyStable {
		t.Fatalf("chain = %+v asked %v", c, asked)
	}
	if c.Levels[0].Role != "not_apex" || c.Levels[1].Link != "digest_match" || c.Levels[2].Link != "digest_match" || c.Levels[3].Link != "root_anchor" || c.Levels[1].Links[0].MatchedKey == nil || !c.Levels[1].Keys[0].SEP {
		t.Fatalf("levels = %+v", c.Levels)
	}

	// A DS that no key hashes to breaks the chain.
	sets["example.com. 43"] = nativeSetReply{rdatas: [][]byte{testDS("example.com", testDNSKEY(t))}, flags: fresh}
	c, _ = (&Service{}).InvestigateDNSSEC(context.Background(), "www.example.com", chainExecutor(t, sets, &asked))
	if c.Verdict != "broken" || c.Levels[1].Link != "digest_mismatch" {
		t.Fatalf("broken chain = %+v", c)
	}
	// resolved rejecting a set is broken too; not authenticating is insecure.
	sets["example.com. 43"] = nativeSetReply{fail: "DNSSEC validation failed: signature-expired"}
	if c, _ = (&Service{}).InvestigateDNSSEC(context.Background(), "www.example.com", chainExecutor(t, sets, &asked)); c.Verdict != "broken" {
		t.Fatalf("rejected DS = %+v", c)
	}
	sets["example.com. 43"] = nativeSetReply{rdatas: [][]byte{testDS("example.com", example)}, flags: dnsFlagDNS | dnsFlagNetwork}
	if c, _ = (&Service{}).InvestigateDNSSEC(context.Background(), "www.example.com", chainExecutor(t, sets, &asked)); c.Verdict != "insecure" || c.Levels[1].DS.State != "unauthenticated" {
		t.Fatalf("unauthenticated DS = %+v", c)
	}
	// A cached answer's flags are not evidence.
	sets["example.com. 43"] = nativeSetReply{rdatas: [][]byte{testDS("example.com", example)}, flags: fresh | dnsFlagCache}
	if c, _ = (&Service{}).InvestigateDNSSEC(context.Background(), "www.example.com", chainExecutor(t, sets, &asked)); c.Levels[1].DS.State != "unauthenticated" {
		t.Fatalf("cached DS = %+v", c.Levels[1])
	}
}

// A name under vpn0's private routing domain never has its parents asked of
// the global scope; its chain ends where its scope does.
func TestDNSSECChainStaysInsideThePrivateScope(t *testing.T) {
	corp := testDNSKEY(t)
	fresh := dnsFlagDNS | dnsFlagNetwork | dnsFlagAuthenticated
	sets := map[string]nativeSetReply{"corp.example. 48": {rdatas: [][]byte{corp}, flags: fresh}}
	var asked []string
	c, err := (&Service{}).InvestigateDNSSEC(context.Background(), "secret.corp.example", chainExecutor(t, sets, &asked))
	if err != nil || c.Error != "" {
		t.Fatalf("chain = %+v, %v", c, err)
	}
	if c.Verdict != "anchored" || c.Levels[1].Link != "no_ds" || c.Levels[2].Role != "not_queried" || c.Levels[3].Role != "not_queried" || c.Levels[2].Scope != "global" {
		t.Fatalf("chain = %+v", c)
	}
	for _, q := range asked {
		if q == "example. 48" || q == "example. 43" || strings.HasPrefix(q, ". ") {
			t.Fatalf("an ancestor outside the private scope was asked: %v", asked)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("asked = %v", asked)
	}
}

func TestDNSSECChainRefusesWithoutANativeOwner(t *testing.T) {
	var asked []string
	executor := chainExecutor(t, nil, &asked)
	c, err := (&Service{}).InvestigateDNSSEC(context.Background(), "www.example.com", func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "busctl" && len(args) > 7 && args[7] == "GetNameOwner" {
			return "", fmt.Errorf("unavailable")
		}
		return executor(ctx, name, args...)
	})
	if err != nil || c.Error == "" || c.Verdict != "unknown" || len(asked) != 0 {
		t.Fatalf("chain = %+v, %v (asked %v)", c, err, asked)
	}
	if _, err := (&Service{}).InvestigateDNSSEC(context.Background(), "bad name", executor); err == nil {
		t.Fatal("an invalid name was walked")
	}
}

// The root anchors are the two IANA key-signing keys systemd-resolved 257
// carries built in; a root key is matched only by digest, never by tag alone.
func TestDNSSECRootAnchorsMatchByDigestOnly(t *testing.T) {
	if len(rootAnchors) != 2 || rootAnchors[0].KeyTag != 20326 || rootAnchors[1].KeyTag != 38696 {
		t.Fatalf("anchors = %+v", rootAnchors)
	}
	key := testDNSKEY(t)
	forged := []DNSSECDelegation{{KeyTag: dnsKeyTag(key), Algorithm: 15, DigestType: 2, Digest: strings.Repeat("00", 32)}}
	if links, verdict := matchDelegations(".", [][]byte{key}, forged); verdict != "digest_mismatch" || links[0].Matched {
		t.Fatalf("a tag-only match was accepted: %+v %s", links, verdict)
	}
	unsupported := []DNSSECDelegation{{KeyTag: dnsKeyTag(key), Algorithm: 15, DigestType: 3, Digest: "00"}}
	if links, _ := matchDelegations(".", [][]byte{key}, unsupported); links[0].Supported || links[0].Matched {
		t.Fatalf("an unsupported digest type matched: %+v", links)
	}
}
