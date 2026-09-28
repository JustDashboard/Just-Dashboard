package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Each line is git's own stderr for that failure. Only the code may leave:
// the sentence is fixed, whatever the remote printed.
func TestGitFailureCodeNamesTheRemoteAnswer(t *testing.T) {
	t.Parallel()
	for output, want := range map[string]string{
		"fatal: Authentication failed for 'https://github.com/acme/app.git/'":                            "source_auth_failed",
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled":             "source_auth_failed",
		"git@github.com: Permission denied (publickey).":                                                 "source_auth_failed",
		"remote: Repository not found.\nfatal: repository 'https://github.com/acme/gone.git/' not found": "source_repository_missing",
		"fatal: unable to access 'https://github.com/acme/app.git/': Could not resolve host: github.com": "source_unreachable",
		"fatal: couldn't find remote ref refs/heads/release":                                             "source_revision_unavailable",
		"error: Server does not allow request for unadvertised object 3f2a1b":                            "source_revision_unavailable",
		"fatal: something nobody has seen before":                                                        "",
	} {
		if got := gitFailureCode(output, errors.New("exit status 128")); got != want {
			t.Fatalf("%q = %q, want %q", output, got, want)
		}
	}
	if got := gitFailureCode("", fmt.Errorf("git fetch failed: %w", context.DeadlineExceeded)); got != "source_unreachable" {
		t.Fatalf("a timed-out fetch = %q", got)
	}
}

func TestSourceFailureKeepsTheRemoteTextOut(t *testing.T) {
	t.Parallel()
	command := &gitCommandError{code: "source_auth_failed", err: errors.New("git ls-remote https://github.com/acme/app.git failed: exit status 128")}
	err := sourceFailure(command, "Git branch could not be read")
	var failure *SourceFailure
	if !errors.As(err, &failure) || failure.Code != "source_auth_failed" || !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("err = %#v", err)
	}
	if strings.Contains(err.Error(), "ls-remote") || strings.Contains(err.Error(), "exit status") {
		t.Fatalf("the command's text leaked: %q", err)
	}
	plain := sourceFailure(&gitCommandError{err: errors.New("exit status 1")}, "Git branch could not be read")
	if errors.As(plain, &failure) || plain.Error() != "deployment source is unavailable: Git branch could not be read" {
		t.Fatalf("unclassified = %v", plain)
	}
	for err, want := range map[error]string{
		fmt.Errorf("%w: gone", ErrRefNotFound):             "ref_not_found",
		&SourceFailure{Code: "source_unreachable"}:         "source_unreachable",
		fmt.Errorf("%w: unreadable", ErrSourceUnavailable): "source_unavailable",
		nil: "source_unavailable",
	} {
		if got := gitWatchReason(err); got != want {
			t.Fatalf("watch reason for %v = %q, want %q", err, got, want)
		}
	}
}
