package ghx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RepoSummary is one repository the signed-in account can reach, in the shape
// a chooser lists them: enough to recognise the project and enough to deploy
// it without asking GitHub a second question.
type RepoSummary struct {
	NameWithOwner string    `json:"nameWithOwner"`
	Name          string    `json:"name"`
	Owner         string    `json:"owner"`
	Description   string    `json:"description,omitempty"`
	URL           string    `json:"url"`
	CloneURL      string    `json:"cloneUrl"`
	DefaultBranch string    `json:"defaultBranch,omitempty"`
	Language      string    `json:"language,omitempty"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	Archived      bool      `json:"archived"`
	PushedAt      time.Time `json:"pushedAt,omitempty"`
}

// Branch is one ref a deployment can track.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected,omitempty"`
	Default   bool   `json:"default,omitempty"`
}

const repoListFields = "name,nameWithOwner,owner,description,url,isPrivate,isFork,isArchived,defaultBranchRef,primaryLanguage,pushedAt"

// ListRepos returns the repositories the credential can see, most recently
// pushed first.
//
// The filter is applied here rather than passed to gh because `gh repo list`
// has no query flag — its only argument is an owner — and `gh search repos`
// answers a different question: it searches all of GitHub, so a private
// repository of the signed-in account may not come back at all. Listing once
// and matching locally is the version where typing narrows what is already on
// screen instead of replacing it with search results.
func (s *Service) ListRepos(ctx context.Context, dir, query string, limit int) ([]RepoSummary, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	if limit <= 0 || limit > 300 {
		limit = 200
	}
	out, err := s.run(ctx, dir, "", "repo", "list",
		"--limit", fmt.Sprint(limit), "--json", repoListFields)
	if err != nil {
		return nil, ghErr("list your GitHub repositories", out)
	}
	var body []struct {
		Name        string `json:"name"`
		FullName    string `json:"nameWithOwner"`
		Description string `json:"description"`
		URL         string `json:"url"`
		IsPrivate   bool   `json:"isPrivate"`
		IsFork      bool   `json:"isFork"`
		IsArchived  bool   `json:"isArchived"`
		Owner       struct {
			Login string `json:"login"`
		} `json:"owner"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		PrimaryLanguage struct {
			Name string `json:"name"`
		} `json:"primaryLanguage"`
		PushedAt time.Time `json:"pushedAt"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return nil, fmt.Errorf("could not read gh's answer: %w", err)
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	repos := make([]RepoSummary, 0, len(body))
	for _, r := range body {
		if needle != "" && !strings.Contains(strings.ToLower(r.FullName+" "+r.Description), needle) {
			continue
		}
		repos = append(repos, RepoSummary{
			NameWithOwner: r.FullName,
			Name:          r.Name,
			Owner:         r.Owner.Login,
			Description:   r.Description,
			URL:           r.URL,
			CloneURL:      r.URL + ".git",
			DefaultBranch: r.DefaultBranchRef.Name,
			Language:      r.PrimaryLanguage.Name,
			Private:       r.IsPrivate,
			Fork:          r.IsFork,
			Archived:      r.IsArchived,
			PushedAt:      r.PushedAt,
		})
	}
	sort.SliceStable(repos, func(i, j int) bool { return repos[i].PushedAt.After(repos[j].PushedAt) })
	return repos, nil
}

// ListBranches returns the branches of one repository, with the default first.
//
// The repository is named rather than inferred from a checkout: the point of
// this call is to choose a branch of something that has not been cloned yet.
func (s *Service) ListBranches(ctx context.Context, dir, nameWithOwner string) ([]Branch, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	if !validRepoName(nameWithOwner) {
		return nil, fmt.Errorf("%q is not an owner/repository name", nameWithOwner)
	}
	info, err := s.run(ctx, dir, "", "api", "repos/"+nameWithOwner, "--jq", ".default_branch")
	if err != nil {
		return nil, ghErr("read this repository on GitHub", info)
	}
	defaultBranch := strings.TrimSpace(info)
	out, err := s.run(ctx, dir, "", "api", "--paginate",
		"repos/"+nameWithOwner+"/branches?per_page=100")
	if err != nil {
		return nil, ghErr("list this repository's branches", out)
	}
	var body []struct {
		Name      string `json:"name"`
		Protected bool   `json:"protected"`
	}
	// --paginate concatenates one JSON array per page, so a repository with
	// more than a hundred branches arrives as "[...][...]". Splitting the
	// stream is how gh's own consumers read it.
	decoder := json.NewDecoder(strings.NewReader(out))
	for {
		var page []struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
		}
		if err := decoder.Decode(&page); err != nil {
			break
		}
		body = append(body, page...)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("could not read gh's answer: no branches were returned")
	}
	branches := make([]Branch, 0, len(body))
	for _, b := range body {
		branches = append(branches, Branch{
			Name: b.Name, Protected: b.Protected, Default: b.Name == defaultBranch,
		})
	}
	sort.SliceStable(branches, func(i, j int) bool {
		if branches[i].Default != branches[j].Default {
			return branches[i].Default
		}
		return branches[i].Name < branches[j].Name
	})
	return branches, nil
}

// validRepoName keeps the owner/name out of argv positions it has no business
// reaching: this string is interpolated into a REST path, and GitHub's own
// rules for both halves are narrow enough to check exactly.
func validRepoName(name string) bool {
	owner, repo, ok := strings.Cut(name, "/")
	if !ok || owner == "" || repo == "" || len(name) > 140 {
		return false
	}
	for _, part := range []string{owner, repo} {
		for _, r := range part {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case r == '-' || r == '_' || r == '.':
			default:
				return false
			}
		}
		if strings.HasPrefix(part, ".") || part == ".." {
			return false
		}
	}
	return true
}
