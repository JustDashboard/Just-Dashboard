package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SourceFailure is a source that could not be read, named by what git's own
// output proved. Only the code and a fixed sentence leave the boundary: the
// remote controls git's stderr, which can echo a credential or a header, so
// the text itself is read inside runPlanningGit and never propagated.
type SourceFailure struct {
	Code    string
	Message string
}

func (e *SourceFailure) Error() string { return ErrSourceUnavailable.Error() + ": " + e.Message }

func (e *SourceFailure) Unwrap() error { return ErrSourceUnavailable }

// gitCommandError carries the code gitFailureCode read from a failed git
// command's bounded output alongside the redacted error it always returned.
type gitCommandError struct {
	code string
	err  error
}

func (e *gitCommandError) Error() string { return e.err.Error() }

func (e *gitCommandError) Unwrap() error { return e.err }

// gitFailureSignatures are fixed substrings of git's own failure output, in
// the order they are tested. A private GitHub repository the credential
// cannot see answers "Repository not found", so that code's sentence names
// both possibilities.
var gitFailureSignatures = []struct {
	code    string
	needles []string
}{
	{"source_revision_unavailable", []string{
		"not our ref", "couldn't find remote ref", "unadvertised object", "no such remote ref",
		"reference is not a tree", "did not send all necessary objects",
	}},
	{"source_auth_failed", []string{
		"authentication failed", "could not read username", "could not read password",
		"permission denied (publickey", "http basic: access denied", "returned error: 401",
		"returned error: 403", "invalid username or password", "invalid username or token",
		"terminal prompts disabled", "bad credentials",
	}},
	{"source_repository_missing", []string{
		"repository not found", "does not appear to be a git repository", "returned error: 404",
		"project you were looking for could not be found",
	}},
	{"source_unreachable", []string{
		"could not resolve host", "connection timed out", "connection refused", "network is unreachable",
		"failed to connect to", "operation timed out", "could not resolve proxy", "tls handshake",
		"gnutls_handshake", "ssl_connect", "early eof", "connection reset",
	}},
}

func gitFailureCode(output string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "source_unreachable"
	}
	lower := strings.ToLower(output)
	for _, signature := range gitFailureSignatures {
		for _, needle := range signature.needles {
			if strings.Contains(lower, needle) {
				return signature.code
			}
		}
	}
	return ""
}

var sourceFailureMessages = map[string]string{
	"source_auth_failed":          "the Git remote refused the credential; it may have expired or lost access to this repository",
	"source_repository_missing":   "the Git remote has no such repository, or the credential cannot see it",
	"source_unreachable":          "the Git remote could not be reached from this server",
	"source_revision_unavailable": "the recorded commit is no longer on the remote; a force-push or a deleted branch removed it",
}

// sourceFailure names why a git command failed when its output proved it,
// and otherwise keeps the fallback every caller already reported.
func sourceFailure(err error, fallback string) error {
	var command *gitCommandError
	if errors.As(err, &command) && sourceFailureMessages[command.code] != "" {
		return &SourceFailure{Code: command.code, Message: sourceFailureMessages[command.code]}
	}
	return fmt.Errorf("%w: %s", ErrSourceUnavailable, fallback)
}

// gitWatchReason is the reason the watcher records for a branch it could not
// read, so the header can say what stopped automatic deploys.
func gitWatchReason(err error) string {
	var failure *SourceFailure
	switch {
	case errors.Is(err, ErrRefNotFound):
		return "ref_not_found"
	case errors.As(err, &failure):
		return failure.Code
	}
	return "source_unavailable"
}
