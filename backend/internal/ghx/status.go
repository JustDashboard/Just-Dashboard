package ghx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// CommitStatus is one state posted against a commit: what GitHub shows next
// to the SHA and on the pull request that carries it.
type CommitStatus struct {
	// State is pending, success, failure or error — GitHub's closed set.
	State       string
	TargetURL   string
	Description string
	// Context names the reporter, e.g. "just-dashboard/production". One
	// context is one row in the checks list; a second run overwrites it.
	Context string
}

var (
	commitSHARE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	statusContextRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/-]{0,254}$`)
)

// PostCommitStatus writes a status to repos/{owner}/{name}/statuses/{sha}
// through gh's REST helper with the dashboard's own credential. Every argument
// is validated before it reaches argv or a REST path.
func (s *Service) PostCommitStatus(ctx context.Context, nameWithOwner, sha string, status CommitStatus) error {
	if !s.Available() {
		return ErrNotInstalled
	}
	if !validRepoName(nameWithOwner) {
		return fmt.Errorf("repository %q is not owner/name", nameWithOwner)
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !commitSHARE.MatchString(sha) {
		return fmt.Errorf("commit %q is not a full object id", sha)
	}
	switch status.State {
	case "pending", "success", "failure", "error":
	default:
		return fmt.Errorf("commit status state %q is not accepted by GitHub", status.State)
	}
	if !statusContextRE.MatchString(status.Context) {
		return fmt.Errorf("commit status context %q is invalid", status.Context)
	}
	description := strings.TrimSpace(status.Description)
	if len(description) > 140 {
		// GitHub rejects longer descriptions outright rather than truncating.
		description = description[:137] + "..."
	}
	args := []string{"api", "-X", "POST", "repos/" + nameWithOwner + "/statuses/" + sha,
		"-f", "state=" + status.State, "-f", "context=" + status.Context}
	if description != "" {
		args = append(args, "-f", "description="+description)
	}
	if target := strings.TrimSpace(status.TargetURL); target != "" {
		if !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "http://") {
			return fmt.Errorf("commit status target %q must be an http(s) URL", target)
		}
		args = append(args, "-f", "target_url="+target)
	}
	out, err := s.run(ctx, "", "", args...)
	if err != nil {
		return ghErr("post the commit status to GitHub", out)
	}
	return nil
}
