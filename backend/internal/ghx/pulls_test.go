package ghx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// recordingService answers every gh call with reply and keeps the argv, so a
// test can say exactly what would have reached the binary.
func recordingService(t *testing.T, reply string) (*Service, *[][]string) {
	t.Helper()
	calls := &[][]string{}
	s := New()
	s.command = func(_ context.Context, dir, input string, args ...string) (string, error) {
		if dir != "" {
			t.Fatalf("a repository-addressed call ran in a checkout: %q", dir)
		}
		*calls = append(*calls, append([]string{}, args...))
		return reply, nil
	}
	return s, calls
}

// The list is where the "Test this pull request" button lives, so it has to
// carry what that decision needs: the exact head commit, whether the head is
// a fork, and the labels and merge state the row draws.
func TestPullRequestFoldsHeadForkLabelsAndMerged(t *testing.T) {
	raw := `{"number":9,"title":"Fix","url":"https://github.com/o/r/pull/9","state":"MERGED",
	  "headRefName":"fix","baseRefName":"main","author":{"login":"ann"},
	  "headRefOid":"` + reviewSHA + `","headRepository":{"name":"r-fork"},"headRepositoryOwner":{"login":"bob"},
	  "isCrossRepository":true,"updatedAt":"2026-09-25T00:29:18Z",
	  "labels":[{"name":"bug"},{"name":""},{"name":"ready"}],"mergedAt":"2026-09-25T01:00:00Z"}`
	var p ghPull
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	pr := p.pullRequest()
	if pr.HeadSHA != reviewSHA || pr.HeadRepository != "bob/r-fork" || !pr.Fork || !pr.Merged {
		t.Fatalf("fold = %+v", pr)
	}
	if strings.Join(pr.Labels, ",") != "bug,ready" || pr.UpdatedAt.IsZero() || pr.State != "merged" {
		t.Fatalf("fold = %+v", pr)
	}

	// A same-repository, still open request: not a fork, not merged, and a
	// deleted head repository leaves the name empty rather than "owner/".
	raw = `{"number":10,"state":"OPEN","headRepository":null,"headRepositoryOwner":{"login":"o"},"mergedAt":null}`
	p = ghPull{}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if pr := p.pullRequest(); pr.Fork || pr.Merged || pr.HeadRepository != "" || pr.Labels != nil {
		t.Fatalf("fold = %+v", pr)
	}
}

func TestListPullsInAddressesTheRepository(t *testing.T) {
	s, calls := recordingService(t, `[{"number":1,"state":"OPEN","headRefOid":"`+reviewSHA+`"}]`)
	pulls, err := s.ListPullsIn(context.Background(), "", "Wayy01/lampino", "open", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 1 || pulls[0].HeadSHA != reviewSHA {
		t.Fatalf("pulls = %+v", pulls)
	}
	want := "pr list --repo Wayy01/lampino --state open --limit 100 --json " + pullFields
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
	if !strings.Contains(pullFields, "headRefOid") || !strings.Contains(pullFields, "isCrossRepository") {
		t.Fatalf("the list does not ask for the head commit and fork-ness: %s", pullFields)
	}
	for _, bad := range []struct{ repo, state string }{
		{"owner/repo/extra", "open"}, {"owner/repo?x=1", "open"}, {"Wayy01/lampino", "OPEN"}, {"Wayy01/lampino", "--all"},
	} {
		if _, err := s.ListPullsIn(context.Background(), "", bad.repo, bad.state, 10); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("a refused call still reached gh: %v", *calls)
	}
}

func TestViewPullInAddressesTheRepository(t *testing.T) {
	s, calls := recordingService(t, `{"number":7,"state":"OPEN","mergeable":"MERGEABLE","headRefOid":"`+reviewSHA+`"}`)
	pr, err := s.ViewPullIn(context.Background(), "", "Wayy01/lampino", 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || pr.Mergeable != "mergeable" || pr.HeadSHA != reviewSHA {
		t.Fatalf("pull = %+v", pr)
	}
	want := "pr view 7 --repo Wayy01/lampino --json " + pullDetailFields
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
	if _, err := s.ViewPullIn(context.Background(), "", "Wayy01/lampino", 0); err == nil {
		t.Fatal("accepted request number 0")
	}
	if _, err := s.ViewPullIn(context.Background(), "", "../lampino", 7); err == nil {
		t.Fatal("accepted a path as a repository")
	}
}

// Merging pinned to a head commit is what makes "merge what I reviewed" true:
// the flag reaches gh only with a full SHA, and never with anything else.
func TestMergePullPinsTheHeadCommit(t *testing.T) {
	s, calls := recordingService(t, "")
	ctx := context.Background()
	if err := s.MergePullIn(ctx, "", "Wayy01/lampino", 7, "squash", true, reviewSHA); err != nil {
		t.Fatal(err)
	}
	if err := s.MergePull(ctx, "", 8, "", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MergePull(ctx, "", 9, "rebase", false, reviewSHA); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pr merge 7 --repo Wayy01/lampino --squash --delete-branch --match-head-commit " + reviewSHA,
		"pr merge 8 --merge",
		"pr merge 9 --rebase --match-head-commit " + reviewSHA,
	}
	for i, w := range want {
		if got := strings.Join((*calls)[i], " "); got != w {
			t.Errorf("call %d argv = %q\nwant %q", i, got, w)
		}
	}
	before := len(*calls)
	for _, bad := range []struct {
		repo, method, sha string
	}{
		{"Wayy01/lampino", "fast-forward", ""},
		{"Wayy01/lampino", "merge", "abc123"},
		{"Wayy01/lampino", "merge", "--match-head-commit"},
		{"Wayy01/lampino/x", "merge", ""},
	} {
		if err := s.MergePullIn(ctx, "", bad.repo, 7, bad.method, false, bad.sha); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := s.MergePull(ctx, "", 7, "merge", false, "not-a-sha"); err == nil {
		t.Error("MergePull accepted a partial SHA")
	}
	if len(*calls) != before {
		t.Fatalf("a refused merge still reached gh: %v", (*calls)[before:])
	}
}
