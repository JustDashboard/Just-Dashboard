package ghx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListIssuesAddressesTheRepositoryAndFolds(t *testing.T) {
	s, calls := recordingService(t, `[{"number":3,"title":"Crash","url":"https://github.com/o/r/issues/3","state":"OPEN",
	  "author":{"login":"ann"},"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-02T00:00:00Z",
	  "comments":[{},{}],"labels":[{"name":"bug"}],"assignees":[{"login":"bob"},{"login":""}]}]`)
	issues, err := s.ListIssues(context.Background(), "", "Wayy01/lampino", "open", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v", issues)
	}
	i := issues[0]
	if i.Number != 3 || i.State != "open" || i.Author != "ann" || i.Comments != 2 ||
		strings.Join(i.Labels, ",") != "bug" || strings.Join(i.Assignees, ",") != "bob" {
		t.Fatalf("fold = %+v", i)
	}
	want := "issue list --repo Wayy01/lampino --state open --limit 30 --json " + issueFields
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
	// gh pr list knows "merged"; gh issue list does not, and passing it
	// through would be a gh usage error dressed as a GitHub failure.
	for _, bad := range []struct{ repo, state string }{
		{"Wayy01/lampino", "merged"}, {"Wayy01/lampino", ""}, {"owner/repo#1", "open"},
	} {
		if _, err := s.ListIssues(context.Background(), "", bad.repo, bad.state, 10); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("a refused call still reached gh: %v", *calls)
	}
}

// The comment is prose the operator typed. It must reach gh on stdin, so a
// body starting with a dash or holding a shell construct is just text.
func TestCommentIssueSendsTheBodyThroughStdin(t *testing.T) {
	var argv []string
	var stdin string
	s := New()
	s.command = func(_ context.Context, dir, input string, args ...string) (string, error) {
		if dir != "" {
			t.Fatalf("ran in a checkout: %q", dir)
		}
		argv, stdin = append([]string{}, args...), input
		return `{"id":1}`, nil
	}
	body := "  --delete-branch $(rm -rf /) `x`\n"
	if err := s.CommentIssue(context.Background(), "", "Wayy01/lampino", 12, body); err != nil {
		t.Fatal(err)
	}
	want := "api --hostname github.com repos/Wayy01/lampino/issues/12/comments --method POST --input -"
	if got := strings.Join(argv, " "); got != want {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(stdin), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["body"] != strings.TrimSpace(body) {
		t.Fatalf("body = %q", payload["body"])
	}
	argv = nil
	for _, bad := range []struct {
		repo string
		n    int
		body string
	}{
		{"Wayy01/lampino", 12, "   \n"},
		{"Wayy01/lampino", 12, strings.Repeat("x", 60001)},
		{"Wayy01/lampino", 0, "hello"},
		{"Wayy01/lampino/../x", 12, "hello"},
	} {
		if err := s.CommentIssue(context.Background(), "", bad.repo, bad.n, bad.body); err == nil {
			t.Errorf("accepted number %d body %q in %q", bad.n, bad.body[:min(len(bad.body), 10)], bad.repo)
		}
	}
	if argv != nil {
		t.Fatalf("a refused comment still reached gh: %v", argv)
	}
}
