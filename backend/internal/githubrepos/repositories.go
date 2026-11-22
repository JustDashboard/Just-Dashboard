package githubrepos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// listConcurrency is how many installations are listed at once.
const listConcurrency = 4

// rawRepository is a repository as GitHub's REST API answers it.
type rawRepository struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	FullName        string          `json:"full_name"`
	Owner           Owner           `json:"owner"`
	Description     *string         `json:"description"`
	Homepage        *string         `json:"homepage"`
	Visibility      string          `json:"visibility"`
	Private         bool            `json:"private"`
	Language        *string         `json:"language"`
	Stars           int             `json:"stargazers_count"`
	Forks           int             `json:"forks_count"`
	OpenIssues      int             `json:"open_issues_count"`
	Archived        bool            `json:"archived"`
	Fork            bool            `json:"fork"`
	IsTemplate      bool            `json:"is_template"`
	DefaultBranch   string          `json:"default_branch"`
	HTMLURL         string          `json:"html_url"`
	CloneURL        string          `json:"clone_url"`
	SSHURL          string          `json:"ssh_url"`
	Topics          []string        `json:"topics"`
	CreatedAt       *time.Time      `json:"created_at"`
	UpdatedAt       *time.Time      `json:"updated_at"`
	PushedAt        *time.Time      `json:"pushed_at"`
	Permissions     map[string]bool `json:"permissions"`
	HasIssues       bool            `json:"has_issues"`
	HasWiki         bool            `json:"has_wiki"`
	HasProjects     bool            `json:"has_projects"`
	HasDiscussions  bool            `json:"has_discussions"`
	AllowMerge      *bool           `json:"allow_merge_commit"`
	AllowSquash     *bool           `json:"allow_squash_merge"`
	AllowRebase     *bool           `json:"allow_rebase_merge"`
	AllowAutoMerge  *bool           `json:"allow_auto_merge"`
	DeleteOnMerge   *bool           `json:"delete_branch_on_merge"`
	AllowUpdate     *bool           `json:"allow_update_branch"`
	SignoffRequired *bool           `json:"web_commit_signoff_required"`
	Parent          *struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"parent"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// repositoryOf is GitHub's answer as the page lists it, with the person's
// role, the relation to the connected account and the abilities through the
// installation it was read through.
func repositoryOf(raw rawRepository, installation Installation, login string) Repository {
	visibility := raw.Visibility
	if visibility == "" {
		visibility = "public"
		if raw.Private {
			visibility = "private"
		}
	}
	repository := Repository{
		ID: raw.ID, Name: raw.Name, FullName: raw.FullName, Owner: raw.Owner, Description: deref(raw.Description),
		Visibility: visibility, Private: raw.Private, Language: deref(raw.Language), Stars: raw.Stars, Forks: raw.Forks,
		OpenIssues: raw.OpenIssues, Archived: raw.Archived, Fork: raw.Fork, IsTemplate: raw.IsTemplate,
		DefaultBranch: raw.DefaultBranch, HTMLURL: raw.HTMLURL, CloneURL: raw.CloneURL, SSHURL: raw.SSHURL,
		Topics: raw.Topics, CreatedAt: timeOf(raw.CreatedAt), UpdatedAt: timeOf(raw.UpdatedAt), PushedAt: timeOf(raw.PushedAt),
		InstallationID: installation.ID, Role: RoleFromPermissions(raw.Permissions), Relation: RelationOf(raw.Owner, login),
	}
	if len(repository.Topics) == 0 {
		repository.Topics = nil
	}
	repository.Can = ComputeAbilities(repository, installation, raw.HasIssues)
	return repository
}

// PartialListing is one installation whose repositories could not be read;
// the others' still came back.
type PartialListing struct {
	InstallationID int64  `json:"installationId"`
	Account        string `json:"account"`
	Message        string `json:"message"`
}

// RepositoryList is GitHubRepositoryList.
type RepositoryList struct {
	Connection   Connection       `json:"connection"`
	Repositories []Repository     `json:"repositories"`
	Truncated    bool             `json:"truncated"`
	Partial      []PartialListing `json:"partial"`
	ReadAt       time.Time        `json:"readAt,omitzero"`
	RateLimit    *RateLimit       `json:"rateLimit,omitempty"`
}

