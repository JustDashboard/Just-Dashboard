package ghx

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Check runs and commit statuses arrive from two endpoints in two
// vocabularies; the page draws one list, failures first, with only links a
// browser should open.
func TestPullChecksFoldsRunsAndStatuses(t *testing.T) {
	var endpoints []string
	s := New()
	s.command = func(_ context.Context, dir, input string, args ...string) (string, error) {
		if dir != "" || input != "" {
			t.Fatalf("dir %q stdin %q", dir, input)
		}
		if len(args) != 4 || args[0] != "api" || args[1] != "--hostname" || args[2] != "github.com" {
			t.Fatalf("argv = %v", args)
		}
		endpoints = append(endpoints, args[3])
		switch {
		case strings.HasSuffix(args[3], "/check-runs?per_page=100"):
			return `{"total_count":3,"check_runs":[
			  {"name":"build","status":"completed","conclusion":"success","html_url":"https://github.com/o/r/actions/runs/1/job/2","started_at":"2026-09-25T00:00:00Z","completed_at":"2026-09-25T00:01:00Z","app":{"name":"GitHub Actions","slug":"github-actions"}},
			  {"name":"lint","status":"completed","conclusion":"failure","html_url":"","details_url":"javascript:alert(1)","app":{"slug":"lintbot"}},
			  {"name":"deploy","status":"waiting","conclusion":null,"details_url":"https://ci.example/deploy/9","app":{"name":"CI"}}]}`, nil
		case strings.HasSuffix(args[3], "/status?per_page=100"):
			return `{"state":"pending","statuses":[
			  {"context":"vercel","state":"pending","target_url":"https://vercel.example/x","created_at":"2026-09-25T00:00:00Z","updated_at":"2026-09-25T00:00:30Z","creator":{"login":"vercel[bot]"}},
			  {"context":"coverage","state":"error","target_url":"/relative","created_at":"2026-09-25T00:00:00Z","updated_at":"2026-09-25T00:00:30Z"},
			  {"context":"a-first","state":"success","target_url":"https://cov.example/1"}]}`, nil
		}
		return "", fmt.Errorf("unexpected endpoint %s", args[3])
	}
	checks, err := s.PullChecks(context.Background(), "", "Wayy01/lampino", reviewSHA)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"repos/Wayy01/lampino/commits/" + reviewSHA + "/check-runs?per_page=100",
		"repos/Wayy01/lampino/commits/" + reviewSHA + "/status?per_page=100",
	}
	if strings.Join(endpoints, " ") != strings.Join(want, " ") {
		t.Fatalf("endpoints = %v", endpoints)
	}
	var order []string
	for _, c := range checks {
		order = append(order, c.Name+":"+c.Status+"/"+c.Conclusion)
	}
	if got := strings.Join(order, " "); got != "coverage:completed/failure lint:completed/failure deploy:in_progress/ vercel:in_progress/pending a-first:completed/success build:completed/success" {
		t.Fatalf("order = %s", got)
	}
	byName := map[string]CheckRun{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	if byName["lint"].URL != "" || byName["coverage"].URL != "" {
		t.Fatalf("kept a link a browser must not open: %q %q", byName["lint"].URL, byName["coverage"].URL)
	}
	if byName["deploy"].URL != "https://ci.example/deploy/9" || byName["vercel"].URL != "https://vercel.example/x" {
		t.Fatalf("dropped a third-party https link: %+v %+v", byName["deploy"], byName["vercel"])
	}
	if byName["build"].App != "GitHub Actions" || byName["lint"].App != "lintbot" || byName["vercel"].App != "vercel[bot]" {
		t.Fatalf("app names: %q %q %q", byName["build"].App, byName["lint"].App, byName["vercel"].App)
	}
	if byName["build"].StartedAt == nil || byName["build"].CompletedAt == nil || byName["vercel"].CompletedAt != nil || byName["coverage"].CompletedAt == nil {
		t.Fatalf("timestamps: build %+v vercel %+v coverage %+v", byName["build"], byName["vercel"], byName["coverage"])
	}
}

// The SHA and the repository are interpolated into a REST path, so anything
// that is not exactly a full SHA in exactly owner/name stops before gh.
func TestPullChecksRefusesBadInput(t *testing.T) {
	s, calls := recordingService(t, `{}`)
	for _, bad := range []struct{ repo, sha string }{
		{"Wayy01/lampino", "main"},
		{"Wayy01/lampino", reviewSHA[:39]},
		{"Wayy01/lampino", reviewSHA + "/../x"},
		{"Wayy01/lampino?x", reviewSHA},
		{"", reviewSHA},
	} {
		if _, err := s.PullChecks(context.Background(), "", bad.repo, bad.sha); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused read still reached gh: %v", *calls)
	}
}
