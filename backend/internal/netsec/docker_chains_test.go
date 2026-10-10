package netsec

import (
	"context"
	"strings"
	"testing"
)

// A transcript of `iptables -t nat -S DOCKER` on a host with a loopback
// publication, a wildcard one, a range and Docker's per-bridge RETURN.
const dockerNATTranscript = `-N DOCKER
-A DOCKER -i docker0 -j RETURN
-A DOCKER -d 127.0.0.1/32 ! -i br-93e5e9c9442b -p tcp -m tcp --dport 40507 -j DNAT --to-destination 10.0.4.3:3000
-A DOCKER ! -i docker0 -p tcp -m tcp --dport 5432 -j DNAT --to-destination 10.0.0.2:5432
-A DOCKER ! -i docker0 -p udp -m udp --dport 6000:6010 -j DNAT --to-destination 10.0.0.5:6000-6010
`

func TestParseDockerNATReadsBindingsAndSkipsOtherShapes(t *testing.T) {
	rules := ParseDockerNAT(dockerNATTranscript)
	if len(rules) != 3 {
		t.Fatalf("rules: %+v", rules)
	}
	loopback, wildcard, ranged := rules[0], rules[1], rules[2]
	if loopback.HostIP != "127.0.0.1/32" || loopback.ExceptInterface != "br-93e5e9c9442b" || loopback.To != "10.0.4.3:3000" || !loopback.Covers("tcp", "127.0.0.1", 40507) || loopback.Covers("tcp", "", 40507) {
		t.Fatalf("loopback publication: %+v", loopback)
	}
	if wildcard.HostIP != "" || !wildcard.Covers("tcp", "", 5432) || wildcard.Covers("udp", "", 5432) {
		t.Fatalf("wildcard publication: %+v", wildcard)
	}
	if !ranged.Covers("udp", "", 6005) || ranged.Covers("udp", "", 6011) {
		t.Fatalf("ranged publication: %+v", ranged)
	}
}

func TestParseChainRulesReadsPolicyAndOrder(t *testing.T) {
	policy, rules := ParseChainRules("-P FORWARD DROP\n-A FORWARD -j DOCKER-USER\n-A FORWARD -j DOCKER-FORWARD\n-A OTHER -j ACCEPT\n", "FORWARD")
	if policy != "DROP" || len(rules) != 2 || rules[0] != "-A FORWARD -j DOCKER-USER" {
		t.Fatalf("policy %q rules %v", policy, rules)
	}
}

func TestReadDockerChainsKeepsEachPartsError(t *testing.T) {
	previous := run
	t.Cleanup(func() { run = previous })
	var calls []string
	run = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "-t nat -S DOCKER":
			return "", errString("iptables: No chain/target/match by that name.")
		case "-S DOCKER-USER":
			return "-N DOCKER-USER\n-A DOCKER-USER -j RETURN\n", nil
		}
		return "-P FORWARD ACCEPT\n", nil
	}
	chains := ReadDockerChains(t.Context(), "inet6")
	if chains.NATError == "" || len(chains.NAT) != 0 || len(chains.User) != 1 || chains.ForwardPolicy != "ACCEPT" {
		t.Fatalf("chains: %+v", chains)
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "ip6tables ") || !strings.Contains(call, "-S") {
			t.Fatalf("an IPv6 reading lists with ip6tables only, and never changes a rule: %v", calls)
		}
	}
}

// FORWARD on a host running Tailscale, Docker 28+ and ufw, as `iptables -S`
// lists it: Tailscale's chain first, then Docker's, then ufw's.
func TestForwardOrderFindsDockersAcceptAheadOfUfw(t *testing.T) {
	_, forward := ParseChainRules(`-P FORWARD DROP
-A FORWARD -j ts-forward
-A FORWARD -j DOCKER-USER
-A FORWARD -j DOCKER-FORWARD
-A FORWARD -j ufw-before-logging-forward
-A FORWARD -j ufw-before-forward
`, "FORWARD")
	_, filter := ParseChainRules(`-N DOCKER
-A DOCKER -d 10.0.0.3/32 ! -i docker0 -o docker0 -p tcp -m tcp --dport 80 -j ACCEPT
-A DOCKER -d 10.0.0.2/32 ! -i docker0 -o docker0 -p tcp -m tcp --dport 5432 -j ACCEPT
`, "DOCKER")
	chains := DockerChains{Forward: forward, Filter: filter}
	first, ahead := chains.ForwardOrder()
	if !first || len(ahead) != 1 || ahead[0] != "ts-forward" {
		t.Fatalf("Docker first %v, ahead %v", first, ahead)
	}
	if rule := chains.DockerAccept("tcp", "10.0.0.3", 80); rule == "" {
		t.Fatal("Docker's accept for the container's port was not found")
	}
	if rule := chains.DockerAccept("tcp", "10.0.0.3", 8080); rule != "" {
		t.Fatalf("another port's accept matched: %s", rule)
	}
	if rule := chains.DockerAccept("tcp", "10.0.0.30", 80); rule != "" {
		t.Fatalf("another address's accept matched: %s", rule)
	}
	_, ufwFirst := ParseChainRules("-A FORWARD -j ufw-before-forward\n-A FORWARD -j DOCKER-USER\n-A FORWARD -j DOCKER-FORWARD\n", "FORWARD")
	if first, _ := (DockerChains{Forward: ufwFirst}).ForwardOrder(); first {
		t.Fatal("ufw's chain ahead of Docker's decides first")
	}
}