// Repositories lists every repository the connection reaches through the
// App's installations it can see, answered from memory for a minute unless
// refresh is set.
//
// Each installation is listed on its own, four at once, all of them sharing
// one budget of MaxPages pages. One that fails goes into Partial and the
// rest still answer, except for the two failures that are the account's and
// not the installation's: a sign-in GitHub no longer accepts, and a spent
// rate limit, which are the whole answer.
func (s *Service) Repositories(ctx context.Context, userID, id int64, refresh bool) (RepositoryList, error) {
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return RepositoryList{}, err
	}
	if !refresh {
		s.mu.Lock()
		cached, ok := s.lists[id]
		s.mu.Unlock()
		if ok && s.now().Sub(cached.at) < listTTL {
			return cached.list, nil
		}
	}
	client := s.client(c)
	installations, err := s.installations(ctx, c, client, true)
	if err != nil {
		return RepositoryList{}, err
	}
	var budget atomic.Int64
	budget.Store(MaxPages)
	type result struct {
		installation Installation
		repositories []rawRepository
		truncated    bool
		err          error
	}
	results := make([]result, len(installations))
	slots := make(chan struct{}, listConcurrency)
	var wg sync.WaitGroup
	for i, installation := range installations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			repositories, truncated, err := listInstallation(ctx, client, installation.ID, &budget)
			results[i] = result{installation: installation, repositories: repositories, truncated: truncated, err: err}
		}()
	}
	wg.Wait()

	list := RepositoryList{Repositories: []Repository{}, Partial: []PartialListing{}, ReadAt: s.now().UTC()}
	seen := map[int64]bool{}
	for _, r := range results {
		if r.err != nil {
			switch KindOf(r.err) {
			case KindUnauthorized, KindRateLimited:
				return RepositoryList{}, r.err
			}
			var reconnectErr *ReconnectError
			if errors.As(r.err, &reconnectErr) {
				return RepositoryList{}, r.err
			}
			if ctx.Err() != nil {
				return RepositoryList{}, ctx.Err()
			}
			list.Partial = append(list.Partial, PartialListing{InstallationID: r.installation.ID, Account: r.installation.Account.Login, Message: r.err.Error()})
			continue
		}
		list.Truncated = list.Truncated || r.truncated
		for _, raw := range r.repositories {
			if seen[raw.ID] {
				continue
			}
			seen[raw.ID] = true
			list.Repositories = append(list.Repositories, repositoryOf(raw, r.installation, c.Login))
		}
	}
	sort.Slice(list.Repositories, func(i, j int) bool {
		return strings.ToLower(list.Repositories[i].FullName) < strings.ToLower(list.Repositories[j].FullName)
	})
	if rate := client.LastRateLimit(); rate.Limit > 0 {
		list.RateLimit = &rate
	}
	if fresh, err := s.store.Connection(ctx, userID, id); err == nil {
		c = fresh
	}
	list.Connection = c
	s.mu.Lock()
	trimListsLocked(s.lists, s.now())
	s.lists[id] = cachedList{at: s.now(), list: list}
	s.mu.Unlock()
	return list, nil
}

// listInstallation walks one installation's repositories, spending the
// shared page budget.
func listInstallation(ctx context.Context, client *Client, installationID int64, budget *atomic.Int64) ([]rawRepository, bool, error) {
	out := []rawRepository{}
	next := "/user/installations/" + strconv.FormatInt(installationID, 10) + "/repositories?per_page=100"
	for next != "" {
		if budget.Add(-1) < 0 {
			return out, true, nil
		}
		response, err := client.Request(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, false, err
		}
		var page struct {
			Repositories []rawRepository `json:"repositories"`
		}
		if err := json.Unmarshal(response.Body, &page); err != nil {
			return nil, false, &APIError{Kind: KindUnavailable, Message: "GitHub's repository list was not readable", Path: next}
		}
		out = append(out, page.Repositories...)
		next = NextLink(response.Header.Get("Link"))
	}
	return out, false, nil
}

