package deploy

import "testing"

func TestAdoptionFindingAcknowledgementsFollowEvidence(t *testing.T) {
	first := AdoptionIssue{Code: "persistent_data_reused", Message: "Keep a verified backup", Service: "web", Field: "volumes"}
	second := first
	second.Service = "worker"
	adoption := &WorkloadAdoption{Issues: []AdoptionIssue{first, second}, Warnings: []string{"web: Keep a verified backup", "worker: Keep a verified backup"}}
	findings := adoptionPreflightFindings(adoption)
	if len(findings) != 2 || findings[0].Code == findings[1].Code || findings[0].IssueCode != first.Code {
		t.Fatalf("structured warnings duplicated or collapsed: %#v", findings)
	}
	adoption.Issues = []AdoptionIssue{second, first}
	reordered := adoptionPreflightFindings(adoption)
	if reordered[0].Code != findings[1].Code || reordered[1].Code != findings[0].Code {
		t.Fatal("reordering changed evidence acknowledgement identity")
	}
	adoption.Issues[0].Message = "Different backup evidence"
	changed := adoptionPreflightFindings(adoption)
	if changed[0].Code == findings[1].Code {
		t.Fatal("changed evidence reused an acknowledgement")
	}
	legacy := adoptionPreflightFindings(&WorkloadAdoption{Warnings: []string{"Old capture"}, Blockers: []string{"Old blocker"}})
	if len(legacy) != 2 || legacy[0].Code != "adoption_warning_1" || legacy[1].Severity != PreflightBlocked {
		t.Fatalf("old drafts lost their findings: %#v", legacy)
	}
}
