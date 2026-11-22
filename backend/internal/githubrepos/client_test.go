package githubrepos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "ghu_test", nil })
}

func TestRequestSendsTheTokenOnlyInTheHeader(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_test" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-GitHub-Api-Version") == "" || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("headers = %v", r.Header)
		}
		if strings.Contains(r.URL.String(), "ghu_test") {
			t.Errorf("token in URL %s", r.URL)
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		_, _ = w.Write([]byte(`{"login":"octo"}`))
	})
	var out struct{ Login string }
	if _, err := client.Do(context.Background(), http.MethodGet, "/user", nil, &out); err != nil || out.Login != "octo" {
		t.Fatalf("Do = %v %+v", err, out)
	}
	if rate := client.LastRateLimit(); rate.Limit != 5000 || rate.Remaining != 4999 || rate.ResetAt.Unix() != 1900000000 {
		t.Fatalf("rate = %+v", rate)
	}
}

func TestRefusalsAreClassified(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header map[string]string
		body   string
		kind   Kind
		retry  bool
	}{
		{"revoked", 401, nil, `{"message":"Bad credentials"}`, KindUnauthorized, false},
		{"primary limit", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1900000000"}, `{"message":"API rate limit exceeded"}`, KindRateLimited, true},
		{"secondary limit with retry-after", 403, map[string]string{"Retry-After": "30"}, `{"message":"You have exceeded a secondary rate limit"}`, KindRateLimited, true},
		{"secondary limit without header", 403, nil, `{"message":"You have exceeded a secondary rate limit."}`, KindRateLimited, true},
		{"too many requests", 429, nil, `{}`, KindRateLimited, false},
		{"forbidden", 403, map[string]string{"X-RateLimit-Remaining": "4000"}, `{"message":"Resource not accessible by integration"}`, KindForbidden, false},
		{"missing", 404, nil, `{"message":"Not Found"}`, KindNotFound, false},
		{"validation", 422, nil, `{"message":"Repository creation failed.","errors":[{"resource":"Repository","code":"custom","field":"name","message":"name already exists on this account"}]}`, KindValidation, false},
		{"not mergeable", 405, nil, `{"message":"Pull Request is not mergeable"}`, KindConflict, false},
		{"down", 502, nil, `<html>`, KindUnavailable, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				for key, value := range test.header {
					w.Header().Set(key, value)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			_, err := client.Request(context.Background(), http.MethodGet, "/repos/o/r", nil)
			var api *APIError
			if !errors.As(err, &api) {
				t.Fatalf("err = %v", err)
			}
			if api.Kind != test.kind || KindOf(err) != test.kind {
				t.Fatalf("kind = %s, want %s", api.Kind, test.kind)
			}
			if test.retry && api.RetryAt.IsZero() {
				t.Fatalf("rate limit without RetryAt: %+v", api)
			}
			if test.kind == KindValidation && !strings.Contains(err.Error(), "name already exists on this account") {
				t.Fatalf("validation message lost: %v", err)
			}
		})
	}
}

func TestWalkFollowsLinksOnTheAPIHostOnlyAndStopsAtTheCap(t *testing.T) {
	served := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		served++
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		// The Link names a foreign host; only its path may be followed.
		w.Header().Set("Link", fmt.Sprintf(`<https://evil.example/items?page=%d>; rel="next", <https://evil.example/items?page=99>; rel="last"`, atoi(page)+1))
		_ = json.NewEncoder(w).Encode([]int{atoi(page)})
	})
	seen := 0
	truncated, err := client.Walk(context.Background(), "/items?per_page=100", func(page []byte) error {
		seen++
		return nil
	})
	if err != nil || !truncated || seen != MaxPages || served != MaxPages {
		t.Fatalf("walk = %v truncated=%v seen=%d served=%d", err, truncated, seen, served)
	}
	seen = 0
	truncated, err = client.Walk(context.Background(), "/items", func(page []byte) error {
		seen++
		if seen == 2 {
			return ErrStopWalk
		}
		return nil
	})
	if err != nil || truncated || seen != 2 {
		t.Fatalf("stopped walk = %v %v %d", err, truncated, seen)
	}
}

func TestATokenSourceFailureIsNotSentToGitHub(t *testing.T) {
	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer server.Close()
	refusal := &ReconnectError{Login: "octo", Status: "expired"}
	client := NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "", refusal })
	_, err := client.Request(context.Background(), http.MethodGet, "/user", nil)
	var reconnect *ReconnectError
	if !errors.As(err, &reconnect) || reached {
		t.Fatalf("err = %v reached = %v", err, reached)
	}
}

func TestNamesAndPaths(t *testing.T) {
	for _, bad := range [][2]string{{"", "r"}, {"-o", "r"}, {"o", ".."}, {"o", "."}, {"o/x", "r"}, {"o", "r/../x"}, {"o", "r?x"}, {"o", strings.Repeat("a", 101)}} {
		if _, err := RepoPath(bad[0], bad[1]); err == nil {
			t.Errorf("RepoPath(%q, %q) accepted", bad[0], bad[1])
		}
	}
	if path, err := RepoPath("Octo-Org", "my.repo_1"); err != nil || path != "/repos/Octo-Org/my.repo_1" {
		t.Fatalf("RepoPath = %q %v", path, err)
	}
	if got := EscapeRef("feature/a b#1"); got != "feature/a%20b%231" {
		t.Fatalf("EscapeRef = %q", got)
	}
}

func TestAbilitiesNameWhatIsMissing(t *testing.T) {
	installation := Installation{Account: Owner{Login: "acme", Type: "Organization"}, Permissions: map[string]string{
		"administration": "write", "contents": "write", "issues": "write", "pull_requests": "write", "actions": "read", "metadata": "read",
	}}
	writer := Repository{FullName: "acme/app", Role: "write"}
	can := ComputeAbilities(writer, installation, true)
	if can.Administer || !can.Push || !can.Merge || !can.Issues || can.Actions || !can.CreateIssues {
		t.Fatalf("writer = %+v", can)
	}
	if !strings.Contains(can.Reasons["administer"], "needs admin") || !strings.Contains(can.Reasons["actions"], "Actions: write") {
		t.Fatalf("reasons = %v", can.Reasons)
	}
	archived := Repository{FullName: "acme/old", Role: "admin", Archived: true}
	can = ComputeAbilities(archived, installation, false)
	if !can.Administer || can.Push || can.Issues || can.CreateIssues || !strings.Contains(can.Reasons["push"], "archived") {
		t.Fatalf("archived = %+v", can)
	}
	suspended := installation
	suspended.Suspended = true
	if can := ComputeAbilities(Repository{FullName: "acme/app", Role: "admin"}, suspended, true); can.Administer || can.Push {
		t.Fatalf("suspended = %+v", can)
	}
	access := &RepoAccess{Owner: "acme", Name: "app", Repository: Repository{FullName: "acme/app", Can: ComputeAbilities(writer, installation, true)}}
	var refusal *NotPermittedError
	if err := access.Require(access.Repository.Can.Administer, "administer"); !errors.As(err, &refusal) || refusal.Reason == "" {
		t.Fatalf("Require = %v", err)
	}
	if access.Path("/branches") != "/repos/acme/app/branches" {
		t.Fatalf("Path = %q", access.Path("/branches"))
	}
	if RoleFromPermissions(map[string]bool{"pull": true, "triage": true}) != "triage" || RelationOf(Owner{Login: "Octo", Type: "User"}, "octo") != RelationOwned {
		t.Fatal("role or relation")
	}
	_ = time.Now
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
