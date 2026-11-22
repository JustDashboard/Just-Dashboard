package githubrepos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Branch is one branch of a repository.
type Branch struct {
	Name      string `json:"name"`
	SHA       string `json:"sha"`
	Protected bool   `json:"protected"`
	Default   bool   `json:"default"`
}

// BranchPage is one page of a repository's branches.
type BranchPage struct {
	Branches []Branch `json:"branches"`
	HasMore  bool     `json:"hasMore"`
}

type rawBranch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
	Protected bool `json:"protected"`
}

func (a *RepoAccess) branchOf(raw rawBranch) Branch {
	return Branch{Name: raw.Name, SHA: raw.Commit.SHA, Protected: raw.Protected, Default: raw.Name == a.Repository.DefaultBranch}
}

// Branches reads one page of 100 branches, the default one marked.
func (a *RepoAccess) Branches(ctx context.Context, page int) (*BranchPage, error) {
	var raw []rawBranch
	more, err := a.Client.Page(ctx, pagedPath(a.Path("/branches"), 100, page, nil), &raw)
	if err != nil {
		return nil, err
	}
	out := &BranchPage{Branches: make([]Branch, 0, len(raw)), HasMore: more}
	for _, item := range raw {
		out.Branches = append(out.Branches, a.branchOf(item))
	}
	return out, nil
}

// ClassicProtection is a branch's classic protection, summarised.
type ClassicProtection struct {
	RequiredReviews         int      `json:"requiredReviews,omitempty"`
	DismissStaleReviews     bool     `json:"dismissStaleReviews,omitempty"`
	RequireCodeOwnerReviews bool     `json:"requireCodeOwnerReviews,omitempty"`
	RequiredChecks          []string `json:"requiredChecks"`
	StrictChecks            bool     `json:"strictChecks,omitempty"`
	EnforceAdmins           bool     `json:"enforceAdmins,omitempty"`
	LinearHistory           bool     `json:"linearHistory,omitempty"`
	AllowForcePushes        bool     `json:"allowForcePushes,omitempty"`
	AllowDeletions          bool     `json:"allowDeletions,omitempty"`
	ConversationResolution  bool     `json:"conversationResolution,omitempty"`
}

// BranchRule is one ruleset rule that applies to a branch, in words.
type BranchRule struct {
	Type        string `json:"type"`
	Summary     string `json:"summary"`
	RulesetID   int64  `json:"rulesetId,omitempty"`
	RulesetName string `json:"rulesetName,omitempty"`
}

// BranchProtection is what guards one branch: classic protection and the
// ruleset rules that apply to it. Either half GitHub would not show this
// person is marked unreadable with a sentence, not answered as a failure.
type BranchProtection struct {
	Branch             string             `json:"branch"`
	Protected          bool               `json:"protected"`
	Protection         *ClassicProtection `json:"protection,omitempty"`
	ProtectionReadable bool               `json:"protectionReadable"`
	ProtectionMessage  string             `json:"protectionMessage,omitempty"`
	Rules              []BranchRule       `json:"rules"`
	RulesReadable      bool               `json:"rulesReadable"`
	RulesMessage       string             `json:"rulesMessage,omitempty"`
}

type enabledFlag struct {
	Enabled bool `json:"enabled"`
}

type rawProtection struct {
	RequiredStatusChecks *struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
		} `json:"checks"`
	} `json:"required_status_checks"`
	EnforceAdmins              *enabledFlag `json:"enforce_admins"`
	RequiredPullRequestReviews *struct {
		DismissStaleReviews          bool `json:"dismiss_stale_reviews"`
		RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
		RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
	} `json:"required_pull_request_reviews"`
	RequiredLinearHistory          *enabledFlag `json:"required_linear_history"`
	AllowForcePushes               *enabledFlag `json:"allow_force_pushes"`
	AllowDeletions                 *enabledFlag `json:"allow_deletions"`
	RequiredConversationResolution *enabledFlag `json:"required_conversation_resolution"`
}

func (raw rawProtection) summary() *ClassicProtection {
	out := &ClassicProtection{RequiredChecks: []string{}}
	on := func(flag *enabledFlag) bool { return flag != nil && flag.Enabled }
	if checks := raw.RequiredStatusChecks; checks != nil {
		out.StrictChecks = checks.Strict
		seen := map[string]bool{}
		add := func(name string) {
			if name != "" && !seen[name] {
				seen[name] = true
				out.RequiredChecks = append(out.RequiredChecks, name)
			}
		}
		for _, check := range checks.Checks {
			add(check.Context)
		}
		for _, context := range checks.Contexts {
			add(context)
		}
	}
	if reviews := raw.RequiredPullRequestReviews; reviews != nil {
		// A review requirement with a count of zero still means changes
		// arrive through a pull request; the count is only the approvals.
		out.RequiredReviews = reviews.RequiredApprovingReviewCount
		out.DismissStaleReviews = reviews.DismissStaleReviews
		out.RequireCodeOwnerReviews = reviews.RequireCodeOwnerReviews
	}
	out.EnforceAdmins = on(raw.EnforceAdmins)
	out.LinearHistory = on(raw.RequiredLinearHistory)
	out.AllowForcePushes = on(raw.AllowForcePushes)
	out.AllowDeletions = on(raw.AllowDeletions)
	out.ConversationResolution = on(raw.RequiredConversationResolution)
	return out
}

