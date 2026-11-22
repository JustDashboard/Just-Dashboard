package githubrepos

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIssuesLeaveOutPullRequests(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/issues?labels=bug%2Cgood+first+issue&page=1&per_page=30&state=all", 200, `[
		{"number":1,"title":"Broken","state":"closed","state_reason":"not_planned","user":{"login":"mona"},
		 "labels":[{"name":"bug","color":"d73a4a"}],"comments":2,"assignees":[{"login":"hubot"}],"body":"secret"},
		{"number":2,"title":"A pull","state":"open","pull_request":{"url":"x"}}]`)
	page, err := testAccess(t, fake).Issues(context.Background(), "all", []string{"bug", "good first issue"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Issues) != 1 {
		t.Fatalf("issues = %+v", page.Issues)
	}
	issue := page.Issues[0]
	if issue.StateReason != "not_planned" || issue.Assignees[0] != "hubot" || issue.Comments != 2 || issue.Body != nil {
		t.Fatalf("issue = %+v", issue)
	}
	_, err = testAccess(t, newFake(t)).Issues(context.Background(), "merged", nil, 1)
	wantInvalid(t, err)
	_, err = testAccess(t, newFake(t)).Issues(context.Background(), "open", []string{"a,b"}, 1)
	wantInvalid(t, err)
}

func TestAnIssueNumberThatIsAPullRequestIsNotFound(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/issues/3", 200, `{"number":3,"pull_request":{"url":"x"}}`)
	fake.json("GET /repos/octo-org/widgets/issues/4", 200, `{"number":4,"title":"Real","state":"open","body":null}`)
	_, err := testAccess(t, fake).Issue(context.Background(), 3)
	if KindOf(err) != KindNotFound {
		t.Fatalf("pull as issue = %v", err)
	}
	issue, err := testAccess(t, fake).Issue(context.Background(), 4)
	if err != nil || issue.Body == nil || *issue.Body != "" {
		t.Fatalf("issue = %+v %v", issue, err)
	}
	_, err = testAccess(t, fake).UpdateIssue(context.Background(), 3, IssueUpdate{Title: ptr("x")})
	if KindOf(err) != KindNotFound {
		t.Fatalf("update of a pull = %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestCreateIssueChecksTitleBodyAndLabelRights(t *testing.T) {
	fake := newFake(t)
	fake.json("POST /repos/octo-org/widgets/issues", 201, `{"number":9,"title":"New","state":"open"}`)
	issue, err := testAccess(t, fake).CreateIssue(context.Background(), IssueCreate{Title: "  New  ", Labels: []string{"bug"}})
	if err != nil || issue.Number != 9 {
		t.Fatalf("issue = %+v %v", issue, err)
	}
	if body := fake.body("POST /repos/octo-org/widgets/issues"); body["title"] != "New" || fmt.Sprint(body["labels"]) != "[bug]" {
		t.Fatalf("body = %v", body)
	}
	calls := fake.callCount()
	access := testAccess(t, fake)
	for _, bad := range []IssueCreate{
		{Title: " "},
		{Title: strings.Repeat("x", 257)},
		{Title: "ok", Body: strings.Repeat("x", 65537)},
		{Title: "ok", Labels: []string{strings.Repeat("l", 51)}},
	} {
		_, err := access.CreateIssue(context.Background(), bad)
		wantInvalid(t, err)
	}
	// A reader may open an issue but not label it.
	_, err = withRole(testAccess(t, fake), "read").CreateIssue(context.Background(), IssueCreate{Title: "x", Labels: []string{"bug"}})
	wantNotPermitted(t, err, "needs triage")
	if fake.callCount() != calls {
		t.Fatalf("GitHub was asked: %v", fake.calls)
	}
	reader := withRole(testAccess(t, fake), "read")
	if _, err := reader.CreateIssue(context.Background(), IssueCreate{Title: "x"}); err != nil {
		t.Fatalf("a reader opening an issue: %v", err)
	}
	// Issues switched off on the repository.
	off := testAccess(t, fake)
	off.Repository.Can = ComputeAbilities(off.Repository, off.Installation, false)
	_, err = off.CreateIssue(context.Background(), IssueCreate{Title: "x"})
	wantNotPermitted(t, err, "switched off")
}

func TestUpdateIssueSendsOnlyWhatChanged(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/issues/4", 200, `{"number":4,"title":"Old","state":"open"}`)
	fake.json("PATCH /repos/octo-org/widgets/issues/4", 200, `{"number":4,"title":"Old","state":"closed","state_reason":"completed"}`)
	issue, err := testAccess(t, fake).UpdateIssue(context.Background(), 4, IssueUpdate{State: ptr("closed"), StateReason: ptr("completed"), Labels: &[]string{}})
	if err != nil || issue.State != "closed" || issue.StateReason != "completed" {
		t.Fatalf("issue = %+v %v", issue, err)
	}
	body := fake.body("PATCH /repos/octo-org/widgets/issues/4")
	if len(body) != 3 || body["state"] != "closed" || body["state_reason"] != "completed" || fmt.Sprint(body["labels"]) != "[]" {
		t.Fatalf("body = %v", body)
	}
	calls := fake.callCount()
	reader := withRole(testAccess(t, fake), "read")
	_, err = reader.UpdateIssue(context.Background(), 4, IssueUpdate{State: ptr("closed")})
	wantNotPermitted(t, err, "needs triage")
	_, err = testAccess(t, fake).UpdateIssue(context.Background(), 4, IssueUpdate{})
	wantInvalid(t, err)
	_, err = testAccess(t, fake).UpdateIssue(context.Background(), 4, IssueUpdate{StateReason: ptr("duplicate")})
	wantInvalid(t, err)
	if fake.callCount() != calls {
		t.Fatalf("GitHub was asked: %v", fake.calls)
	}
	// The author's own title edit is GitHub's to judge, so it is sent.
	if _, err := reader.UpdateIssue(context.Background(), 4, IssueUpdate{Title: ptr("New")}); err != nil {
		t.Fatalf("a reader editing a title: %v", err)
	}
}

func TestLabelsWalkEveryPage(t *testing.T) {
	fake := newFake(t)
	fake.on("GET /repos/octo-org/widgets/labels?per_page=100", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://api.github.com/repos/octo-org/widgets/labels?per_page=100&page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[{"name":"bug","color":"d73a4a","description":"Something is wrong"}]`))
	})
	fake.json("GET /repos/octo-org/widgets/labels?per_page=100&page=2", 200, `[{"name":"docs","color":"0075ca"}]`)
	labels, err := testAccess(t, fake).Labels(context.Background())
	if err != nil || len(labels) != 2 || labels[0].Description != "Something is wrong" || labels[1].Name != "docs" {
		t.Fatalf("labels = %+v %v", labels, err)
	}
}
