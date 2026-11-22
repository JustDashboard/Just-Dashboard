package githubrepos

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCommitsMapAuthorsAndTreatAnEmptyRepositoryAsNoHistory(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/commits?page=1&per_page=30&sha=dev", 200, `[
		{"sha":"`+sha40+`","html_url":"https://github.com/octo-org/widgets/commit/x",
		 "commit":{"message":"Fix it\n\nBecause.","author":{"name":"Mona","date":"2026-11-01T10:00:00Z"},
		           "committer":{"name":"GitHub","date":"2026-11-01T10:05:00Z"},"verification":{"verified":true}},
		 "author":{"login":"mona"},"committer":{"login":"web-flow"},"parents":[{},{}]},
		{"sha":"`+sha40+`","commit":{"message":"Unmatched","author":{"name":"Someone"}},"author":null,"parents":[{}]}]`)
	page, err := testAccess(t, fake).Commits(context.Background(), "dev", 0)
	if err != nil {
		t.Fatal(err)
	}
	first := page.Commits[0]
	if first.Message != "Fix it\n\nBecause." || first.Author.Name != "Mona" || first.Author.Login != "mona" ||
		first.Author.Date.IsZero() || first.Committer.Login != "web-flow" || first.Verified == nil || !*first.Verified || first.Parents != 2 {
		t.Fatalf("first = %+v", first)
	}
	second := page.Commits[1]
	if second.Author.Login != "" || second.Verified != nil || second.Parents != 1 {
		t.Fatalf("second = %+v", second)
	}
	raw, _ := json.Marshal(second)
	if strings.Contains(string(raw), "0001-01-01") || strings.Contains(string(raw), `"verified"`) {
		t.Fatalf("second as JSON = %s", raw)
	}

	empty := newFake(t)
	empty.json("GET /repos/octo-org/widgets/commits?page=1&per_page=30", 409, `{"message":"Git Repository is empty."}`)
	page, err = testAccess(t, empty).Commits(context.Background(), "", 1)
	if err != nil || len(page.Commits) != 0 || page.HasMore {
		t.Fatalf("empty = %+v %v", page, err)
	}
	_, err = testAccess(t, newFake(t)).Commits(context.Background(), "a..b", 1)
	wantInvalid(t, err)
}

func TestPullsSplitClosedFromMerged(t *testing.T) {
	list := `[
		{"number":1,"title":"Merged","state":"closed","merged_at":"2026-11-02T00:00:00Z","user":{"login":"mona"},
		 "head":{"ref":"a","sha":"` + sha40 + `","repo":{"full_name":"octo-org/widgets"}},"base":{"ref":"main","repo":{"full_name":"octo-org/widgets"}},
		 "labels":[{"name":"bug","color":"d73a4a","description":null}]},
		{"number":2,"title":"Closed","state":"closed","merged_at":null,"user":{"login":"hubot"},
		 "head":{"ref":"b","sha":"` + sha40 + `","repo":{"full_name":"someone/widgets"}},"base":{"ref":"main","repo":{"full_name":"octo-org/widgets"}}}]`
	for state, want := range map[string]int64{"merged": 1, "closed": 2} {
		fake := newFake(t)
		fake.json("GET /repos/octo-org/widgets/pulls?page=1&per_page=30&state=closed", 200, list)
		page, err := testAccess(t, fake).Pulls(context.Background(), state, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Pulls) != 1 || page.Pulls[0].Number != want || page.Pulls[0].State != state {
			t.Fatalf("%s = %+v", state, page.Pulls)
		}
	}
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/pulls?page=1&per_page=30&state=all", 200, list)
	page, err := testAccess(t, fake).Pulls(context.Background(), "all", 1)
	if err != nil || len(page.Pulls) != 2 {
		t.Fatalf("all = %+v %v", page, err)
	}
	if page.Pulls[0].Fork || !page.Pulls[1].Fork || page.Pulls[0].Labels[0].Name != "bug" || page.Pulls[1].Labels == nil {
		t.Fatalf("pulls = %+v", page.Pulls)
	}
	_, err = testAccess(t, newFake(t)).Pulls(context.Background(), "draft", 1)
	wantInvalid(t, err)
}

func TestPullDetailFoldsReviewsChecksAndMergeMethods(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/pulls/5", 200, `{"number":5,"title":"T","state":"open","body":"Text",
		"mergeable":null,"mergeable_state":"unknown","additions":3,"deletions":1,"changed_files":2,"commits":1,
		"user":{"login":"mona"},"head":{"ref":"a","sha":"`+sha40+`","repo":{"full_name":"octo-org/widgets"}},
		"base":{"ref":"main","repo":{"full_name":"octo-org/widgets"}}}`)
	fake.json("GET /repos/octo-org/widgets/pulls/5/reviews?per_page=100", 200, `[
		{"id":1,"user":{"login":"ann"},"state":"CHANGES_REQUESTED"},
		{"id":2,"user":{"login":"bob"},"state":"COMMENTED"},
		{"id":3,"user":{"login":"ann"},"state":"APPROVED"},
		{"id":4,"user":{"login":"ann"},"state":"COMMENTED"}]`)
	fake.json("GET /repos/octo-org/widgets/commits/"+sha40+"/check-runs?per_page=100", 200, `{"check_runs":[
		{"name":"build","status":"completed","conclusion":"success","html_url":"https://github.com/x"},
		{"name":"lint","status":"completed","conclusion":"failure","details_url":"javascript:alert(1)"},
		{"name":"e2e","status":"waiting"}]}`)
	fake.json("GET /repos/octo-org/widgets/commits/"+sha40+"/status?per_page=100", 200, `{"statuses":[
		{"context":"ci/legacy","state":"pending","target_url":"https://ci.example.test/1"},
		{"context":"ci/old","state":"error"}]}`)
	fake.json("GET /repos/octo-org/widgets", 200, `{"allow_merge_commit":false,"allow_squash_merge":true,"allow_rebase_merge":true}`)
	got, err := testAccess(t, fake).Pull(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mergeable != nil || got.Body != "Text" || got.Additions != 3 || got.ChangedFiles != 2 {
		t.Fatalf("pull = %+v", got)
	}
	if len(got.Reviews) != 2 || got.Reviews[0].Author != "bob" || got.Reviews[1].Author != "ann" || got.Reviews[1].State != "APPROVED" {
		t.Fatalf("reviews = %+v", got.Reviews)
	}
	names := []string{}
	for _, check := range got.Checks {
		names = append(names, check.Name+":"+check.Status+":"+check.Source)
	}
	if strings.Join(names, " ") != "ci/old:completed:status lint:completed:check ci/legacy:pending:status e2e:in_progress:check build:completed:check" {
		t.Fatalf("checks = %v", names)
	}
	if got.Checks[1].DetailsURL != "" || !got.ChecksReadable {
		t.Fatalf("lint = %+v", got.Checks[1])
	}
	if strings.Join(got.MergeMethods, ",") != "squash,rebase" {
		t.Fatalf("methods = %v", got.MergeMethods)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"mergeable":null`) {
		t.Fatalf("mergeable not null: %s", raw)
	}
}