type rawRule struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
	RulesetID  int64           `json:"ruleset_id"`
}

// BranchProtection reads what guards one branch. The branch itself is read
// first (anyone who can see the repository may), which says whether classic
// protection is on; the protection's detail needs admin, so a refusal there
// is a sentence. The rules come from the rules-for-a-branch listing, which
// already resolves every ruleset (the repository's and its organisation's)
// that targets this branch; their names come from the repository's ruleset
// listing when it answers.
func (a *RepoAccess) BranchProtection(ctx context.Context, name string) (*BranchProtection, error) {
	if err := ValidBranchName(name); err != nil {
		return nil, err
	}
	var branch rawBranch
	if _, err := a.Client.Do(ctx, http.MethodGet, a.Path("/branches/"+EscapeRef(name)), nil, &branch); err != nil {
		return nil, err
	}
	out := &BranchProtection{Branch: branch.Name, Protected: branch.Protected, Rules: []BranchRule{}}
	if branch.Protected {
		var raw rawProtection
		_, err := a.Client.Do(ctx, http.MethodGet, a.Path("/branches/"+EscapeRef(name)+"/protection"), nil, &raw)
		switch {
		case err == nil:
			out.Protection = raw.summary()
			out.ProtectionReadable = true
		case unreadable(err):
			// GitHub answers 404 here both for "not protected" and for a
			// reader who is not an admin; the branch already said it is
			// protected, so this is the second.
			out.ProtectionMessage = "GitHub shows a branch's protection settings only to the repository's administrators."
		default:
			return nil, err
		}
	} else {
		out.ProtectionReadable = true
	}

	var rules []rawRule
	truncated, err := a.Client.Walk(ctx, a.Path("/rules/branches/"+EscapeRef(name)+"?per_page=100"), func(page []byte) error {
		var batch []rawRule
		if err := json.Unmarshal(page, &batch); err != nil {
			return err
		}
		rules = append(rules, batch...)
		return nil
	})
	switch {
	case err == nil:
		out.RulesReadable = true
		if truncated {
			out.RulesMessage = "More rules apply than were read."
		}
	case unreadable(err):
		out.RulesMessage = refusalWords(err, "the rulesets on this branch")
	default:
		return nil, err
	}
	if len(rules) > 0 {
		names := a.rulesetNames(ctx)
		for _, rule := range rules {
			out.Rules = append(out.Rules, BranchRule{
				Type: rule.Type, Summary: ruleSummary(rule.Type, rule.Parameters),
				RulesetID: rule.RulesetID, RulesetName: names[rule.RulesetID],
			})
		}
		out.Protected = true
	}
	return out, nil
}

// rulesetNames maps ruleset ids to names, best effort: a rule is still shown
// by what it does when its ruleset's name cannot be read.
func (a *RepoAccess) rulesetNames(ctx context.Context) map[int64]string {
	var rulesets []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	names := map[int64]string{}
	if _, err := a.Client.Page(ctx, a.Path("/rulesets?includes_parents=true&per_page=100"), &rulesets); err != nil {
		return names
	}
	for _, ruleset := range rulesets {
		names[ruleset.ID] = ruleset.Name
	}
	return names
}

