package githubrepos

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const sha40 = "0123456789abcdef0123456789abcdef01234567"

func TestBranchesMarkTheDefaultAndReadTheNextLink(t *testing.T) {
	fake := newFake(t)
	fake.on("GET /repos/octo-org/widgets/branches?page=2&per_page=100", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://api.github.com/repos/octo-org/widgets/branches?page=3&per_page=100>; rel="next"`)
		_, _ = w.Write([]byte(`[{"name":"main","commit":{"sha":"` + sha40 + `"},"protected":true},{"name":"feature/x","commit":{"sha":"` + sha40 + `"},"protected":false}]`))
	})
	page, err := testAccess(t, fake).Branches(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Branches) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if b := page.Branches[0]; !b.Default || !b.Protected || b.SHA != sha40 {
		t.Fatalf("main = %+v", b)
	}
	if page.Branches[1].Default {
		t.Fatalf("feature/x marked default")
	}
}

func TestBranchProtectionSummarisesClassicProtectionAndRules(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/branches/release/1", 200, `{"name":"release/1","protected":true}`)
	fake.json("GET /repos/octo-org/widgets/branches/release/1/protection", 200, `{
		"required_status_checks":{"strict":true,"contexts":["build","test"],"checks":[{"context":"build"}]},
		"enforce_admins":{"enabled":true},
		"required_pull_request_reviews":{"required_approving_review_count":2,"dismiss_stale_reviews":true},
		"required_linear_history":{"enabled":true},
		"allow_force_pushes":{"enabled":false},
		"required_conversation_resolution":{"enabled":true}}`)
	fake.json("GET /repos/octo-org/widgets/rules/branches/release/1?per_page=100", 200, `[
		{"type":"pull_request","ruleset_id":9,"parameters":{"required_approving_review_count":2,"require_code_owner_review":true}},
		{"type":"required_status_checks","ruleset_id":9,"parameters":{"required_status_checks":[{"context":"build"},{"context":"test"}]}},
		{"type":"non_fast_forward","ruleset_id":9},
		{"type":"required_linear_history","ruleset_id":10},
		{"type":"commit_message_pattern","ruleset_id":10,"parameters":{"operator":"starts_with","pattern":"JD-","negate":false}},
		{"type":"some_new_rule","ruleset_id":11}]`)
	fake.json("GET /repos/octo-org/widgets/rulesets?includes_parents=true&per_page=100", 200, `[{"id":9,"name":"Release guard"},{"id":10,"name":"Org policy"}]`)
	got, err := testAccess(t, fake).BranchProtection(context.Background(), "release/1")
	if err != nil {
		t.Fatal(err)
	}
	p := got.Protection
	if !got.Protected || !got.ProtectionReadable || p == nil || p.RequiredReviews != 2 || !p.DismissStaleReviews ||
		strings.Join(p.RequiredChecks, ",") != "build,test" || !p.StrictChecks || !p.EnforceAdmins || !p.LinearHistory ||
		p.AllowForcePushes || !p.ConversationResolution {
		t.Fatalf("protection = %+v %+v", got, p)
	}
	want := []string{
		"Pull request with 2 approvals, code owner review",
		"Required checks: build, test",
		"No force pushes",
		"Linear history",
		`Commit messages must start with "JD-"`,
		"Some new rule",
	}
	if !got.RulesReadable || len(got.Rules) != len(want) {
		t.Fatalf("rules = %+v", got.Rules)
	}
	for i, summary := range want {
		if got.Rules[i].Summary != summary {
			t.Errorf("rule %d = %q, want %q", i, got.Rules[i].Summary, summary)
		}
	}
	if got.Rules[0].RulesetName != "Release guard" || got.Rules[5].RulesetName != "" || got.Rules[5].RulesetID != 11 {
		t.Fatalf("ruleset names = %+v", got.Rules)
	}
}

func TestBranchProtectionANonAdminGetsASentenceNotAnError(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/branches/main", 200, `{"name":"main","protected":true}`)
	fake.json("GET /repos/octo-org/widgets/branches/main/protection", 404, `{"message":"Not Found"}`)
	fake.json("GET /repos/octo-org/widgets/rules/branches/main?per_page=100", 403, `{"message":"Resource not accessible by integration"}`)
	got, err := withRole(testAccess(t, fake), "write").BranchProtection(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProtectionReadable || got.ProtectionMessage == "" || got.Protection != nil || !got.Protected {
		t.Fatalf("protection = %+v", got)
	}
	if got.RulesReadable || !strings.Contains(got.RulesMessage, "Resource not accessible") || got.Rules == nil {
		t.Fatalf("rules = %+v", got)
	}
}

func TestBranchProtectionOfAnUnprotectedBranchAsksNoAdminRead(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/branches/dev", 200, `{"name":"dev","protected":false}`)
	fake.json("GET /repos/octo-org/widgets/rules/branches/dev?per_page=100", 200, `[]`)
	got, err := testAccess(t, fake).BranchProtection(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Protected || !got.ProtectionReadable || !got.RulesReadable || len(got.Rules) != 0 {
		t.Fatalf("protection = %+v", got)
	}
}

func TestCreateBranchFromABranchReadsItsTip(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/git/ref/heads/main", 200, `{"object":{"sha":"`+sha40+`"}}`)
	fake.json("POST /repos/octo-org/widgets/git/refs", 201, `{"ref":"refs/heads/feature/new","object":{"sha":"`+sha40+`"}}`)
	branch, err := testAccess(t, fake).CreateBranch(context.Background(), "feature/new", "main")
	if err != nil {
		t.Fatal(err)
	}
	if branch.Name != "feature/new" || branch.SHA != sha40 || branch.Default {
		t.Fatalf("branch = %+v", branch)
	}
	if body := fake.body("POST /repos/octo-org/widgets/git/refs"); body["ref"] != "refs/heads/feature/new" || body["sha"] != sha40 {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateBranchFromACommitAsksNothingFirst(t *testing.T) {
	fake := newFake(t)
	fake.json("POST /repos/octo-org/widgets/git/refs", 201, `{"ref":"refs/heads/x","object":{"sha":"`+sha40+`"}}`)
	if _, err := testAccess(t, fake).CreateBranch(context.Background(), "x", strings.ToUpper(sha40)); err != nil {
		t.Fatal(err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("calls = %v", fake.calls)
	}
}

func TestBranchChangesAreRefusedBeforeGitHubIsAsked(t *testing.T) {
	fake := newFake(t)
	access := testAccess(t, fake)
	_, err := access.CreateBranch(context.Background(), "refs/heads/x", "main")
	wantInvalid(t, err)
	_, err = access.CreateBranch(context.Background(), "x", "main..dev")
	wantInvalid(t, err)
	wantInvalid(t, access.DeleteBranch(context.Background(), "main"))
	wantInvalid(t, access.DeleteBranch(context.Background(), "a b"))
	reader := withRole(testAccess(t, fake), "triage")
	_, err = reader.CreateBranch(context.Background(), "x", "main")
	wantNotPermitted(t, err, "needs write")
	wantNotPermitted(t, reader.DeleteBranch(context.Background(), "dev"), "needs write")
	if fake.callCount() != 0 {
		t.Fatalf("GitHub was asked: %v", fake.calls)
	}
}

func TestDeleteBranchEscapesTheName(t *testing.T) {
	fake := newFake(t)
	fake.on("DELETE /repos/octo-org/widgets/git/refs/heads/feature/a%23b", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if err := testAccess(t, fake).DeleteBranch(context.Background(), "feature/a#b"); err != nil {
		t.Fatal(err)
	}
}
