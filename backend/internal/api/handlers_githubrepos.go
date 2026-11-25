package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The GitHub page (/github): GitHub accounts each dashboard account connects
// through this dashboard's own repository App, and the repositories they
// reach. It is apart from the Git page's GitHub routes (/git/github, the gh
// sign-in of the host account that owns a checkout) and from the deploy App
// (/deploy/github-app): see internal/githubrepos for why.
//
// Every route needs a browser session. A connection belongs to the dashboard
// account that made it and is invisible to every other one, administrators
// included, so nothing here lets one person act on GitHub as another.
//
//	read           the App's state, the account's own connections, each
//	               one's profile, pull requests, issues and activity, the
//	               repositories they reach and everything about one
//	service.control connecting and disconnecting the account's own GitHub
//	               accounts, and the recoverable changes on GitHub: creating a
//	               repository, its settings, archiving, branches, issues,
//	               merging and closing pull requests, workflows, inviting a
//	               collaborator, cloning onto this server
//	s.destructive  deleting a branch, removing a collaborator or an
//	               invitation, changing a repository's visibility (ordinary
//	               confirmation), and deleting a repository (the typed phrase
//	               of its full name, invariant 3)
//	system.admin   creating the App and forgetting it
func (s *Server) mountGitHubPageRoutes(r chi.Router) {
	r.Route("/github", func(r chi.Router) {
		r.Use(httpx.RequireSession)
		// The App, the sign-in flows, the connections, the repository list,
		// creating a repository (handlers_githubrepos_core.go).
		s.mountGitHubAccountRoutes(r)
		r.Route("/connections/{connection}/repos/{owner}/{repo}", func(r chi.Router) {
			// One repository: its detail, settings, archive, visibility,
			// deletion and cloning onto this server (core).
			s.mountGitHubRepositoryRoutes(r)
			// What is inside it: branches, commits, pull requests, issues,
			// labels, workflows and runs, collaborators
			// (handlers_githubrepos_resources.go).
			s.mountGitHubResourceRoutes(r)
		})
	})
}

// githubRepo resolves {connection}/{owner}/{repo} for the signed-in account:
// its own connection, a client acting as that GitHub account, and the
// repository with its abilities. A test replaces it through
// githubAccessOverride.
func (s *Server) githubRepo(r *http.Request) (*githubrepos.RepoAccess, error) {
	if s.githubAccessOverride != nil {
		return s.githubAccessOverride(r)
	}
	return s.resolveGitHubRepoAccess(r)
}

// githubPageErr answers an error from the GitHub page's work in the page's
// words. Each code is one the page tells apart:
//
//	409 github_not_configured  no App yet: the page offers to set one up
//	404 github_connection      not one of this account's connections
//	409 github_reconnect       GitHub no longer accepts the sign-in
//	403 github_permission      refused here, with the reason (role or grant)
//	403 github_forbidden       refused by GitHub, with its message
//	404 github_not_found       GitHub says it does not exist, or cannot be seen
//	422 github_validation      GitHub refused the values, with its reasons
//	409 github_conflict        not mergeable, an empty repository
//	429 github_rate_limited    GitHub's rate limit, with when it lifts
//	502 github_unavailable     GitHub could not be reached or answered badly
//	504 timeout                the request ran out of time
//	400 github_state           a callback whose state was not issued to this
//	                           account, expired, or was used already
//	409 github_callback_unregistered  a sign-in from an address the App
//	                           has no callback URL for; names the ones it has
//	409 github_device_flow_disabled   the App's Enable Device Flow box is not
//	                           ticked; says where to tick it
//	400 bad_request            a value refused before GitHub was asked (a
//	                           branch name git would not take, a merge
//	                           without the head it was shown)
//
// GitHub refusing a sign-in exchange (a code that expired, a client secret
// reset on GitHub) is github_validation with GitHub's reason in words, and a
// second App while one exists is github_conflict.
func githubPageErr(err error) error {
	if err == nil {
		return nil
	}
	var already *httpx.APIError
	if errors.As(err, &already) {
		return err
	}
	var reconnect *githubrepos.ReconnectError
	var refused *githubrepos.NotPermittedError
	var api *githubrepos.APIError
	var unregistered *githubrepos.CallbackUnregisteredError
	var deviceDisabled *githubrepos.DeviceFlowDisabledError
	var oauth *githubrepos.OAuthError
	var invalid *githubrepos.InvalidError
	switch {
	case errors.As(err, &invalid):
		return httpx.BadRequest("%s", invalid.Reason)
	case errors.Is(err, githubrepos.ErrState):
		return httpx.Err(http.StatusBadRequest, "github_state", err.Error())
	case errors.As(err, &unregistered):
		return httpx.Err(http.StatusConflict, "github_callback_unregistered", unregistered.Error())
	case errors.As(err, &deviceDisabled):
		return httpx.Err(http.StatusConflict, "github_device_flow_disabled", deviceDisabled.Error())
	case errors.As(err, &oauth):
		return httpx.Err(http.StatusUnprocessableEntity, "github_validation", oauth.Error())
	case errors.Is(err, githubrepos.ErrAppExists):
		return httpx.Err(http.StatusConflict, "github_conflict", err.Error())
	case errors.Is(err, githubrepos.ErrNotConfigured):
		return httpx.Err(http.StatusConflict, "github_not_configured", err.Error())
	case errors.Is(err, githubrepos.ErrConnectionNotFound):
		return httpx.Err(http.StatusNotFound, "github_connection", err.Error())
	case errors.As(err, &reconnect):
		return httpx.Err(http.StatusConflict, "github_reconnect", reconnect.Error())
	case errors.As(err, &refused):
		return httpx.Err(http.StatusForbidden, "github_permission", refused.Reason)
	case errors.Is(err, context.DeadlineExceeded):
		return httpx.Timeout(err)
	case errors.As(err, &api):
		message := api.Error()
		switch api.Kind {
		case githubrepos.KindUnauthorized:
			return httpx.Err(http.StatusConflict, "github_reconnect", "GitHub no longer accepts this account's sign-in. Connect it again.")
		case githubrepos.KindRateLimited:
			wait := time.Duration(0)
			text := "GitHub's rate limit for this account is spent."
			if !api.RetryAt.IsZero() {
				wait = time.Until(api.RetryAt)
				text = fmt.Sprintf("GitHub's rate limit for this account is spent until %s UTC.", api.RetryAt.UTC().Format("15:04"))
			}
			return httpx.Err(http.StatusTooManyRequests, "github_rate_limited", text).Because(message, "").RetryAfter(wait)
		case githubrepos.KindForbidden:
			return httpx.Err(http.StatusForbidden, "github_forbidden", "GitHub refused this: "+message)
		case githubrepos.KindNotFound:
			return httpx.Err(http.StatusNotFound, "github_not_found", "GitHub has no such thing, or this account cannot see it.")
		case githubrepos.KindValidation:
			return httpx.Err(http.StatusUnprocessableEntity, "github_validation", message)
		case githubrepos.KindConflict:
			return httpx.Err(http.StatusConflict, "github_conflict", message)
		default:
			return httpx.Err(http.StatusBadGateway, "github_unavailable", "GitHub could not be reached or did not answer usefully.").Because("", message).Retry()
		}
	}
	return err
}
