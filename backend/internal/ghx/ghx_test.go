package ghx

import "testing"

// `gh auth status` has no --json and is written for a person to read, so the
// wording is the contract whether GitHub likes it or not. These are the two
// shapes gh has printed across the versions a server is likely to carry —
// Debian's and a current release — and the sign-in state of the whole page
// hangs off matching both.
func TestParseAuthStatusReadsBothWordings(t *testing.T) {
	current := `github.com
  ✓ Logged in to github.com account Wayy01 (keyring)
  - Active account: true
  - Git operations protocol: https
  - Token: gho_************************************
  - Token scopes: 'gist', 'read:org', 'repo', 'workflow'
`
	older := `github.com
  ✓ Logged in to github.com as Wayy01 (oauth_token)
  ✓ Git operations for github.com configured to use ssh protocol.
  ✓ Token: *******************
  ✓ Token scopes: gist, read:org, repo
`
	for _, tc := range []struct {
		name, out, protocol string
		scopes              int
	}{
		{"current", current, "https", 4},
		{"older", older, "ssh", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := parseAuthStatus(tc.out)
			if !acc.LoggedIn {
				t.Fatal("did not read a signed-in account")
			}
			if acc.Host != "github.com" || acc.Login != "Wayy01" {
				t.Fatalf("host/login = %q/%q", acc.Host, acc.Login)
			}
			if acc.Protocol != tc.protocol {
				t.Errorf("protocol = %q, want %q", acc.Protocol, tc.protocol)
			}
			if len(acc.Scopes) != tc.scopes {
				t.Errorf("scopes = %v, want %d of them", acc.Scopes, tc.scopes)
			}
		})
	}
}

// Nobody signed in must not read as somebody signed in — the failure would be
// a page offering to push with credentials that do not exist.
func TestParseAuthStatusWhenSignedOut(t *testing.T) {
	acc := parseAuthStatus("You are not logged into any GitHub hosts. To log in, run: gh auth login\n")
	if acc.LoggedIn {
		t.Fatal("read a signed-in account out of a signed-out status")
	}
	if got := firstMeaningfulLine("You are not logged into any GitHub hosts. Run gh auth login\n"); got == "" {
		t.Error("no reason to show the operator")
	}
}

func TestCreatePullReadsTheURLGhPrints(t *testing.T) {
	out := "Warning: 3 uncommitted changes\nhttps://github.com/Wayy01/Just-Dashboard/pull/19\n"
	if got := lastURL(out); got != "https://github.com/Wayy01/Just-Dashboard/pull/19" {
		t.Fatalf("lastURL = %q", got)
	}
}

// A branch name reaches gh as an argument. The leading-dash case is the one
// that matters: it is what stops a branch from being read as an option.
func TestValidateBranchRefusesAnOption(t *testing.T) {
	for _, bad := range []string{"", "--force", "a b", "../etc", "feat;rm"} {
		if err := validateBranch(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, good := range []string{"main", "fix/detect-container-ip", "release-0.5.8", "user@host"} {
		if err := validateBranch(good); err != nil {
			t.Errorf("refused %q: %v", good, err)
		}
	}
}

// The status rollup mixes GitHub's two check shapes, and one failure has to
// outrank any number of passes.
func TestPullRequestFoldsChecksAndReview(t *testing.T) {
	var p ghPull
	p.ReviewDecision = "APPROVED"
	p.Rollup = []struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}{
		{State: "SUCCESS"},
		{Status: "COMPLETED", Conclusion: "SUCCESS"},
	}
	pr := p.pullRequest()
	if pr.Checks != "success" || pr.Review != "approved" {
		t.Fatalf("all passing = checks %q review %q", pr.Checks, pr.Review)
	}
	p.Rollup = append(p.Rollup, struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}{Status: "IN_PROGRESS"})
	if pr := p.pullRequest(); pr.Checks != "pending" {
		t.Fatalf("one pending among passes = %q, want pending", pr.Checks)
	}
	p.Rollup = append(p.Rollup, struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}{Status: "COMPLETED", Conclusion: "FAILURE"})
	if pr := p.pullRequest(); pr.Checks != "failure" {
		t.Fatalf("one failure among the rest = %q, want failure", pr.Checks)
	}
	if pr := (ghPull{}).pullRequest(); pr.Checks != "" || pr.Review != "" {
		t.Fatalf("no checks and no review should be empty, got %q / %q", pr.Checks, pr.Review)
	}
}
