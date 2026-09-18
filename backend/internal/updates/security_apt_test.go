package updates

import (
	"strings"
	"testing"
)

func TestSecurityUpgradePinsOnlySecurityCandidates(t *testing.T) {
	packages := []Package{
		{Name: "secure", Current: "1", Candidate: "2", Security: true, Architecture: "amd64"},
		{Name: "ordinary", Current: "1", Candidate: "3", Security: false, Architecture: "amd64"},
	}
	args, err := aptSecurityUpgradeArgs(packages, func(args []string) (string, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "secure:amd64=2") || strings.Contains(joined, "ordinary") || strings.Contains(joined, "-t ") {
			t.Fatalf("unrestricted targets: %s", joined)
		}
		return "Inst secure [1] (2 Debian:stable-security [amd64])\n", nil
	})
	if err != nil || len(args) == 0 {
		t.Fatalf("security plan rejected: %v", err)
	}
	for _, simulation := range []string{
		"Inst ordinary [1] (3 Debian:stable [amd64])",
		"Inst dependency (2 Debian:stable [amd64])",
		"Remv required [1]",
		"Inst secure [1] (3 Debian:stable-security [amd64])",
	} {
		if _, err := aptSecurityUpgradeArgs(packages, func([]string) (string, error) { return simulation, nil }); err == nil {
			t.Errorf("widened simulation accepted: %s", simulation)
		}
	}
}

func TestNoSecurityCandidateNeverFallsBackToFullUpgrade(t *testing.T) {
	_, err := aptSecurityUpgradeArgs([]Package{{Name: "ordinary", Current: "1", Candidate: "2"}}, func([]string) (string, error) {
		t.Fatal("must not build a command for a full upgrade")
		return "", nil
	})
	if err == nil {
		t.Fatal("empty security set accepted")
	}
}