// ruleSummary is one ruleset rule in words. A type this does not know is
// shown by its name, so a rule GitHub adds later is still listed.
func ruleSummary(kind string, parameters json.RawMessage) string {
	var p struct {
		RequiredApprovingReviewCount   int  `json:"required_approving_review_count"`
		RequireCodeOwnerReview         bool `json:"require_code_owner_review"`
		DismissStaleReviewsOnPush      bool `json:"dismiss_stale_reviews_on_push"`
		RequiredReviewThreadResolution bool `json:"required_review_thread_resolution"`
		RequiredStatusChecks           []struct {
			Context string `json:"context"`
		} `json:"required_status_checks"`
		StrictRequiredStatusChecksPolicy bool     `json:"strict_required_status_checks_policy"`
		RequiredDeploymentEnvironments   []string `json:"required_deployment_environments"`
		Operator                         string   `json:"operator"`
		Pattern                          string   `json:"pattern"`
		Negate                           bool     `json:"negate"`
		Workflows                        []struct {
			Path string `json:"path"`
		} `json:"workflows"`
	}
	if len(parameters) > 0 {
		_ = json.Unmarshal(parameters, &p)
	}
	switch kind {
	case "creation":
		return "Only people allowed to bypass may create matching branches"
	case "update":
		return "Only people allowed to bypass may push"
	case "deletion":
		return "No deletions"
	case "non_fast_forward":
		return "No force pushes"
	case "required_linear_history":
		return "Linear history"
	case "required_signatures":
		return "Signed commits"
	case "merge_queue":
		return "Merges go through a merge queue"
	case "pull_request":
		text := "Pull request"
		switch p.RequiredApprovingReviewCount {
		case 0:
			text += " (no approvals required)"
		case 1:
			text += " with 1 approval"
		default:
			text += fmt.Sprintf(" with %d approvals", p.RequiredApprovingReviewCount)
		}
		var extra []string
		if p.RequireCodeOwnerReview {
			extra = append(extra, "code owner review")
		}
		if p.DismissStaleReviewsOnPush {
			extra = append(extra, "stale approvals dismissed")
		}
		if p.RequiredReviewThreadResolution {
			extra = append(extra, "conversations resolved")
		}
		if len(extra) > 0 {
			text += ", " + strings.Join(extra, ", ")
		}
		return text
	case "required_status_checks":
		var names []string
		for _, check := range p.RequiredStatusChecks {
			if check.Context != "" {
				names = append(names, check.Context)
			}
		}
		text := "Required checks"
		if len(names) > 0 {
			text += ": " + strings.Join(names, ", ")
		}
		if p.StrictRequiredStatusChecksPolicy {
			text += " (branch up to date)"
		}
		return text
	case "required_deployments":
		if len(p.RequiredDeploymentEnvironments) > 0 {
			return "Deployed first to: " + strings.Join(p.RequiredDeploymentEnvironments, ", ")
		}
		return "Required deployments"
	case "workflows":
		var paths []string
		for _, workflow := range p.Workflows {
			if workflow.Path != "" {
				paths = append(paths, workflow.Path)
			}
		}
		if len(paths) > 0 {
			return "Required workflows: " + strings.Join(paths, ", ")
		}
		return "Required workflows"
	case "code_scanning":
		return "Code scanning results required"
	case "commit_message_pattern", "commit_author_email_pattern", "committer_email_pattern", "branch_name_pattern", "tag_name_pattern":
		subject := map[string]string{
			"commit_message_pattern":      "Commit messages",
			"commit_author_email_pattern": "Commit author emails",
			"committer_email_pattern":     "Committer emails",
			"branch_name_pattern":         "Branch names",
			"tag_name_pattern":            "Tag names",
		}[kind]
		verb := map[string]string{
			"starts_with": "start with", "ends_with": "end with", "contains": "contain", "regex": "match",
		}[p.Operator]
		if verb == "" || p.Pattern == "" {
			return subject + " follow a pattern"
		}
		if p.Negate {
			return fmt.Sprintf("%s must not %s %q", subject, verb, p.Pattern)
		}
		return fmt.Sprintf("%s must %s %q", subject, verb, p.Pattern)
	}
	if kind == "" {
		return "A rule"
	}
	words := strings.ReplaceAll(kind, "_", " ")
	return strings.ToUpper(words[:1]) + words[1:]
}

// CreateBranch makes name point at from: a full commit name, or a branch
// whose tip is read first. Branches are refs, so this is the refs API, which
// needs contents:write and write on the repository (Abilities.Push).
func (a *RepoAccess) CreateBranch(ctx context.Context, name, from string) (*Branch, error) {
	if err := ValidBranchName(name); err != nil {
		return nil, err
	}
	from = strings.TrimSpace(from)
	if from == "" {
		from = a.Repository.DefaultBranch
	}
	sha := strings.ToLower(from)
	if !IsFullSHA(sha) {
		if err := ValidBranchName(from); err != nil {
			return nil, invalid("start the branch from a branch name or a full 40-character commit")
		}
	}
	if err := a.Require(a.Repository.Can.Push, "push"); err != nil {
		return nil, err
	}
	if !IsFullSHA(sha) {
		var ref struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if _, err := a.Client.Do(ctx, http.MethodGet, a.Path("/git/ref/heads/"+EscapeRef(from)), nil, &ref); err != nil {
			return nil, err
		}
		sha = ref.Object.SHA
		if !IsFullSHA(sha) {
			return nil, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub did not say which commit " + from + " is on", Path: a.Path("/git/ref/heads/")}
		}
	}
	var created struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	body := map[string]string{"ref": "refs/heads/" + name, "sha": sha}
	if _, err := a.Client.Do(ctx, http.MethodPost, a.Path("/git/refs"), body, &created); err != nil {
		return nil, err
	}
	if created.Object.SHA != "" {
		sha = created.Object.SHA
	}
	return &Branch{Name: name, SHA: sha, Default: name == a.Repository.DefaultBranch}, nil
}

// DeleteBranch removes a branch. The default branch is refused here: GitHub
// refuses it too, but only after the person confirmed something that could
// never happen.
func (a *RepoAccess) DeleteBranch(ctx context.Context, name string) error {
	if err := ValidBranchName(name); err != nil {
		return err
	}
	if name == a.Repository.DefaultBranch {
		return invalid("%s is the default branch of %s; choose another default branch before deleting it", name, a.Repository.FullName)
	}
	if err := a.Require(a.Repository.Can.Push, "push"); err != nil {
		return err
	}
	_, err := a.Client.Request(ctx, http.MethodDelete, a.Path("/git/refs/heads/"+EscapeRef(name)), nil)
	return err
}
