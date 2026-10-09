package netsec

import "testing"

func TestInboundRuleForReadsOnlyRulesWhoseTargetIsSaid(t *testing.T) {
	ufw := &FirewallStatus{Backend: BackendUFW, Available: true, Enabled: true, Rules: []Rule{
		{Number: 1, Action: "ALLOW", Direction: "IN", From: "Anywhere", To: "8000/tcp", Port: "8000", Protocol: "tcp", Raw: "8000/tcp ALLOW IN Anywhere"},
		{Number: 2, Action: "ALLOW", Direction: "IN", From: "Anywhere", To: "9000/tcp on tailscale0", Port: "9000", Protocol: "tcp", Raw: "9000/tcp on tailscale0 ALLOW IN Anywhere"},
		{Number: 3, Action: "DENY", Direction: "IN", From: "203.0.113.0/24", To: "7000:7010/tcp", Port: "7000:7010", Protocol: "tcp", Raw: "7000:7010/tcp DENY IN 203.0.113.0/24"},
		{Number: 4, Action: "ALLOW", Direction: "OUT", From: "Anywhere", To: "6000/tcp", Port: "6000", Protocol: "tcp", Raw: "6000/tcp ALLOW OUT Anywhere"},
	}}
	if r, ok := InboundRuleFor(ufw, "tcp", 8000); !ok || r.Number != 1 {
		t.Fatalf("8000: %+v %v", r, ok)
	}
	if r, ok := InboundRuleFor(ufw, "tcp", 7005); !ok || r.Number != 3 {
		t.Fatalf("a range decides its ports: %+v %v", r, ok)
	}
	if _, ok := InboundRuleFor(ufw, "udp", 8000); ok {
		t.Fatal("a TCP rule does not decide UDP")
	}
	if _, ok := InboundRuleFor(ufw, "tcp", 6000); ok {
		t.Fatal("an outbound rule decides no inbound port")
	}
	if r, ok := InboundRuleFor(ufw, "tcp", 9000); ok {
		t.Fatalf("an interface-limited rule is not read as deciding the port everywhere: %+v", r)
	}
}
