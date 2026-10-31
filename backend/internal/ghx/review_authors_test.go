package ghx

import (
	"context"
	"strings"
	"testing"
)

func TestCommitAuthorsMapsEmailsAndLeavesOutWhoGitHubCannotName(t *testing.T) {
	s := New()
	s.command = func(_ context.Context, _, _ string, args ...string) (string, error) {
		if args[0] == "repo" {
			return `{"url":"https://github.com/o/r","nameWithOwner":"o/r"}`, nil
		}
		if got := strings.Join(args, " "); got != "api --hostname github.com repos/o/r/commits?per_page=100" {
			t.Fatalf("argv: %s", got)
		}
		return `[
		  {"commit":{"author":{"email":"Me@Example.com"}},"author":{"login":"Wayy01","type":"User"}},
		  {"commit":{"author":{"email":"noreply@github.com"}},"author":{"login":"web-flow","type":"User"}},
		  {"commit":{"author":{"email":"bot@example.com"}},"author":{"login":"dependabot[bot]","type":"Bot"}},
		  {"commit":{"author":{"email":"stranger@example.com"}},"author":null}
		]`, nil
	}
	authors, err := s.CommitAuthors(context.Background(), "/srv/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(authors) != 1 || authors["me@example.com"] != "Wayy01" {
		t.Fatalf("authors: %v", authors)
	}
}