func TestPullDetailKeepsStatusesWhenCheckRunsAreRefused(t *testing.T) {
	fake := newFake(t)
	fake.json("GET /repos/octo-org/widgets/pulls/5", 200, `{"number":5,"state":"open","mergeable":true,"head":{"ref":"a","sha":"`+sha40+`"},"base":{"ref":"main"}}`)
	fake.json("GET /repos/octo-org/widgets/pulls/5/reviews?per_page=100", 200, `[]`)
	fake.json("GET /repos/octo-org/widgets/commits/"+sha40+"/check-runs?per_page=100", 403, `{"message":"Resource not accessible by integration"}`)
	fake.json("GET /repos/octo-org/widgets/commits/"+sha40+"/status?per_page=100", 200, `{"statuses":[{"context":"ci","state":"success"}]}`)
	fake.json("GET /repos/octo-org/widgets", 200, `{}`)
	got, err := testAccess(t, fake).Pull(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChecksReadable || !strings.Contains(got.ChecksMessage, "check runs") || len(got.Checks) != 1 || got.Checks[0].Conclusion != "success" {
		t.Fatalf("checks = %+v %q", got.Checks, got.ChecksMessage)
	}
	if got.Mergeable == nil || !*got.Mergeable || len(got.MergeMethods) != 3 {
		t.Fatalf("pull = %+v", got)
	}
}

func TestMergeSendsTheHeadAndRefusesWithoutIt(t *testing.T) {
	fake := newFake(t)
	fake.json("PUT /repos/octo-org/widgets/pulls/5/merge", 200, `{"merged":true,"sha":"`+sha40+`","message":"Pull Request successfully merged"}`)
	access := testAccess(t, fake)
	result, err := access.Merge(context.Background(), 5, MergeRequest{Method: "squash", SHA: sha40, Title: "Squash"})
	if err != nil || !result.Merged || result.SHA != sha40 {
		t.Fatalf("merge = %+v %v", result, err)
	}
	if body := fake.body("PUT /repos/octo-org/widgets/pulls/5/merge"); body["sha"] != sha40 || body["merge_method"] != "squash" || body["commit_title"] != "Squash" {
		t.Fatalf("body = %v", body)
	}
	calls := fake.callCount()
	_, err = access.Merge(context.Background(), 5, MergeRequest{Method: "squash"})
	wantInvalid(t, err)
	_, err = access.Merge(context.Background(), 5, MergeRequest{Method: "octopus", SHA: sha40})
	wantInvalid(t, err)
	_, err = withRole(testAccess(t, fake), "triage").Merge(context.Background(), 5, MergeRequest{Method: "merge", SHA: sha40})
	wantNotPermitted(t, err, "needs write")
	if fake.callCount() != calls {
		t.Fatalf("GitHub was asked: %v", fake.calls)
	}
}

func TestMergeConflictsAreConflicts(t *testing.T) {
	for _, status := range []int{405, 409} {
		fake := newFake(t)
		fake.json("PUT /repos/octo-org/widgets/pulls/5/merge", status, `{"message":"Head branch was modified"}`)
		_, err := testAccess(t, fake).Merge(context.Background(), 5, MergeRequest{Method: "merge", SHA: sha40})
		if KindOf(err) != KindConflict {
			t.Fatalf("%d = %v", status, err)
		}
	}
}

func TestSetPullState(t *testing.T) {
	fake := newFake(t)
	fake.json("PATCH /repos/octo-org/widgets/pulls/5", 200, `{"number":5,"state":"closed","head":{"ref":"a"},"base":{"ref":"main"}}`)
	pull, err := testAccess(t, fake).SetPullState(context.Background(), 5, "closed")
	if err != nil || pull.State != "closed" {
		t.Fatalf("pull = %+v %v", pull, err)
	}
	_, err = testAccess(t, fake).SetPullState(context.Background(), 5, "merged")
	wantInvalid(t, err)
	_, err = withRole(testAccess(t, fake), "read").SetPullState(context.Background(), 5, "open")
	wantNotPermitted(t, err, "needs triage")
}
