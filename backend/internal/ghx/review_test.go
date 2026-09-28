package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

const reviewSHA = "0123456789012345678901234567890123456789"

func reviewService(t *testing.T, api func(string, string) (string, error)) *Service {
	t.Helper()
	s := New()
	s.command = func(_ context.Context, dir, input string, args ...string) (string, error) {
		if dir != "checkout" {
			t.Fatalf("lost checkout identity: %q", dir)
		}
		if args[0] == "repo" {
			return `{"nameWithOwner":"owner/repo","url":"https://github.example/owner/repo"}`, nil
		}
		if args[0] == "pr" {
			return `{"number":7,"state":"OPEN","headRefOid":"` + reviewSHA + `","baseRefOid":"` + reviewSHA + `","changedFiles":101}`, nil
		}
		if args[0] == "api" {
			if args[2] != "github.example" {
				t.Fatal("lost Enterprise hostname")
			}
			return api(args[3], input)
		}
		return "", fmt.Errorf("unexpected command %v", args)
	}
	return s
}
func TestReviewPinsCommitAndSendsBodyThroughStdin(t *testing.T) {
	calls := 0
	s := reviewService(t, func(endpoint, input string) (string, error) {
		calls++
		if endpoint != "repos/owner/repo/pulls/7/reviews" {
			t.Fatal(endpoint)
		}
		var payload map[string]string
		if err := json.Unmarshal([]byte(input), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["commit_id"] != reviewSHA || payload["body"] != "$(never execute)\n`literal`" || payload["event"] != "REQUEST_CHANGES" {
			t.Fatal(payload)
		}
		return `{}`, nil
	})
	err := s.ReviewPull(context.Background(), "checkout", 7, ReviewRequest{HeadSHA: reviewSHA, Event: "REQUEST_CHANGES", Body: "$(never execute)\n`literal`"})
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []ReviewRequest{{HeadSHA: strings.Repeat("a", 40), Event: "APPROVE"}, {HeadSHA: reviewSHA, Event: "EXEC"}, {HeadSHA: reviewSHA, Event: "COMMENT"}} {
		if s.ReviewPull(context.Background(), "checkout", 7, req) == nil {
			t.Fatal("accepted unsafe/stale review")
		}
	}
	if calls != 1 {
		t.Fatalf("posted %d reviews", calls)
	}
}
func TestPullFilePaginationAndMissingPatch(t *testing.T) {
	s := reviewService(t, func(endpoint, input string) (string, error) {
		if !strings.HasSuffix(endpoint, "files?per_page=100&page=1") {
			t.Fatal(endpoint)
		}
		return `[{"filename":"image.png","status":"added","additions":0,"deletions":0}]`, nil
	})
	result, err := s.PullFiles(context.Background(), "checkout", 7, 1, reviewSHA)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasMore || result.Limited || result.Files[0].Patch != "" {
		t.Fatal(result)
	}
	if _, err := s.PullFiles(context.Background(), "checkout", 7, 31, reviewSHA); err == nil {
		t.Fatal("allowed unsupported page")
	}
}
func TestRunLogsValidateMembershipAndFilterSteps(t *testing.T) {
	s := New()
	calls := 0
	s.command = func(_ context.Context, dir, input string, args ...string) (string, error) {
		if args[len(args)-2] == "--json" {
			return `{"jobs":[{"databaseId":2,"steps":[{"name":"Test","number":1}]}]}`, nil
		}
		calls++
		if strings.Join(args, " ") != "run view 1 --job 2 --log" {
			t.Fatal(args)
		}
		return "Build\tCheckout\tnot this\nBuild\tTest\tassertion failed\n", nil
	}
	out, err := s.RunLog(context.Background(), "checkout", 1, 2, "Test", false)
	if err != nil || out != "assertion failed" {
		t.Fatal(out, err)
	}
	if _, err := s.RunLog(context.Background(), "checkout", 1, 3, "", false); err == nil {
		t.Fatal("read foreign job")
	}
	if _, err := s.RunLog(context.Background(), "checkout", 1, 2, "Other", false); err == nil {
		t.Fatal("read unknown step")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestBoundedGitHubOutput(t *testing.T) {
	var out boundedOutput
	body := strings.Repeat("x", 9<<20)
	n, err := out.Write([]byte(body))
	if err != nil || n != len(body) || out.Len() != 8<<20 || !out.overflow {
		t.Fatal(n, err, out.Len())
	}
	var copied boundedOutput
	if _, err := io.Copy(&copied, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if copied.Len() != 8<<20 || !copied.overflow {
		t.Fatal("stream copy bypassed output limit")
	}
}
