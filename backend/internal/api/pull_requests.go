package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
)

// pullRequests is how the deploy pages and the preview reconciler read a
// repository's pull requests. Which identity answers is the trust model's
// decision, not the caller's: the GitHub App's installation where the App is
// installed on the repository, else the dashboard's own gh login (dir "").
// Never a checkout found by matching a remote — a host account owning a
// checkout under the git roots would otherwise be supplying the head commit
// this server builds and runs.
//
// The reads are cached briefly because the Overview polls and the reconciler
// asks every minute, and GitHub's quota is not theirs to spend; the gh
// subprocesses are bounded so a fleet of projects cannot fork a gh per
// project at once.
type pullRequests struct {
	github *ghx.Service
	app    *githubapp.Service
	git    *gitx.Service
	// detectTailnet answers what this host is on its tailnet. A seam, so the
	// routes can be driven on a host without Tailscale.
	detectTailnet func(context.Context) selfcfg.Identity
	// slots bounds the gh subprocesses this reader runs at once.
	slots chan struct{}

	mu         sync.Mutex
	lists      map[string]cachedPulls
	checkouts  map[string]cachedPulls
	identities map[string]cachedIdentity
	login      cachedLogin
	repoPaths  map[string]string
	repoPathAt time.Time
	tailnet    selfcfg.Identity
	tailnetAt  time.Time
}

const (
	pullListTTL     = 30 * time.Second
	pullCheckoutTTL = 60 * time.Second
	pullIdentityTTL = 2 * time.Minute
	pullTailnetTTL  = 30 * time.Second
	pullReadSlots   = 4
	// pullListLimit bounds one repository's open list on the deploy page. A
	// project with more open pull requests than this shows the newest.
	pullListLimit = 50
)

type cachedPulls struct {
	at         time.Time
	repository string
	identity   string
	pulls      []ghx.PullRequest
}

type cachedIdentity struct {
	at     time.Time
	kind   string
	reason string
}

type cachedLogin struct {
	at       time.Time
	loggedIn bool
}

// errPullRequestsUnavailable says why no identity can read a repository, in
// the words the page's reason field carries.
type errPullRequestsUnavailable struct{ reason string }

func (e errPullRequestsUnavailable) Error() string {
	switch e.reason {
	case "sign_in_required":
		return "the dashboard is not signed in to GitHub; sign in on the Git page or install the GitHub App on this repository"
	case "app_not_installed":
		return "the GitHub App is not installed on this repository and the GitHub CLI is not available"
	default:
		return "neither the GitHub CLI nor a GitHub App is available to read pull requests"
	}
}

// errGitHubLoginRequired is a deploy-page write (merge, comment) asked of a
// dashboard that is not signed in to GitHub. Those go through the dashboard's
// own login only: a checkout owner's credential is the Git page's.
var errGitHubLoginRequired = errors.New("the dashboard is not signed in to GitHub; sign in on the Git page first")

func newPullRequests(github *ghx.Service, app *githubapp.Service, git *gitx.Service) *pullRequests {
	return &pullRequests{
		github: github, app: app, git: git,
		detectTailnet: selfcfg.DetectTailscale,
		slots:         make(chan struct{}, pullReadSlots),
		lists:         map[string]cachedPulls{},
		checkouts:     map[string]cachedPulls{},
		identities:    map[string]cachedIdentity{},
	}
}

// repositoryOf is the owner/name a deploy project's source points at, when
// that source is a github.com repository.
func (p *pullRequests) repositoryOf(summary deploy.DeploymentSummary) (string, bool) {
	repository := deploy.GitHubRepository(deploy.SourceIdentity{
		Kind: deploy.SourceGit, Remote: summary.SourceRemote, Repository: summary.SourceRepository,
	})
	return repository, repository != ""
}

