package netsec

import (
	"strings"
	"testing"
)

func unknownByID(p *Posture, id string) *PostureUnknown {
	for i := range p.Unknowns {
		if p.Unknowns[i].ID == id {
			return &p.Unknowns[i]
		}
	}
	return nil
}

func TestAssessAlwaysNamesProviderPolicyAsUnknown(t *testing.T) {
	p := Assess(AssessInput{PublicAddressRead: true})
	provider := unknownByID(p, "unknown.provider")
	if provider == nil || !strings.Contains(provider.Detail, "no public address on any interface") {
		t.Fatalf("provider policy must always be unknown: %+v", p.Unknowns)
	}
	if nft := unknownByID(p, "unknown.nftables"); nft == nil || !strings.Contains(nft.Title, "not read") {
		t.Fatalf("an unread ruleset is unknown, not clean: %+v", p.Unknowns)
	}
	p = Assess(AssessInput{PublicAddressRead: true, PublicAddress: "203.0.113.4", Policy: &PolicyCoverage{}})
	if provider := unknownByID(p, "unknown.provider"); !strings.Contains(provider.Detail, "203.0.113.4") {
		t.Fatalf("a public interface address is named: %+v", provider)
	}
	if unknownByID(p, "unknown.nftables") != nil {
		t.Fatal("a ruleset read with no foreign chain adds no nftables unknown")
	}
}

func TestAssessNamesComplexForeignNftablesChainsOnly(t *testing.T) {
	p := Assess(AssessInput{Policy: &PolicyCoverage{Layers: []PolicyLayer{
		{Family: "ip", Table: "filter", Chain: "INPUT", Hook: "input", Policy: "drop", Status: "blocked"},
		{Family: "inet", Table: "firewalld", Chain: "filter_INPUT", Hook: "input", Policy: "accept", Status: "unknown"},
		{Family: "inet", Table: "jd_gateway", Chain: "forward", Hook: "forward", Policy: "accept", Status: "owned"},
		{Family: "inet", Table: "crowdsec", Chain: "crowdsec-chain", Hook: "input", Policy: "accept", Status: "unknown"},
		{Family: "inet", Table: "edge", Chain: "pre", Hook: "prerouting", Policy: "accept", Status: "checked"},
		{Family: "inet", Table: "edge", Chain: "out", Hook: "output", Policy: "drop", Status: "blocked"},
	}}})
	nft := unknownByID(p, "unknown.nftables")
	if nft == nil || len(nft.Subjects) != 1 || !strings.Contains(nft.Subjects[0], "inet crowdsec crowdsec-chain") {
		t.Fatalf("only a foreign chain with a decision this check does not evaluate is unknown: %+v", nft)
	}
	if baseline := Assess(AssessInput{Policy: &PolicyCoverage{}}); len(p.Findings) != len(baseline.Findings) || p.Status != baseline.Status {
		t.Fatalf("an unknown layer is not a finding: %+v against %+v", p.Findings, baseline.Findings)
	}
	p = Assess(AssessInput{Policy: &PolicyCoverage{Error: "nft printed its ruleset in a form this dashboard could not read"}})
	if nft := unknownByID(p, "unknown.nftables"); nft == nil || !strings.Contains(nft.Detail, "could not read") {
		t.Fatalf("an unreadable ruleset: %+v", nft)
	}
}
