package githubrepos

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// CommitPerson is a commit's author or committer: the name git recorded,
// and the GitHub account GitHub matched it to when it did.
type CommitPerson struct {
	Name  string    `json:"name"`
	Login string    `json:"login,omitempty"`
	Date  time.Time `json:"date,omitzero"`
}

// Commit is one commit in a repository's history.
type Commit struct {
	SHA string `json:"sha"`
	// Message is whole; the page shows its first line.
	Message   string        `json:"message"`
	Author    CommitPerson  `json:"author"`
	Committer *CommitPerson `json:"committer,omitempty"`
	HTMLURL   string        `json:"htmlUrl"`
	// Verified is GitHub's verdict on the signature, left out when GitHub
	// gave none.
	Verified *bool `json:"verified,omitempty"`
	Parents  int   `json:"parents"`
}

// CommitPage is one page of history.
type CommitPage struct {
	Commits []Commit `json:"commits"`
	HasMore bool     `json:"hasMore"`
}

type rawCommit struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  *struct {
			Name string     `json:"name"`
			Date *time.Time `json:"date"`
		} `json:"author"`
		Committer *struct {
			Name string     `json:"name"`
			Date *time.Time `json:"date"`
		} `json:"committer"`
		Verification *struct {
			Verified bool `json:"verified"`
		} `json:"verification"`
	} `json:"commit"`
	Author    *rawUser   `json:"author"`
	Committer *rawUser   `json:"committer"`
	Parents   []struct{} `json:"parents"`
}

func (raw rawCommit) commit() Commit {
	out := Commit{SHA: raw.SHA, Message: raw.Commit.Message, HTMLURL: raw.HTMLURL, Parents: len(raw.Parents)}
	if author := raw.Commit.Author; author != nil {
		out.Author = CommitPerson{Name: author.Name, Date: timeOf(author.Date)}
	}
	out.Author.Login = loginOf(raw.Author)
	if committer := raw.Commit.Committer; committer != nil {
		out.Committer = &CommitPerson{Name: committer.Name, Login: loginOf(raw.Committer), Date: timeOf(committer.Date)}
	}
	if verification := raw.Commit.Verification; verification != nil {
		verified := verification.Verified
		out.Verified = &verified
	}
	return out
}

// Commits reads one page of 30 commits, newest first, from start: a branch
// name or a commit, or the default branch when empty. An empty repository
// has no history rather than an error (GitHub answers it with a 409).
func (a *RepoAccess) Commits(ctx context.Context, start string, page int) (*CommitPage, error) {
	start = strings.TrimSpace(start)
	if start != "" && !shortSHARE.MatchString(strings.ToLower(start)) {
		if err := ValidBranchName(start); err != nil {
			return nil, invalid("start the history from a branch name or a commit")
		}
	}
	var raw []rawCommit
	more, err := a.Client.Page(ctx, pagedPath(a.Path("/commits"), 30, page, url.Values{"sha": {start}}), &raw)
	if err != nil {
		if KindOf(err) == KindConflict {
			return &CommitPage{Commits: []Commit{}}, nil
		}
		return nil, err
	}
	out := &CommitPage{Commits: make([]Commit, 0, len(raw)), HasMore: more}
	for _, item := range raw {
		out.Commits = append(out.Commits, item.commit())
	}
	return out, nil
}