// covers reports whether an installation reaches the repository with id.
// "all" reaches every repository of its account; "selected" only those its
// owner picked, which the connection's last list answers when it has the
// repository, and a walk of the installation otherwise.
func (s *Service) covers(ctx context.Context, client *Client, connection int64, installation Installation, repositoryID int64) (bool, error) {
	if installation.RepositorySelection != "selected" {
		return true, nil
	}
	s.mu.Lock()
	cached, ok := s.lists[connection]
	s.mu.Unlock()
	if ok && s.now().Sub(cached.at) < listTTL {
		for _, r := range cached.list.Repositories {
			if r.ID == repositoryID && r.InstallationID == installation.ID {
				return true, nil
			}
		}
	}
	found := false
	_, err := client.Walk(ctx, "/user/installations/"+strconv.FormatInt(installation.ID, 10)+"/repositories?per_page=100", func(page []byte) error {
		var body struct {
			Repositories []struct {
				ID int64 `json:"id"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(page, &body); err != nil {
			return err
		}
		for _, r := range body.Repositories {
			if r.ID == repositoryID {
				found = true
				return ErrStopWalk
			}
		}
		return nil
	})
	return found, err
}

func installationFor(installations []Installation, owner string) (Installation, bool) {
	for _, installation := range installations {
		if equalFold(installation.Account.Login, owner) {
			return installation, true
		}
	}
	return Installation{}, false
}

func notFound(path string) *APIError {
	return &APIError{Status: http.StatusNotFound, Kind: KindNotFound, Path: path}
}

// Access resolves one repository for one request: the account's own
// connection, GitHub's answer for the repository with the person's token, and
// the installation that reaches it. A repository no installation this
// connection can see covers is not found: the page manages only what the App
// was given.
func (s *Service) Access(ctx context.Context, userID, id int64, owner, name string) (*RepoAccess, error) {
	path, err := RepoPath(owner, name)
	if err != nil {
		return nil, notFound("/repos/{owner}/{repo}")
	}
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	key := keyOf(id, owner, name)
	s.mu.Lock()
	cached, ok := s.access[key]
	s.mu.Unlock()
	if ok && s.now().Sub(cached.at) < accessTTL {
		access := cached.access
		access.Client = s.client(c)
		return &access, nil
	}
	client := s.client(c)
	var raw rawRepository
	if _, err := client.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	access, err := s.accessOf(ctx, c, client, raw)
	if err != nil {
		return nil, err
	}
	return access, nil
}

// accessOf finishes an access from GitHub's answer for the repository, and
// caches it under both the address asked for and GitHub's own spelling.
func (s *Service) accessOf(ctx context.Context, c Connection, client *Client, raw rawRepository) (*RepoAccess, error) {
	installation, ok := installationFor(c.Installations, raw.Owner.Login)
	if !ok || s.now().Sub(c.InstallationsReadAt) >= installationsTTL {
		installations, err := s.installations(ctx, c, client, true)
		if err != nil {
			return nil, err
		}
		installation, ok = installationFor(installations, raw.Owner.Login)
	}
	if !ok {
		return nil, notFound("/repos/{owner}/{repo}")
	}
	covered, err := s.covers(ctx, client, c.ID, installation, raw.ID)
	if err != nil {
		return nil, err
	}
	if !covered {
		return nil, notFound("/repos/{owner}/{repo}")
	}
	access := RepoAccess{
		Client: client, ConnectionID: c.ID, Login: c.Login, Owner: raw.Owner.Login, Name: raw.Name,
		Repository: repositoryOf(raw, installation, c.Login), Installation: installation,
	}
	s.remember(access, raw)
	return &access, nil
}

func (s *Service) remember(access RepoAccess, raw rawRepository) {
	stored := access
	stored.Client = nil
	s.mu.Lock()
	defer s.mu.Unlock()
	trimAccessLocked(s.access, s.now())
	s.access[keyOf(access.ConnectionID, access.Owner, access.Name)] = cachedAccess{at: s.now(), access: stored, raw: raw}
}

// RepositorySettings is GitHubRepositorySettings. The merge settings are
// only stated by GitHub to people who may change them, so each is absent
// rather than false when GitHub did not say.
type RepositorySettings struct {
	HasIssues                bool  `json:"hasIssues"`
	HasWiki                  bool  `json:"hasWiki"`
	HasProjects              bool  `json:"hasProjects"`
	HasDiscussions           bool  `json:"hasDiscussions"`
	AllowMergeCommit         *bool `json:"allowMergeCommit,omitempty"`
	AllowSquashMerge         *bool `json:"allowSquashMerge,omitempty"`
	AllowRebaseMerge         *bool `json:"allowRebaseMerge,omitempty"`
	AllowAutoMerge           *bool `json:"allowAutoMerge,omitempty"`
	DeleteBranchOnMerge      *bool `json:"deleteBranchOnMerge,omitempty"`
	AllowUpdateBranch        *bool `json:"allowUpdateBranch,omitempty"`
	WebCommitSignoffRequired *bool `json:"webCommitSignoffRequired,omitempty"`
}

// RepositoryParent is the repository a fork was made from.
type RepositoryParent struct {
	FullName string `json:"fullName"`
	HTMLURL  string `json:"htmlUrl"`
}

// RepositoryDetail is GitHubRepositoryDetail.
type RepositoryDetail struct {
	Repository
	Homepage     string             `json:"homepage,omitempty"`
	Settings     RepositorySettings `json:"settings"`
	Installation Installation       `json:"installation"`
	Parent       *RepositoryParent  `json:"parent,omitempty"`
}

func detailOf(raw rawRepository, installation Installation, login string) RepositoryDetail {
	detail := RepositoryDetail{
		Repository: repositoryOf(raw, installation, login), Homepage: deref(raw.Homepage), Installation: installation,
		Settings: RepositorySettings{
			HasIssues: raw.HasIssues, HasWiki: raw.HasWiki, HasProjects: raw.HasProjects, HasDiscussions: raw.HasDiscussions,
			AllowMergeCommit: raw.AllowMerge, AllowSquashMerge: raw.AllowSquash, AllowRebaseMerge: raw.AllowRebase,
			AllowAutoMerge: raw.AllowAutoMerge, DeleteBranchOnMerge: raw.DeleteOnMerge, AllowUpdateBranch: raw.AllowUpdate,
			WebCommitSignoffRequired: raw.SignoffRequired,
		},
	}
	if raw.Parent != nil && raw.Parent.FullName != "" {
		detail.Parent = &RepositoryParent{FullName: raw.Parent.FullName, HTMLURL: raw.Parent.HTMLURL}
	}
	return detail
}

// Detail is everything the repository's page shows about it, from the answer
// its access was resolved with when that is still cached.
func (s *Service) Detail(ctx context.Context, access *RepoAccess) (RepositoryDetail, error) {
	s.mu.Lock()
	cached, ok := s.access[keyOf(access.ConnectionID, access.Owner, access.Name)]
	s.mu.Unlock()
	raw := cached.raw
	if !ok || s.now().Sub(cached.at) >= accessTTL || raw.ID != access.Repository.ID {
		if _, err := access.Client.Do(ctx, http.MethodGet, access.Path(""), nil, &raw); err != nil {
			return RepositoryDetail{}, err
		}
	}
	return detailOf(raw, access.Installation, access.Login), nil
}

// License is one licence GitHub can add to a new repository.
type License struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	SPDXID string `json:"spdxId,omitempty"`
}

// CreateOwner is an account a repository may be created under.
type CreateOwner struct {
	Login               string `json:"login"`
	Type                string `json:"type"`
	InstallationID      int64  `json:"installationId"`
	RepositorySelection string `json:"repositorySelection"`
	CanCreate           bool   `json:"canCreate"`
	Note                string `json:"note,omitempty"`
}

// CreateOptions is GitHubCreateOptions.
type CreateOptions struct {
	Owners             []CreateOwner `json:"owners"`
	GitignoreTemplates []string      `json:"gitignoreTemplates"`
	Licenses           []License     `json:"licenses"`
}

// createOwners are the accounts a repository may be created under through
// this connection: the connected account itself and each organisation, where
// the App is installed. Creating needs the App's administration: write there;
// whether the person may create in an organisation is GitHub's to say.
func createOwners(installations []Installation, login string) []CreateOwner {
	owners := []CreateOwner{}
	for _, installation := range installations {
		account := installation.Account
		if account.Type != "Organization" && !equalFold(account.Login, login) {
			continue
		}
		owner := CreateOwner{
			Login: account.Login, Type: account.Type, InstallationID: installation.ID,
			RepositorySelection: installation.RepositorySelection, CanCreate: true,
		}
		if owner.Type == "" {
			owner.Type = "User"
		}
		switch {
		case installation.Suspended:
			owner.CanCreate = false
			owner.Note = fmt.Sprintf("The GitHub App is suspended on %s.", account.Login)
		case !installation.Grants("administration", "write"):
			owner.CanCreate = false
			owner.Note = fmt.Sprintf("The GitHub App was not granted Administration: write on %s. Accept the permission on GitHub, then refresh.", account.Login)
		case installation.RepositorySelection == "selected":
			owner.Note = fmt.Sprintf("The App reaches only the repositories selected for it on %s: add the new repository to them in the installation's settings on GitHub before it shows here.", account.Login)
		}
		owners = append(owners, owner)
	}
	sort.SliceStable(owners, func(i, j int) bool {
		// The connected account first, then organisations by name.
		iUser, jUser := owners[i].Type != "Organization", owners[j].Type != "Organization"
		if iUser != jUser {
			return iUser
		}
		return strings.ToLower(owners[i].Login) < strings.ToLower(owners[j].Login)
	})
	return owners
}

// CreateOptions answers who a repository may be created under, and GitHub's
// gitignore templates and licences, which are cached for a day.
func (s *Service) CreateOptions(ctx context.Context, userID, id int64) (CreateOptions, error) {
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return CreateOptions{}, err
	}
	client := s.client(c)
	installations, err := s.installations(ctx, c, client, false)
	if err != nil {
		return CreateOptions{}, err
	}
	options := CreateOptions{Owners: createOwners(installations, c.Login), GitignoreTemplates: []string{}, Licenses: []License{}}
	s.mu.Lock()
	cached := s.options
	s.mu.Unlock()
	if !cached.at.IsZero() && s.now().Sub(cached.at) < optionsTTL {
		options.GitignoreTemplates, options.Licenses = cached.gitignores, cached.licenses
		return options, nil
	}
	var templates []string
	if _, err := client.Do(ctx, http.MethodGet, "/gitignore/templates", nil, &templates); err != nil {
		return CreateOptions{}, err
	}
	var licenses []struct {
		Key    string `json:"key"`
		Name   string `json:"name"`
		SPDXID string `json:"spdx_id"`
	}
	if _, err := client.Do(ctx, http.MethodGet, "/licenses", nil, &licenses); err != nil {
		return CreateOptions{}, err
	}
	if templates == nil {
		templates = []string{}
	}
	for _, l := range licenses {
		options.Licenses = append(options.Licenses, License{Key: l.Key, Name: l.Name, SPDXID: l.SPDXID})
	}
	options.GitignoreTemplates = templates
	s.mu.Lock()
	s.options = cachedOptions{at: s.now(), gitignores: templates, licenses: options.Licenses}
	s.mu.Unlock()
	return options, nil
}

// CreateRequest is GitHubCreateRepository.
type CreateRequest struct {
	Owner             string `json:"owner"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	Visibility        string `json:"visibility"`
	AutoInit          bool   `json:"autoInit"`
	GitignoreTemplate string `json:"gitignoreTemplate,omitempty"`
	LicenseTemplate   string `json:"licenseTemplate,omitempty"`
}

// CreateResult is GitHubCreateResult.
type CreateResult struct {
	Repository Repository `json:"repository"`
	Reachable  bool       `json:"reachable"`
	Notice     string     `json:"notice,omitempty"`
}

var templateRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]{0,99}$`)

const maxDescription = 350

func validDescription(description string) error {
	if utf8.RuneCountInString(description) > maxDescription {
		return refuse(fmt.Sprintf("the description may be at most %d characters", maxDescription))
	}
	if strings.ContainsAny(description, "\x00\r\n") {
		return refuse("the description is one line of text")
	}
	return nil
}

// Create makes a repository under the connected account or an organisation,
// refusing before GitHub is asked what can be told here: a name GitHub would
// refuse, an owner the App is not installed on or may not create under.
func (s *Service) Create(ctx context.Context, userID, id int64, request CreateRequest) (CreateResult, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Owner = strings.TrimSpace(request.Owner)
	request.Description = strings.TrimSpace(request.Description)
	switch {
	case !ValidRepoName(request.Name):
		return CreateResult{}, refuse("a repository name may use letters, digits, dots, dashes and underscores, up to 100 characters")
	case !ValidOwner(request.Owner):
		return CreateResult{}, refuse("choose the account or organisation to create the repository under")
	case request.Visibility != "public" && request.Visibility != "private":
		return CreateResult{}, refuse("visibility is public or private")
	case request.GitignoreTemplate != "" && !templateRE.MatchString(request.GitignoreTemplate):
		return CreateResult{}, refuse("that .gitignore template is not one GitHub offers")
	case request.LicenseTemplate != "" && !templateRE.MatchString(request.LicenseTemplate):
		return CreateResult{}, refuse("that licence is not one GitHub offers")
	}
	if err := validDescription(request.Description); err != nil {
		return CreateResult{}, err
	}
	c, err := s.active(ctx, userID, id)
	if err != nil {
		return CreateResult{}, err
	}
	client := s.client(c)
	installations, err := s.installations(ctx, c, client, false)
	if err != nil {
		return CreateResult{}, err
	}
	var owner *CreateOwner
	for _, candidate := range createOwners(installations, c.Login) {
		if equalFold(candidate.Login, request.Owner) {
			owner = &candidate
			break
		}
	}
	if owner == nil {
		return CreateResult{}, &NotPermittedError{Reason: fmt.Sprintf("The GitHub App is not installed on %s, so a repository cannot be created there from this dashboard. Install it there on GitHub, then refresh.", request.Owner)}
	}
	if !owner.CanCreate {
		return CreateResult{}, &NotPermittedError{Reason: owner.Note}
	}
	installation, _ := installationFor(installations, owner.Login)
	body := map[string]any{
		"name": request.Name, "private": request.Visibility == "private", "auto_init": request.AutoInit,
	}
	if request.Description != "" {
		body["description"] = request.Description
	}
	if request.GitignoreTemplate != "" {
		body["gitignore_template"] = request.GitignoreTemplate
	}
	if request.LicenseTemplate != "" {
		body["license_template"] = request.LicenseTemplate
	}
	path := "/user/repos"
	if owner.Type == "Organization" {
		path = "/orgs/" + url.PathEscape(owner.Login) + "/repos"
		body["visibility"] = request.Visibility
	}
	var raw rawRepository
	if _, err := client.Do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return CreateResult{}, err
	}
	s.forgetConnection(c.ID)
	result := CreateResult{Repository: repositoryOf(raw, installation, c.Login), Reachable: true}
	if covered, err := s.covers(ctx, client, c.ID, installation, raw.ID); err != nil || !covered {
		result.Reachable = false
		result.Notice = fmt.Sprintf("GitHub created %s, but the App reaches only the repositories selected for it on %s. Add %s to them in the installation's settings on GitHub (%s) to manage it here.",
			raw.FullName, owner.Login, raw.Name, installation.SettingsURL)
	}
	return result, nil
}

// RepositoryUpdate is GitHubRepositoryUpdate: only the fields sent change.
type RepositoryUpdate struct {
	Name                     *string   `json:"name,omitempty"`
	Description              *string   `json:"description,omitempty"`
	Homepage                 *string   `json:"homepage,omitempty"`
	DefaultBranch            *string   `json:"defaultBranch,omitempty"`
	Topics                   *[]string `json:"topics,omitempty"`
	IsTemplate               *bool     `json:"isTemplate,omitempty"`
	HasIssues                *bool     `json:"hasIssues,omitempty"`
	HasWiki                  *bool     `json:"hasWiki,omitempty"`
	HasProjects              *bool     `json:"hasProjects,omitempty"`
	HasDiscussions           *bool     `json:"hasDiscussions,omitempty"`
	AllowMergeCommit         *bool     `json:"allowMergeCommit,omitempty"`
	AllowSquashMerge         *bool     `json:"allowSquashMerge,omitempty"`
	AllowRebaseMerge         *bool     `json:"allowRebaseMerge,omitempty"`
	AllowAutoMerge           *bool     `json:"allowAutoMerge,omitempty"`
	DeleteBranchOnMerge      *bool     `json:"deleteBranchOnMerge,omitempty"`
	AllowUpdateBranch        *bool     `json:"allowUpdateBranch,omitempty"`
	WebCommitSignoffRequired *bool     `json:"webCommitSignoffRequired,omitempty"`
}

var (
	topicRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
	branchRE = regexp.MustCompile(`^[^\x00-\x20\x7f~^:?*\[\\]{1,255}$`)
)

// githubFields is the update in GitHub's names, with only the fields sent.
func (u RepositoryUpdate) githubFields() (map[string]any, error) {
	fields := map[string]any{}
	if u.Name != nil {
		name := strings.TrimSpace(*u.Name)
		if !ValidRepoName(name) {
			return nil, refuse("a repository name may use letters, digits, dots, dashes and underscores, up to 100 characters")
		}
		fields["name"] = name
	}
	if u.Description != nil {
		description := strings.TrimSpace(*u.Description)
		if err := validDescription(description); err != nil {
			return nil, err
		}
		fields["description"] = description
	}
	if u.Homepage != nil {
		homepage := strings.TrimSpace(*u.Homepage)
		if homepage != "" {
			parsed, err := url.Parse(homepage)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || len(homepage) > 255 {
				return nil, refuse("the website must be an http or https address")
			}
		}
		fields["homepage"] = homepage
	}
	if u.DefaultBranch != nil {
		branch := strings.TrimSpace(*u.DefaultBranch)
		if !branchRE.MatchString(branch) || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".lock") {
			return nil, refuse("that is not a branch name")
		}
		fields["default_branch"] = branch
	}
	for name, value := range map[string]*bool{
		"is_template": u.IsTemplate, "has_issues": u.HasIssues, "has_wiki": u.HasWiki, "has_projects": u.HasProjects,
		"has_discussions": u.HasDiscussions, "allow_merge_commit": u.AllowMergeCommit, "allow_squash_merge": u.AllowSquashMerge,
		"allow_rebase_merge": u.AllowRebaseMerge, "allow_auto_merge": u.AllowAutoMerge, "delete_branch_on_merge": u.DeleteBranchOnMerge,
		"allow_update_branch": u.AllowUpdateBranch, "web_commit_signoff_required": u.WebCommitSignoffRequired,
	} {
		if value != nil {
			fields[name] = *value
		}
	}
	if u.Topics != nil {
		if len(*u.Topics) > 20 {
			return nil, refuse("a repository may have at most 20 topics")
		}
		for _, topic := range *u.Topics {
			if !topicRE.MatchString(topic) {
				return nil, refuse(fmt.Sprintf("%q is not a topic: lowercase letters, digits and hyphens, up to 50 characters", topic))
			}
		}
	}
	return fields, nil
}

// refuseArchived is the archived repository's rule: read-only until it is
// unarchived, which is the one change allowed on it.
func refuseArchived(access *RepoAccess) error {
	if access.Repository.Archived {
		return &NotPermittedError{Reason: fmt.Sprintf("%s is archived, so it is read-only until it is unarchived.", access.Repository.FullName)}
	}
	return nil
}

// Update changes the repository's settings: PATCH with only the fields sent,
// and the topics through their own call. A rename answers the new name.
func (s *Service) Update(ctx context.Context, access *RepoAccess, update RepositoryUpdate) (RepositoryDetail, error) {
	if err := access.Require(access.Repository.Can.Administer, "administer"); err != nil {
		return RepositoryDetail{}, err
	}
	if err := refuseArchived(access); err != nil {
		return RepositoryDetail{}, err
	}
	fields, err := update.githubFields()
	if err != nil {
		return RepositoryDetail{}, err
	}
	if len(fields) == 0 && update.Topics == nil {
		return RepositoryDetail{}, refuse("nothing to change")
	}
	defer s.Forget(access.ConnectionID, access.Owner, access.Name)
	var raw rawRepository
	path := access.Path("")
	patched := false
	if len(fields) > 0 {
		if _, err := access.Client.Do(ctx, http.MethodPatch, path, fields, &raw); err != nil {
			return RepositoryDetail{}, err
		}
		patched = true
		path = "/repos/" + raw.Owner.Login + "/" + raw.Name
	}
	if update.Topics != nil {
		var topics struct {
			Names []string `json:"names"`
		}
		if _, err := access.Client.Do(ctx, http.MethodPut, path+"/topics", map[string]any{"names": *update.Topics}, &topics); err != nil {
			return RepositoryDetail{}, err
		}
		if patched {
			raw.Topics = topics.Names
		}
	}
	if !patched {
		if _, err := access.Client.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return RepositoryDetail{}, err
		}
	}
	return s.changed(access, raw), nil
}

// changed records GitHub's answer to a change as the repository's access, so
// the next request on it reads the new state, and answers the detail.
func (s *Service) changed(access *RepoAccess, raw rawRepository) RepositoryDetail {
	if raw.Permissions == nil {
		// Some answers leave the permissions hash out; the person's role did
		// not change by changing the repository.
		raw.Permissions = map[string]bool{}
		switch access.Repository.Role {
		case "admin":
			raw.Permissions["admin"] = true
		case "maintain":
			raw.Permissions["maintain"] = true
		case "write":
			raw.Permissions["push"] = true
		case "triage":
			raw.Permissions["triage"] = true
		}
	}
	s.Forget(access.ConnectionID, access.Owner, access.Name)
	next := RepoAccess{
		Client: access.Client, ConnectionID: access.ConnectionID, Login: access.Login, Owner: raw.Owner.Login, Name: raw.Name,
		Repository: repositoryOf(raw, access.Installation, access.Login), Installation: access.Installation,
	}
	s.remember(next, raw)
	return detailOf(raw, access.Installation, access.Login)
}

// SetArchived archives or unarchives the repository.
func (s *Service) SetArchived(ctx context.Context, access *RepoAccess, archived bool) (RepositoryDetail, error) {
	if err := access.Require(access.Repository.Can.Administer, "administer"); err != nil {
		return RepositoryDetail{}, err
	}
	var raw rawRepository
	if _, err := access.Client.Do(ctx, http.MethodPatch, access.Path(""), map[string]any{"archived": archived}, &raw); err != nil {
		s.Forget(access.ConnectionID, access.Owner, access.Name)
		return RepositoryDetail{}, err
	}
	return s.changed(access, raw), nil
}

// SetVisibility makes the repository public or private.
func (s *Service) SetVisibility(ctx context.Context, access *RepoAccess, visibility string) (RepositoryDetail, error) {
	if visibility != "public" && visibility != "private" {
		return RepositoryDetail{}, refuse("visibility is public or private")
	}
	if err := access.Require(access.Repository.Can.Administer, "administer"); err != nil {
		return RepositoryDetail{}, err
	}
	if err := refuseArchived(access); err != nil {
		return RepositoryDetail{}, err
	}
	var raw rawRepository
	if _, err := access.Client.Do(ctx, http.MethodPatch, access.Path(""), map[string]any{"visibility": visibility}, &raw); err != nil {
		s.Forget(access.ConnectionID, access.Owner, access.Name)
		return RepositoryDetail{}, err
	}
	return s.changed(access, raw), nil
}

// Delete deletes the repository on GitHub. An archived repository may be
// deleted: the abilities policy does not hold Delete to the archive rule,
// as GitHub does not.
func (s *Service) Delete(ctx context.Context, access *RepoAccess) error {
	if err := access.Require(access.Repository.Can.Delete, "delete"); err != nil {
		return err
	}
	defer s.Forget(access.ConnectionID, access.Owner, access.Name)
	_, err := access.Client.Request(ctx, http.MethodDelete, access.Path(""), nil)
	return err
}