// acquire takes one of the gh subprocess slots, or gives up with the context.
func (p *pullRequests) acquire(ctx context.Context) (func(), error) {
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// identity decides who reads a repository: "app" when the App is installed
// on it, "cli" when the dashboard's own gh login can, else the reason the
// page shows. Both answers are kept for two minutes, because each App
// answer is a JWT-authenticated round trip and each CLI answer a subprocess.
func (p *pullRequests) identity(ctx context.Context, repository string) (string, error) {
	key := strings.ToLower(repository)
	p.mu.Lock()
	cached, ok := p.identities[key]
	p.mu.Unlock()
	if ok && time.Since(cached.at) < pullIdentityTTL {
		if cached.kind != "" {
			return cached.kind, nil
		}
		return "", errPullRequestsUnavailable{reason: cached.reason}
	}
	kind, reason := p.resolveIdentity(ctx, repository)
	p.mu.Lock()
	p.identities[key] = cachedIdentity{at: time.Now(), kind: kind, reason: reason}
	p.mu.Unlock()
	if kind != "" {
		return kind, nil
	}
	return "", errPullRequestsUnavailable{reason: reason}
}

func (p *pullRequests) resolveIdentity(ctx context.Context, repository string) (kind, reason string) {
	if p.app != nil && p.app.Installed(ctx, repository) {
		return "app", ""
	}
	if p.github != nil && p.github.Available() {
		if p.cliLoggedIn(ctx) {
			return "cli", ""
		}
		return "", "sign_in_required"
	}
	if p.appConfigured(ctx) {
		return "", "app_not_installed"
	}
	return "", "not_installed"
}

func (p *pullRequests) appConfigured(ctx context.Context) bool {
	if p.app == nil {
		return false
	}
	_, err := p.app.Client(ctx)
	return err == nil
}

// cliLoggedIn is whether the dashboard's own account is signed in to GitHub,
// kept for two minutes: the answer is a gh subprocess, and every poll of
// every project's Overview would otherwise ask it again.
func (p *pullRequests) cliLoggedIn(ctx context.Context) bool {
	p.mu.Lock()
	login := p.login
	p.mu.Unlock()
	if !login.at.IsZero() && time.Since(login.at) < pullIdentityTTL {
		return login.loggedIn
	}
	loggedIn := false
	if release, err := p.acquire(ctx); err == nil {
		account, err := p.github.Status(ctx, "")
		release()
		loggedIn = err == nil && account != nil && account.LoggedIn
	}
	p.mu.Lock()
	p.login = cachedLogin{at: time.Now(), loggedIn: loggedIn}
	p.mu.Unlock()
	return loggedIn
}

// List is a repository's pull requests in one state, read through the
// trusted identity and kept for thirty seconds. It answers which identity
// read them, since the page says so.
func (p *pullRequests) List(ctx context.Context, repository, state string) ([]ghx.PullRequest, string, error) {
	key := strings.ToLower(repository) + "|" + state
	p.mu.Lock()
	cached, ok := p.lists[key]
	p.mu.Unlock()
	if ok && time.Since(cached.at) < pullListTTL {
		return append([]ghx.PullRequest(nil), cached.pulls...), cached.identity, nil
	}
	kind, err := p.identity(ctx, repository)
	if err != nil {
		return nil, "", err
	}
	var pulls []ghx.PullRequest
	switch kind {
	case "app":
		// The REST listing has no "merged" word; closed ones carry the flag.
		want := state
		if state == "merged" {
			want = "closed"
		}
		listed, err := p.app.PullRequests(ctx, repository, want)
		if err != nil {
			return nil, "", err
		}
		for _, item := range listed {
			if state == "merged" && !item.Merged {
				continue
			}
			pulls = append(pulls, appPullRequest(item))
		}
	default:
		release, err := p.acquire(ctx)
		if err != nil {
			return nil, "", err
		}
		pulls, err = p.github.ListPullsIn(ctx, "", repository, state, pullListLimit)
		release()
		if err != nil {
			return nil, "", err
		}
	}
	if pulls == nil {
		pulls = []ghx.PullRequest{}
	}
	p.mu.Lock()
	p.lists[key] = cachedPulls{at: time.Now(), repository: repository, identity: kind, pulls: pulls}
	p.mu.Unlock()
	return append([]ghx.PullRequest(nil), pulls...), kind, nil
}

// Get reads one pull request in full through the trusted identity. It is
// never cached: its answer is what a preview is approved for.
func (p *pullRequests) Get(ctx context.Context, repository string, number int) (*ghx.PullRequest, error) {
	kind, err := p.identity(ctx, repository)
	if err != nil {
		return nil, err
	}
	if kind == "app" {
		pull, err := p.app.PullRequest(ctx, repository, number)
		if err != nil {
			return nil, err
		}
		converted := appPullRequest(*pull)
		return &converted, nil
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.github.ViewPullIn(ctx, "", repository, number)
}

// Checks lists what GitHub reported on a commit: check runs and statuses in
// one list, failures first.
func (p *pullRequests) Checks(ctx context.Context, repository, sha string) ([]ghx.CheckRun, error) {
	kind, err := p.identity(ctx, repository)
	if err != nil {
		return nil, err
	}
	if kind == "app" {
		checks, err := p.app.PullRequestChecks(ctx, repository, sha)
		// An App the owner has not granted checks:read yet still answers the
		// statuses; that partial list is the answer, not an empty one.
		if err != nil && !errors.Is(err, githubapp.ErrPermission) {
			return nil, err
		}
		out := make([]ghx.CheckRun, 0, len(checks))
		for _, check := range checks {
			out = append(out, ghx.CheckRun(check))
		}
		return out, nil
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	checks, err := p.github.PullChecks(ctx, "", repository, sha)
	if checks == nil && err == nil {
		checks = []ghx.CheckRun{}
	}
	return checks, err
}

// HeadOf is the current head of a pull request, from the cached open list
// when it is there and from a read otherwise.
func (p *pullRequests) HeadOf(ctx context.Context, repository string, number int) (string, error) {
	p.mu.Lock()
	cached, ok := p.lists[strings.ToLower(repository)+"|open"]
	p.mu.Unlock()
	if ok && time.Since(cached.at) < pullListTTL {
		for _, pull := range cached.pulls {
			if pull.Number == number && pull.HeadSHA != "" {
				return pull.HeadSHA, nil
			}
		}
	}
	pull, err := p.Get(ctx, repository, number)
	if err != nil {
		return "", err
	}
	return pull.HeadSHA, nil
}

// cliWrite is the identity every deploy-page write goes through: the
// dashboard's own gh login, and nothing else.
func (p *pullRequests) cliWrite(ctx context.Context) error {
	if p.github == nil || !p.github.Available() {
		return ghx.ErrNotInstalled
	}
	if !p.cliLoggedIn(ctx) {
		return errGitHubLoginRequired
	}
	return nil
}

// Merge merges a pull request as the dashboard's own account.
func (p *pullRequests) Merge(ctx context.Context, repository string, number int, method string, deleteBranch bool, headSHA string) error {
	if err := p.cliWrite(ctx); err != nil {
		return err
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return p.github.MergePullIn(ctx, "", repository, number, method, deleteBranch, headSHA)
}

// Comment posts a plain comment on a pull request as the dashboard's own
// account.
func (p *pullRequests) Comment(ctx context.Context, repository string, number int, body string) error {
	if err := p.cliWrite(ctx); err != nil {
		return err
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return p.github.CommentIssue(ctx, "", repository, number, body)
}

// ForgetLogin drops what is remembered about the dashboard's own sign-in and
// the identities decided on it, so signing in or out on the Git page shows on
// the deploy pages at once rather than two minutes later.
func (p *pullRequests) ForgetLogin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.login = cachedLogin{}
	for key, identity := range p.identities {
		if identity.kind != "app" {
			delete(p.identities, key)
		}
	}
}

// Invalidate forgets what was read about a repository, so the answer after a
// mutation is GitHub's rather than the cache's.
func (p *pullRequests) Invalidate(repository string) {
	key := strings.ToLower(strings.TrimSuffix(repository, ".git"))
	p.mu.Lock()
	defer p.mu.Unlock()
	for cacheKey := range p.lists {
		if strings.HasPrefix(cacheKey, key+"|") {
			delete(p.lists, cacheKey)
		}
	}
	for path, cached := range p.checkouts {
		if strings.EqualFold(strings.TrimSuffix(cached.repository, ".git"), key) {
			delete(p.checkouts, path)
		}
	}
}

// rateLimitWordsRE is GitHub's quota refusal as gh relays it in text: the
// phrase itself, or the status spelled out as one. A bare number is never
// enough — pull request #429, a commit prefix or a request id mention 429
// without GitHub having refused anything.
var rateLimitWordsRE = regexp.MustCompile(`(?i)\brate[ -]limit|\babuse detection\b|\bHTTP 429\b|\bstatus(?: code)? 429\b`)

// rateLimited recognises GitHub refusing a read for quota: a 403 that says
// so, a 429, or the same words relayed by gh as text.
func rateLimited(err error) bool {
	var api *githubapp.APIError
	if errors.As(err, &api) {
		return api.Status == http.StatusTooManyRequests ||
			(api.Status == http.StatusForbidden && rateLimitWordsRE.MatchString(api.Message))
	}
	return rateLimitWordsRE.MatchString(err.Error())
}

// PullRequestState is the reconciler's reading of one pull request. Every
// failure is wrapped as unreadable, and a quota refusal as rate limited, so
// nothing but a state that was actually read can close a preview.
func (p *pullRequests) PullRequestState(ctx context.Context, repository string, number int) (deploy.PullRequestState, error) {
	pull, err := p.Get(ctx, repository, number)
	if err != nil {
		if rateLimited(err) {
			return deploy.PullRequestState{}, fmt.Errorf("%w: %v", deploy.ErrPullRequestRateLimited, err)
		}
		return deploy.PullRequestState{}, fmt.Errorf("%w: %v", deploy.ErrPullRequestUnreadable, err)
	}
	return deploy.PullRequestState{
		Open:    pull.State == "open",
		Merged:  pull.Merged || pull.State == "merged",
		HeadSHA: pull.HeadSHA,
		Title:   pull.Title,
	}, nil
}

// CheckoutPulls is the Git page's reading of one checkout's open pull
// requests, as that checkout's owner: display for that page, not trust.
// Kept a minute per checkout, because the list page polls.
func (p *pullRequests) CheckoutPulls(ctx context.Context, path, repository string) ([]ghx.PullRequest, error) {
	p.mu.Lock()
	cached, ok := p.checkouts[path]
	p.mu.Unlock()
	if ok && time.Since(cached.at) < pullCheckoutTTL {
		return append([]ghx.PullRequest(nil), cached.pulls...), nil
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	pulls, err := p.github.ListPulls(ctx, path, "open", 30)
	release()
	if err != nil {
		return nil, err
	}
	if pulls == nil {
		pulls = []ghx.PullRequest{}
	}
	p.mu.Lock()
	p.checkouts[path] = cachedPulls{at: time.Now(), repository: repository, pulls: pulls}
	p.mu.Unlock()
	return append([]ghx.PullRequest(nil), pulls...), nil
}

// checkoutFor is the path of a checkout of the repository under the git
// roots, for the Overview's link to the Git page. Display only: nothing is
// read through it. The discovery walks every root and runs git per
// checkout, so its answer is kept for a minute.
func (p *pullRequests) checkoutFor(ctx context.Context, repository string) string {
	p.mu.Lock()
	paths, at := p.repoPaths, p.repoPathAt
	p.mu.Unlock()
	if paths == nil || time.Since(at) >= pullCheckoutTTL {
		paths = map[string]string{}
		if p.git != nil && p.git.Available() {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			repos, err := p.git.Discover(ctx)
			cancel()
			if err == nil {
				for _, repo := range repos {
					name := deploy.GitHubRepository(deploy.SourceIdentity{Kind: deploy.SourceGit, Remote: repo.Remote})
					if name == "" {
						continue
					}
					if _, seen := paths[strings.ToLower(name)]; !seen {
						paths[strings.ToLower(name)] = repo.Path
					}
				}
			}
		}
		p.mu.Lock()
		p.repoPaths, p.repoPathAt = paths, time.Now()
		p.mu.Unlock()
	}
	return paths[strings.ToLower(strings.TrimSuffix(repository, ".git"))]
}

// Tailnet is what this host is on its tailnet, kept for thirty seconds
// because the Overview polls it and the answer is a host command.
func (p *pullRequests) Tailnet(ctx context.Context) selfcfg.Identity {
	p.mu.Lock()
	identity, at := p.tailnet, p.tailnetAt
	p.mu.Unlock()
	if !at.IsZero() && time.Since(at) < pullTailnetTTL {
		return identity
	}
	identity = p.detectTailnet(ctx)
	p.mu.Lock()
	p.tailnet, p.tailnetAt = identity, time.Now()
	p.mu.Unlock()
	return identity
}

// appPullRequest is the App's reading in the gh-backed shape, field by field:
// the page draws one type whichever identity answered. Review and Checks stay
// empty, since the REST listing carries neither.
func appPullRequest(pull githubapp.PullRequest) ghx.PullRequest {
	return ghx.PullRequest{
		Number: pull.Number, Title: pull.Title, URL: pull.URL, State: pull.State, Draft: pull.Draft,
		Head: pull.Head, Base: pull.Base, Author: pull.Author, CreatedAt: pull.CreatedAt,
		Comments: pull.Comments, Mergeable: pull.Mergeable,
		Additions: pull.Additions, Deletions: pull.Deletions, Files: pull.Files, Body: pull.Body,
		HeadSHA: pull.HeadSHA, BaseSHA: pull.BaseSHA,
		HeadRepository: pull.HeadRepository, Fork: pull.Fork, UpdatedAt: pull.UpdatedAt,
		Labels: pull.Labels, Merged: pull.Merged,
	}
}
